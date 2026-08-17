package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/services/admin"
	"ails-hpc/pkg/store"

	"github.com/gin-gonic/gin"
)

// newRolesFixture 复刻 router.go 的角色管理路由装配（真实 sqlite 库 + 四种子用户）。
// actor claims 不带 Perms → actorPermissions 回退内置映射（与生产中间件刷新语义一致）。
func newRolesFixture(t *testing.T) (*gin.Engine, store.AdminStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auth.SetSecret([]byte("roles-test-secret"))

	stRaw, err := store.Open(t.TempDir() + "/roles.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = stRaw.Close() })
	st, ok := stRaw.(store.AdminStore)
	if !ok {
		t.Fatal("sqlite store must implement AdminStore")
	}

	ctx := context.Background()
	if _, err := st.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := st.CreateTenant(ctx, "bio-lab", ""); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	for _, u := range []store.NewUser{
		{Username: "padmin", Password: "platform123", Role: auth.RoleSystemAdmin, TenantSlug: "system"},
		{Username: "tadmin", Password: "tenant123", Role: auth.RoleTenantAdmin, TenantSlug: "hpc-lab"},
		{Username: "alice", Password: "alice12345", Role: auth.RoleMember, TenantSlug: "hpc-lab"},
		{Username: "biouser", Password: "biouser123", Role: auth.RoleMember, TenantSlug: "bio-lab"},
	} {
		if _, err := st.CreateUser(ctx, u); err != nil {
			t.Fatalf("seed %s: %v", u.Username, err)
		}
	}

	h := admin.NewAdminHandler(admin.NewService(st, &fakeProvision{}))
	r := gin.New()
	setClaims := func(username, role, tid string) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Set("claims", &auth.Claims{Username: username, Role: role, OrgSlug: tid, TID: tid})
			c.Next()
		}
	}
	with := func(username, role, tid string, gate string, h2 gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{setClaims(username, role, tid),
			auth.RequirePermission(gate), h2}
	}
	adminRole := func(username, role, tid, path, method string, h2 gin.HandlerFunc) {
		switch method {
		case http.MethodGet:
			r.GET(path, with(username, role, tid, auth.PermRolesManage, h2)...)
		case http.MethodPost:
			r.POST(path, with(username, role, tid, auth.PermRolesManage, h2)...)
		case http.MethodPatch:
			r.PATCH(path, with(username, role, tid, auth.PermRolesManage, h2)...)
		case http.MethodDelete:
			r.DELETE(path, with(username, role, tid, auth.PermRolesManage, h2)...)
		}
	}
	tenantRole := func(username, role, tid, path, method string, gate string, h2 gin.HandlerFunc) {
		switch method {
		case http.MethodGet:
			r.GET(path, with(username, role, tid, gate, h2)...)
		case http.MethodPost:
			r.POST(path, with(username, role, tid, gate, h2)...)
		case http.MethodPatch:
			r.PATCH(path, with(username, role, tid, gate, h2)...)
		case http.MethodDelete:
			r.DELETE(path, with(username, role, tid, gate, h2)...)
		}
	}

	pa, ta := "padmin", "tadmin"
	sys, hp := "system", "hpc-lab"
	adminRole(pa, auth.RoleSystemAdmin, sys, "/api/v1/admin/roles", http.MethodGet, h.ListPlatformRoles)
	adminRole(pa, auth.RoleSystemAdmin, sys, "/api/v1/admin/roles", http.MethodPost, h.CreatePlatformRole)
	adminRole(pa, auth.RoleSystemAdmin, sys, "/api/v1/admin/roles/:name", http.MethodPatch, h.UpdatePlatformRole)
	adminRole(pa, auth.RoleSystemAdmin, sys, "/api/v1/admin/roles/:name", http.MethodDelete, h.DeletePlatformRole)
	adminRole(pa, auth.RoleSystemAdmin, sys, "/api/v1/admin/users/:username/role", http.MethodPatch, h.AssignPlatformRole)
	r.GET("/api/v1/admin/tenants/:slug/roles",
		setClaims(pa, auth.RoleSystemAdmin, sys),
		auth.RequirePermission(auth.PermRolesManage), h.ListTenantRoles)

	tenantRole(ta, auth.RoleTenantAdmin, hp, "/api/v1/tenants/me/roles", http.MethodGet, auth.PermTenantRolesManage, h.ListMyRoles)
	tenantRole(ta, auth.RoleTenantAdmin, hp, "/api/v1/tenants/me/roles", http.MethodPost, auth.PermTenantRolesManage, h.CreateMyRole)
	tenantRole(ta, auth.RoleTenantAdmin, hp, "/api/v1/tenants/me/roles/:name", http.MethodPatch, auth.PermTenantRolesManage, h.UpdateMyRole)
	tenantRole(ta, auth.RoleTenantAdmin, hp, "/api/v1/tenants/me/roles/:name", http.MethodDelete, auth.PermTenantRolesManage, h.DeleteMyRole)
	tenantRole(ta, auth.RoleTenantAdmin, hp, "/api/v1/tenants/me/users/:username/role", http.MethodPatch, auth.PermTenantUsersManage, h.AssignMyRole)
	return r, st
}

