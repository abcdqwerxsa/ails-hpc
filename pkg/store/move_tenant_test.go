package store

import (
	"context"
	"testing"
)

// MoveUserTenant 的核心场景：普通租户 member → system 租户 admin（此前无接口可走的
// "提升平台管理员"路径），及归属非法组合拒绝、幂等边界。
func TestMoveUserTenant(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	impl := st.(*sqliteStore)

	if _, err := impl.CreateTenant(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := impl.CreateUser(ctx, NewUser{Username: "alice", Password: "pw123456", Role: "member", TenantSlug: "acme"}); err != nil {
		t.Fatal(err)
	}

	// 迁移 + 改派 admin（同事务）
	if err := impl.MoveUserTenant(ctx, "alice", "system", "admin"); err != nil {
		t.Fatalf("move to system+admin: %v", err)
	}
	u, ok := impl.Lookup("alice")
	if !ok {
		t.Fatal("lookup failed")
	}
	if u.TenantSlug != "system" || u.Role != "admin" {
		t.Fatalf("want system/admin, got %s/%s", u.TenantSlug, u.Role)
	}
	perms := u.Permissions
	if len(perms) == 0 {
		t.Fatal("permissions not populated from role")
	}

	// 迁回普通租户须同时降级角色（member 在 system / admin 在 acme 均非法）
	if err := impl.MoveUserTenant(ctx, "alice", "acme", "admin"); err == nil {
		t.Fatal("admin in real tenant should be rejected")
	}
	if err := impl.MoveUserTenant(ctx, "alice", "acme", "member"); err != nil {
		t.Fatalf("move back acme+member: %v", err)
	}
	u, _ = impl.Lookup("alice")
	if u.TenantSlug != "acme" || u.Role != "member" {
		t.Fatalf("want acme/member, got %s/%s", u.TenantSlug, u.Role)
	}

	// 目标租户不存在 / 用户不存在
	if err := impl.MoveUserTenant(ctx, "alice", "nope", "member"); err == nil {
		t.Fatal("unknown tenant should be rejected")
	}
	if err := impl.MoveUserTenant(ctx, "ghost", "system", "admin"); err == nil {
		t.Fatal("unknown user should be rejected")
	}
}
