package admin_test

// 安全审计 2026-08-19 回归（admin 服务层）：
//   - P0-2 预约 users/nodes/partition/startTime 白名单（'...' 朴素包裹可被内含
//     单引号逃逸 → root 注入）
//   - P1-5 租户侧 AssignRole：目标角色权限必须 ⊆ 调用者（弱角色不可派 tenant_admin）
//   - P2   内置 admin 账号保护（非 admin 基角色不可重置/禁用）与租户侧自禁用守卫

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/services/admin"
	"ails-hpc/pkg/store"
)

func newSecurityService(t *testing.T) (*admin.Service, store.AdminStore) {
	t.Helper()
	stRaw, err := store.Open(t.TempDir() + "/sec.db")
	if err != nil {
		t.Fatalf("open: %v", err)
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
	for _, u := range []store.NewUser{
		{Username: "padmin", Password: "platform123", Role: auth.RoleSystemAdmin, TenantSlug: "system"},
		{Username: "tadmin", Password: "tenant12345", Role: auth.RoleTenantAdmin, TenantSlug: "hpc-lab"},
		{Username: "alice", Password: "alice12345", Role: auth.RoleMember, TenantSlug: "hpc-lab"},
	} {
		if _, err := st.CreateUser(ctx, u); err != nil {
			t.Fatalf("seed %s: %v", u.Username, err)
		}
	}
	return admin.NewService(st, &fakeProvision{}), st
}

func TestCreateReservation_InjectionRejected(t *testing.T) {
	svc, _ := newSecurityService(t)
	var cmds []string
	svc.SetClusterRunner(func(args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(args, " "))
		return []byte(""), nil
	})
	ctx := context.Background()

	bad := []struct{ start, nodes, part, users, accounts, flags string }{
		{"2026-01-01T00:00'; id; '", "", "", "", "", ""}, // starttime 单引号逃逸
		{"", "node1'; id; '", "", "", "", ""},            // nodes
		{"", "", "stand;ard", "", "", ""},                // partition 分号
		{"", "", "", "u1'; touch /tmp/x; '", "", ""},     // users
		{"", "", "", "", "hpc-lab; rm -rf /", ""},        // accounts 注入
		{"", "", "", "", "", "MAINT'; id; '"},            // flags 注入
		{"2086-01-01 00:00", "", "", "", "", ""},         // 时间格式（空格替代 T）
		{"", "", "", "u1 u2", "", ""},                    // 空格列表
	}
	for i, c := range bad {
		_, err := svc.CreateReservation(ctx, "padmin", "maint"+strings.Repeat("a", 0), c.start, 30, c.nodes, c.users, c.accounts, c.part, c.flags, "")
		if err == nil {
			t.Errorf("case %d: want reject, got nil；cmds=%v", i, cmds)
		}
	}
	// 良性值仍通（命令确实到达 runner）
	if _, err := svc.CreateReservation(ctx, "padmin", "maint-ok", "2026-12-31T09:30", 30, "node[1-2]", "tadmin,alice", "hpc-lab", "standard", "MAINT", ""); err != nil {
		t.Fatalf("benign reservation rejected: %v", err)
	}
	sawCreate := false
	for _, c := range cmds {
		if strings.Contains(c, "scontrol create reservation") {
			sawCreate = true
		}
	}
	if !sawCreate {
		t.Fatalf("create command not invoked: %v", cmds)
	}
}

func TestAssignRole_TenantSubset_NoEscalation(t *testing.T) {
	svc, _ := newSecurityService(t)
	ctx := context.Background()

	// 弱调用者（自定义角色持有 tenant:users:manage 但无 tenant_admin 全集）派内置
	// tenant_admin → ErrRoleEscalation（P1-5：路由门为 tenant:users:manage，提权
	// 在服务层按"目标角色 ⊆ 调用者"拦截）。
	weakPerms := []string{auth.PermClusterRead, auth.PermTenantUsersRead, auth.PermTenantUsersManage}
	err := svc.AssignRole(ctx, "helpdesk", weakPerms, "hpc-lab", "alice", auth.RoleTenantAdmin, "")
	if !errors.Is(err, admin.ErrRoleEscalation) {
		t.Fatalf("weak caller assigning tenant_admin: err=%v, want ErrRoleEscalation", err)
	}
	// 同一弱调用者派内置 member（member 含 billing/jobs/ide 等超集权限）→ 同样拒绝
	// （⊆ 语义对称：全集角色谁都派不了，除非自己持有全集）
	if err := svc.AssignRole(ctx, "helpdesk", weakPerms, "hpc-lab", "alice", auth.RoleMember, ""); !errors.Is(err, admin.ErrRoleEscalation) {
		t.Fatalf("weak caller assigning builtin member: err=%v, want ErrRoleEscalation", err)
	}
	// 弱调用者派权限 ⊆ 自身的自定义角色 → 放行
	taPerms0 := auth.SortedPermissions(auth.PermissionsOf(&auth.Claims{Role: auth.RoleTenantAdmin}))
	if _, err := svc.CreateRole(ctx, "tadmin", taPerms0, store.NewRole{
		Name: "reader", BaseRole: auth.RoleMember, Permissions: []string{auth.PermClusterRead}, TenantSlug: "hpc-lab",
	}, ""); err != nil {
		t.Fatalf("seed custom role: %v", err)
	}
	if err := svc.AssignRole(ctx, "helpdesk", weakPerms, "hpc-lab", "alice", "reader", ""); err != nil {
		t.Fatalf("weak caller assigning subset custom role: %v", err)
	}
	// tenant_admin（全集）派 tenant_admin → 放行（平级）
	taPerms := auth.SortedPermissions(auth.PermissionsOf(&auth.Claims{Role: auth.RoleTenantAdmin}))
	if err := svc.AssignRole(ctx, "tadmin", taPerms, "hpc-lab", "alice", auth.RoleTenantAdmin, ""); err != nil {
		t.Fatalf("tenant_admin assigning tenant_admin: %v", err)
	}
}

func TestBuiltinAdminProtection(t *testing.T) {
	svc, _ := newSecurityService(t)
	ctx := context.Background()

	// 前置：弱基角色调用者（member）——非 admin 基角色
	if err := svc.ResetPlatformUserPassword(ctx, "alice", "padmin", "NewPass#12345", ""); !errors.Is(err, admin.ErrAdminTarget) {
		t.Fatalf("member resetting admin password: err=%v, want ErrAdminTarget", err)
	}
	if err := svc.UpdatePlatformUser(ctx, "alice", "padmin", "", "disabled", ""); !errors.Is(err, admin.ErrAdminTarget) {
		t.Fatalf("member disabling admin: err=%v, want ErrAdminTarget", err)
	}
	// admin 基角色互操作放行；自操作放行（自身守卫另行）
	if err := svc.ResetPlatformUserPassword(ctx, "padmin", "padmin", "NewPass#12345x", ""); err != nil {
		t.Fatalf("admin self reset: %v", err)
	}
	// 租户侧自禁用守卫（P2：与平台 ErrSelfDisable 对齐）
	if err := svc.UpdateMyUser(ctx, "tadmin", "hpc-lab", "tadmin", "", "disabled", ""); !errors.Is(err, admin.ErrSelfDisable) {
		t.Fatalf("tenant_admin self disable: err=%v, want ErrSelfDisable", err)
	}
}
