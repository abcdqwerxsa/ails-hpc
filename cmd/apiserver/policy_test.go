package main

// A1 密码与会话策略端到端：复杂度、历史 N 次不可重用、强制改密门、会话台账与
// 全设备登出。全栈夹具（真路由 + sqlite）。

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"ails-hpc/pkg/auth"

	"github.com/gin-gonic/gin"
)

// TestPolicy_PasswordComplexity 复杂度策略：弱密码 400，强密码 200。
func TestPolicy_PasswordComplexity(t *testing.T) {
	r, _ := setupRBACStack(t)
	tok := loginViaAPI(t, r, "alice", "alice12345")

	try := func(np string) int {
		body := `{"oldPassword":"alice12345","newPassword":` + quote(np) + `}`
		w := doAuthRaw(r, http.MethodPost, "/api/v1/auth/password", body, tok)
		return w
	}
	for _, weak := range []string{"Ab1!", "alllowercase1!", "ALLUPPER1!", "NoDigits!!", "NoSymbol11a"} {
		if c := try(weak); c != http.StatusBadRequest {
			t.Errorf("weak password %q: want 400 got %d", weak, c)
		}
	}
	if c := try("Strong#Pass1"); c != http.StatusOK {
		t.Errorf("strong password: want 200 got %d", c)
	}
}

// TestPolicy_PasswordHistory 历史 N 次不可重用：改密后改回旧密码 → 400。
func TestPolicy_PasswordHistory(t *testing.T) {
	r, _ := setupRBACStack(t)
	// 第一轮：alice12345 → New#Pass1
	tok1 := loginViaAPI(t, r, "alice", "alice12345")
	if c := doAuthRaw(r, http.MethodPost, "/api/v1/auth/password",
		`{"oldPassword":"alice12345","newPassword":"New#Pass1"}`, tok1); c != http.StatusOK {
		t.Fatalf("first change: %d", c)
	}
	// 第二轮：New#Pass1 → Second#Pass2
	tok2 := loginViaAPI(t, r, "alice", "New#Pass1")
	if c := doAuthRaw(r, http.MethodPost, "/api/v1/auth/password",
		`{"oldPassword":"New#Pass1","newPassword":"Second#Pass2"}`, tok2); c != http.StatusOK {
		t.Fatalf("second change: %d", c)
	}
	// 第三轮：试图改回 alice12345（历史第 2 条）→ 400
	tok3 := loginViaAPI(t, r, "alice", "Second#Pass2")
	if c := doAuthRaw(r, http.MethodPost, "/api/v1/auth/password",
		`{"oldPassword":"Second#Pass2","newPassword":"alice12345"}`, tok3); c != http.StatusBadRequest {
		t.Fatalf("reuse old password: want 400 got %d", c)
	}
	// 试图改回 New#Pass1（历史第 1 条）→ 400
	if c := doAuthRaw(r, http.MethodPost, "/api/v1/auth/password",
		`{"oldPassword":"Second#Pass2","newPassword":"New#Pass1"}`, tok3); c != http.StatusBadRequest {
		t.Fatalf("reuse recent password: want 400 got %d", c)
	}
	// 全新密码 → 200
	if c := doAuthRaw(r, http.MethodPost, "/api/v1/auth/password",
		`{"oldPassword":"Second#Pass2","newPassword":"Third#Pass3"}`, tok3); c != http.StatusOK {
		t.Fatalf("fresh password: want 200 got %d", c)
	}
}

