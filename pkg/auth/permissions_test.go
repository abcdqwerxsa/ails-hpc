package auth

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// R1 等价性证明：把替换前的 RequireRole 路由矩阵（历史事实）与替换后的路由→权限点
// 映射逐条对照——每个路由上，持有权限点的角色集合必须与历史上被 RequireRole 放行的
// 角色集合完全一致。任何一侧漂移（新增权限点忘配角色 / 角色权限越界）都会被捕获。

// historicRouteRoles 是 RequireRole 时代的路由 → 放行角色矩阵（从旧 router.go 摘录，
// 不随实现演进——它锁定的就是"过去"）。anyAuth = 所有已认证角色（无角色门）。
var historicRouteRoles = map[string][]string{
	"GET  /slurm/nodes":                         {anyAuth},
	"GET  /slurm/jobs":                          {anyAuth},
	"GET  /slurm/jobs/:id/detail":               {anyAuth},
	"GET  /slurm/jobs/history":                  {anyAuth},
	"GET  /slurm/partitions":                    {anyAuth},
	"GET  /slurm/monitor/snapshot":              {anyAuth},
	"GET  /slurm/monitor/history":               {anyAuth},
	"POST /slurm/nodes/:name/state":             {RoleSystemAdmin},
	"POST /slurm/jobs/submit":                   {RoleMember, RoleTenantAdmin},
	"POST /slurm/jobs/:id/cancel":               {RoleMember, RoleTenantAdmin},
	"POST /slurm/jobs/:id/hold":                 {RoleMember, RoleTenantAdmin},
	"POST /slurm/jobs/:id/requeue":              {RoleMember, RoleTenantAdmin},
	"POST /slurm/containers/launch":             {RoleMember, RoleTenantAdmin},
	"GET  /slurm/containers/list":               {RoleMember, RoleTenantAdmin},
	"DELETE /slurm/containers/:id":              {RoleMember, RoleTenantAdmin},
	"POST /slurm/containers/:id/extend":         {RoleMember, RoleTenantAdmin},
	"ANY  /ide/:session/*any":                   {RoleMember, RoleTenantAdmin},
	"GET  /slurm/billing/usage":                 {RoleMember, RoleTenantAdmin, RoleOpsAdmin},
	"GET  /slurm/billing/export":                {RoleMember, RoleTenantAdmin, RoleOpsAdmin},
	"GET  /admin/tenants":                       {RoleSystemAdmin},
	"POST /admin/tenants":                       {RoleSystemAdmin},
	"PATCH /admin/tenants/:slug":                {RoleSystemAdmin},
	"GET  /admin/tenants/:slug/users":           {RoleSystemAdmin},
	"POST /admin/users":                         {RoleSystemAdmin},
	"GET  /admin/audit":                         {RoleSystemAdmin},
	"GET  /admin/reservations":                  {RoleSystemAdmin},
	"POST /admin/reservations":                  {RoleSystemAdmin},
	"DELETE /admin/reservations/:name":          {RoleSystemAdmin},
	"GET  /admin/qos":                           {RoleSystemAdmin},
	"POST /admin/qos":                           {RoleSystemAdmin},
	"PATCH /admin/tenants/:slug/qos":            {RoleSystemAdmin},
	"GET  /tenants/me/users":                    {RoleTenantAdmin},
	"POST /tenants/me/users":                    {RoleTenantAdmin},
	"PATCH /tenants/me/users/:username":         {RoleTenantAdmin},
	"POST /tenants/me/users/:username/password": {RoleTenantAdmin},
}

// routePermissions 是 R1 后的路由 → 权限点映射（与 cmd/apiserver/router.go 装配一致；
// 漂移时本测试与 router_test.go 的矩阵测试双重捕获）。
var routePermissions = map[string][]string{
	"GET  /slurm/nodes":                         {PermClusterRead},
	"GET  /slurm/jobs":                          {PermClusterRead},
	"GET  /slurm/jobs/:id/detail":               {PermClusterRead},
	"GET  /slurm/jobs/history":                  {PermClusterRead},
	"GET  /slurm/partitions":                    {PermClusterRead},
	"GET  /slurm/monitor/snapshot":              {PermClusterRead},
	"GET  /slurm/monitor/history":               {PermClusterRead},
	"POST /slurm/nodes/:name/state":             {PermNodesManage},
	"POST /slurm/jobs/submit":                   {PermJobsSubmit},
	"POST /slurm/jobs/:id/cancel":               {PermJobsControl},
	"POST /slurm/jobs/:id/hold":                 {PermJobsControl},
	"POST /slurm/jobs/:id/requeue":              {PermJobsControl},
	"POST /slurm/containers/launch":             {PermIdeManage},
	"GET  /slurm/containers/list":               {PermIdeList},
	"DELETE /slurm/containers/:id":              {PermIdeManage},
	"POST /slurm/containers/:id/extend":         {PermIdeManage},
	"ANY  /ide/:session/*any":                   {PermIdeManage},
	"GET  /slurm/billing/usage":                 {PermBillingRead},
	"GET  /slurm/billing/export":                {PermBillingRead},
	"GET  /admin/tenants":                       {PermTenantsRead},
	"POST /admin/tenants":                       {PermTenantsManage},
	"PATCH /admin/tenants/:slug":                {PermTenantsManage},
	"GET  /admin/tenants/:slug/users":           {PermTenantsRead},
	"POST /admin/users":                         {PermUsersCreate},
	"GET  /admin/audit":                         {PermAuditRead},
	"GET  /admin/reservations":                  {PermReservationsManage},
	"POST /admin/reservations":                  {PermReservationsManage},
	"DELETE /admin/reservations/:name":          {PermReservationsManage},
	"GET  /admin/qos":                           {PermQosManage},
	"POST /admin/qos":                           {PermQosManage},
	"PATCH /admin/tenants/:slug/qos":            {PermQosManage},
	"GET  /tenants/me/users":                    {PermTenantUsersRead},
	"POST /tenants/me/users":                    {PermTenantUsersManage},
	"PATCH /tenants/me/users/:username":         {PermTenantUsersManage},
	"POST /tenants/me/users/:username/password": {PermTenantUsersResetPassword},
}

