package store

import (
	"context"
	"database/sql"
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
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path)
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
	return &sqliteStore{db: db}, nil
}

func (s *sqliteStore) Close() error { return s.db.Close() }

// userRow 是 users ⨝ tenants 的读投影；tenant_slug 同时填充 auth.User.OrgSlug
// （兼容既有 tenantResolver/claims 路径——它们以 orgSlug 为租户标识直至 Phase 2 的 tid）。
const userSelect = `
SELECT u.username, u.password_hash, u.role, t.slug,
       u.cluster_user, u.uid, u.gid, u.account, u.status, u.token_version
FROM users u JOIN tenants t ON t.id = u.tenant_id`

func scanUser(row interface{ Scan(...any) error }) (*auth.User, error) {
	var u auth.User
	if err := row.Scan(&u.Username, &u.PasswordHash, &u.Role, &u.TenantSlug,
		&u.ClusterUser, &u.UID, &u.GID, &u.Account, &u.Status, &u.TokenVersion); err != nil {
		return nil, err
	}
	u.OrgSlug = u.TenantSlug // 兼容：迁移期租户标识 = orgSlug
	if u.Status == "" {
		u.Status = "active"
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
