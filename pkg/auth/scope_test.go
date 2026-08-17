package auth_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ails-hpc/pkg/auth"

	"github.com/gin-gonic/gin"
)

// TestScope_FromClaims 按 authoritative role 推导可见范围；未知角色最保守（Self）。
func TestScope_FromClaims(t *testing.T) {
	cases := []struct {
		name string
		role string
		want auth.ScopeMode
	}{
		{"member→Self", auth.RoleMember, auth.ScopeSelf},
		{"unknown→Self(保守)", "intern", auth.ScopeSelf},
		{"tenant_admin→Tenant", auth.RoleTenantAdmin, auth.ScopeTenant},
		{"ops_admin→All", auth.RoleOpsAdmin, auth.ScopeAll},
		{"admin→All", auth.RoleSystemAdmin, auth.ScopeAll},
	}
	for _, tc := range cases {
		sc := auth.ScopeFromClaims(&auth.Claims{Role: tc.role, Username: "u", ClusterUser: "cu", OrgSlug: "hpc-lab"})
		if sc.Mode != tc.want {
			t.Errorf("%s: Mode=%v want %v", tc.name, sc.Mode, tc.want)
		}
	}
	// nil claims → 零值（Self+空身份，AllowsUser 恒 false）
	if sc := auth.ScopeFromClaims(nil); sc.AllowsUser("anyone") {
		t.Error("nil claims must allow nobody")
	}
}

// TestScope_TenantFallback 迁移期租户标识回退：TID 优先，空则 OrgSlug。
func TestScope_TenantFallback(t *testing.T) {
	sc := auth.ScopeFromClaims(&auth.Claims{Role: auth.RoleTenantAdmin, OrgSlug: "hpc-lab"})
	if sc.TenantSlug != "hpc-lab" {
		t.Errorf("fallback tenant = %q, want hpc-lab", sc.TenantSlug)
	}
	sc2 := auth.ScopeFromClaims(&auth.Claims{Role: auth.RoleTenantAdmin, OrgSlug: "hpc-lab", TID: "lab-x"})
	if sc2.TenantSlug != "lab-x" {
		t.Errorf("TID priority: tenant = %q, want lab-x", sc2.TenantSlug)
	}
}

// TestScope_AllowsUser Self 仅本人可见；Tenant/All 由列表过滤收口（恒真）。
func TestScope_AllowsUser(t *testing.T) {
	self := auth.ScopeFromClaims(&auth.Claims{Role: auth.RoleMember, ClusterUser: "ailsmember"})
	if !self.AllowsUser("ailsmember") || self.AllowsUser("ailsops") {
		t.Error("ScopeSelf must allow only the caller's clusterUser")
	}
	tenant := auth.ScopeFromClaims(&auth.Claims{Role: auth.RoleTenantAdmin, OrgSlug: "hpc-lab"})
	if !tenant.AllowsUser("anyone") {
		t.Error("ScopeTenant is list-filtered downstream; AllowsUser itself is permissive")
	}
}

// --- Phase 2：tid/ver claims + 带库实校中间件 + 自助改密 ---

