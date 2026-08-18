package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ails-hpc/pkg/auth"
)

// newAdminStore 开一个测试库并断言其写面可用（Open 返回 Store，db 实现满足 AdminStore）。
func newAdminStore(t *testing.T) AdminStore {
	t.Helper()
	st := newTestStore(t)
	admin, ok := st.(AdminStore)
	if !ok {
		t.Fatalf("sqlite store does not implement AdminStore")
	}
	return admin
}

// rawInsertUser 绕过 CreateUser 直插一行用户（uid 带宽/用尽测试的钩子）。
func rawInsertUser(t *testing.T, st Store, username string, uid int) {
	t.Helper()
	impl, ok := st.(*sqliteStore)
	if !ok {
		t.Fatalf("rawInsertUser requires the sqlite store")
	}
	_, err := impl.db.Exec(`
		INSERT INTO users (username, password_hash, role, tenant_id, cluster_user, uid, gid, account)
		SELECT ?, 'raw-no-login', 'member', t.id, ?, ?, 2000, ?
		FROM tenants t WHERE t.slug = 'hpc-lab'`, username, username, uid, username)
	if err != nil {
		t.Fatalf("rawInsertUser %s: %v", username, err)
	}
}

func TestCreateTenant(t *testing.T) {
	ctx := context.Background()
	admin := newAdminStore(t)

	tn, err := admin.CreateTenant(ctx, "hpc-lab", "HPC Lab")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if tn.ID == 0 || tn.Slug != "hpc-lab" || tn.Name != "HPC Lab" ||
		tn.ParentAccount != "hpc-lab" || tn.Status != "active" {
		t.Errorf("created tenant = %+v", tn)
	}
	if got, err := admin.TenantBySlug(ctx, "hpc-lab"); err != nil || got.ID != tn.ID {
		t.Errorf("TenantBySlug after create: %v, %v", got, err)
	}

	// 空 name 默认 = slug
	if tn, err := admin.CreateTenant(ctx, "bio-lab", ""); err != nil || tn.Name != "bio-lab" {
		t.Errorf("empty name should default to slug: %+v, %v", tn, err)
	}

	// 重复 slug
	if _, err := admin.CreateTenant(ctx, "hpc-lab", "again"); !errors.Is(err, ErrTenantExists) {
		t.Errorf("duplicate slug: want ErrTenantExists, got %v", err)
	}

	// 保留租户 'system' 不可经 API 建
	if _, err := admin.CreateTenant(ctx, "system", "System"); !errors.Is(err, ErrTenantReserved) {
		t.Errorf("'system': want ErrTenantReserved, got %v", err)
	}

	// slug 字符集
	if _, err := admin.CreateTenant(ctx, "Bad Slug!", "x"); !errors.Is(err, ErrInvalidSlug) {
		t.Errorf("bad slug: want ErrInvalidSlug, got %v", err)
	}
}

func TestSetTenantStatus(t *testing.T) {
	ctx := context.Background()
	admin := newAdminStore(t)
	if _, err := admin.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatal(err)
	}

	// suspend / resume
	if err := admin.SetTenantStatus(ctx, "hpc-lab", "suspended"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if got, _ := admin.TenantBySlug(ctx, "hpc-lab"); got.Status != "suspended" {
		t.Errorf("status = %q, want suspended", got.Status)
	}
	if err := admin.SetTenantStatus(ctx, "hpc-lab", "active"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got, _ := admin.TenantBySlug(ctx, "hpc-lab"); got.Status != "active" {
		t.Errorf("status = %q, want active", got.Status)
	}

	// 非法状态 / 未知租户
	if err := admin.SetTenantStatus(ctx, "hpc-lab", "paused"); !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("bad status: want ErrInvalidStatus, got %v", err)
	}
	if err := admin.SetTenantStatus(ctx, "ghost", "active"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown tenant: want ErrNotFound, got %v", err)
	}

	// 'system' 不可挂起（admin/ops_admin 全住其中，挂起 = 平台自锁）
	if err := admin.SetTenantStatus(ctx, "system", "suspended"); !errors.Is(err, ErrTenantReserved) {
		t.Errorf("suspend 'system': want ErrTenantReserved, got %v", err)
	}
}

