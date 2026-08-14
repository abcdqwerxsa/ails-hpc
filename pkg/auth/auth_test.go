package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ails-hpc/pkg/auth"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func init() {
	// 测试包统一注入签名密钥
	auth.SetSecret([]byte("test-secret-key"))
}

func hashPw(t *testing.T, pw string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	return string(h)
}

func newTestStore(t *testing.T) auth.UserStore {
	return auth.NewUserStoreFromList([]auth.User{
		{Username: "admin", PasswordHash: hashPw(t, "admin123"), Role: "admin", OrgSlug: "hpc-lab", TenantNS: "default", ClusterUser: "ailsadmin", Account: "ailsadmin"},
		{Username: "member", PasswordHash: hashPw(t, "member123"), Role: "member", OrgSlug: "hpc-lab", TenantNS: "default", ClusterUser: "ailsmember", Account: "ailsmember"},
	})
}

func TestUserStore_Verify(t *testing.T) {
	store := newTestStore(t)

	u, err := store.Verify("admin", "admin123")
	if err != nil || u == nil || u.Role != "admin" {
		t.Fatalf("expected admin verify success, got u=%+v err=%v", u, err)
	}

	if _, err := store.Verify("admin", "wrong"); err != auth.ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for wrong password, got %v", err)
	}

	// 未知用户必须返回同一错误，避免用户名枚举
	if _, err := store.Verify("ghost", "whatever"); err != auth.ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for unknown user, got %v", err)
	}
}

func TestJWT_RoundTrip_AndExpiry(t *testing.T) {
	tok, err := auth.GenerateToken("admin", "admin", "hpc-lab", "default", "ailsadmin", "ailsadmin")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	claims, err := auth.VerifyToken(tok)
	if err != nil || claims.Role != "admin" {
		t.Fatalf("verify valid token: claims=%+v err=%v", claims, err)
	}

	// 过期令牌必须被拒
	expired, _ := auth.GenerateTokenWithTTL("admin", "admin", "hpc-lab", "default", "ailsadmin", "ailsadmin", -1*time.Hour)
	if _, err := auth.VerifyToken(expired); err == nil {
		t.Fatalf("expected error for expired token, got nil")
	}

	// 篡改签名必须被拒
	tampered := tok[:len(tok)-2] + "AA"
	if _, err := auth.VerifyToken(tampered); err == nil {
		t.Fatalf("expected error for tampered token, got nil")
	}
}

// TestJWTMiddleware_IDECookie 验证 Web-IDE 反代凭证链：
//  1. ?token= 首跳通过并种下 Path 限定 cookie；
//  2. 仅带 cookie 的后续请求（Jupyter 重定向/XHR、code-server 资源/WS）通过；
//  3. 无任何凭证 401；非 /ide/ 路径不接受 query/cookie 兜底。
func TestJWTMiddleware_IDECookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tok, _ := auth.GenerateToken("member", "member", "hpc-lab", "default", "ailsmember", "ailsmember")

	newRouter := func() *gin.Engine {
		r := gin.New()
		r.Use(auth.JWTAuthMiddleware())
		r.Any("/api/v1/ide/:session/*any", func(c *gin.Context) { c.Status(200) })
		r.GET("/api/v1/slurm/nodes", func(c *gin.Context) { c.Status(200) })
		return r
	}

	// 1. ?token= 首跳 → 200 + Set-Cookie
	r := newRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/ide/abc123/?token="+tok, nil)
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("ide first hop with ?token: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "ails_ide_token" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("ide first hop must set ails_ide_token cookie")
	}
	if cookie.Path != "/api/v1/ide/" || !cookie.HttpOnly {
		t.Errorf("cookie scope wrong: Path=%q HttpOnly=%v", cookie.Path, cookie.HttpOnly)
	}

	// 2. 仅 cookie（模拟 Jupyter 302 后的 /lab、XHR、WS）→ 200
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/api/v1/ide/abc123/lab", nil)
	req2.AddCookie(cookie)
	r.ServeHTTP(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("ide subresource with cookie: want 200 got %d", w2.Code)
	}

	// 3. 无凭证 → 401
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/api/v1/ide/abc123/lab", nil)
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusUnauthorized {
		t.Fatalf("ide without credential: want 401 got %d", w3.Code)
	}

	// 4. 非 /ide/ 路径：query/cookie 兜底不得生效
	w4 := httptest.NewRecorder()
	req4, _ := http.NewRequest("GET", "/api/v1/slurm/nodes?token="+tok, nil)
	req4.AddCookie(cookie)
	r.ServeHTTP(w4, req4)
	if w4.Code != http.StatusUnauthorized {
		t.Fatalf("non-ide path must not accept query/cookie fallback: want 401 got %d", w4.Code)
	}
}

func TestLogin_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newTestStore(t)
	handler := auth.NewAuthHandler(store)
	r := gin.New()
	r.POST("/api/v1/auth/login", handler.Login)

	// 成功 → 200 + React 契约 {token, user:{username,role,orgSlug,tenantNs}}
	body := `{"username":"admin","password":"admin123","orgSlug":"hpc-lab"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp auth.LoginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}
	if resp.Token == "" || resp.User.Role != "admin" || resp.User.Username != "admin" {
		t.Fatalf("unexpected login response: %+v", resp)
	}

	// 错误密码 → 401
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"nope"}`))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d", w2.Code)
	}

	// 缺字段 → 400
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin"}`))
	req3.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing password, got %d", w3.Code)
	}
}