var allBuiltinRoles = []string{RoleSystemAdmin, RoleOpsAdmin, RoleTenantAdmin, RoleMember}

const anyAuth = "any-authenticated" // 历史矩阵中"所有已认证角色"的占位

// TestPermissionMatrixEquivalence 逐路由对照：{角色: 持有该路由权限} == 历史 RequireRole 放行集。
func TestPermissionMatrixEquivalence(t *testing.T) {
	if len(historicRouteRoles) != len(routePermissions) {
		t.Fatalf("route tables drifted: historic=%d routes, permission-mapped=%d routes",
			len(historicRouteRoles), len(routePermissions))
	}
	for route, wantRoles := range historicRouteRoles {
		perms, ok := routePermissions[route]
		if !ok {
			t.Fatalf("route %q missing from permission mapping", route)
		}
		for _, role := range allBuiltinRoles {
			gotOK := false
			for _, p := range perms {
				if permSetOf(BuiltinRolePermissions[role]...)[p] {
					gotOK = true
					break
				}
			}
			wantOK := wantRoles[0] == anyAuth || contains(wantRoles, role)
			if gotOK != wantOK {
				t.Errorf("%s: role %q access = %v, historic RequireRole = %v (drift!)",
					route, role, gotOK, wantOK)
			}
		}
	}
}

// TestAllPermissionsInVocabulary 权限点清单与常量块一致（词汇表自洽）。
func TestAllPermissionsInVocabulary(t *testing.T) {
	set := permSetOf(AllPermissions...)
	if len(set) != len(AllPermissions) {
		t.Fatalf("AllPermissions contains duplicates: %v", AllPermissions)
	}
	for role, perms := range BuiltinRolePermissions {
		for _, p := range perms {
			if !set[p] {
				t.Errorf("role %q references unknown permission %q", role, p)
			}
		}
	}
	// 每个权限点至少被一个内置角色持有（孤儿权限点 = 词汇表泄漏）
	for _, p := range AllPermissions {
		held := false
		for _, perms := range BuiltinRolePermissions {
			if contains(perms, p) {
				held = true
				break
			}
		}
		if !held {
			t.Errorf("permission %q held by no builtin role (orphan vocabulary)", p)
		}
	}
}

// TestRequirePermissionGate 中间件行为：持全量权限放行；缺一 403 且 required=权限点。
func TestRequirePermissionGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	SetSecret([]byte("perm-test"))
	tok, err := GenerateToken("alice", RoleMember, "t", "ns", "cu", "acct")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	verified, err := VerifyToken(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	allow := func(cl *Claims, perms ...string) int {
		r := gin.New()
		r.GET("/x", JWTAuthMiddleware(), RequirePermission(perms...), func(c *gin.Context) {
			c.Status(http.StatusOK)
		})
		req, _ := http.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		// JWTAuthMiddleware 会把验签后的 claims 写回上下文；这里直接注入模拟中间件产物
		r2 := gin.New()
		r2.GET("/x", func(c *gin.Context) { c.Set("claims", cl); c.Next() }, RequirePermission(perms...), func(c *gin.Context) {
			c.Status(http.StatusOK)
		})
		req2, _ := http.NewRequest(http.MethodGet, "/x", nil)
		w2 := httptest.NewRecorder()
		r2.ServeHTTP(w2, req2)
		_ = w // 保持 w 引用（第一段仅为说明完整链路）
		return w2.Code
	}

	if got := allow(verified, PermJobsSubmit, PermJobsControl); got != http.StatusOK {
		t.Errorf("member with jobs perms: want 200 got %d", got)
	}
	if got := allow(verified, PermJobsSubmit, PermNodesManage); got != http.StatusForbidden {
		t.Errorf("member lacking nodes:manage: want 403 got %d", got)
	}
	if got := allow(nil, PermJobsSubmit); got != http.StatusForbidden {
		t.Errorf("nil claims: want 403 got %d", got)
	}
}

// TestRequirePermissionMissingDetail 403 体 required extra 携带权限点清单。
func TestRequirePermissionMissingDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		tok, _ := GenerateToken("bob", RoleMember, "t", "ns", "cu", "acct")
		cl, _ := VerifyToken(tok)
		c.Set("claims", cl)
		c.Next()
	}, RequirePermission(PermNodesManage), func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), PermNodesManage) {
		t.Errorf("403 body should mention %q, got %s", PermNodesManage, w.Body.String())
	}
}

// TestSortedPermissions 排序稳定（/auth/me 输出契约）。
func TestSortedPermissions(t *testing.T) {
	got := SortedPermissions(permSetOf(PermJobsControl, PermJobsSubmit, PermClusterRead))
	want := []string{PermClusterRead, PermJobsControl, PermJobsSubmit}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SortedPermissions = %v want %v", got, want)
	}
}

// TestValidPermission 白名单判定。
func TestValidPermission(t *testing.T) {
	if !ValidPermission(PermBillingRead) {
		t.Error("billing:read should be valid")
	}
	if ValidPermission("billing:read:all") || ValidPermission("") {
		t.Error("unknown permissions must be rejected")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
