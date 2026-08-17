package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"ails-hpc/pkg/auth"

	_ "modernc.org/sqlite" // 纯 Go 驱动（无 cgo，单二进制约束）
)

// sqliteStore 是 Store 的 sqlite 实现（WAL + busy_timeout；apiserver 单写者）。
type sqliteStore struct {
	db *sql.DB
}

// compile-time：sqliteStore 必须可直接替换 yaml/内存用户库。
var _ Store = (*sqliteStore)(nil)

// Open 打开（必要时创建）sqlite 用户库并补齐迁移。DSN 采用 WAL + busy_timeout
// （设计 §2.1：单写者场景下足够的并发配置）。
func Open(path string) (Store, error) {
	// C1:外键执行(sqlite 默认关闭——REFERENCES 声明此前只是文档)。
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// sqlite 单文件库：串行化连接避免 SQLITE_BUSY 边角（写少读多，代价可忽略）。
	db.SetMaxOpenConns(1)
	if err := migrate(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	// 保留租户 'system' 恒存在（admin/ops_admin 的归属，设计 §2.3）——冷启动空库
	// 即可建平台管理员做 bootstrap；幂等，重开不重复。CreateTenant 仍拒绝经 API 建。
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO tenants (slug, name, parent_account) VALUES (?, ?, ?)
		ON CONFLICT(slug) DO NOTHING`, systemTenant, systemTenant, systemTenant); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ensure reserved tenant %q: %w", systemTenant, err)
	}
	return &sqliteStore{db: db}, nil
}

func (s *sqliteStore) Close() error { return s.db.Close() }

// userRow 是 users ⨝ tenants ⨝ roles 的读投影；tenant_slug 同时填充 auth.User.OrgSlug
// （兼容既有 tenantResolver/claims 路径——它们以 orgSlug 为租户标识直至 Phase 2 的 tid）。
// R2 起 LEFT JOIN roles：role_id/实际角色名/权限点（role_id NULL 或角色缺失时回退内置映射）。
const userSelect = `
SELECT u.username, u.password_hash, u.role, t.slug,
       u.cluster_user, u.uid, u.gid, u.account, u.status, u.token_version,
       u.role_id, COALESCE(r.name, u.role), r.permissions,
       u.auth_source, COALESCE(u.oidc_sub, '')
FROM users u JOIN tenants t ON t.id = u.tenant_id
LEFT JOIN roles r ON r.id = u.role_id`

func scanUser(row interface{ Scan(...any) error }) (*auth.User, error) {
	var u auth.User
	var roleID sql.NullInt64
	var roleName string
	var permsJSON sql.NullString
	if err := row.Scan(&u.Username, &u.PasswordHash, &u.Role, &u.TenantSlug,
		&u.ClusterUser, &u.UID, &u.GID, &u.Account, &u.Status, &u.TokenVersion,
		&roleID, &roleName, &permsJSON, &u.AuthSource, &u.OIDCSub); err != nil {
		return nil, err
	}
	u.OrgSlug = u.TenantSlug // 兼容：迁移期租户标识 = orgSlug
	if u.Status == "" {
		u.Status = "active"
	}
	if u.AuthSource == "" {
		u.AuthSource = "local"
	}
	u.RoleID = roleID.Int64
	if roleID.Valid && roleName != "" && roleName != u.Role {
		u.RoleName = roleName // 自定义角色名（内置角色名与 u.Role 相同，不重复携带）
	}
	if permsJSON.Valid && permsJSON.String != "" {
		// 角色权限 JSON 损坏不致命：留空 → 解析器回退 BuiltinRolePermissions[u.Role]
		_ = json.Unmarshal([]byte(permsJSON.String), &u.Permissions)
	}
	return &u, nil
}

// Lookup 按用户名查找（不做凭证校验）；满足 auth.UserStore。
func (s *sqliteStore) Lookup(username string) (*auth.User, bool) {
	u, err := scanUser(s.db.QueryRow(userSelect+` WHERE u.username = ?`, username))
	if err != nil {
		return nil, false
	}
	return u, true
}

// Verify 校验用户名+密码（bcrypt；auth_source=local）。满足 auth.UserStore。
func (s *sqliteStore) Verify(username, password string) (*auth.User, error) {
	u, ok := s.Lookup(username)
	if !ok {
		return nil, auth.ErrInvalidCredentials
	}
	if u.Status != "active" {
		return nil, auth.ErrInvalidCredentials // 禁用用户与"密码错"同文案，防探测
	}
	if err := auth.CompareHashAndPassword(u.PasswordHash, password); err != nil {
		return nil, auth.ErrInvalidCredentials
	}
	return u, nil
}

// ListUsers 全量用户（tenantResolver 派生租户成员用）。
func (s *sqliteStore) ListUsers() []*auth.User {
	rows, err := s.db.Query(userSelect + ` ORDER BY u.username`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []*auth.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			continue
		}
		out = append(out, u)
	}
	return out
}

// SetPassword 更新哈希并 bump token_version（在途 JWT 即刻失效；满足 auth.UserStore）。
func (s *sqliteStore) SetPassword(username, bcryptHash string) error {
	res, err := s.db.Exec(`
		UPDATE users SET password_hash = ?, token_version = token_version + 1, updated_at = datetime('now')
		WHERE username = ?`, bcryptHash, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return auth.ErrInvalidCredentials
	}
	return nil
}

// UserVersion 返回用户当前 token_version。
func (s *sqliteStore) UserVersion(username string) (int, bool) {
	var ver int
	err := s.db.QueryRow(`SELECT token_version FROM users WHERE username = ?`, username).Scan(&ver)
	if err != nil {
		return 0, false
	}
	return ver, true
}

// UserByOIDCSub 按绑定的 SSO 身份查用户（S1 回落路径；auth.OIDCProvisioner 面）。
func (s *sqliteStore) UserByOIDCSub(sub string) (*auth.User, bool) {
	u, err := scanUser(s.db.QueryRow(userSelect+` WHERE u.oidc_sub = ?`, sub))
	if err != nil {
		return nil, false
	}
	return u, true
}

// Tenants 列出全部租户。
func (s *sqliteStore) Tenants(ctx context.Context) ([]Tenant, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, slug, name, parent_account, status FROM tenants ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tenant{}
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.ParentAccount, &t.Status); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TenantBySlug 按 slug 查租户。
func (s *sqliteStore) TenantBySlug(ctx context.Context, slug string) (*Tenant, error) {
	var t Tenant
	err := s.db.QueryRowContext(ctx,
		`SELECT id, slug, name, parent_account, status FROM tenants WHERE slug = ?`, slug).
		Scan(&t.ID, &t.Slug, &t.Name, &t.ParentAccount, &t.Status)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ClusterUsersOfTenant 返回租户成员的 clusterUser 清单。
func (s *sqliteStore) ClusterUsersOfTenant(ctx context.Context, tenantSlug string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.cluster_user FROM users u
		JOIN tenants t ON t.id = u.tenant_id
		WHERE t.slug = ? AND u.status = 'active'
		ORDER BY u.cluster_user`, tenantSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var cu string
		if err := rows.Scan(&cu); err != nil {
			return nil, err
		}
		out = append(out, cu)
	}
	return out, rows.Err()
}