// TestPolicy_MustChangeGate 强制改密门：被管理员重置后，业务端点 403（code=
// must_change_password），仅自助面放行；改密成功后恢复。
func TestPolicy_MustChangeGate(t *testing.T) {
	r, st := setupRBACStack(t)
	ctx := context.Background()
	tadmin := loginViaAPI(t, r, "tadmin", "tenant12345")

	// 管理员重置 alice 密码 → must_change_password=1
	if c, b := doAuth(r, http.MethodPost, "/api/v1/tenants/me/users/alice/password",
		`{"newPassword":"Reset#Pass1"}`, tadmin); c != http.StatusOK {
		t.Fatalf("admin reset: %d %s", c, b)
	}
	alice := loginViaAPI(t, r, "alice", "Reset#Pass1")

	// 业务端点 → 403 + code
	code, body := doAuth(r, http.MethodGet, "/api/v1/slurm/nodes", "", alice)
	if code != http.StatusForbidden || !strings.Contains(body, "must_change_password") {
		t.Fatalf("gated node read: %d %s", code, body)
	}
	if c, _ := doAuth(r, http.MethodPost, "/api/v1/slurm/jobs/submit",
		`{"name":"x","script":"echo hi"}`, alice); c != http.StatusForbidden {
		t.Fatalf("gated submit: want 403 got %d", c)
	}
	// 自助面放行：/auth/me、/auth/me/sessions、/auth/password
	if c, b := doAuth(r, http.MethodGet, "/api/v1/auth/me", "", alice); c != http.StatusOK || !strings.Contains(b, `"mustChangePassword":true`) {
		t.Fatalf("me during gate: %d %s", c, b)
	}
	if c, _ := doAuth(r, http.MethodGet, "/api/v1/auth/me/sessions", "", alice); c != http.StatusOK {
		t.Errorf("sessions during gate: %d", c)
	}
	// 改密成功 → 门开
	if c := doAuthRaw(r, http.MethodPost, "/api/v1/auth/password",
		`{"oldPassword":"Reset#Pass1","newPassword":"Fresh#Pass9"}`, alice); c != http.StatusOK {
		t.Fatalf("change during gate: %d", c)
	}
	// 重新登录后业务恢复
	alice2 := loginViaAPI(t, r, "alice", "Fresh#Pass9")
	if c, _ := doAuth(r, http.MethodGet, "/api/v1/slurm/nodes", "", alice2); c != http.StatusOK {
		t.Errorf("after change, node read: %d", c)
	}
	_ = st
	_ = ctx
}

// TestPolicy_SessionsAndLogoutAll 会话台账与全设备登出。
func TestPolicy_SessionsAndLogoutAll(t *testing.T) {
	r, _ := setupRBACStack(t)
	// 两次登录 = 两条会话（token 都有效）
	tok1 := loginViaAPI(t, r, "alice", "alice12345")
	tok2 := loginViaAPI(t, r, "alice", "alice12345")

	// 会话清单可见两条
	_, body := doAuth(r, http.MethodGet, "/api/v1/auth/me/sessions", "", tok1)
	if !strings.Contains(body, `"sessions"`) || strings.Count(body, `"id"`) < 2 {
		t.Fatalf("sessions list: %s", body)
	}
	// 两个 token 都能读节点
	if c := doRequest(r, http.MethodGet, "/api/v1/slurm/nodes", "", tok2); c != http.StatusOK {
		t.Fatalf("second token pre-logout: %d", c)
	}
	// 全设备登出（用 tok1）→ tok1/tok2 都失效（401）
	if c, _ := doAuth(r, http.MethodPost, "/api/v1/auth/logout-all", "", tok1); c != http.StatusOK {
		t.Fatalf("logout-all: %d", c)
	}
	if c := doRequest(r, http.MethodGet, "/api/v1/slurm/nodes", "", tok1); c != http.StatusUnauthorized {
		t.Errorf("tok1 after logout-all: want 401 got %d", c)
	}
	if c := doRequest(r, http.MethodGet, "/api/v1/slurm/nodes", "", tok2); c != http.StatusUnauthorized {
		t.Errorf("tok2 after logout-all: want 401 got %d", c)
	}
}

// TestPolicy_CreateUserForceChange API 建户初始密码强制首登改密（tadmin 建 bob）。
func TestPolicy_CreateUserForceChange(t *testing.T) {
	r, st := setupRBACStack(t)
	tadmin := loginViaAPI(t, r, "tadmin", "tenant12345")

	if c, b := doAuth(r, http.MethodPost, "/api/v1/tenants/me/users",
		`{"username":"newbie","password":"Init#Pass1","role":"member"}`, tadmin); c != http.StatusOK {
		t.Fatalf("create user: %d %s", c, b)
	}
	u, ok := st.(interface {
		Lookup(string) (*auth.User, bool)
	}).Lookup("newbie")
	if !ok || !u.MustChangePassword {
		t.Fatalf("new user must have must_change_password=1")
	}
	// 新用户登录即被门拦
	tok := loginViaAPI(t, r, "newbie", "Init#Pass1")
	if c := doRequest(r, http.MethodGet, "/api/v1/slurm/nodes", "", tok); c != http.StatusForbidden {
		t.Errorf("newbie gated: want 403 got %d", c)
	}
}

// doAuthRaw 是 doAuth 的状态码形态。
func doAuthRaw(r *gin.Engine, method, path, body, token string) int {
	code, _ := doAuth(r, method, path, body, token)
	return code
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	b.WriteString(s)
	b.WriteByte('"')
	return b.String()
}
