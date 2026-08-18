package auth

// OIDC 单元/内部测试：PKCE、state 一次性、ID token 验签（RS256/ES256）、
// 关联令牌、用户名收敛。fake IdP 是 httptest 服务器（discovery/token/jwks）。

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// fakeIdP 是最小 OIDC 提供方（RS256 签 id_token；token 端点回显收到的 code_verifier 供断言）。
type fakeIdP struct {
	srv          *httptest.Server
	key          *rsa.PrivateKey
	kid          string
	issuer       string
	LastVerifier string
	Claims       map[string]any // 注入 id_token 的额外 claim
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	f := &fakeIdP{key: key, kid: "test-kid-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 f.srv.URL,
			"authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint":         f.srv.URL + "/token",
			"jwks_uri":               f.srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		e := base64.RawURLEncoding.EncodeToString(bigToBytes(65537))
		n := base64.RawURLEncoding.EncodeToString(f.key.N.Bytes())
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{
				{"kty": "RSA", "kid": f.kid, "alg": "RS256", "use": "sig", "n": n, "e": e},
			},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.LastVerifier = r.PostFormValue("code_verifier")
		tok := f.signIDToken(t, map[string]any{
			"iss":                f.srv.URL,
			"aud":                cfgClientID,
			"sub":                "sub-12345",
			"exp":                time.Now().Add(5 * time.Minute).Unix(),
			"preferred_username": "sso.user@example.com",
			"email":              "sso.user@example.com",
			"name":               "SSO User",
		})
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "id_token": tok, "token_type": "Bearer", "expires_in": 300,
		})
	})
	f.srv = httptest.NewServer(mux)
	f.issuer = f.srv.URL
	t.Cleanup(f.srv.Close)
	return f
}

const cfgClientID = "ails-portal"

func (f *fakeIdP) signIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	for k, v := range f.Claims {
		claims[k] = v // 注入 claim 覆盖默认值（测试可改写 sub/preferred_username 等）
	}
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": f.kid})
	pl, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(pl)
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func bigToBytes(i int64) []byte {
	return []byte{byte(i >> 24), byte(i >> 16), byte(i >> 8), byte(i)}
}

// newTestOIDCClient 用 fake IdP 装配进程级客户端。
func newTestOIDCClient(t *testing.T, f *fakeIdP) *OIDCClient {
	t.Helper()
	SetSecret([]byte("oidc-test-secret"))
	cl := NewOIDCClient(OIDCConfig{
		Issuer: f.srv.URL, ClientID: cfgClientID, ClientSecret: "s3cret",
		RedirectURL: "http://portal.example.com/api/v1/auth/oidc/callback",
	})
	SetOIDCClient(cl)
	t.Cleanup(func() { SetOIDCClient(nil) })
	return cl
}

func TestPKCE_S256(t *testing.T) {
	v, c, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(v) < 43 || len(v) > 128 {
		t.Errorf("verifier length %d out of RFC7636 range", len(v))
	}
	if c != PKCEChallenge(v) {
		t.Error("challenge must be S256(verifier)")
	}
	if strings.Contains(c, "+") || strings.Contains(c, "/") {
		t.Error("challenge must be base64url")
	}
}

func TestStateStore_OneShot(t *testing.T) {
	PutState("s1", &oidcSession{verifier: "v1"})
	got, ok := TakeState("s1")
	if !ok || got.verifier != "v1" {
		t.Fatalf("first take: ok=%v verifier=%q", ok, got.verifier)
	}
	if _, ok := TakeState("s1"); ok {
		t.Error("state must be single-use (replay must fail)")
	}
	// 过期态
	PutState("s2", &oidcSession{verifier: "v", issuedAt: time.Now().Add(-11 * time.Minute)})
	if _, ok := TakeState("s2"); ok {
		t.Error("expired state must be rejected")
	}
}

func TestLinkToken_MintVerify(t *testing.T) {
	SetSecret([]byte("link-secret"))
	tok, err := MintLinkToken("sub-1", "alice")
	if err != nil {
		t.Fatal(err)
	}
	sub, user, err := VerifyLinkToken(tok)
	if err != nil || sub != "sub-1" || user != "alice" {
		t.Fatalf("verify: %v %q %q", err, sub, user)
	}
	// 篡改
	if _, _, err := VerifyLinkToken(tok + "x"); err == nil {
		t.Error("tampered link token must fail")
	}
	if _, _, err := VerifyLinkToken("garbage"); err == nil {
		t.Error("garbage link token must fail")
	}
}

