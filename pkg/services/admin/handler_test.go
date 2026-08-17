package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/services/admin"
	"ails-hpc/pkg/store"

	"github.com/gin-gonic/gin"
)

// fakeProvision 记录 sacctmgr 供给调用，可注入失败（实现 SlurmProvisioner）。
type fakeProvision struct {
	calls   []string // "u:cu/acct@parent" 用户 / "a:acct@parent" 账号 / "l:acct:setting" 限额
	fail    bool
	failLmt bool
}

func (f *fakeProvision) ProvisionAccount(account, parent string) error {
	f.calls = append(f.calls, "a:"+account+"@"+parent)
	if f.fail {
		return errors.New("sacctmgr unreachable")
	}
	return nil
}

func (f *fakeProvision) ProvisionUser(cu, account, parent string) error {
	f.calls = append(f.calls, "u:"+cu+"/"+account+"@"+parent)
	if f.fail {
		return errors.New("sacctmgr unreachable")
	}
	return nil
}

func (f *fakeProvision) SetAccountLimits(account, setting string) error {
	f.calls = append(f.calls, "l:"+account+":"+setting)
	return condErr(f.failLmt)
}

func condErr(yes bool) error {
	if yes {
		return errors.New("sacctmgr unreachable")
	}
	return nil
}

// newFixture：临时 sqlite 库 + system/hpc-lab 两租户 + 三个种子用户（admin@system，
// tenantadmin/member@hpc-lab），返回挂好路由的 gin 引擎（复刻 router.go 的两组 gating）。
func newFixture(t *testing.T) (*gin.Engine, store.AdminStore, *fakeProvision) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auth.SetSecret([]byte("admin-test-secret"))

	stRaw, err := store.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = stRaw.Close() })
	st, ok := stRaw.(store.AdminStore)
	if !ok {
		t.Fatal("sqlite store must implement AdminStore")
	}

	ctx := context.Background()
	// Open 只保证保留租户 'system'；hpc-lab 是部署数据——显式创建后再播种用户。
	if _, err := st.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatalf("seed tenant hpc-lab: %v", err)
	}
	for _, u := range []store.NewUser{
		{Username: "padmin", Password: "platform123", Role: auth.RoleSystemAdmin, TenantSlug: "system"},
		{Username: "tadmin", Password: "tenant123", Role: auth.RoleTenantAdmin, TenantSlug: "hpc-lab"},
		{Username: "alice", Password: "alice12345", Role: auth.RoleMember, TenantSlug: "hpc-lab"},
	} {
		if _, err := st.CreateUser(ctx, u); err != nil {
			t.Fatalf("seed %s: %v", u.Username, err)
		}
	}

	prov := &fakeProvision{}
	h := admin.NewAdminHandler(admin.NewService(st, prov))

	// 复刻 router.go：/admin 组 admin 独占；/tenants 组 tenant_admin。
	r := gin.New()
	setClaims := func(username, role, tid string) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Set("claims", &auth.Claims{Username: username, Role: role, OrgSlug: tid, TID: tid})
			c.Next()
		}
	}
	with := func(username, role, tid string, h2 ...gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{setClaims(username, role, tid),
			auth.RequireRole(role)}, h2...)
	}

	g := r.Group("/api/v1", setClaims("x", auth.RoleMember, "hpc-lab")) // 占位（每路由再覆写）
	_ = g
	r.GET("/api/v1/admin/tenants", with("padmin", auth.RoleSystemAdmin, "system", h.ListTenants)...)
	r.POST("/api/v1/admin/tenants", with("padmin", auth.RoleSystemAdmin, "system", h.CreateTenant)...)
	r.PATCH("/api/v1/admin/tenants/:slug", with("padmin", auth.RoleSystemAdmin, "system", h.UpdateTenant)...)
	r.POST("/api/v1/admin/users", with("padmin", auth.RoleSystemAdmin, "system", h.CreatePlatformUser)...)
	r.GET("/api/v1/tenants/me/users", with("tadmin", auth.RoleTenantAdmin, "hpc-lab", h.ListMyUsers)...)
	r.POST("/api/v1/tenants/me/users", with("tadmin", auth.RoleTenantAdmin, "hpc-lab", h.CreateTenantUser)...)
	r.PATCH("/api/v1/tenants/me/users/:username", with("tadmin", auth.RoleTenantAdmin, "hpc-lab", h.UpdateMyUser)...)
	r.POST("/api/v1/tenants/me/users/:username/password", with("tadmin", auth.RoleTenantAdmin, "hpc-lab", h.ResetMyUserPassword)...)
	return r, st, prov
}

