package auth_test

// T1 个人 API token 全链路（auth 层，假 PATManager + 内存用户库）：
// 签发（明文一次性/配额）→ PAT 认证（/auth/me 通过、权限点生效）→ 吊销即失效 →
// 过期拒绝 → 锁改密期间 403。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ails-hpc/pkg/auth"

	"github.com/gin-gonic/gin"
)

// patFakeStore 内存用户库 + 内存 PAT 表（实现 auth.PATManager）。
type patFakeStore struct {
	auth.UserStore
	tokens  map[int64]*auth.PATRecord
	hashes  map[string]int64 // tokenHash → id
	infos   map[int64]auth.PATInfo
	nextID  int64
	touched int
	quota   int // 0=不限
}

func newPatFakeStore(t *testing.T, users []auth.User) *patFakeStore {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auth.SetSecret([]byte("pat-test-secret-32-bytes-aaaa"))
	return &patFakeStore{
		UserStore: auth.NewUserStoreFromList(users),
		tokens:    map[int64]*auth.PATRecord{},
		hashes:    map[string]int64{},
		infos:     map[int64]auth.PATInfo{},
		nextID:    1,
	}
}

func (f *patFakeStore) CreateAPIToken(_ context.Context, username, name, hash, prefix, expires string) (int64, error) {
	if f.quota > 0 && len(f.tokens) >= f.quota {
		return 0, auth.ErrTokenQuota
	}
	id := f.nextID
	f.nextID++
	f.tokens[id] = &auth.PATRecord{ID: id, Username: username, ExpiresAt: expires}
	f.hashes[hash] = id
	f.infos[id] = auth.PATInfo{ID: id, Name: name, Prefix: prefix, CreatedAt: "now"}
	return id, nil
}

func (f *patFakeStore) ListAPITokens(_ context.Context, username string) ([]auth.PATInfo, error) {
	out := []auth.PATInfo{}
	for _, rec := range f.tokens {
		if rec.Username == username {
			info := f.infos[rec.ID]
			info.Revoked = rec.Revoked
			out = append(out, info)
		}
	}
	return out, nil
}

func (f *patFakeStore) RevokeAPIToken(_ context.Context, username string, id int64) error {
	rec, ok := f.tokens[id]
	if !ok || rec.Username != username || rec.Revoked {
		return fmt.Errorf("not found")
	}
	rec.Revoked = true
	return nil
}

func (f *patFakeStore) LookupAPIToken(hash string) (auth.PATRecord, error) {
	id, ok := f.hashes[hash]
	if !ok {
		return auth.PATRecord{}, fmt.Errorf("not found")
	}
	return *f.tokens[id], nil
}

func (f *patFakeStore) TouchAPIToken(id int64) error { f.touched++; return nil }

// patFixture：登录（拿 JWT 管理令牌）+ PAT 中间件路由 + /auth/me。
func patFixture(t *testing.T, users []auth.User) (*gin.Engine, *patFakeStore) {
	hash, err := auth.BcryptGenerateFromPassword("x")
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	for i := range users {
		users[i].PasswordHash = hash
	}
	st := newPatFakeStore(t, users)
	h := auth.NewAuthHandlerNoRate(st)
	r := gin.New()
	r.POST("/api/v1/auth/login", h.Login)
	grp := r.Group("/api/v1", auth.JWTAuthMiddlewareWithStore(st))
	{
		grp.GET("/auth/me", h.Me)
		grp.POST("/auth/tokens", h.CreateAPIToken)
		grp.GET("/auth/tokens", h.ListAPITokens)
		grp.DELETE("/auth/tokens/:id", h.RevokeAPIToken)
		grp.GET("/probe", func(c *gin.Context) { c.Status(200) }) // 业务端点代表（must-change 门）
	}
	return r, st
}