func rolesCall(r *gin.Engine, method, path, body string) (int, map[string]any) {
	var rd *bytes.Reader
	if body != "" {
		rd = bytes.NewReader([]byte(body))
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// TestRoles_CreateSubset 防提权核心：租户角色权限必须是创建者（tenant_admin）权限子集。
func TestRoles_CreateSubset(t *testing.T) {
	r, _ := newRolesFixture(t)

	// 子集（tenant_admin 自身权限的一部分）→ 200
	code, body := rolesCall(r, http.MethodPost, "/api/v1/tenants/me/roles",
		`{"name":"dev","description":"dev role","permissions":["jobs:submit","jobs:control","cluster:read"]}`)
	if code != http.StatusOK {
		t.Fatalf("create subset role: want 200 got %d body=%v", code, body)
	}
	role := body["role"].(map[string]any)
	if role["name"] != "dev" || role["isSystem"] != false || role["userCount"] != float64(0) {
		t.Errorf("unexpected role shape: %v", role)
	}

	// 超出父集（nodes:manage 不属于 tenant_admin）→ 400
	code, body = rolesCall(r, http.MethodPost, "/api/v1/tenants/me/roles",
		`{"name":"esc","permissions":["nodes:manage"]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("escalation create: want 400 got %d body=%v", code, body)
	}

	// 词汇表外权限点 → 400（即使子集规则通过也不可能——词汇表校验先行拦截）
	code, _ = rolesCall(r, http.MethodPost, "/api/v1/tenants/me/roles",
		`{"name":"weird","permissions":["super:power"]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("unknown permission: want 400 got %d", code)
	}
}

// TestRoles_ReservedAndDuplicate 内置名保留（400）与作用域内重名（409）。
func TestRoles_ReservedAndDuplicate(t *testing.T) {
	r, _ := newRolesFixture(t)
	if code, _ := rolesCall(r, http.MethodPost, "/api/v1/tenants/me/roles",
		`{"name":"member","permissions":["cluster:read"]}`); code != http.StatusBadRequest {
		t.Errorf("builtin name: want 400 got %d", code)
	}
	if code, _ := rolesCall(r, http.MethodPost, "/api/v1/tenants/me/roles",
		`{"name":"dev","permissions":["cluster:read"]}`); code != http.StatusOK {
		t.Fatalf("first create: %d", code)
	}
	if code, _ := rolesCall(r, http.MethodPost, "/api/v1/tenants/me/roles",
		`{"name":"dev","permissions":["cluster:read"]}`); code != http.StatusConflict {
		t.Errorf("duplicate name: want 409 got %d", code)
	}
}

// TestRoles_AssignAndLiveEffect 指派后即刻生效：Lookup 的角色面（Role/RoleName/Permissions）
// 反映新角色——中间件按此刷新 claims。
func TestRoles_AssignAndLiveEffect(t *testing.T) {
	r, st := newRolesFixture(t)
	if code, _ := rolesCall(r, http.MethodPost, "/api/v1/tenants/me/roles",
		`{"name":"dev","permissions":["cluster:read","jobs:submit"]}`); code != http.StatusOK {
		t.Fatalf("create: %d", code)
	}
	code, body := rolesCall(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/role", `{"role":"dev"}`)
	if code != http.StatusOK {
		t.Fatalf("assign: want 200 got %d body=%v", code, body)
	}

	alice, ok := st.(store.Store).Lookup("alice")
	if !ok {
		t.Fatal("alice gone")
	}
	if alice.RoleName != "dev" {
		t.Errorf("alice.RoleName = %q, want dev", alice.RoleName)
	}
	if alice.Role != auth.RoleMember {
		t.Errorf("alice.Role(base) = %q, want member", alice.Role)
	}
	permSet := map[string]bool{}
	for _, p := range alice.Permissions {
		permSet[p] = true
	}
	if !permSet[auth.PermJobsSubmit] || permSet[auth.PermJobsControl] {
		t.Errorf("alice.Permissions = %v, want exactly [cluster:read jobs:submit]", alice.Permissions)
	}
}

// TestRoles_SystemImmutable 系统角色不可删改。
func TestRoles_SystemImmutable(t *testing.T) {
	r, _ := newRolesFixture(t)
	if code, _ := rolesCall(r, http.MethodPatch, "/api/v1/admin/roles/member",
		`{"permissions":["cluster:read"]}`); code != http.StatusConflict {
		t.Errorf("update system role: want 409 got %d", code)
	}
	if code, _ := rolesCall(r, http.MethodDelete, "/api/v1/admin/roles/admin", ""); code != http.StatusConflict {
		t.Errorf("delete system role: want 409 got %d", code)
	}
}

// TestRoles_DeleteInUse 在用角色不可删（409）；改派后可删（200）。
func TestRoles_DeleteInUse(t *testing.T) {
	r, _ := newRolesFixture(t)
	rolesCall(r, http.MethodPost, "/api/v1/tenants/me/roles", `{"name":"dev","permissions":["cluster:read"]}`)
	rolesCall(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/role", `{"role":"dev"}`)
	if code, _ := rolesCall(r, http.MethodDelete, "/api/v1/tenants/me/roles/dev", ""); code != http.StatusConflict {
		t.Fatalf("delete in-use: want 409 got %d", code)
	}
	// 改派回内置 member 后可删
	if code, _ := rolesCall(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/role", `{"role":"member"}`); code != http.StatusOK {
		t.Fatalf("reassign to builtin: %d", code)
	}
	if code, _ := rolesCall(r, http.MethodDelete, "/api/v1/tenants/me/roles/dev", ""); code != http.StatusOK {
		t.Fatalf("delete after reassign: want 200 got %d", code)
	}
}

// TestRoles_CrossTenantIsolation 跨租户角色读写 → 404（hpc-lab 的 tadmin 触不到
// bio-lab 的同名角色）；跨租户指派被拒。
func TestRoles_CrossTenantIsolation(t *testing.T) {
	r, st := newRolesFixture(t)
	ctx := context.Background()
	// 直接在 bio-lab 建同名角色（绕过 API——模拟另一租户管理员已建）
	if _, err := st.CreateRole(ctx, store.NewRole{
		Name: "dev", BaseRole: auth.RoleMember, Permissions: []string{auth.PermClusterRead},
		TenantSlug: "bio-lab",
	}); err != nil {
		t.Fatalf("seed bio role: %v", err)
	}

	// hpc-lab 的 tadmin 改 bio-lab 的 dev → 404（作用域内解析不到自己的 dev……先建自己的）
	rolesCall(r, http.MethodPost, "/api/v1/tenants/me/roles", `{"name":"dev","permissions":["cluster:read"]}`)
	// 删自己的 dev 后，再试删/改 → 404（只剩 bio-lab 的同名角色，作用域隔离）
	rolesCall(r, http.MethodDelete, "/api/v1/tenants/me/roles/dev", "")
	if code, _ := rolesCall(r, http.MethodDelete, "/api/v1/tenants/me/roles/dev", ""); code != http.StatusNotFound {
		t.Errorf("delete cross-tenant same-name role: want 404 got %d", code)
	}
	if code, _ := rolesCall(r, http.MethodPatch, "/api/v1/tenants/me/roles/dev", `{"permissions":[]}`); code != http.StatusNotFound {
		t.Errorf("update cross-tenant role: want 404 got %d", code)
	}
	// 把 bio-lab 用户指派到 hpc-lab 侧解析的角色：member 为内置角色可解析，但目标用户
	// 不在 tadmin 的租户 → 不可成功（404：UpdateMyUser 式成员校验不在本路径，依赖
	// SetUserRole 的归属/存在校验兜底——非 200 即达标）
	code, _ := rolesCall(r, http.MethodPatch, "/api/v1/tenants/me/users/biouser/role", `{"role":"member"}`)
	if code == http.StatusOK {
		// 内置 member 角色对任何租户合法——但 biouser 属 bio-lab：tadmin 仍能改派它？
		// 不应能（越权改外租户用户）。断言必须拒绝。
		t.Errorf("cross-tenant user reassign via builtin role must not succeed, got 200")
	}
}

// TestRoles_PlatformAdminSubset 平台侧同理：admin 不能授予自己没有的权限
//（admin 无 jobs:submit——纯监控角色）。
func TestRoles_PlatformAdminSubset(t *testing.T) {
	r, _ := newRolesFixture(t)
	code, _ := rolesCall(r, http.MethodPost, "/api/v1/admin/roles",
		`{"name":"auditor","permissions":["audit:read","tenants:read"]}`)
	if code != http.StatusOK {
		t.Fatalf("platform subset role: want 200 got %d", code)
	}
	code, body := rolesCall(r, http.MethodPost, "/api/v1/admin/roles",
		`{"name":"jobadmin","permissions":["jobs:submit"]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("admin granting jobs:submit: want 400 got %d body=%v", code, body)
	}
}

// TestRoles_MemberGate member 无角色管理权限点 → 403（路由门）。
func TestRoles_MemberGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auth.SetSecret([]byte("gate-test"))
	// 最小路由：仅证明权限门对 member 关闭
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		c.Set("claims", &auth.Claims{Username: "alice", Role: auth.RoleMember})
		c.Next()
	}, auth.RequirePermission(auth.PermTenantRolesManage), func(c *gin.Context) { c.Status(200) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("member on tenant:roles:manage: want 403 got %d", w.Code)
	}
}