func TestGenerateTokenClaims_TidVerRoundtrip(t *testing.T) {
	auth.SetSecret([]byte("tid-ver-test"))
	tok, err := auth.GenerateTokenClaims(auth.Claims{
		Username: "member", Role: "member", ClusterUser: "ailsmember",
		Account: "ailsmember", TID: "hpc-lab", Ver: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := auth.VerifyToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if cl.TID != "hpc-lab" || cl.Ver != 3 {
		t.Errorf("roundtrip: tid=%q ver=%d, want hpc-lab/3", cl.TID, cl.Ver)
	}
}

func newMemStore(t *testing.T) auth.UserStore {
	t.Helper()
	hash, _ := auth.BcryptGenerateFromPassword("member123")
	return auth.NewUserStoreFromList([]auth.User{
		{Username: "member", PasswordHash: hash, Role: "member",
			OrgSlug: "hpc-lab", ClusterUser: "ailsmember", Account: "ailsmember"},
	})
}

func TestMiddlewareWithStore_LiveChecks(t *testing.T) {
	auth.SetSecret([]byte("mw-live-test"))
	store := newMemStore(t)

	mkToken := func(ver int) string {
		tok, _ := auth.GenerateTokenClaims(auth.Claims{
			Username: "member", Role: "member", ClusterUser: "ailsmember",
			Account: "ailsmember", TID: "hpc-lab", Ver: ver,
		})
		return tok
	}
	router := func() *gin.Engine {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(auth.JWTAuthMiddlewareWithStore(store))
		r.GET("/x", func(c *gin.Context) { c.Status(200) })
		return r
	}
	hit := func(tok string) int {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/x", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		router().ServeHTTP(w, req)
		return w.Code
	}

	// ver 匹配 → 200
	if c := hit(mkToken(0)); c != 200 {
		t.Errorf("matching ver: want 200 got %d", c)
	}
	// 改密 → 版本 bump=1；旧 ver=0 令牌即刻 401，新 ver=1 令牌 200
	newHash, err := auth.BcryptGenerateFromPassword("newpass99")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPassword("member", newHash); err != nil {
		t.Fatal(err)
	}
	if c := hit(mkToken(0)); c != http.StatusUnauthorized {
		t.Errorf("stale ver must 401, got %d", c)
	}
	if c := hit(mkToken(1)); c != 200 {
		t.Errorf("fresh ver: want 200 got %d", c)
	}
}

func TestChangePassword_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auth.SetSecret([]byte("pw-change-test"))
	store := newMemStore(t)
	h := auth.NewAuthHandler(store)

	r := gin.New()
	r.POST("/api/v1/auth/password", func(c *gin.Context) {
		c.Set("claims", &auth.Claims{Username: "member", Role: "member"})
		h.ChangePassword(c)
	})

	call := func(body string) int {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/v1/auth/password", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w.Code
	}

	if c := call(`{"oldPassword":"wrong","newPassword":"newpass99"}`); c != 401 {
		t.Errorf("wrong old: want 401 got %d", c)
	}
	if c := call(`{"oldPassword":"member123","newPassword":"short"}`); c != 400 {
		t.Errorf("weak new: want 400 got %d", c)
	}
	if c := call(`{"oldPassword":"member123","newPassword":"newpass99"}`); c != 200 {
		t.Errorf("valid change: want 200 got %d", c)
	}
	// 新密码可登录、旧密码失效；版本已 bump（旧令牌吊销）
	if _, err := store.Verify("member", "newpass99"); err != nil {
		t.Errorf("login with new password: %v", err)
	}
	if _, err := store.Verify("member", "member123"); err == nil {
		t.Error("old password must be rejected")
	}
	if ver, _ := store.UserVersion("member"); ver != 1 {
		t.Errorf("token_version = %d, want 1", ver)
	}
}

func TestSetPassword_YamlSurgicalWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "users.yaml")
	yaml := `# 保留的注释头
users:
  - username: member
    password_hash: "OLD"
    role: member
    orgSlug: hpc-lab
    clusterUser: ailsmember
    uid: 2003
    gid: 2000
    account: ailsmember
`
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := auth.LoadUserStore(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPassword("member", "NEWHASH"); err != nil {
		t.Fatal(err)
	}
	// 文件被外科式改写：只有 hash 行变，注释保留
	out, _ := os.ReadFile(p)
	s := string(out)
	if !strings.Contains(s, "# 保留的注释头") {
		t.Error("comment header lost in surgical rewrite")
	}
	if strings.Contains(s, `"OLD"`) || !strings.Contains(s, "NEWHASH") {
		t.Errorf("hash not replaced:\n%s", s)
	}
	// 重新加载生效
	st2, _ := auth.LoadUserStore(p)
	if u, ok := st2.Lookup("member"); !ok || u.PasswordHash != "NEWHASH" {
		t.Errorf("reload: hash=%q ok=%v", u.PasswordHash, ok)
	}
}
