package store

// v4 迁移（oidc_sub/auth_source）与 OIDC 写面（S1/S4）回归。

import (
	"context"
	"path/filepath"
	"testing"

	"ails-hpc/pkg/auth"
)

func TestMigrationV4OIDCColumns(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "v4.db")
	st, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ImportYaml(st, writeYaml(t, dir)); err != nil {
		t.Fatal(err)
	}
	impl := st.(Store).(*sqliteStore)

	// auth_source CHECK 已扩到 oidc
	if _, err := impl.db.Exec(`UPDATE users SET auth_source = 'oidc' WHERE username = 'member'`); err != nil {
		t.Fatalf("auth_source=oidc must be accepted after v4: %v", err)
	}
	if _, err := impl.db.Exec(`UPDATE users SET auth_source = 'wechat' WHERE username = 'member'`); err == nil {
		t.Fatal("auth_source CHECK must still reject unknown values")
	}
	// 重建表保数据（导入 3 用户全在 + role_id 回填未被破坏）
	var n int
	if err := impl.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("users count = %d, want 3 (rebuild must preserve rows)", n)
	}
	var nullRoleID int
	if err := impl.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role_id IS NULL`).Scan(&nullRoleID); err != nil {
		t.Fatal(err)
	}
	if nullRoleID != 0 {
		t.Errorf("role_id backfill lost for %d users during v4 rebuild", nullRoleID)
	}
	_ = st.Close()
}

func TestOIDCLinkUnlink(t *testing.T) {
	st := newTestStore(t).(AdminStore)
	ctx := context.Background()
	if _, err := st.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(ctx, NewUser{
		Username: "alice", Password: "alice12345", Role: auth.RoleMember, TenantSlug: "hpc-lab",
	}); err != nil {
		t.Fatal(err)
	}

	// 绑定 → 查回落
	if err := st.LinkOIDC("alice", "sub-1"); err != nil {
		t.Fatalf("link: %v", err)
	}
	u, ok := st.UserByOIDCSub("sub-1")
	if !ok || u.Username != "alice" {
		t.Fatalf("UserByOIDCSub = %v %v", u, ok)
	}
	if u.AuthSource != "local" {
		t.Errorf("auth_source = %q, want local (绑定不改来源——密码并行)", u.AuthSource)
	}

	// 重复绑定同一 sub 到他人 → ErrAlreadyLinked
	if _, err := st.CreateUser(ctx, NewUser{
		Username: "bob", Password: "bob1234567", Role: auth.RoleMember, TenantSlug: "hpc-lab",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkOIDC("bob", "sub-1"); err == nil {
		t.Fatal("one sub must not bind to two accounts")
	}

	// 解绑 → 再查不中
	if err := st.UnlinkOIDC("alice"); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if _, ok := st.UserByOIDCSub("sub-1"); ok {
		t.Fatal("sub must be unlinked")
	}
}

func TestOIDCUnlinkRefusesOIDCOnlyAccounts(t *testing.T) {
	st := newTestStore(t).(AdminStore)
	impl := st.(Store).(*sqliteStore)
	// 直接落一个 auth_source=oidc 账号（JIT 形态）
	if _, err := impl.db.Exec(`INSERT INTO users (username, password_hash, role, tenant_id,
		cluster_user, uid, gid, account, auth_source, oidc_sub)
		SELECT 'sso_user', 'x', 'member', t.id, 'sso_user', 2900, 2000, 'sso_user', 'oidc', 'sub-9'
		FROM tenants t WHERE t.slug = 'system'`); err != nil {
		t.Fatal(err)
	}
	if err := st.UnlinkOIDC("sso_user"); err == nil {
		t.Fatal("auth_source=oidc account must not be unlinked (would lock out)")
	}
}

func TestOIDCProvision(t *testing.T) {
	st := newTestStore(t).(AdminStore)
	ctx := context.Background()
	if _, err := st.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatal(err)
	}

	u, err := st.ProvisionOIDCUser("zhang_san", "z@ex.com", "Zhang San", auth.RoleMember, "hpc-lab", "sub-100")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if u.Username != "zhang_san" || u.AuthSource != "oidc" || u.TenantSlug != "hpc-lab" {
		t.Errorf("provisioned = %+v", u)
	}
	// 回查
	got, ok := st.UserByOIDCSub("sub-100")
	if !ok || got.Username != "zhang_san" {
		t.Fatalf("lookup by sub failed: %v %v", got, ok)
	}
	// OIDC 账号本地密码不可用（随机哈希）
	if err := auth.CompareHashAndPassword(got.PasswordHash, "zhang_san"); err == nil {
		t.Error("random local password must not match the username")
	}
	// 角色-租户归属：member 不可住 system
	if _, err := st.ProvisionOIDCUser("bad1", "", "", auth.RoleMember, "system", "sub-101"); err == nil {
		t.Error("member into 'system' must be rejected")
	}
	// admin 必须住 system
	if _, err := st.ProvisionOIDCUser("bad2", "", "", auth.RoleSystemAdmin, "hpc-lab", "sub-102"); err == nil {
		t.Error("admin into non-system tenant must be rejected")
	}
	// 挂起租户不可 JIT
	if err := st.SetTenantStatus(ctx, "hpc-lab", "suspended"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ProvisionOIDCUser("bad3", "", "", auth.RoleMember, "hpc-lab", "sub-103"); err == nil {
		t.Error("provisioning into suspended tenant must be rejected")
	}
}
