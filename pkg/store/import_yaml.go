package store

import (
	"context"
	"fmt"

	"ails-hpc/pkg/auth"
)

// ImportYaml 把 users.yaml 用户库导入 sqlite（多租户 Phase 1 迁移工具，幂等可重跑）。
//
// 语义（设计 §7 Phase 1）：
//   - 始终确保保留租户 'system' 存在（admin/ops_admin 的归属，Phase 3 起强制）；
//   - 按用户 OrgSlug 去重建租户（parent_account=slug，Phase 5 起 fairshare 层级用）；
//   - 用户逐条 upsert（按 username），bcrypt 哈希原样保留 → 开发账号密码不变；
//   - 重跑安全：已存在的用户按 username 更新而非报错。
//
// 返回 (导入/更新的用户数, error)。
func ImportYaml(st Store, yamlPath string) (int, error) {
	src, err := auth.LoadUserStore(yamlPath)
	if err != nil {
		return 0, fmt.Errorf("store: load yaml %s: %w", yamlPath, err)
	}
	users := src.ListUsers()
	if len(users) == 0 {
		return 0, fmt.Errorf("store: yaml %s contains no users", yamlPath)
	}

	ctx := context.Background()
	// 租户：保留 'system' + 用户出现的全部 orgSlug（均以 slug 为父账号名）。
	slugs := map[string]bool{"system": true}
	for _, u := range users {
		if u.TenantSlug == "" {
			return 0, fmt.Errorf("store: user %s has no tenant (orgSlug/tenantSlug empty)", u.Username)
		}
		slugs[u.TenantSlug] = true
	}
	for slug := range slugs {
		if err := ensureTenant(ctx, st, slug); err != nil {
			return 0, err
		}
	}

	impl, ok := st.(*sqliteStore)
	if !ok {
		return 0, fmt.Errorf("store: import requires the sqlite store")
	}
	n := 0
	for _, u := range users {
		if u.Role == "" || u.ClusterUser == "" || u.Account == "" {
			return 0, fmt.Errorf("store: user %s missing role/clusterUser/account", u.Username)
		}
		_, err := impl.db.ExecContext(ctx, `
			INSERT INTO users (username, password_hash, role, role_id, tenant_id, cluster_user, uid, gid, account, status)
			SELECT ?, ?, ?, r.id, t.id, ?, ?, ?, ?, 'active'
			FROM tenants t
			CROSS JOIN (SELECT id FROM roles WHERE name = ? AND tenant_id IS NULL) r
			WHERE t.slug = ?
			ON CONFLICT(username) DO UPDATE SET
				password_hash = excluded.password_hash,
				role          = excluded.role,
				role_id       = excluded.role_id,
				tenant_id     = excluded.tenant_id,
				cluster_user  = excluded.cluster_user,
				uid           = excluded.uid,
				gid           = excluded.gid,
				account       = excluded.account,
				status        = 'active',
				updated_at    = datetime('now')`,
			u.Username, u.PasswordHash, u.Role, u.ClusterUser, u.UID, u.GID, u.Account, u.Role, u.TenantSlug)
		if err != nil {
			return n, fmt.Errorf("store: upsert user %s: %w", u.Username, err)
		}
		n++
	}
	return n, nil
}

// ensureTenant 幂等建租户（slug 即父账号名，Phase 5 起 sacctmgr 用）。
func ensureTenant(ctx context.Context, st Store, slug string) error {
	if _, err := st.TenantBySlug(ctx, slug); err == nil {
		return nil
	} else if err != ErrNotFound {
		return err
	}
	impl, ok := st.(*sqliteStore)
	if !ok {
		return fmt.Errorf("store: ensureTenant requires the sqlite store")
	}
	_, err := impl.db.ExecContext(ctx, `
		INSERT INTO tenants (slug, name, parent_account) VALUES (?, ?, ?)
		ON CONFLICT(slug) DO NOTHING`, slug, slug, slug)
	return err
}
