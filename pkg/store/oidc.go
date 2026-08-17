package store

// OIDC 账号关联写面（S1/S4；auth.OIDCProvisioner 的 store 侧实现）。

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"ails-hpc/pkg/auth"
	"golang.org/x/crypto/bcrypt"
)

// ErrAlreadyLinked 目标 OIDC 身份已绑定到其他账号（唯一索引兜底之上的友好错误）。
var ErrAlreadyLinked = errors.New("store: oidc identity already linked to another account")

// ErrNoLocalCredential auth_source=oidc 的账号无本地密码——解绑会自锁，拒绝。
var ErrNoLocalCredential = errors.New("store: account has no local password (auth_source=oidc); unlink would lock it out")

// LinkOIDC 绑定 sub 到本地账号（S4 撞名确认/已登录绑定共用）。
// 绑定后 auth_source 保持 local（本地密码仍可用；SSO 与密码并行——设计决策）。
func (s *sqliteStore) LinkOIDC(username, sub string) error {
	if sub == "" {
		return fmt.Errorf("store: empty oidc sub")
	}
	// 唯一性预检（给出可操作文案；部分唯一索引兜底并发）
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM users WHERE oidc_sub = ?`, sub).Scan(&one)
	if err == nil {
		return fmt.Errorf("%w (sub=%s)", ErrAlreadyLinked, maskSub(sub))
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	res, err := s.db.Exec(`
		UPDATE users SET oidc_sub = ?, updated_at = datetime('now') WHERE username = ?`,
		sub, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: user %s", ErrNotFound, username)
	}
	return nil
}

// UnlinkOIDC 解绑。auth_source=oidc 的账号拒绝（无本地密码，解绑即自锁）。
func (s *sqliteStore) UnlinkOIDC(username string) error {
	var authSource string
	err := s.db.QueryRow(`SELECT auth_source FROM users WHERE username = ?`, username).Scan(&authSource)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: user %s", ErrNotFound, username)
	}
	if err != nil {
		return err
	}
	if authSource == "oidc" {
		return ErrNoLocalCredential
	}
	res, err := s.db.Exec(`
		UPDATE users SET oidc_sub = NULL, updated_at = datetime('now') WHERE username = ?`, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: user %s", ErrNotFound, username)
	}
	return nil
}

// ProvisionOIDCUser JIT 开户（S2 映射落库）：
//   - 随机本地密码哈希（auth_source=oidc 账号不经本地密码登录；哈希存在仅满足非空约束，
//     且为真随机 32B——防对 SSO 账号做本地撞库）
//   - 角色名解析：平台作用域（内置/平台自定义）优先，其次目标租户作用域的自定义角色
//   - 角色-租户归属规则与 CreateUser §2.3 一致（admin/ops ↔ system；其余 ↔ 真实租户）
//   - cluster_user/account = username、uid=NextUID、gid=2000
func (s *sqliteStore) ProvisionOIDCUser(username, email, displayName, roleName, tenantSlug, sub string) (*auth.User, error) {
	if sub == "" {
		return nil, fmt.Errorf("store: empty oidc sub")
	}
	ctx := context.Background()

	// 角色解析：平台 → 租户作用域
	role, err := s.RoleByName(ctx, "", roleName)
	if errors.Is(err, ErrNotFound) {
		role, err = s.RoleByName(ctx, tenantSlug, roleName)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: role %s", ErrNotFound, roleName)
	}
	baseRole := role.BaseRole

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(raw)), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	clusterUser := username
	account := username
	uid, err := s.NextUID(ctx)
	if err != nil {
		return nil, err
	}

	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var tenantStatus string
		var tenantID int64
		err := tx.QueryRowContext(ctx,
			`SELECT id, status FROM tenants WHERE slug = ?`, tenantSlug).Scan(&tenantID, &tenantStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: tenant %s", ErrNotFound, tenantSlug)
		}
		if err != nil {
			return err
		}
		if tenantStatus != "active" {
			return fmt.Errorf("%w: tenant %s is %s", ErrTenantSuspended, tenantSlug, tenantStatus)
		}
		// 角色-租户归属（与 CreateUser §2.3 同规）：
		// 内置 admin/ops ↔ system；内置 member/tenant_admin ↔ 真实租户；
		// 平台自定义角色 ↔ system；租户自定义角色 ↔ 本租户。
		switch {
		case role.IsSystem && (baseRole == auth.RoleSystemAdmin || baseRole == auth.RoleOpsAdmin):
			if tenantSlug != systemTenant {
				return fmt.Errorf("%w: %s must belong to tenant 'system'", ErrRoleTenantMismatch, baseRole)
			}
		case role.IsSystem:
			if tenantSlug == systemTenant {
				return fmt.Errorf("%w: %s cannot belong to reserved tenant 'system'", ErrRoleTenantMismatch, baseRole)
			}
		case role.TenantSlug == "": // 平台自定义角色（base 恒 ops/admin）
			if tenantSlug != systemTenant {
				return fmt.Errorf("%w: platform role cannot be provisioned into tenant %s", ErrRoleTenantMismatch, tenantSlug)
			}
		case role.TenantSlug != tenantSlug:
			return fmt.Errorf("%w: role %s does not belong to tenant %s", ErrRoleTenantMismatch, roleName, tenantSlug)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO users (username, password_hash, role, role_id, tenant_id, cluster_user,
			                   uid, gid, account, display_name, email, auth_source, oidc_sub, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, 2000, ?, ?, ?, 'oidc', ?, 'active')`,
			username, string(hash), baseRole, sql.NullInt64{Int64: role.ID, Valid: true},
			tenantID, clusterUser, uid, account, displayName, email, sub); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return fmt.Errorf("%w: username %s already taken", ErrDuplicateUser, username)
			}
			return fmt.Errorf("store: provision %s: %w", username, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	u, ok := s.UserByOIDCSub(sub)
	if !ok {
		return nil, fmt.Errorf("store: provisioned user %s not found by sub", username)
	}
	return u, nil
}

// maskSub 审计/错误文案里的 sub 脱敏（保留前 8 字符）。
func maskSub(sub string) string {
	if len(sub) <= 8 {
		return sub
	}
	return sub[:8] + "…"
}
