package auth

// OIDC 回调全流程（handler 层）：已绑定登录 / 撞名确认 / JIT 开户 / 拒绝 / 绑定流程。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// flowFixture 装配：fake IdP + 内存用户库 + 假 provisioner + 挂好路由的 gin。
type flowFixture struct {
	r    *gin.Engine
	idp  *fakeIdP
	prov *fakeProvisioner
	h    *OIDCHandler
}

func newFlowFixture(t *testing.T, store UserStore, prov OIDCProvisioner, mapping OIDCMappingConfig) *flowFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	SetSecret([]byte("oidc-flow-secret"))
	f := newFakeIdP(t)
	newTestOIDCClient(t, f)
	prov.(*fakeProvisioner).store = store // JIT 用户同步入库（活体 Lookup 需要）
	h := NewOIDCHandler(store, prov, mapping)
	h.PortalURL = "/portal/"
	r := gin.New()
	r.GET("/api/v1/auth/oidc/login", h.Login)
	r.GET("/api/v1/auth/oidc/callback", h.Callback)
	r.GET("/api/v1/auth/oidc/bind", h.BindLogin)
	r.POST("/api/v1/auth/oidc/link", h.Link)
	r.POST("/api/v1/auth/oidc/unlink", h.Unlink)
	return &flowFixture{r: r, idp: f, prov: prov.(*fakeProvisioner), h: h}
}

// startLogin 发起登录并取回 state。
func (fx *flowFixture) startLogin(t *testing.T) string {
	t.Helper()
	w := httptest.NewRecorder()
	fx.r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("login start: %d", w.Code)
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("state")
}

// completeCallback 模拟 IdP 回跳（code 任意——fake IdP token 端点不校验）。
func (fx *flowFixture) completeCallback(t *testing.T, state string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	fx.r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/oidc/callback?state="+url.QueryEscape(state)+"&code=abc", nil))
	return w
}

// fragmentParam 从 302 Location 的 hash fragment（#/path?query）里取查询参数。
func fragmentParam(t *testing.T, location, key string) string {
	t.Helper()
	i := strings.Index(location, "#")
	if i < 0 {
		t.Fatalf("no fragment in %s", location)
	}
	frag := location[i+1:]
	if j := strings.IndexByte(frag, '?'); j >= 0 {
		frag = frag[j+1:]
	}
	q, err := url.ParseQuery(frag)
	if err != nil {
		t.Fatal(err)
	}
	return q.Get(key)
}

