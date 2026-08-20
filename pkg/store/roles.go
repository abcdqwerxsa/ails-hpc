package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"ails-hpc/pkg/auth"
)

// resyncBuiltinRoles 把四个内置系统角色的 permissions 行对齐到代码权威清单
// （auth.BuiltinRolePermissions）。系统角色不可经 API 改（ErrRoleSystem），而词汇表
// 扩充（如 partitions:manage/users:manage）只改代码——没有 resync 就没有合法通道让
// 旧库跟上（#64 生产 verify 抓到 admin 面 403 即此坑）。幂等，Open 每次执行。
func resyncBuiltinRoles(ctx context.Context, db *sql.DB) error {
	for name, perms := range auth.BuiltinRolePermissions {
		sorted := append([]string(nil), perms...)
		sort.Strings(sorted)
		buf, err := json.Marshal(sorted)
		if err != nil {
			return fmt.Errorf("store: builtin role %s: %w", name, err)
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE roles SET permissions = ? WHERE is_system = 1 AND tenant_id IS NULL AND name = ?`,
			string(buf), name); err != nil {
			return fmt.Errorf("store: resync builtin role %s: %w", name, err)
		}
	}
	return nil
}

// 角色管理错误（R3；handler 按 sentinel 映射 HTTP：escalation/reserved/invalid→400，
// system/in-use/exists→409，not-found→404）。
var (
	// ErrRoleExists 角色名在作用域内已存在（平台全局 / 租户内）。
	ErrRoleExists = errors.New("store: role already exists")
	// ErrRoleSystem 内置系统角色不可删改（is_system=1，升级路径与默认行为的锚）。
	ErrRoleSystem = errors.New("store: system role is immutable")
	// ErrRoleInUse 角色仍有用户在用（须先改派再删；外键为 DB 层兜底）。
	ErrRoleInUse = errors.New("store: role is in use by users")
	// ErrRoleReserved 自定义角色名与内置四角色重名（防显示与语义混淆）。
	ErrRoleReserved = errors.New("store: role name reserved by builtin role")
	// ErrInvalidPermission 权限点不在权威词汇表（auth.AllPermissions）内。
	ErrInvalidPermission = errors.New("store: invalid permission point")
	// ErrInvalidBaseRole base_role 不在角色作用域允许的基角色集合内。
	ErrInvalidBaseRole = errors.New("store: invalid base role")
	// ErrRoleTenantMismatch 复用 admin.go 的同名 sentinel（用户/角色两侧的角色-租户
	// 归属违规共用一个 400 语义）。
)

// Role 是 roles 表的读投影（含租户 slug 与在用用户数，管理页显示用）。
type Role struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	BaseRole    string   `json:"baseRole"`
	IsSystem    bool     `json:"isSystem"`
	TenantSlug  string   `json:"tenantSlug,omitempty"` // 空 = 平台角色
	UserCount   int      `json:"userCount"`
}

// NewRole 是 CreateRole 的入参。
type NewRole struct {
	Name        string
	Description string
	Permissions []string
	BaseRole    string // 数据范围基角色（scope 推导）
	TenantSlug  string // 空 = 平台角色
}

const roleSelect = `
SELECT r.id, r.name, r.description, r.permissions, r.base_role, r.is_system,
       COALESCE(t.slug, ''), (SELECT COUNT(*) FROM users u WHERE u.role_id = r.id)
FROM roles r LEFT JOIN tenants t ON t.id = r.tenant_id`

func scanRole(row interface{ Scan(...any) error }) (*Role, error) {
	var r Role
	var perms string
	if err := row.Scan(&r.ID, &r.Name, &r.Description, &perms, &r.BaseRole,
		&r.IsSystem, &r.TenantSlug, &r.UserCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(perms), &r.Permissions); err != nil {
		return nil, fmt.Errorf("store: role %s permissions JSON: %w", r.Name, err)
	}
	return &r, nil
}

// validateRolePerms 权限点白名单（词汇表外一律拒绝——自定义角色不可发明权限）。
func validateRolePerms(perms []string) error {
	valid := map[string]bool{}
	for _, p := range auth.AllPermissions {
		valid[p] = true
	}
	for _, p := range perms {
		if !valid[p] {
			return fmt.Errorf("%w: %q", ErrInvalidPermission, p)
		}
	}
	return nil
}

// ListRoles 列角色。tenantSlug="" → 平台角色（tenant_id IS NULL）；否则该租户的角色
// （含平台角色不含——租户只见自己的自定义角色 + 服务层另行附内置角色语义）。
func (s *sqliteStore) ListRoles(ctx context.Context, tenantSlug string) ([]Role, error) {
	q := roleSelect
	var args []any
	if tenantSlug == "" {
		q += ` WHERE r.tenant_id IS NULL`
	} else {
		q += ` JOIN tenants tt ON tt.slug = ? WHERE r.tenant_id = tt.id`
		args = append(args, tenantSlug)
	}
	q += ` ORDER BY r.is_system DESC, r.name`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// RoleByName 按名称查角色。tenantSlug="" → 平台作用域；否则租户作用域（不含平台）。
func (s *sqliteStore) RoleByName(ctx context.Context, tenantSlug, name string) (*Role, error) {
	q := roleSelect
	var args []any
	if tenantSlug == "" {
		q += ` WHERE r.tenant_id IS NULL AND r.name = ?`
	} else {
		q += ` JOIN tenants tt ON tt.slug = ? WHERE r.tenant_id = tt.id AND r.name = ?`
		args = append(args, tenantSlug)
	}
	args = append(args, name)
	return scanRole(s.db.QueryRowContext(ctx, q, args...))
}

// CreateRole 建自定义角色（系统角色经迁移 seed，不经本入口）。
// 基角色作用域规则：平台角色 base 四选一（2026-08-19 起放开 member/tenant_admin——平台
// 管理员需要定义"仅本人/本租户数据范围"的自定义角色，如作业提交员）；租户角色 base ∈
// {member, tenant_admin}。
func (s *sqliteStore) CreateRole(ctx context.Context, in NewRole) (*Role, error) {
	if !unixSafeRE.MatchString(in.Name) {
		return nil, fmt.Errorf("%w: %q (want ^[a-z_][a-z0-9_-]{0,31}$)", ErrInvalidUsername, in.Name)
	}
	if in.Name == auth.RoleSystemAdmin || in.Name == auth.RoleOpsAdmin ||
		in.Name == auth.RoleTenantAdmin || in.Name == auth.RoleMember {
		return nil, fmt.Errorf("%w: %s collides with a builtin role", ErrRoleReserved, in.Name)
	}
	if err := validateRolePerms(in.Permissions); err != nil {
		return nil, err
	}
	allowedBase := map[string]bool{}
	if in.TenantSlug == "" {
		allowedBase[auth.RoleSystemAdmin] = true
		allowedBase[auth.RoleOpsAdmin] = true
		allowedBase[auth.RoleTenantAdmin] = true
		allowedBase[auth.RoleMember] = true
	} else {
		allowedBase[auth.RoleTenantAdmin] = true
		allowedBase[auth.RoleMember] = true
	}
	if !allowedBase[in.BaseRole] {
		return nil, fmt.Errorf("%w: %q not allowed for a %s role",
			ErrInvalidBaseRole, in.BaseRole, scopeWord(in.TenantSlug))
	}
	sort.Strings(in.Permissions)
	permsJSON, _ := json.Marshal(in.Permissions)

	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var tenantID any // nil = 平台
		if in.TenantSlug != "" {
			var id int64
			err := tx.QueryRowContext(ctx, `SELECT id FROM tenants WHERE slug = ?`, in.TenantSlug).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: tenant %s", ErrNotFound, in.TenantSlug)
			}
			if err != nil {
				return err
			}
			tenantID = id
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO roles (name, description, permissions, base_role, is_system, tenant_id)
			VALUES (?, ?, ?, ?, 0, ?)`,
			in.Name, in.Description, string(permsJSON), in.BaseRole, tenantID); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return fmt.Errorf("%w: %s", ErrRoleExists, in.Name)
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.RoleByName(ctx, in.TenantSlug, in.Name)
}

// UpdateRole 改自定义角色的权限/描述（系统角色 ErrRoleSystem）。permissions 为 nil =
// 不改；desc 为 nil = 不改。
func (s *sqliteStore) UpdateRole(ctx context.Context, roleID int64, permissions []string, desc *string) (*Role, error) {
	cur, err := s.roleByID(ctx, roleID)
	if err != nil {
		return nil, err
	}
	if cur.IsSystem {
		return nil, fmt.Errorf("%w: %s", ErrRoleSystem, cur.Name)
	}
	if permissions != nil {
		if err := validateRolePerms(permissions); err != nil {
			return nil, err
		}
		sort.Strings(permissions)
		buf, _ := json.Marshal(permissions)
		if _, err := s.db.ExecContext(ctx,
			`UPDATE roles SET permissions = ? WHERE id = ?`, string(buf), roleID); err != nil {
			return nil, err
		}
	}
	if desc != nil {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE roles SET description = ? WHERE id = ?`, *desc, roleID); err != nil {
			return nil, err
		}
	}
	return s.roleByID(ctx, roleID)
}

// DeleteRole 删自定义角色。系统角色拒绝；在用（users.role_id 引用）拒绝——须先改派
// （FK 是 DB 层兜底，这里给出可操作的 409 文案）。
func (s *sqliteStore) DeleteRole(ctx context.Context, roleID int64) error {
	cur, err := s.roleByID(ctx, roleID)
	if err != nil {
		return err
	}
	if cur.IsSystem {
		return fmt.Errorf("%w: %s", ErrRoleSystem, cur.Name)
	}
	if cur.UserCount > 0 {
		return fmt.Errorf("%w: %s still assigned to %d user(s); reassign first",
			ErrRoleInUse, cur.Name, cur.UserCount)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM roles WHERE id = ?`, roleID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: role %d", ErrNotFound, roleID)
	}
	return nil
}

// SetUserRole 把用户改派到角色（username 权威）。归属校验：
//   - 系统角色（is_system=1）是全局模板，按 base_role 判归属（与 CreateUser 的
//     角色-租户规则一致：admin/ops_admin ↔ system 租户；member/tenant_admin ↔ 真实租户）；
//   - 自定义平台角色（tenant_id NULL）只可指派给 system 租户用户；
//   - 自定义租户角色只可指派给本租户用户（跨租户 → ErrRoleTenantMismatch）。
//
// 同事务更新 users.role（=角色 base_role）与 users.role_id——中间件每请求按库刷新，
// 改派即刻生效（无需 bump token_version）。
func (s *sqliteStore) SetUserRole(ctx context.Context, username string, roleID int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var baseRole string
		var isSystem bool
		var roleTenant sql.NullInt64
		err := tx.QueryRowContext(ctx,
			`SELECT base_role, is_system, tenant_id FROM roles WHERE id = ?`, roleID).
			Scan(&baseRole, &isSystem, &roleTenant)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: role %d", ErrNotFound, roleID)
		}
		if err != nil {
			return err
		}
		var userTenant int64
		var userTenantSlug string
		err = tx.QueryRowContext(ctx, `
			SELECT t.id, t.slug FROM users u JOIN tenants t ON t.id = u.tenant_id
			WHERE u.username = ?`, username).Scan(&userTenant, &userTenantSlug)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: user %s", ErrNotFound, username)
		}
		if err != nil {
			return err
		}
		switch {
		case isSystem:
			// 全局模板：base_role 决定可住租户（§2.3 角色归属）
			if baseRole == auth.RoleSystemAdmin || baseRole == auth.RoleOpsAdmin {
				if userTenantSlug != systemTenant {
					return fmt.Errorf("%w: %s must belong to tenant 'system'", ErrRoleTenantMismatch, baseRole)
				}
			} else if userTenantSlug == systemTenant {
				return fmt.Errorf("%w: %s cannot belong to reserved tenant 'system'", ErrRoleTenantMismatch, baseRole)
			}
		case !roleTenant.Valid:
			if userTenantSlug != systemTenant {
				return fmt.Errorf("%w: platform role cannot be assigned to user of tenant %s",
					ErrRoleTenantMismatch, userTenantSlug)
			}
		case roleTenant.Int64 != userTenant:
			return fmt.Errorf("%w: cross-tenant role assignment refused", ErrRoleTenantMismatch)
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE users SET role = ?, role_id = ?, updated_at = datetime('now')
			WHERE username = ?`, baseRole, roleID, username)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: user %s", ErrNotFound, username)
		}
		return nil
	})
}

// MoveUserTenant 把用户迁移到目标租户，并在同一事务里改派到该租户下合法的角色。
// 单独迁移不可行：角色-租户归属规则会让"先迁租户"或"先改角色"各被对方不变量拒绝
// （如普通租户的 member 迁入 system 前必须已是 admin/ops_admin，而改派 admin 又要求
// 已在 system）——所以本入口把两步合一，按"最终 (角色, 租户) 组合"做一次归属校验。
// role 按平台作用域名解析（内置四角色或平台自定义角色）。改派即刻生效（无需 bump
// token_version，与 SetUserRole 同语义）。
func (s *sqliteStore) MoveUserTenant(ctx context.Context, username, tenantSlug, roleName string) error {
	r, err := s.RoleByName(ctx, "", roleName)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: role %s", ErrNotFound, roleName)
	}
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var targetTenant int64
		var targetStatus string
		err := tx.QueryRowContext(ctx,
			`SELECT id, status FROM tenants WHERE slug = ?`, tenantSlug).
			Scan(&targetTenant, &targetStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: tenant %s", ErrNotFound, tenantSlug)
		}
		if err != nil {
			return err
		}
		if targetStatus != "active" {
			return fmt.Errorf("%w: tenant %s is %s", ErrTenantSuspended, tenantSlug, targetStatus)
		}
		// 归属校验与 SetUserRole 同规，但比较对象是"迁移后的租户"。
		switch {
		case r.IsSystem:
			if r.BaseRole == auth.RoleSystemAdmin || r.BaseRole == auth.RoleOpsAdmin {
				if tenantSlug != systemTenant {
					return fmt.Errorf("%w: %s must belong to tenant 'system'", ErrRoleTenantMismatch, r.BaseRole)
				}
			} else if tenantSlug == systemTenant {
				return fmt.Errorf("%w: %s cannot belong to reserved tenant 'system'", ErrRoleTenantMismatch, r.BaseRole)
			}
		case r.TenantSlug == "":
			if tenantSlug != systemTenant {
				return fmt.Errorf("%w: platform role cannot be assigned to user of tenant %s", ErrRoleTenantMismatch, tenantSlug)
			}
		case r.TenantSlug != tenantSlug:
			return fmt.Errorf("%w: role %s does not belong to tenant %s", ErrRoleTenantMismatch, roleName, tenantSlug)
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE users SET tenant_id = ?, role = ?, role_id = ?, updated_at = datetime('now')
			WHERE username = ?`, targetTenant, r.BaseRole, r.ID, username)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: user %s", ErrNotFound, username)
		}
		return nil
	})
}

func (s *sqliteStore) roleByID(ctx context.Context, id int64) (*Role, error) {
	return scanRole(s.db.QueryRowContext(ctx, roleSelect+` WHERE r.id = ?`, id))
}

func scopeWord(tenantSlug string) string {
	if tenantSlug == "" {
		return "platform"
	}
	return "tenant"
}
