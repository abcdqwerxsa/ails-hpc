package main

// R5 越权对抗测试：真实 NewRouter + 真实 sqlite 用户库（带 store 的中间件每请求刷新
// 角色面）+ mock slurmrestd。攻击面覆盖：
//   - 自定义角色提权（超出父集 → 400，服务端子集校验）
//   - 伪造 claims（token 里塞 Perms=["nodes:manage"]——中间件按库覆写，伪造无效）
//   - 角色链归纳提权（被授予角色管理权的低权角色，再建角色仍不可越界）
//   - 跨租户角色读写（404）与跨租户用户改派（404）
//   - 在用角色删除（409）与改派后删除（200）
//   - 角色改派即刻生效（在途 token 不重登即失去/获得权限）

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/services/admin"
	"ails-hpc/pkg/services/billing"
	"ails-hpc/pkg/services/cluster"
	"ails-hpc/pkg/services/common"
	"ails-hpc/pkg/services/containers"
	"ails-hpc/pkg/services/jobs"
	"ails-hpc/pkg/services/nodes"
	"ails-hpc/pkg/slurmrest"
	"ails-hpc/pkg/store"

	"github.com/gin-gonic/gin"
)

// noopProvisioner 测试供给桩（不触集群）。
type noopProvisioner struct{}

func (noopProvisioner) ProvisionAccount(account, parentAccount string) error  { return nil }
func (noopProvisioner) ProvisionUser(cu, account, parentAccount string) error { return nil }
func (noopProvisioner) SetAccountLimits(account, setting string) error        { return nil }