func patLogin(t *testing.T, r *gin.Engine, user, pass string) string {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"`+user+`","password":"`+pass+`"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var out struct{ Token string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Token
}

func patCall(r *gin.Engine, method, path, token string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	return w
}

func TestPAT_FullLifecycle(t *testing.T) {
	r, st := patFixture(t, []auth.User{
		{Username: "alice", Role: auth.RoleMember, TenantSlug: "hpc-lab", ClusterUser: "alice", Status: "active"},
	})
	jwt := patLogin(t, r, "alice", "x")

	// 1) 签发：明文只出现一次 + 前缀形态
	w := patCall(r, "POST", "/api/v1/auth/tokens", jwt)
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
		Name  string `json:"name"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if !strings.HasPrefix(created.Token, "ailspat_") || len(created.Token) < 40 {
		t.Fatalf("token shape: %q", created.Token)
	}
	if created.Name != "api-token" { // 空名 → 默认
		t.Fatalf("default name: %q", created.Name)
	}

	// 2) PAT 调业务端点：/auth/me 通过且身份正确
	w = patCall(r, "GET", "/api/v1/auth/me", created.Token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "alice") {
		t.Fatalf("PAT /auth/me: %d %s", w.Code, w.Body.String())
	}
	if st.touched == 0 {
		t.Error("last_used not touched")
	}

	// 3) 列表：无明文/哈希泄漏
	w = patCall(r, "GET", "/api/v1/auth/tokens", created.Token)
	if w.Code != 200 || strings.Contains(w.Body.String(), created.Token[8:]) {
		t.Fatalf("list leaks plaintext: %d %s", w.Code, w.Body.String())
	}

	// 4) 吊销 → 即刻 401
	if w := patCall(r, "DELETE", fmt.Sprintf("/api/v1/auth/tokens/%d", created.ID), created.Token); w.Code != 200 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	if w := patCall(r, "GET", "/api/v1/auth/me", created.Token); w.Code != 401 {
		t.Fatalf("revoked PAT still valid: %d", w.Code)
	}

	// 5) 伪造/未知令牌 → 401
	if w := patCall(r, "GET", "/api/v1/auth/me", "ailspat_forged"); w.Code != 401 {
		t.Fatalf("forged PAT: %d", w.Code)
	}
}

func TestPAT_QuotaAndExpiry(t *testing.T) {
	r, st := patFixture(t, []auth.User{
		{Username: "bob", Role: auth.RoleMember, TenantSlug: "hpc-lab", Status: "active"},
	})
	st.quota = 1
	tok := patLogin(t, r, "bob", "x")
	if w := patCall(r, "POST", "/api/v1/auth/tokens", tok); w.Code != 200 {
		t.Fatalf("first create: %d", w.Code)
	}
	if w := patCall(r, "POST", "/api/v1/auth/tokens", tok); w.Code != 409 {
		t.Fatalf("quota: want 409 got %d", w.Code)
	}

	// 过期令牌：放宽配额再签一枚，把库内记录改为过期 → 即刻 401
	st.quota = 2
	var second struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if w := patCall(r, "POST", "/api/v1/auth/tokens", tok); w.Code != 200 {
		t.Fatalf("second create: %d %s", w.Code, w.Body.String())
	} else {
		_ = json.Unmarshal(w.Body.Bytes(), &second)
	}
	st.tokens[second.ID].ExpiresAt = time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")
	if w := patCall(r, "GET", "/api/v1/auth/me", second.Token); w.Code != 401 {
		t.Fatalf("expired PAT: %d", w.Code)
	}
}

func TestPAT_MustChangeBlocked(t *testing.T) {
	r, _ := patFixture(t, []auth.User{
		{Username: "carol", Role: auth.RoleMember, TenantSlug: "hpc-lab", Status: "active", MustChangePassword: true},
	})
	jwt := patLogin(t, r, "carol", "x")
	// 锁改密期间：/auth/me 放行，但签发令牌（业务端点）被 403
	if w := patCall(r, "GET", "/api/v1/auth/me", jwt); w.Code != 200 {
		t.Fatalf("me during lock: %d", w.Code)
	}
	if w := patCall(r, "POST", "/api/v1/auth/tokens", jwt); w.Code != 403 {
		t.Fatalf("token create during must-change: want 403 got %d", w.Code)
	}
}