func TestOpenEnsuresSystemTenant(t *testing.T) {
	admin := newAdminStore(t)
	tn, err := admin.TenantBySlug(context.Background(), "system")
	if err != nil || tn.Slug != "system" || tn.Status != "active" || tn.ParentAccount != "system" {
		t.Errorf("fresh Open must guarantee reserved 'system' tenant: %+v, %v", tn, err)
	}
	// 自动供给不等于开放 API 创建：'system' 仍拒绝经 CreateTenant 建。
	if _, err := admin.CreateTenant(context.Background(), "system", "x"); !errors.Is(err, ErrTenantReserved) {
		t.Errorf("CreateTenant('system') after auto-provision: want ErrTenantReserved, got %v", err)
	}
}

func TestCreateUser(t *testing.T) {
	ctx := context.Background()
	admin := newAdminStore(t)
	if _, err := admin.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.CreateTenant(ctx, "bio-lab", ""); err != nil {
		t.Fatal(err)
	}

	// 默认值 + bcrypt
	u, err := admin.CreateUser(ctx, NewUser{
		Username: "alice", Password: "password123",
		Role: auth.RoleMember, TenantSlug: "hpc-lab",
		DisplayName: "Alice", Email: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ClusterUser != "alice" || u.Account != "alice" || u.UID != 2001 || u.GID != 2000 {
		t.Errorf("defaults wrong: clusterUser=%q account=%q uid=%d gid=%d",
			u.ClusterUser, u.Account, u.UID, u.GID)
	}
	if u.TenantSlug != "hpc-lab" || u.OrgSlug != "hpc-lab" || u.Status != "active" || u.TokenVersion != 0 {
		t.Errorf("derived fields wrong: %+v", u)
	}
	if u.PasswordHash == "password123" {
		t.Error("password must not be stored in clear")
	}
	if err := auth.CompareHashAndPassword(u.PasswordHash, "password123"); err != nil {
		t.Errorf("hash does not verify: %v", err)
	}
	// 读面回读一致
	if got, ok := admin.Lookup("alice"); !ok || got.UID != 2001 || got.ClusterUser != "alice" ||
		got.TenantSlug != "hpc-lab" || got.Status != "active" {
		t.Errorf("Lookup(alice) = %+v, ok=%v", got, ok)
	}
	if _, err := admin.Verify("alice", "password123"); err != nil {
		t.Errorf("Verify fresh user: %v", err)
	}

	// 显式 clusterUser/UID 按传入值；account 回退 clusterUser
	u2, err := admin.CreateUser(ctx, NewUser{
		Username: "bob", Password: "password123",
		Role: auth.RoleTenantAdmin, TenantSlug: "hpc-lab",
		ClusterUser: "bob_cu", UID: 2500,
	})
	if err != nil || u2.ClusterUser != "bob_cu" || u2.Account != "bob_cu" || u2.UID != 2500 {
		t.Errorf("explicit clusterUser/uid: %+v, %v", u2, err)
	}

	// 角色-租户归属（设计 §2.3）
	if _, err := admin.CreateUser(ctx, NewUser{
		Username: "sadmin", Password: "password123",
		Role: auth.RoleSystemAdmin, TenantSlug: "hpc-lab",
	}); !errors.Is(err, ErrRoleTenantMismatch) {
		t.Errorf("admin→hpc-lab: want ErrRoleTenantMismatch, got %v", err)
	}
	if _, err := admin.CreateUser(ctx, NewUser{
		Username: "opsadmin", Password: "password123",
		Role: auth.RoleOpsAdmin, TenantSlug: "system",
	}); err != nil {
		t.Errorf("ops_admin→system should be allowed: %v", err)
	}
	if _, err := admin.CreateUser(ctx, NewUser{
		Username: "m2", Password: "password123",
		Role: auth.RoleMember, TenantSlug: "system",
	}); !errors.Is(err, ErrRoleTenantMismatch) {
		t.Errorf("member→system: want ErrRoleTenantMismatch, got %v", err)
	}
	if _, err := admin.CreateUser(ctx, NewUser{
		Username: "m3", Password: "password123",
		Role: auth.RoleMember, TenantSlug: "ghost",
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("member→ghost tenant: want ErrNotFound, got %v", err)
	}

	// 唯一性：username / cluster_user / uid / account → ErrDuplicateUser
	dup := NewUser{Password: "password123", Role: auth.RoleMember, TenantSlug: "bio-lab"}
	for _, tc := range []struct {
		name string
		in   NewUser
	}{
		{"username", func() NewUser { d := dup; d.Username = "alice"; return d }()},
		{"cluster_user", func() NewUser {
			d := dup
			d.Username, d.ClusterUser = "carol", "alice"
			return d
		}()},
		{"uid", func() NewUser {
			d := dup
			d.Username, d.UID = "dave", 2001
			return d
		}()},
		{"account", func() NewUser {
			d := dup
			d.Username, d.Account = "erin", "alice"
			return d
		}()},
	} {
		if _, err := admin.CreateUser(ctx, tc.in); !errors.Is(err, ErrDuplicateUser) {
			t.Errorf("duplicate %s: want ErrDuplicateUser, got %v", tc.name, err)
		}
	}

	// 入参校验
	bad := []struct {
		name string
		in   NewUser
		want error
	}{
		{"username charset", NewUser{Username: "Bad Name", Password: "password123", Role: auth.RoleMember, TenantSlug: "hpc-lab"}, ErrInvalidUsername},
		{"role", NewUser{Username: "frank", Password: "password123", Role: "superuser", TenantSlug: "hpc-lab"}, ErrInvalidRole},
		{"short password", NewUser{Username: "gina", Password: "short", Role: auth.RoleMember, TenantSlug: "hpc-lab"}, ErrWeakPassword},
		{"clusterUser charset", NewUser{Username: "hank", Password: "password123", Role: auth.RoleMember, TenantSlug: "hpc-lab", ClusterUser: "Bad CU"}, ErrInvalidClusterUser},
		{"account charset", NewUser{Username: "iris", Password: "password123", Role: auth.RoleMember, TenantSlug: "hpc-lab", Account: "Bad Acct"}, ErrInvalidAccount},
		{"uid below band", NewUser{Username: "jack", Password: "password123", Role: auth.RoleMember, TenantSlug: "hpc-lab", UID: 1500}, ErrInvalidUID},
		{"uid above band", NewUser{Username: "kate", Password: "password123", Role: auth.RoleMember, TenantSlug: "hpc-lab", UID: 3500}, ErrInvalidUID},
	}
	for _, tc := range bad {
		if _, err := admin.CreateUser(ctx, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("validation %s: want %v, got %v", tc.name, tc.want, err)
		}
	}

	// 失败的建用户不得留半行
	for _, u := range admin.ListUsers() {
		switch u.Username {
		case "carol", "dave", "erin", "frank", "gina", "hank", "iris", "jack", "kate", "m2", "m3", "sadmin":
			t.Errorf("rejected user %s must not exist", u.Username)
		}
	}
}

func TestNextUID(t *testing.T) {
	ctx := context.Background()
	admin := newAdminStore(t)
	if _, err := admin.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatal(err)
	}

	// 空库起点
	if uid, err := admin.NextUID(ctx); err != nil || uid != 2001 {
		t.Errorf("NextUID empty = %d, %v; want 2001", uid, err)
	}

	// max+1
	rawInsertUser(t, admin, "raw1", 2001)
	if uid, err := admin.NextUID(ctx); err != nil || uid != 2002 {
		t.Errorf("NextUID after 2001 = %d, %v; want 2002", uid, err)
	}

	// 库内遗留低 uid 不把分配拉出带宽下界
	st2 := newTestStore(t)
	admin2, ok := st2.(AdminStore)
	if !ok {
		t.Fatal("sqlite store does not implement AdminStore")
	}
	if _, err := admin2.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatal(err)
	}
	rawInsertUser(t, st2, "legacy", 1500)
	if uid, err := admin2.NextUID(ctx); err != nil || uid != 2001 {
		t.Errorf("NextUID with legacy uid 1500 = %d, %v; want 2001 (clamped)", uid, err)
	}

	// 带宽用尽：2999 已占 → 下一个越界
	rawInsertUser(t, admin, "rawmax", 2999)
	if _, err := admin.NextUID(ctx); !errors.Is(err, ErrUIDExhausted) {
		t.Errorf("NextUID after 2999: want ErrUIDExhausted, got %v", err)
	}
	// 用尽后 CreateUser（uid=0 走 NextUID）同样失败
	if _, err := admin.CreateUser(ctx, NewUser{
		Username: "noroom", Password: "password123", Role: auth.RoleMember, TenantSlug: "hpc-lab",
	}); !errors.Is(err, ErrUIDExhausted) {
		t.Errorf("CreateUser with exhausted band: want ErrUIDExhausted, got %v", err)
	}
}

func TestUpdateUserStatus(t *testing.T) {
	ctx := context.Background()
	admin := newAdminStore(t)
	if _, err := admin.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.CreateUser(ctx, NewUser{
		Username: "alice", Password: "password123", Role: auth.RoleMember, TenantSlug: "hpc-lab",
	}); err != nil {
		t.Fatal(err)
	}

	v0, ok := admin.UserVersion("alice")
	if !ok || v0 != 0 {
		t.Fatalf("UserVersion = %d, %v; want 0", v0, ok)
	}

	// disable：状态 + token_version+1（在途令牌吊销）+ 登录被拒
	if err := admin.UpdateUserStatus(ctx, "alice", "disabled"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if v, _ := admin.UserVersion("alice"); v != v0+1 {
		t.Errorf("version after disable = %d, want %d", v, v0+1)
	}
	if u, ok := admin.Lookup("alice"); !ok || u.Status != "disabled" {
		t.Errorf("Lookup after disable = %+v, %v", u, ok)
	}
	if _, err := admin.Verify("alice", "password123"); err != auth.ErrInvalidCredentials {
		t.Errorf("Verify disabled user: want ErrInvalidCredentials, got %v", err)
	}

	// re-enable：版本不回退（旧令牌不复活）
	if err := admin.UpdateUserStatus(ctx, "alice", "active"); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if v, _ := admin.UserVersion("alice"); v != v0+1 {
		t.Errorf("version after re-enable = %d, want %d (no rollback)", v, v0+1)
	}
	if u, ok := admin.Lookup("alice"); !ok || u.Status != "active" {
		t.Errorf("Lookup after re-enable = %+v, %v", u, ok)
	}
	if _, err := admin.Verify("alice", "password123"); err != nil {
		t.Errorf("Verify re-enabled user: %v", err)
	}

	// 非法状态 / 未知用户
	if err := admin.UpdateUserStatus(ctx, "alice", "paused"); !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("bad status: want ErrInvalidStatus, got %v", err)
	}
	if err := admin.UpdateUserStatus(ctx, "ghost", "disabled"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user: want ErrNotFound, got %v", err)
	}
}

func TestResetUserPassword(t *testing.T) {
	ctx := context.Background()
	admin := newAdminStore(t)
	if _, err := admin.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.CreateUser(ctx, NewUser{
		Username: "alice", Password: "password123", Role: auth.RoleMember, TenantSlug: "hpc-lab",
	}); err != nil {
		t.Fatal(err)
	}

	newHash, err := auth.BcryptGenerateFromPassword("newpassword45")
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.ResetUserPassword(ctx, "alice", newHash); err != nil {
		t.Fatalf("ResetUserPassword: %v", err)
	}
	if v, _ := admin.UserVersion("alice"); v != 1 {
		t.Errorf("version after reset = %d, want 1", v)
	}
	if _, err := admin.Verify("alice", "newpassword45"); err != nil {
		t.Errorf("Verify new password: %v", err)
	}
	if _, err := admin.Verify("alice", "password123"); err != auth.ErrInvalidCredentials {
		t.Errorf("old password must fail: got %v", err)
	}

	if err := admin.ResetUserPassword(ctx, "ghost", newHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user: want ErrNotFound, got %v", err)
	}
	if err := admin.ResetUserPassword(ctx, "alice", "not-a-hash"); !errors.Is(err, ErrInvalidHash) {
		t.Errorf("non-bcrypt hash: want ErrInvalidHash, got %v", err)
	}
}

func TestListTenantUsers(t *testing.T) {
	ctx := context.Background()
	admin := newAdminStore(t)
	for _, slug := range []string{"hpc-lab", "bio-lab"} {
		if _, err := admin.CreateTenant(ctx, slug, ""); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(username, tenant, role string) {
		t.Helper()
		if _, err := admin.CreateUser(ctx, NewUser{
			Username: username, Password: "password123", Role: role, TenantSlug: tenant,
		}); err != nil {
			t.Fatalf("create %s: %v", username, err)
		}
	}
	mk("zoe", "hpc-lab", auth.RoleMember)
	mk("alice", "hpc-lab", auth.RoleMember)
	mk("bill", "bio-lab", auth.RoleMember)
	mk("opsadmin", "system", auth.RoleOpsAdmin) // system 里唯一的（ops_admin 角色才能住 system）

	users, err := admin.ListTenantUsers(ctx, "hpc-lab")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Username != "alice" || users[1].Username != "zoe" {
		t.Errorf("hpc-lab users = %+v, want [alice zoe] sorted", users)
	}
	for _, u := range users {
		if u.TenantSlug != "hpc-lab" {
			t.Errorf("user %s tenant = %q, want hpc-lab", u.Username, u.TenantSlug)
		}
		if u.PasswordHash != "" {
			t.Errorf("user %s leaks password hash", u.Username)
		}
	}

	// JSON 序列化不含任何口令痕迹（auth.User 对 hash json:"-"，外加置空防线）
	b, err := json.Marshal(users)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Contains(strings.ToLower(s), "password") || strings.Contains(s, "$2") {
		t.Errorf("marshaled tenant users leak secret material: %s", s)
	}

	// 其他租户不受串扰；未知租户 = 空列表
	if got, _ := admin.ListTenantUsers(ctx, "bio-lab"); len(got) != 1 || got[0].Username != "bill" {
		t.Errorf("bio-lab users = %+v, want [bill]", got)
	}
	if got, _ := admin.ListTenantUsers(ctx, "system"); len(got) != 1 || got[0].Username != "opsadmin" {
		t.Errorf("system users = %+v, want [opsadmin]", got)
	}
	if got, err := admin.ListTenantUsers(ctx, "ghost"); err != nil || len(got) != 0 {
		t.Errorf("ghost tenant users = %+v, %v; want empty, nil", got, err)
	}
}

func TestWriteAudit(t *testing.T) {
	ctx := context.Background()
	admin := newAdminStore(t)

	if err := admin.WriteAudit(ctx, "admin", "user.create", "user:alice", "req-123", `{"role":"member"}`); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}
	// 空 detail 默认 '{}'
	if err := admin.WriteAudit(ctx, "admin", "tenant.create", "tenant:hpc-lab", "req-124", ""); err != nil {
		t.Fatalf("WriteAudit (empty detail): %v", err)
	}

	impl := admin.(*sqliteStore)
	rows, err := impl.db.Query(`
		SELECT actor, action, target, detail, request_id FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []map[string]string
	for rows.Next() {
		var actor, action, target, detail, requestID string
		if err := rows.Scan(&actor, &action, &target, &detail, &requestID); err != nil {
			t.Fatal(err)
		}
		got = append(got, map[string]string{
			"actor": actor, "action": action, "target": target,
			"detail": detail, "request_id": requestID,
		})
	}
	if len(got) != 2 {
		t.Fatalf("audit_log rows = %d, want 2: %v", len(got), got)
	}
	if got[0]["actor"] != "admin" || got[0]["action"] != "user.create" || got[0]["target"] != "user:alice" ||
		got[0]["detail"] != `{"role":"member"}` || got[0]["request_id"] != "req-123" {
		t.Errorf("row 1 = %v", got[0])
	}
	if got[1]["detail"] != "{}" {
		t.Errorf("empty detail should default to '{}', got %q", got[1]["detail"])
	}
}

func TestCreateUserSuspendedTenant(t *testing.T) {
	ctx := context.Background()
	admin := newAdminStore(t)
	if _, err := admin.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatal(err)
	}
	if err := admin.SetTenantStatus(ctx, "hpc-lab", "suspended"); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.CreateUser(ctx, NewUser{
		Username: "alice", Password: "password123", Role: auth.RoleMember, TenantSlug: "hpc-lab",
	}); !errors.Is(err, ErrTenantSuspended) {
		t.Errorf("create into suspended tenant: want ErrTenantSuspended, got %v", err)
	}
	if u, ok := admin.Lookup("alice"); ok {
		t.Errorf("rejected user must not be persisted: %+v", u)
	}

	// 恢复后可建
	if err := admin.SetTenantStatus(ctx, "hpc-lab", "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.CreateUser(ctx, NewUser{
		Username: "alice", Password: "password123", Role: auth.RoleMember, TenantSlug: "hpc-lab",
	}); err != nil {
		t.Errorf("create after resume: %v", err)
	}
}

// TestForeignKeyBlocksOrphanUser：C1 外键执行——绕过应用层直插孤儿 tenant_id 必须失败。
func TestForeignKeyBlocksOrphanUser(t *testing.T) {
	st := newTestStore(t)
	impl := st.(*sqliteStore)
	_, err := impl.db.Exec(`INSERT INTO users (username, password_hash, role, tenant_id, cluster_user, uid, gid, account)
		VALUES ('orphan', 'x', 'member', 99999, 'orphan_cu', 2999, 2000, 'orphan_cu')`)
	if err == nil {
		t.Fatal("FK not enforced: orphan tenant_id insert must fail")
	}
}

// TestUpdateUserDisplayNameAndListPlatformUsers v3-U：显示名写入/清除/超长，
// 与全平台目录（跨租户、含显示名、不含哈希）。
func TestUpdateUserDisplayNameAndListPlatformUsers(t *testing.T) {
	ctx := context.Background()
	admin := newAdminStore(t)
	for _, slug := range []string{"hpc-lab", "bio-lab"} {
		if _, err := admin.CreateTenant(ctx, slug, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := admin.CreateUser(ctx, NewUser{
		Username: "alice", Password: "password123", Role: auth.RoleMember, TenantSlug: "hpc-lab",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.CreateUser(ctx, NewUser{
		Username: "bob", Password: "password123", Role: auth.RoleMember, TenantSlug: "bio-lab",
	}); err != nil {
		t.Fatal(err)
	}

	// 写入 → ListTenantUsers/ListPlatformUsers 都能看到
	if err := admin.UpdateUserDisplayName(ctx, "alice", "Alice Zhang"); err != nil {
		t.Fatalf("set display name: %v", err)
	}
	tusers, err := admin.ListTenantUsers(ctx, "hpc-lab")
	if err != nil {
		t.Fatal(err)
	}
	if len(tusers) != 1 || tusers[0].DisplayName != "Alice Zhang" {
		t.Errorf("tenant users after rename = %+v", tusers)
	}

	// 全平台目录：跨租户、按 username 排序、哈希清空
	all, err := admin.ListPlatformUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Username != "alice" || all[1].Username != "bob" {
		t.Fatalf("platform users = %+v", all)
	}
	if all[0].DisplayName != "Alice Zhang" || all[0].TenantSlug != "hpc-lab" || all[1].TenantSlug != "bio-lab" {
		t.Errorf("platform rows = %+v", all)
	}
	for _, u := range all {
		if u.PasswordHash != "" {
			t.Errorf("platform directory leaked hash for %s", u.Username)
		}
	}

	// 超长 → ErrInvalidDisplayName；不存在 → ErrNotFound；清除（空串）
	if err := admin.UpdateUserDisplayName(ctx, "alice", strings.Repeat("x", 65)); !errors.Is(err, ErrInvalidDisplayName) {
		t.Errorf("oversize display name: want ErrInvalidDisplayName, got %v", err)
	}
	if err := admin.UpdateUserDisplayName(ctx, "ghost", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user: want ErrNotFound, got %v", err)
	}
	if err := admin.UpdateUserDisplayName(ctx, "alice", ""); err != nil {
		t.Fatalf("clear display name: %v", err)
	}
	if all, _ = admin.ListPlatformUsers(ctx); all[0].DisplayName != "" {
		t.Errorf("clear display name failed: %+v", all[0])
	}
}