func TestSanitizeUsername(t *testing.T) {
	cases := map[string]string{
		"Zhang.San@Example.com": "zhang_san",
		"  -lisi- ":             "lisi-",
		"wangwu!#":              "wangwu",
		"123dev":                "u123dev",
		"":                      "",
		"---":                   "",
	}
	for in, want := range cases {
		if got := sanitizeUsername(in); got != want {
			t.Errorf("sanitizeUsername(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOIDCClient_VerifyIDToken(t *testing.T) {
	f := newFakeIdP(t)
	cl := newTestOIDCClient(t, f)

	tok := f.signIDToken(t, map[string]any{
		"iss": f.srv.URL, "aud": cfgClientID, "sub": "sub-9",
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	idc, err := cl.VerifyIDToken(tok)
	if err != nil {
		t.Fatalf("verify ok-token: %v", err)
	}
	if idc.Sub != "sub-9" {
		t.Errorf("sub = %q", idc.Sub)
	}
	// 错误签发方
	bad := f.signIDToken(t, map[string]any{
		"iss": "https://evil.example.com", "aud": cfgClientID, "sub": "s",
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	if _, err := cl.VerifyIDToken(bad); err == nil {
		t.Error("iss mismatch must fail")
	}
	// 过期
	expd := f.signIDToken(t, map[string]any{
		"iss": f.srv.URL, "aud": cfgClientID, "sub": "s",
		"exp": time.Now().Add(-time.Minute).Unix(),
	})
	if _, err := cl.VerifyIDToken(expd); err == nil {
		t.Error("expired token must fail")
	}
	// aud 不符
	aud := f.signIDToken(t, map[string]any{
		"iss": f.srv.URL, "aud": "other-client", "sub": "s",
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	if _, err := cl.VerifyIDToken(aud); err == nil {
		t.Error("aud mismatch must fail")
	}
	// 篡改载荷
	parts := strings.Split(tok, ".")
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"evil"}`)) + "." + parts[2]
	if _, err := cl.VerifyIDToken(tampered); err == nil {
		t.Error("tampered payload must fail")
	}
}

// TestOIDC_ParseRoles claim 解析兼容数组/逗号串/对象数组。
func TestOIDC_ParseRoles(t *testing.T) {
	mk := func(raw string) *IDTokenClaims {
		var cl IDTokenClaims
		_ = json.Unmarshal([]byte(raw), &cl.raw)
		return &cl
	}
	if got := mk(`{"roles":["a","b"]}`).ParseRoles("roles"); len(got) != 2 {
		t.Errorf("array form: %v", got)
	}
	if got := mk(`{"groups":"x, y"}`).ParseRoles("groups"); len(got) != 2 || got[0] != "x" {
		t.Errorf("csv form: %v", got)
	}
	if got := mk(`{"roles":[{"name":"r1"},{"value":"r2"}]}`).ParseRoles("roles"); len(got) != 2 {
		t.Errorf("object form: %v", got)
	}
	if got := mk(`{"other":1}`).ParseRoles("roles"); got != nil {
		t.Errorf("missing claim: %v", got)
	}
}

// TestOIDC_LoginRedirect PKCE 参数与 state 进 authorize URL。
func TestOIDC_LoginRedirect(t *testing.T) {
	f := newFakeIdP(t)
	newTestOIDCClient(t, f)
	store := NewUserStoreFromList([]User{{Username: "u1", Role: RoleMember, TenantSlug: "t"}})
	h := NewOIDCHandler(store, nil, OIDCMappingConfig{})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/auth/oidc/login", h.Login)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("login: want 302 got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	for _, want := range []string{"code_challenge=", "code_challenge_method=S256", "state=", "response_type=code"} {
		if !strings.Contains(loc, want) {
			t.Errorf("authorize URL missing %s: %s", want, loc)
		}
	}
}

// TestOIDC_ConfigEndpoint 公开配置端点（前端 S3 按钮）。
func TestOIDC_ConfigEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newFakeIdP(t)
	newTestOIDCClient(t, f)
	store := NewUserStoreFromList(nil)
	h := NewOIDCHandler(store, nil, OIDCMappingConfig{})
	r := gin.New()
	r.GET("/config", h.Config)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/config", nil))
	if !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Errorf("enabled config: %s", w.Body.String())
	}

	SetOIDCClient(nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/config", nil))
	if !strings.Contains(w2.Body.String(), `"enabled":false`) {
		t.Errorf("disabled config: %s", w2.Body.String())
	}
}

// fakeProvisioner 记录调用的 OIDC 写面假实现。store 非空时同步落用户
// （issueAndRedirect 的活体 Lookup 需要）。
type fakeProvisioner struct {
	linked      map[string]string // username → sub
	provisioned []string
	failLink    bool
	store       UserStore
}

func (p *fakeProvisioner) UserByOIDCSub(sub string) (*User, bool) {
	for name, s := range p.linked {
		if s == sub {
			return &User{Username: name, Role: RoleMember, TenantSlug: "hpc-lab", Status: "active"}, true
		}
	}
	return nil, false
}
func (p *fakeProvisioner) LinkOIDC(username, sub string) error {
	if p.failLink {
		return fmt.Errorf("boom")
	}
	if p.linked == nil {
		p.linked = map[string]string{}
	}
	p.linked[username] = sub
	return nil
}
func (p *fakeProvisioner) UnlinkOIDC(username string) error {
	delete(p.linked, username)
	return nil
}
func (p *fakeProvisioner) ProvisionOIDCUser(username, email, displayName, roleName, tenantSlug, sub string) (*User, error) {
	p.provisioned = append(p.provisioned, username+"/"+roleName+"@"+tenantSlug)
	if p.linked == nil {
		p.linked = map[string]string{}
	}
	p.linked[username] = sub
	u := &User{Username: username, Role: RoleMember, TenantSlug: tenantSlug,
		OrgSlug: tenantSlug, ClusterUser: username, Account: username,
		Status: "active", OIDCSub: sub}
	if p.store != nil {
		if s, ok := p.store.(*userStoreImpl); ok {
			s.users[username] = u
		}
	}
	return u, nil
}