// setupRBACStack 全栈夹具：sqlite 库（system/hpc-lab/bio-lab + 种子用户）+ 生产路由表。
// 返回的 *admin.Service 供测试注入集群命令执行面（SetClusterRunner——分区/预约直通面）。
func setupRBACStack(t *testing.T) (*gin.Engine, store.AdminStore, *admin.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auth.SetSecret([]byte("rbac-adversarial-secret"))

	stRaw, err := store.Open(filepath.Join(t.TempDir(), "rbac.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = stRaw.Close() })
	st := stRaw.(store.AdminStore)

	ctx := t.Context()
	for _, slug := range []string{"hpc-lab", "bio-lab"} {
		if _, err := st.CreateTenant(ctx, slug, ""); err != nil {
			t.Fatalf("seed tenant %s: %v", slug, err)
		}
	}
	for _, u := range []store.NewUser{
		{Username: "padmin", Password: "platform123", Role: auth.RoleSystemAdmin, TenantSlug: "system"},
		{Username: "tadmin", Password: "tenant12345", Role: auth.RoleTenantAdmin, TenantSlug: "hpc-lab"},
		{Username: "alice", Password: "alice12345", Role: auth.RoleMember, TenantSlug: "hpc-lab"},
		{Username: "bob", Password: "bob1234567", Role: auth.RoleMember, TenantSlug: "hpc-lab"},
		{Username: "biomember", Password: "biomember12", Role: auth.RoleMember, TenantSlug: "bio-lab"},
		{Username: "puser", Password: "puser123456", Role: auth.RoleOpsAdmin, TenantSlug: "system"},
	} {
		if _, err := st.CreateUser(ctx, u); err != nil {
			t.Fatalf("seed %s: %v", u.Username, err)
		}
	}

	mock := common.NewMockSlurmServer()
	t.Cleanup(mock.Close)
	slurmClient := slurmrest.NewClient(mock.URL, "hpcuser", "test-token")
	billingService := billing.NewBillingServiceWithFetcher(&zeroSacctFetcher{})

	tenantMembers := func(tenant string) ([]string, error) {
		return st.ClusterUsersOfTenant(ctx, tenant)
	}

	authHandler := auth.NewAuthHandler(st)
	authHandler.SetAuditSink(st) // A2：登录审计出口（生产同装配）

	adminSvc := admin.NewService(st, noopProvisioner{})
	// 集群直通面默认罐头（分区端点在无 docker 的测试环境确定性返回；具体测试可再覆写）
	adminSvc.SetClusterRunner(func(args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "scontrol" && args[1] == "show" {
			return []byte("PartitionName=debug\n   Default=YES MaxTime=UNLIMITED State=UP Nodes=c1\n"), nil
		}
		return []byte(""), nil
	})
	h := Handlers{
		Auth:       authHandler,
		Cluster:    cluster.NewClusterHandler(cluster.NewClusterService(slurmClient)),
		Nodes:      nodes.NewNodeHandler(nodes.NewNodeServiceWithApplier(slurmClient, func(string, string, string) error { return nil })),
		Jobs:       jobs.NewJobHandlerScoped(jobs.NewJobService(slurmClient), tenantMembers),
		Containers: containers.NewContainerHandlerScoped(containers.NewContainerService(slurmClient), tenantMembers),
		Billing:    billing.NewBillingHandlerWithScope(billingService, tenantMembers),
		Admin:      admin.NewAdminHandler(adminSvc),
		Audit:      st, // A2：/slurm/** 变更操作审计出口
	}
	return NewRouter(h), st, adminSvc
}

// loginViaAPI 走真实登录端点取 token（签发路径全量参与）。
func loginViaAPI(t *testing.T, r *gin.Engine, username, password string) string {
	t.Helper()
	code, body := doAuth(r, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"username":%q,"password":%q}`, username, password), "")
	if code != http.StatusOK {
		t.Fatalf("login %s: %d %s", username, code, body)
	}
	var resp auth.LoginResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal login: %v", err)
	}
	return resp.Token
}

// TestRBAC_EscalationOnCreate 提权攻击：tenant_admin/低权角色建角色时请求超出父集的
// 权限 → 400；角色链（被授予角色管理权的自定义角色）再建角色同样不可越界。
func TestRBAC_EscalationOnCreate(t *testing.T) {
	r, _, _ := setupRBACStack(t)
	tok := loginViaAPI(t, r, "tadmin", "tenant12345")

	// 1) tenant_admin 直接要 nodes:manage → 400
	if c, b := doAuth(r, http.MethodPost, "/api/v1/tenants/me/roles",
		`{"name":"esc1","permissions":["nodes:manage"]}`, tok); c != http.StatusBadRequest {
		t.Fatalf("direct escalation: want 400 got %d body=%s", c, b)
	}
	// 2) 合法子集角色 sub-admin（含角色管理权——权限上是 tenant_admin 子集）
	if c, b := doAuth(r, http.MethodPost, "/api/v1/tenants/me/roles",
		`{"name":"sub-admin","permissions":["cluster:read","tenant:users:read","tenant:users:manage","tenant:roles:manage"]}`, tok); c != http.StatusOK {
		t.Fatalf("create sub-admin: want 200 got %d body=%s", c, b)
	}
	// bob 拿到 sub-admin
	if c, b := doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/bob/role",
		`{"role":"sub-admin"}`, tok); c != http.StatusOK {
		t.Fatalf("assign bob: %d %s", c, b)
	}
	// 3) bob（持角色管理权）建角色试图注入越界权限 → 400（归纳：子集的子集仍不可越界）
	bobTok := loginViaAPI(t, r, "bob", "bob1234567")
	if c, b := doAuth(r, http.MethodPost, "/api/v1/tenants/me/roles",
		`{"name":"esc2","permissions":["tenant:roles:manage","nodes:manage"]}`, bobTok); c != http.StatusBadRequest {
		t.Fatalf("chained escalation: want 400 got %d body=%s", c, b)
	}
	// 4) bob 更新既有角色放大权限（收缩后放大）→ 400
	if c, b := doAuth(r, http.MethodPatch, "/api/v1/tenants/me/roles/sub-admin",
		`{"permissions":["cluster:read","qos:manage"]}`, bobTok); c != http.StatusBadRequest {
		t.Fatalf("update escalation: want 400 got %d body=%s", c, b)
	}
}

// TestRBAC_ForgedClaimsForgedNoMore 伪造令牌攻击：手工铸造携带 Perms=["nodes:manage"]
// 的 token——带 store 的中间件每请求按库覆写 claims.Perms，伪造面被丢弃 → 403。
func TestRBAC_ForgedClaimsForgedNoMore(t *testing.T) {
	r, st, _ := setupRBACStack(t)
	_ = st

	// alice 是 member（无 nodes:manage）；伪造 token 宣称持有全部权限
	forged, err := auth.GenerateTokenClaims(auth.Claims{
		Username: "alice", Role: auth.RoleMember, OrgSlug: "hpc-lab", TenantNS: "default",
		ClusterUser: "alice", Account: "alice", TID: "hpc-lab", Ver: 0,
		Perms: []string{auth.PermNodesManage, auth.PermJobsSubmit, auth.PermRolesManage},
	})
	if err != nil {
		t.Fatalf("mint forged: %v", err)
	}
	if c, _ := doAuth(r, http.MethodPost, "/api/v1/slurm/nodes/node1/state",
		`{"state":"DRAIN"}`, forged); c != http.StatusForbidden {
		t.Fatalf("forged Perms must be overwritten by store-backed middleware: got %d want 403", c)
	}
	if c, _ := doAuth(r, http.MethodGet, "/api/v1/admin/roles", "", forged); c != http.StatusForbidden {
		t.Fatalf("forged roles:manage must not grant admin access: got %d want 403", c)
	}
}

// TestRBAC_CustomRoleEnforcement 自定义角色的权限面被路由逐点执行：
// dev=[cluster:read,jobs:submit]——可提交；不可 DRAIN/不可 IDE/不可计费。
func TestRBAC_CustomRoleEnforcement(t *testing.T) {
	r, _, _ := setupRBACStack(t)
	tok := loginViaAPI(t, r, "tadmin", "tenant12345")

	if c, b := doAuth(r, http.MethodPost, "/api/v1/tenants/me/roles",
		`{"name":"dev","permissions":["cluster:read","jobs:submit"]}`, tok); c != http.StatusOK {
		t.Fatalf("create dev: %d %s", c, b)
	}
	if c, b := doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/role",
		`{"role":"dev"}`, tok); c != http.StatusOK {
		t.Fatalf("assign alice: %d %s", c, b)
	}
	alice := loginViaAPI(t, r, "alice", "alice12345")

	// /auth/me 自描述与实际执行一致
	_, body := doAuth(r, http.MethodGet, "/api/v1/auth/me", "", alice)
	var me auth.LoginResponse
	_ = json.Unmarshal([]byte(body), &me)
	if me.User.RoleName != "dev" || me.User.Role != auth.RoleMember {
		t.Fatalf("alice me = %+v, want roleName=dev base=member", me.User)
	}

	// 可：读节点、提交作业
	if c, _ := doAuth(r, http.MethodGet, "/api/v1/slurm/nodes", "", alice); c != http.StatusOK {
		t.Errorf("dev cluster:read nodes: got %d", c)
	}
	if c, b := doAuth(r, http.MethodPost, "/api/v1/slurm/jobs/submit",
		`{"name":"devjob","script":"echo hi"}`, alice); c != http.StatusOK {
		t.Errorf("dev jobs:submit: got %d body=%s", c, b)
	}
	// 不可：DRAIN / IDE 列表 / 计费 / 作业控制
	if c := doRequest(r, http.MethodPost, "/api/v1/slurm/nodes/node1/state", `{"state":"DRAIN"}`, alice); c != http.StatusForbidden {
		t.Errorf("dev nodes:manage: want 403 got %d", c)
	}
	if c := doRequest(r, http.MethodGet, "/api/v1/slurm/containers/list", "", alice); c != http.StatusForbidden {
		t.Errorf("dev ide:list: want 403 got %d", c)
	}
	if c := doRequest(r, http.MethodGet, "/api/v1/slurm/billing/usage", "", alice); c != http.StatusForbidden {
		t.Errorf("dev billing:read: want 403 got %d", c)
	}
	if c := doRequest(r, http.MethodPost, "/api/v1/slurm/jobs/1/cancel", "", alice); c != http.StatusForbidden {
		t.Errorf("dev jobs:control: want 403 got %d", c)
	}
}

// TestRBAC_ReassignmentImmediate 角色改派即刻生效：alice 在途 token 不重登，改派到
// viewer（只有 cluster:read）后提交立即 403；改回 dev 后立即 200。
func TestRBAC_ReassignmentImmediate(t *testing.T) {
	r, _, _ := setupRBACStack(t)
	tok := loginViaAPI(t, r, "tadmin", "tenant12345")

	doAuth(r, http.MethodPost, "/api/v1/tenants/me/roles", `{"name":"dev","permissions":["cluster:read","jobs:submit"]}`, tok)
	doAuth(r, http.MethodPost, "/api/v1/tenants/me/roles", `{"name":"viewer","permissions":["cluster:read"]}`, tok)
	doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/role", `{"role":"dev"}`, tok)

	alice := loginViaAPI(t, r, "alice", "alice12345")
	if c, _ := doAuth(r, http.MethodPost, "/api/v1/slurm/jobs/submit",
		`{"name":"j1","script":"echo hi"}`, alice); c != http.StatusOK {
		t.Fatalf("dev submit before reassign: want 200 got %d", c)
	}
	// 改派 viewer —— 在途 token 立即失去提交权（无需 bump token_version / 重登）
	doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/role", `{"role":"viewer"}`, tok)
	if c := doRequest(r, http.MethodPost, "/api/v1/slurm/jobs/submit",
		`{"name":"j2","script":"echo hi"}`, alice); c != http.StatusForbidden {
		t.Fatalf("viewer submit after reassign: want 403 got %d (live refresh broken?)", c)
	}
	// 改回 dev —— 立即恢复
	doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/role", `{"role":"dev"}`, tok)
	if c, _ := doAuth(r, http.MethodPost, "/api/v1/slurm/jobs/submit",
		`{"name":"j3","script":"echo hi"}`, alice); c != http.StatusOK {
		t.Fatalf("dev submit after reassign back: want 200 got %d", c)
	}
}

// TestRBAC_CrossTenantAndDeleteDisposition 跨租户读写（404）+ 在用角色删除处置。
func TestRBAC_CrossTenantAndDeleteDisposition(t *testing.T) {
	r, st, _ := setupRBACStack(t)
	ctx := t.Context()
	tok := loginViaAPI(t, r, "tadmin", "tenant12345")

	// bio-lab 侧已有同名角色（另一租户管理员建的）
	if _, err := st.CreateRole(ctx, store.NewRole{
		Name: "dev", BaseRole: auth.RoleMember, Permissions: []string{auth.PermClusterRead},
		TenantSlug: "bio-lab",
	}); err != nil {
		t.Fatalf("seed bio role: %v", err)
	}
	// hpc-lab 也有自己的 dev 并指派给 alice
	doAuth(r, http.MethodPost, "/api/v1/tenants/me/roles", `{"name":"dev","permissions":["cluster:read","jobs:submit"]}`, tok)
	doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/role", `{"role":"dev"}`, tok)

	// 跨租户改/删：hpc-lab 的 tadmin 对 bio-lab 的 dev 不可见 → 404（先删自己的 dev 制造同名只剩 bio 的场景不可行——
	// 直接用 bio 侧角色名在 hpc-lab 作用域解析：不存在的名字 → 404）
	if c, _ := doAuth(r, http.MethodDelete, "/api/v1/tenants/me/roles/ghost-role", "", tok); c != http.StatusNotFound {
		t.Errorf("delete unknown role: want 404 got %d", c)
	}
	// 改派外租户用户 → 404（目标用户不在本租户，防枚举同语义）
	if c, _ := doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/biomember/role",
		`{"role":"member"}`, tok); c != http.StatusNotFound {
		t.Errorf("assign cross-tenant user: want 404 got %d", c)
	}

	// 在用删除 → 409；改派回内置 member 后删除 → 200
	if c, _ := doAuth(r, http.MethodDelete, "/api/v1/tenants/me/roles/dev", "", tok); c != http.StatusConflict {
		t.Errorf("delete in-use role: want 409 got %d", c)
	}
	if c, _ := doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/role", `{"role":"member"}`, tok); c != http.StatusOK {
		t.Errorf("reassign to builtin member: got %d", c)
	}
	if c, _ := doAuth(r, http.MethodDelete, "/api/v1/tenants/me/roles/dev", "", tok); c != http.StatusOK {
		t.Errorf("delete after reassign: want 200 got %d", c)
	}
	// alice 立即回到 member 面（提交仍可——member 有 jobs:submit；这里验证的是角色行存在）
	alice := loginViaAPI(t, r, "alice", "alice12345")
	_, body := doAuth(r, http.MethodGet, "/api/v1/auth/me", "", alice)
	var me auth.LoginResponse
	_ = json.Unmarshal([]byte(body), &me)
	if me.User.RoleName != "" || me.User.Role != auth.RoleMember {
		t.Errorf("alice after reassign to builtin: roleName=%q role=%q, want empty/member", me.User.RoleName, me.User.Role)
	}
}

// TestRBAC_PlatformRoleIsolation 平台自定义角色不可触租户作用域，反之亦然；
// 且 admin 无 jobs:submit——平台侧也无法铸造可提交作业的平台角色。
func TestRBAC_PlatformRoleIsolation(t *testing.T) {
	r, _, _ := setupRBACStack(t)
	padmin := loginViaAPI(t, r, "padmin", "platform123")
	tadmin := loginViaAPI(t, r, "tadmin", "tenant12345")

	// 平台侧建角色给 jobs:submit → 400（admin 自身无此权限——纯监控角色的边界保持）
	if c, _ := doAuth(r, http.MethodPost, "/api/v1/admin/roles",
		`{"name":"jobber","permissions":["jobs:submit"]}`, padmin); c != http.StatusBadRequest {
		t.Errorf("platform role granting jobs:submit: want 400 got %d", c)
	}
	// 平台角色合法子集 → 200
	if c, b := doAuth(r, http.MethodPost, "/api/v1/admin/roles",
		`{"name":"auditor","permissions":["audit:read","tenants:read"]}`, padmin); c != http.StatusOK {
		t.Fatalf("platform auditor: want 200 got %d body=%s", c, b)
	}
	// tenant_admin 不可触平台角色端点（无 roles:manage）→ 403
	if c := doRequest(r, http.MethodGet, "/api/v1/admin/roles", "", tadmin); c != http.StatusForbidden {
		t.Errorf("tadmin on /admin/roles: want 403 got %d", c)
	}
	// admin 不可触租户角色端点（无 tenant:roles:manage）→ 403
	if c := doRequest(r, http.MethodGet, "/api/v1/tenants/me/roles", "", padmin); c != http.StatusForbidden {
		t.Errorf("admin on /tenants/me/roles: want 403 got %d", c)
	}
}

// TestRBAC_PartitionManageGate 分区管理权限门（v2 增量 partitions:manage）：
//   - 内置 member/tenant_admin 的 GET 与 PATCH 全 403（伪造 token 的 Perms 声明同样被覆写）
//   - admin：GET 解析视图 200 / PATCH 空体 400 / 枚举外值 400 / 合法修改 200 且
//     scontrol 直通命令语法正确、audit_log 落 partition.update
func TestRBAC_PartitionManageGate(t *testing.T) {
	r, st, adminSvc := setupRBACStack(t)

	var gotCmd string
	adminSvc.SetClusterRunner(func(args ...string) ([]byte, error) {
		gotCmd = strings.Join(args, " ")
		if len(args) > 2 && args[0] == "scontrol" && args[1] == "show" {
			return []byte("PartitionName=debug\n   Default=YES MaxTime=UNLIMITED State=UP Nodes=c1\n"), nil
		}
		return []byte(""), nil
	})

	padmin := loginViaAPI(t, r, "padmin", "platform123")
	alice := loginViaAPI(t, r, "alice", "alice12345")
	tadmin := loginViaAPI(t, r, "tadmin", "tenant12345")

	// 1) 无 partitions:manage 的内置角色：GET/PATCH 双 403
	for _, tok := range []string{alice, tadmin} {
		if c := doRequest(r, http.MethodGet, "/api/v1/admin/partitions/debug", "", tok); c != http.StatusForbidden {
			t.Errorf("non-admin GET partition: want 403 got %d", c)
		}
		if c := doRequest(r, http.MethodPatch, "/api/v1/admin/partitions/debug", `{"state":"DOWN"}`, tok); c != http.StatusForbidden {
			t.Errorf("non-admin PATCH partition: want 403 got %d", c)
		}
	}

	// 2) 伪造 token 宣称 partitions:manage（member 身份）→ 中间件按库覆写 → 403
	forged, err := auth.GenerateTokenClaims(auth.Claims{
		Username: "alice", Role: auth.RoleMember, OrgSlug: "hpc-lab", TenantNS: "default",
		ClusterUser: "alice", Account: "alice", TID: "hpc-lab", Ver: 0,
		Perms:       []string{auth.PermPartitionsManage},
	})
	if err != nil {
		t.Fatalf("mint forged: %v", err)
	}
	if c := doRequest(r, http.MethodPatch, "/api/v1/admin/partitions/debug", `{"state":"DOWN"}`, forged); c != http.StatusForbidden {
		t.Errorf("forged partitions:manage: want 403 got %d", c)
	}

	// 3) admin GET → 200 + scontrol show 解析视图
	code, body := doAuth(r, http.MethodGet, "/api/v1/admin/partitions/debug", "", padmin)
	if code != http.StatusOK {
		t.Fatalf("admin GET partition: want 200 got %d body=%s", code, body)
	}
	for _, want := range []string{`"name":"debug"`, `"state":"UP"`, `"default":"YES"`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET partition body missing %s: %s", want, body)
		}
	}

	// 4) admin PATCH 校验面：空体 400（无字段可改）；枚举外值 400（不触 scontrol）
	if c, b := doAuth(r, http.MethodPatch, "/api/v1/admin/partitions/debug", `{}`, padmin); c != http.StatusBadRequest {
		t.Errorf("empty updates: want 400 got %d body=%s", c, b)
	}
	if c, b := doAuth(r, http.MethodPatch, "/api/v1/admin/partitions/debug", `{"state":"SIDEWAYS"}`, padmin); c != http.StatusBadRequest {
		t.Errorf("invalid state: want 400 got %d body=%s", c, b)
	}

	// 5) admin PATCH 合法修改 → 200 + scontrol update 直通语法
	gotCmd = ""
	if c, b := doAuth(r, http.MethodPatch, "/api/v1/admin/partitions/debug",
		`{"state":"DOWN","maxTime":"1-00:00:00"}`, padmin); c != http.StatusOK {
		t.Fatalf("admin PATCH partition: want 200 got %d body=%s", c, b)
	}
	wantCmd := `sh -c scontrol update 'partition=debug' 'State=DOWN' 'MaxTime=1-00:00:00' 2>&1`
	if gotCmd != wantCmd {
		t.Errorf("scontrol cmd:\n got  %s\n want %s", gotCmd, wantCmd)
	}

	// 6) 审计落库：partition.update / actor=padmin / target=partition:debug
	entries, err := st.ListAudit(t.Context(), "padmin", "partition.update", 10)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(entries) != 1 || entries[0].Target != "partition:debug" {
		t.Fatalf("audit = %+v, want one partition:debug entry", entries)
	}
}