func TestOIDCFlow_LinkedUserLogin(t *testing.T) {
	prov := &fakeProvisioner{linked: map[string]string{"alice": "sub-12345"}}
	store := NewUserStoreFromList([]User{
		{Username: "alice", Role: RoleMember, TenantSlug: "hpc-lab", ClusterUser: "alice", Status: "active", OIDCSub: "sub-12345"},
	})
	fx := newFlowFixture(t, store, prov, OIDCMappingConfig{})

	state := fx.startLogin(t)
	w := fx.completeCallback(t, state)
	if w.Code != http.StatusFound {
		t.Fatalf("callback: %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if got := fragmentParam(t, loc, "status"); got != "ok" {
		t.Fatalf("status = %q loc=%s", got, loc)
	}
	token := fragmentParam(t, loc, "token")
	if token == "" {
		t.Fatalf("token missing: %s", loc)
	}
	cl, err := VerifyToken(token)
	if err != nil {
		t.Fatalf("verify portal token: %v", err)
	}
	if cl.Username != "alice" || cl.TID != "hpc-lab" {
		t.Errorf("claims = %+v", cl)
	}
	// PKCE verifier 确实送达 token 端点（S256 链路证据）
	if fx.idp.LastVerifier == "" {
		t.Error("code_verifier not sent to token endpoint")
	}
}

func TestOIDCFlow_StateReplayRejected(t *testing.T) {
	prov := &fakeProvisioner{linked: map[string]string{"alice": "sub-12345"}}
	store := NewUserStoreFromList([]User{{Username: "alice", Role: RoleMember, TenantSlug: "hpc-lab", Status: "active"}})
	fx := newFlowFixture(t, store, prov, OIDCMappingConfig{})

	state := fx.startLogin(t)
	fx.completeCallback(t, state)      // 第一次消耗
	w := fx.completeCallback(t, state) // 重放
	if fragmentParam(t, w.Header().Get("Location"), "status") != "error" {
		t.Error("state replay must be rejected")
	}
	// 伪造 state
	w2 := fx.completeCallback(t, "forged-state")
	if fragmentParam(t, w2.Header().Get("Location"), "status") != "error" {
		t.Error("forged state must be rejected")
	}
}

func TestOIDCFlow_UsernameConflictLink(t *testing.T) {
	// 本地已有同名 alice（未绑定）；IdP preferred_username=sso.user@example.com →
	// 收敛为 sso_user，不撞名…… 为制造撞名，注入 preferred_username=alice
	prov := &fakeProvisioner{}
	store := NewUserStoreFromList([]User{
		{Username: "alice", PasswordHash: hashFor("alice12345"), Role: RoleMember, TenantSlug: "hpc-lab", Status: "active"},
	})
	fx := newFlowFixture(t, store, prov, OIDCMappingConfig{})
	fx.idp.Claims = map[string]any{"preferred_username": "alice"}

	state := fx.startLogin(t)
	w := fx.completeCallback(t, state)
	loc := w.Header().Get("Location")
	if got := fragmentParam(t, loc, "status"); got != "link" {
		t.Fatalf("status = %q (want link), loc=%s", got, loc)
	}
	linkToken := fragmentParam(t, loc, "token")
	if linkToken == "" {
		t.Fatalf("linkToken missing: %s", loc)
	}

	// 确认流：错密码 → 401；正确密码 → 200 + 门户 token + 绑定生效
	body := func(pw string) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(map[string]string{
			"linkToken": linkToken, "username": "alice", "password": pw,
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/auth/oidc/link", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		fx.r.ServeHTTP(w, req)
		return w
	}
	if w := body("wrong-password"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: want 401 got %d", w.Code)
	}
	w = body("alice12345")
	if w.Code != http.StatusOK {
		t.Fatalf("link confirm: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Token == "" {
		t.Fatalf("portal token missing: %s", w.Body.String())
	}
	if _, ok := prov.UserByOIDCSub("sub-12345"); !ok {
		t.Error("alice must be linked after confirmation")
	}
	// 二次 SSO 登录 → 直接 ok
	state2 := fx.startLogin(t)
	w2 := fx.completeCallback(t, state2)
	if got := fragmentParam(t, w2.Header().Get("Location"), "status"); got != "ok" {
		t.Errorf("post-link login status = %q, want ok", got)
	}
}

func TestOIDCFlow_JITProvision(t *testing.T) {
	prov := &fakeProvisioner{}
	store := NewUserStoreFromList(nil)
	mapping := OIDCMappingConfig{
		RolesClaim:     "groups",
		RoleMap:        map[string]string{"hpc-dev": "dev"},
		TenantMap:      map[string]string{"lab-a": "hpc-lab"},
		UnmappedPolicy: "deny",
	}
	fx := newFlowFixture(t, store, prov, mapping)
	fx.idp.Claims = map[string]any{"groups": []string{"lab-a", "hpc-dev"}}

	state := fx.startLogin(t)
	w := fx.completeCallback(t, state)
	if got := fragmentParam(t, w.Header().Get("Location"), "status"); got != "ok" {
		t.Fatalf("JIT status = %q loc=%s", got, w.Header().Get("Location"))
	}
	if len(prov.provisioned) != 1 || prov.provisioned[0] != "sso_user/dev@hpc-lab" {
		t.Errorf("provisioned = %v, want [sso_user/dev@hpc-lab]", prov.provisioned)
	}
}

// Casdoor 形态：不发 preferred_username（用户名在 name claim），JIT 用户名应回退到 name。
func TestOIDCFlow_JITNameClaimFallback(t *testing.T) {
	prov := &fakeProvisioner{}
	store := NewUserStoreFromList(nil)
	mapping := OIDCMappingConfig{
		RolesClaim:     "groups",
		RoleMap:        map[string]string{"hpc-member": "member"},
		TenantMap:      map[string]string{"hpc-member": "default"},
		UnmappedPolicy: "deny",
	}
	fx := newFlowFixture(t, store, prov, mapping)
	fx.idp.Claims = map[string]any{"preferred_username": "", "name": "ssotest", "groups": []string{"hpc-member"}}

	state := fx.startLogin(t)
	w := fx.completeCallback(t, state)
	if got := fragmentParam(t, w.Header().Get("Location"), "status"); got != "ok" {
		t.Fatalf("status = %q loc=%s", got, w.Header().Get("Location"))
	}
	if len(prov.provisioned) != 1 || prov.provisioned[0] != "ssotest/member@default" {
		t.Errorf("provisioned = %v, want [ssotest/member@default]", prov.provisioned)
	}
}

func TestOIDCFlow_JITDenied(t *testing.T) {
	prov := &fakeProvisioner{}
	store := NewUserStoreFromList(nil)
	fx := newFlowFixture(t, store, prov, OIDCMappingConfig{UnmappedPolicy: "deny"})

	state := fx.startLogin(t)
	w := fx.completeCallback(t, state)
	loc := w.Header().Get("Location")
	if got := fragmentParam(t, loc, "status"); got != "error" {
		t.Fatalf("unmapped deny: status = %q loc=%s", got, loc)
	}
	if len(prov.provisioned) != 0 {
		t.Errorf("no provisioning expected, got %v", prov.provisioned)
	}
}

func TestOIDCFlow_JITDefaultPolicy(t *testing.T) {
	prov := &fakeProvisioner{}
	store := NewUserStoreFromList(nil)
	fx := newFlowFixture(t, store, prov, OIDCMappingConfig{
		UnmappedPolicy: "default",
		DefaultRole:    RoleMember,
		DefaultTenant:  "hpc-lab",
	})
	state := fx.startLogin(t)
	w := fx.completeCallback(t, state)
	if got := fragmentParam(t, w.Header().Get("Location"), "status"); got != "ok" {
		t.Fatalf("default policy: status = %q", got)
	}
	if len(prov.provisioned) != 1 || prov.provisioned[0] != "sso_user/member@hpc-lab" {
		t.Errorf("provisioned = %v", prov.provisioned)
	}
}

func TestOIDCFlow_UnlinkRequiresAuth(t *testing.T) {
	prov := &fakeProvisioner{linked: map[string]string{"alice": "sub-1"}}
	store := NewUserStoreFromList([]User{{Username: "alice", Role: RoleMember, TenantSlug: "hpc-lab", Status: "active"}})
	fx := newFlowFixture(t, store, prov, OIDCMappingConfig{})

	w := httptest.NewRecorder()
	fx.r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/auth/oidc/unlink", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unlink without claims: want 401 got %d", w.Code)
	}
}

func hashFor(pw string) string {
	h, _ := BcryptGenerateFromPassword(pw)
	return h
}

// TestOIDCFlow_BindReturnsAuthorizeURL S4 绑定发起：认证端点返回 JSON authorizeUrl
//（前端 XHR 取 URL 再导航——普通 <a> 带不上 JWT 头）。
func TestOIDCFlow_BindReturnsAuthorizeURL(t *testing.T) {
	prov := &fakeProvisioner{}
	store := NewUserStoreFromList([]User{{Username: "alice", Role: RoleMember, TenantSlug: "hpc-lab", Status: "active"}})
	fx := newFlowFixture(t, store, prov, OIDCMappingConfig{})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/auth/oidc/bind", nil)
	req.Header.Set("Authorization", "Bearer bind-token-placeholder")
	fx.r.ServeHTTP(w, req)
	// 该最小装配无 JWT 中间件（claims 为 nil）→ 401；JSON 契约经注入 claims 验证：
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bind without claims must 401, got %d", w.Code)
	}
	// 注入 claims 的装配（复刻 router 的中间件产物）
	r2 := gin.New()
	r2.GET("/api/v1/auth/oidc/bind", func(c *gin.Context) {
		c.Set("claims", &Claims{Username: "alice", Role: RoleMember})
		c.Next()
	}, fx.h.BindLogin)
	w2 := httptest.NewRecorder()
	r2.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/bind", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("bind with claims: %d %s", w2.Code, w2.Body.String())
	}
	var resp struct{ AuthorizeURL string `json:"authorizeUrl"` }
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil || resp.AuthorizeURL == "" {
		t.Fatalf("authorizeUrl missing: %s", w2.Body.String())
	}
}