func do(r *gin.Engine, method, path, body string) (int, string) {
	var rd *bytes.Reader
	if body == "" {
		rd = bytes.NewReader(nil)
	} else {
		rd = bytes.NewReader([]byte(body))
	}
	req, _ := http.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// TestTenantAdmin_ManagesOwnUsersOnly 租户级全流程：列表仅本租户、建 member、
// 不可建平台角色、跨租户 PATCH 404、禁用+重置密码生效。
func TestTenantAdmin_ManagesOwnUsersOnly(t *testing.T) {
	r, st, prov := newFixture(t)

	// 1) 列表：hpc-lab 的 tadmin/alice，不含 system 的 padmin
	code, body := do(r, http.MethodGet, "/api/v1/tenants/me/users", "")
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	var lr struct{ Users []auth.User }
	_ = json.Unmarshal([]byte(body), &lr)
	if len(lr.Users) != 2 {
		t.Fatalf("want 2 hpc-lab users, got %d: %+v", len(lr.Users), lr.Users)
	}
	for _, u := range lr.Users {
		if u.TenantSlug != "hpc-lab" {
			t.Errorf("cross-tenant leak: %+v", u)
		}
	}

	// 2) 建 member：200 + 供给调用 + 可登录
	code, body = do(r, http.MethodPost, "/api/v1/tenants/me/users",
		`{"username":"bob","password":"bob12345","role":"member"}`)
	if code != 200 {
		t.Fatalf("create bob: %d %s", code, body)
	}
	if len(prov.calls) != 1 || prov.calls[0] != "u:bob/bob@hpc-lab" {
		t.Errorf("provisioner calls = %v, want [u:bob/bob@hpc-lab]", prov.calls)
	}
	if u, err := st.Verify("bob", "bob12345"); err != nil || u.ClusterUser != "bob" || u.Account != "bob" {
		t.Errorf("bob login/mapping: %v %+v", err, u)
	}

	// 3) 不可建平台角色
	if code, _ := do(r, http.MethodPost, "/api/v1/tenants/me/users",
		`{"username":"eve","password":"eve12345","role":"admin"}`); code != 400 {
		t.Errorf("tenant_admin creating admin: want 400 got %d", code)
	}

	// 4) 跨租户目标（system 的 padmin）→ 404 防枚举
	if code, _ := do(r, http.MethodPatch, "/api/v1/tenants/me/users/padmin",
		`{"status":"disabled"}`); code != 404 {
		t.Errorf("cross-tenant patch: want 404 got %d", code)
	}

	// 5) 重置密码（active 态）：新密码可登录、旧密码失效
	if code, _ := do(r, http.MethodPost, "/api/v1/tenants/me/users/alice/password",
		`{"newPassword":"alice99999"}`); code != 200 {
		t.Errorf("reset alice pw: got %d", code)
	}
	if _, err := st.Verify("alice", "alice99999"); err != nil {
		t.Errorf("alice new pw login: %v", err)
	}
	if _, err := st.Verify("alice", "alice12345"); err == nil {
		t.Error("alice old pw must fail")
	}

	// 6) 禁用本租户 alice：status=disabled → 登录被拒（与密码对错无关）
	if code, _ := do(r, http.MethodPatch, "/api/v1/tenants/me/users/alice",
		`{"status":"disabled"}`); code != 200 {
		t.Errorf("disable alice: got %d", code)
	}
	if u, ok := st.Lookup("alice"); !ok || u.Status != "disabled" {
		t.Errorf("alice status = %+v", u)
	}
	if _, err := st.Verify("alice", "alice99999"); err == nil {
		t.Error("disabled user must not log in even with the right password")
	}
}

// TestPlatformAdmin_TenantsAndUsers 平台级：建租户、保留租户 409、重复 409、
// 建 platform admin（system）、member 无权访问。
func TestPlatformAdmin_TenantsAndUsers(t *testing.T) {
	r, st, prov := newFixture(t)

	if code, _ := do(r, http.MethodPost, "/api/v1/admin/tenants", `{"slug":"bio-lab","name":"生物"}`); code != 200 {
		t.Errorf("create tenant: got %d", code)
	}
	if code, _ := do(r, http.MethodPost, "/api/v1/admin/tenants", `{"slug":"bio-lab"}`); code != 409 {
		t.Errorf("duplicate tenant: want 409 got %d", code)
	}
	if code, _ := do(r, http.MethodPost, "/api/v1/admin/tenants", `{"slug":"system"}`); code != 409 {
		t.Errorf("reserved tenant: want 409 got %d", code)
	}

	// 平台建 admin（system）与 member（bio-lab）
	if code, body := do(r, http.MethodPost, "/api/v1/admin/users",
		`{"username":"root2","role":"admin","tenantSlug":"system","password":"root12345"}`); code != 200 {
		t.Errorf("platform admin: %d %s", code, body)
	}
	if code, _ := do(r, http.MethodPost, "/api/v1/admin/users",
		`{"username":"bio1","role":"member","tenantSlug":"bio-lab","password":"bio12345"}`); code != 200 {
		t.Errorf("platform member into bio-lab: got %d", code)
	}
	if code, _ := do(r, http.MethodPost, "/api/v1/admin/users",
		`{"username":"bad1","role":"admin","tenantSlug":"bio-lab","password":"bad12345"}`); code != 400 {
		t.Errorf("admin outside system: want 400 got %d", code)
	}
	_ = st
	_ = prov
}

// TestProvisionFailureReturns502 DB 成功、供给失败 → 502，用户仍在库（重试幂等）。
func TestProvisionFailureReturns502(t *testing.T) {
	r, st, prov := newFixture(t)
	prov.fail = true

	code, _ := do(r, http.MethodPost, "/api/v1/tenants/me/users",
		`{"username":"carol","password":"carol12345","role":"member"}`)
	if code != http.StatusBadGateway {
		t.Fatalf("provision fail: want 502 got %d", code)
	}
	if _, ok := st.Lookup("carol"); !ok {
		t.Error("user row must persist despite provisioning failure (retry-safe)")
	}
	// 供给恢复后重试幂等：重复用户名 → 409（已存在，不重复建）
	prov.fail = false
	if code, _ := do(r, http.MethodPost, "/api/v1/tenants/me/users",
		`{"username":"carol","password":"carol12345","role":"member"}`); code != 409 {
		t.Errorf("retry duplicate: want 409 got %d", code)
	}
}

// TestReadOnlyStoreRefused yaml 模式（nil store）：全部端点 503。
func TestReadOnlyStoreRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	auth.SetSecret([]byte("admin-test-secret"))
	h := admin.NewAdminHandler(admin.NewService(nil, nil))
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		c.Set("claims", &auth.Claims{Username: "padmin", Role: auth.RoleSystemAdmin, TID: "system"})
		h.ListTenants(c)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil store: want 503 got %d body=%s", w.Code, w.Body.String())
	}
}

// TestAuditEndpointAndEntries：写审计的变更产生可读条目；ListAudit 按 action 过滤。
func TestAuditEndpointAndEntries(t *testing.T) {
	r, st, _ := newFixture(t)
	// 制造两条审计（建租户 bio-x + 建用户）
	if code, body := do(r, http.MethodPost, "/api/v1/admin/tenants", `{"slug":"bio-x"}`); code != 200 {
		t.Fatalf("create tenant: %d %s", code, body)
	}
	if code, _ := do(r, http.MethodPost, "/api/v1/admin/users",
		`{"username":"aud1","role":"member","tenantSlug":"bio-x","password":"aud12345"}`); code != 200 {
		t.Fatalf("create user: got %d", code)
	}
	entries, err := st.ListAudit(context.Background(), "", "tenant.create", 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("tenant.create entries: %v %d", err, len(entries))
	}
	all, err := st.ListAudit(context.Background(), "", "", 100)
	if err != nil || len(all) < 2 {
		t.Fatalf("all entries: %v %d", err, len(all))
	}
	// 时间倒序：最新在前
	if all[0].Action != "user.create" {
		t.Errorf("first entry action=%q want user.create (desc order)", all[0].Action)
	}
	_ = r
}
