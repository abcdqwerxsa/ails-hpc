package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"ails-hpc/pkg/auth"
)

// TestMigrationV3SeedsSystemRoles v3 迁移：roles 表就位、四内置角色 seed 为系统角色、
// permissions JSON 与 auth.BuiltinRolePermissions 一致（构建期快照）。
func TestMigrationV3SeedsSystemRoles(t *testing.T) {
	st := newTestStore(t)
	impl := st.(*sqliteStore)

	rows, err := impl.db.Query(`SELECT name, base_role, is_system, tenant_id, permissions
		FROM roles ORDER BY id`)
	if err != nil {
		t.Fatalf("query roles: %v", err)
	}
	defer rows.Close()

	got := map[string]struct {
		base  string
		sys   bool
		perms []string
	}{}
	for rows.Next() {
		var name, base string
		var isSys bool
		var tenantID any
		var perms string
		if err := rows.Scan(&name, &base, &isSys, &tenantID, &perms); err != nil {
			t.Fatalf("scan: %v", err)
		}
		var list []string
		if err := json.Unmarshal([]byte(perms), &list); err != nil {
			t.Fatalf("unmarshal perms of %s: %v", name, err)
		}
		if !isSys {
			t.Errorf("role %s must be is_system=1", name)
		}
		if tenantID != nil {
			t.Errorf("builtin role %s must have NULL tenant_id (platform)", name)
		}
		if base != name {
			t.Errorf("builtin role %s base_role=%s, want self", name, base)
		}
		got[name] = struct {
			base  string
			sys   bool
			perms []string
		}{base, isSys, list}
	}
	if len(got) != 4 {
		t.Fatalf("seeded %d system roles, want 4 (%v)", len(got), got)
	}
	for role, want := range auth.BuiltinRolePermissions {
		have := got[role].perms
		if len(have) != len(want) {
			t.Fatalf("role %s: seeded %d perms %v, want %d %v", role, len(have), have, len(want), want)
		}
		set := map[string]bool{}
		for _, p := range have {
			set[p] = true
		}
		for _, p := range want {
			if !set[p] {
				t.Errorf("role %s: seeded perms missing %q (have %v)", role, p, have)
			}
		}
	}
}

// TestMigrationV3BackfillsRoleID 存量 users.role 字符串 → role_id 回填（导入后开库即验）。
func TestMigrationV3BackfillsRoleID(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := ImportYaml(st, writeYaml(t, dir)); err != nil {
		t.Fatalf("import: %v", err)
	}
	_ = st.Close()

	// 重开：v3 已在首开应用；此处验证的是导入写入的 role_id 与 LEFT JOIN 读投影。
	st2, err := Open(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()

	member, ok := st2.Lookup("member")
	if !ok {
		t.Fatal("member not found")
	}
	if member.RoleID == 0 {
		t.Error("member.role_id must be backfilled to the seeded system role")
	}
	if member.Role != auth.RoleMember {
		t.Errorf("member.Role = %q, want member (base role)", member.Role)
	}
	if member.RoleName != "" {
		t.Errorf("member.RoleName = %q, want empty (builtin role)", member.RoleName)
	}
	// DB 权威权限：与内置映射一致（解析链：claims.Perms ← roles.permissions JSON）
	set := map[string]bool{}
	for _, p := range member.Permissions {
		set[p] = true
	}
	for _, p := range auth.BuiltinRolePermissions[auth.RoleMember] {
		if !set[p] {
			t.Errorf("member.Permissions missing %q (have %v)", p, member.Permissions)
		}
	}
}

// TestOpenMigratesV3Idempotently 连续重开不重复 seed（唯一索引 + ON CONFLICT DO NOTHING）。
func TestOpenMigratesV3Idempotently(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.db")
	for i := 0; i < 3; i++ {
		st, err := Open(p)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		_ = st.Close()
	}
	st, err := Open(p)
	if err != nil {
		t.Fatalf("final open: %v", err)
	}
	defer st.Close()
	impl := st.(*sqliteStore)
	var n int
	if err := impl.db.QueryRow(`SELECT COUNT(*) FROM roles WHERE is_system = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("system roles = %d after repeated opens, want 4", n)
	}
}

// TestRoleNameUniqueness 平台名唯一（tenant_id IS NULL）与租户内名唯一。
func TestRoleNameUniqueness(t *testing.T) {
	st := newTestStore(t)
	impl := st.(*sqliteStore)
	// 平台重名 → 唯一索引拒绝
	if _, err := impl.db.Exec(`INSERT INTO roles (name, base_role) VALUES ('admin', 'admin')`); err == nil {
		t.Error("duplicate platform role name must be rejected")
	}
	// 租户角色与平台角色同名：允许（租户作用域独立），但 R3 服务层将禁止（防混淆）
	// —— 本测试只锁 schema 语义。
	tenID := 1 // 'system' 租户 id（Open 保证存在）
	if _, err := impl.db.Exec(`INSERT INTO roles (name, base_role, tenant_id) VALUES ('dev', 'member', ?)`, tenID); err != nil {
		t.Fatalf("tenant role insert: %v", err)
	}
	if _, err := impl.db.Exec(`INSERT INTO roles (name, base_role, tenant_id) VALUES ('dev', 'member', ?)`, tenID); err == nil {
		t.Error("duplicate tenant role name must be rejected")
	}
}

// TestForeignKeyBlocksRoleDropWithUsers 外键执行下，在用角色不可删（R5 删除处置的 DB 兜底）。
func TestForeignKeyBlocksRoleDropWithUsers(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "fk.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ImportYaml(st, writeYaml(t, dir)); err != nil {
		t.Fatal(err)
	}
	impl := st.(*sqliteStore)
	if _, err := impl.db.Exec(`DELETE FROM roles WHERE name = 'member'`); err == nil {
		t.Error("deleting a role still referenced by users must fail (FK)")
	}
	_ = st.Close()
}
