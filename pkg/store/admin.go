package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"ails-hpc/pkg/auth"
	"golang.org/x/crypto/bcrypt"
)

// 写面错误（管理 API 的 handler 按 sentinel 映射 HTTP 状态：ErrDuplicateUser/
// ErrTenantExists→409，ErrNotFound→404，ErrTenantSuspended/ErrTenantReserved→409，
// 其余校验类→400）。全部可 errors.Is 判别，细节在 %w 之后的消息里。
var (
	// ErrInvalidUsername 用户名缺失或字符集不安全（须 ^[a-z_][a-z0-9_-]{0,31}$）。
	ErrInvalidUsername = errors.New("store: invalid username")
	// ErrInvalidSlug 租户 slug 缺失或字符集不安全。
	ErrInvalidSlug = errors.New("store: invalid tenant slug")
	// ErrInvalidRole 角色不在四常量之内。
	ErrInvalidRole = errors.New("store: invalid role")
	// ErrInvalidStatus 状态值不在枚举内（租户 active|suspended；用户 active|disabled）。
	ErrInvalidStatus = errors.New("store: invalid status")
	// ErrRoleTenantMismatch 角色-租户归属违规（设计 §2.3：admin/ops_admin 住 system；
	// tenant_admin/member 必须属于真实租户且不可住 system）。
	ErrRoleTenantMismatch = errors.New("store: role/tenant mismatch")
	// ErrWeakPassword 密码短于 8 字符。
	ErrWeakPassword = errors.New("store: password too short")
	// ErrInvalidClusterUser clusterUser 缺失或非 unix 安全名。
	ErrInvalidClusterUser = errors.New("store: invalid cluster user")
	// ErrInvalidAccount account 缺失或非 unix 安全名。
	ErrInvalidAccount = errors.New("store: invalid account")
	// ErrInvalidUID 显式指定的 uid 超出带宽 2001..2999。
	ErrInvalidUID = errors.New("store: uid out of band")
	// ErrInvalidHash 传入的密码哈希不是 bcrypt 形态。
	ErrInvalidHash = errors.New("store: invalid password hash")
	// ErrDuplicateUser username/cluster_user/uid/account 任一撞已有记录（handler → 409）。
	ErrDuplicateUser = errors.New("store: duplicate user")
	// ErrUIDExhausted uid 带宽 2001..2999 已用尽。
	ErrUIDExhausted = errors.New("store: uid band exhausted")
	// ErrTenantReserved 保留租户 'system' 的受限操作（不可经 API 新建 / 不可挂起）。
	ErrTenantReserved = errors.New("store: tenant slug reserved")
	// ErrTenantSuspended 目标租户已挂起（挂起租户不可新增用户）。
	ErrTenantSuspended = errors.New("store: tenant suspended")
)

const (
	// uidMin/uidMax 是平台分配的 uid 带宽（设计 §3：2001..2999，避让节点既存账号
	// 如 991/64030/1001）。uidMax 封顶在 3000 以下的常见系统用户区间之前。
	uidMin = 2001
	uidMax = 2999
	// defaultGID 平台默认组（与 yaml 时代一致）。
	defaultGID = 2000
	// systemTenant 是保留租户：admin/ops_admin 的归属（设计 §2.3）。
	systemTenant = "system"
)

// unixSafeRE 是 unix 用户名/Slurm 账号的安全字符集：小写字母或下划线开头，
// 仅 [a-z0-9_-]，至多 32 字符（同时约束 username/clusterUser/account/tenant slug）。
var unixSafeRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// NewUser 是 CreateUser 的入参（设计 §5 的 POST 体映射；派生默认值见各字段注释）。
type NewUser struct {
	Username    string
	Password    string // 明文，入库前 bcrypt(DefaultCost)
	Role        string
	TenantSlug  string
	ClusterUser string // 空则 = Username（须 unix 安全：^[a-z_][a-z0-9_-]{0,31}$）
	UID         int    // 0 则 NextUID
	GID         int    // 0 则 2000
	Account     string // 空则 = ClusterUser
	DisplayName string
	Email       string
}

// compile-time：sqliteStore 实现完整写面（yaml 种子库不实现——管理 API 须 db 模式）。
var _ AdminStore = (*sqliteStore)(nil)

// withTx 在单事务内执行 fn（失败整体回滚——建用户的"校验+分配+落库"不落分裂态）。
func (s *sqliteStore) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// CreateTenant 建租户：parent_account=slug（Phase 5 起 sacctmgr 父账号，设计 §2.2）。
// 'system' 为保留租户（bootstrap/导入供给，不开放 API 创建）；slug 冲突返回 ErrTenantExists。
func (s *sqliteStore) CreateTenant(ctx context.Context, slug, name string) (*Tenant, error) {
	if slug == systemTenant {
		return nil, fmt.Errorf("%w: 'system' hosts platform admins and is provisioned at bootstrap",
			ErrTenantReserved)
	}
	if !unixSafeRE.MatchString(slug) {
		return nil, fmt.Errorf("%w: %q (want ^[a-z_][a-z0-9_-]{0,31}$)", ErrInvalidSlug, slug)
	}
	if name == "" {
		name = slug
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO tenants (slug, name, parent_account) VALUES (?, ?, ?)
		ON CONFLICT(slug) DO NOTHING`, slug, name, slug)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("%w: %s", ErrTenantExists, slug)
	}
	return s.TenantBySlug(ctx, slug)
}

// SetTenantStatus 置租户 active|suspended。'system' 不可挂起——admin/ops_admin 全部
// 住在其中，挂起即平台自锁，只能靠库文件手工恢复。
func (s *sqliteStore) SetTenantStatus(ctx context.Context, slug, status string) error {
	if status != "active" && status != "suspended" {
		return fmt.Errorf("%w: %q (want active|suspended)", ErrInvalidStatus, status)
	}
	if slug == systemTenant && status != "active" {
		return fmt.Errorf("%w: suspending 'system' would lock out all platform admins", ErrTenantReserved)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE tenants SET status = ?, updated_at = datetime('now') WHERE slug = ?`, status, slug)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: tenant %s", ErrNotFound, slug)
	}
	return nil
}

// CreateUser 建用户（设计 §4.1/§5）：参数校验 → bcrypt → 事务内租户/唯一性复核 + 落库。
//
// 派生默认值：clusterUser←username、account←clusterUser（约定 account==clusterUser，
// L1/L3 与作业归属的基础）、uid←NextUID、gid←2000。校验规则见各 sentinel 注释；
// 校验-落库同事务（单写者下即终局判定，UNIQUE 约束兜底）。
func (s *sqliteStore) CreateUser(ctx context.Context, in NewUser) (*auth.User, error) {
	if !unixSafeRE.MatchString(in.Username) {
		return nil, fmt.Errorf("%w: %q (want ^[a-z_][a-z0-9_-]{0,31}$)", ErrInvalidUsername, in.Username)
	}
	switch in.Role {
	case auth.RoleSystemAdmin, auth.RoleOpsAdmin, auth.RoleTenantAdmin, auth.RoleMember:
	default:
		return nil, fmt.Errorf("%w: %q (want admin|ops_admin|tenant_admin|member)", ErrInvalidRole, in.Role)
	}
	if len(in.Password) < 8 {
		return nil, fmt.Errorf("%w: need at least 8 characters", ErrWeakPassword)
	}
	clusterUser := in.ClusterUser
	if clusterUser == "" {
		clusterUser = in.Username
	}
	if !unixSafeRE.MatchString(clusterUser) {
		return nil, fmt.Errorf("%w: %q (want ^[a-z_][a-z0-9_-]{0,31}$)", ErrInvalidClusterUser, clusterUser)
	}
	account := in.Account
	if account == "" {
		account = clusterUser
	}
	if !unixSafeRE.MatchString(account) {
		return nil, fmt.Errorf("%w: %q (want ^[a-z_][a-z0-9_-]{0,31}$)", ErrInvalidAccount, account)
	}
	uid := in.UID
	if uid == 0 {
		var err error
		if uid, err = s.NextUID(ctx); err != nil {
			return nil, err
		}
	} else if uid < uidMin || uid > uidMax {
		return nil, fmt.Errorf("%w: %d (band %d..%d)", ErrInvalidUID, uid, uidMin, uidMax)
	}
	gid := in.GID
	if gid == 0 {
		gid = defaultGID
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("store: bcrypt: %w", err)
	}

	out := &auth.User{}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		// 租户存在性 + 角色-租户归属（设计 §2.3）+ 挂起检查，与落库同事务。
		var tenantStatus string
		err := tx.QueryRowContext(ctx, `SELECT status FROM tenants WHERE slug = ?`, in.TenantSlug).
			Scan(&tenantStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: tenant %s", ErrNotFound, in.TenantSlug)
		}
		if err != nil {
			return err
		}
		switch in.Role {
		case auth.RoleSystemAdmin, auth.RoleOpsAdmin:
			if in.TenantSlug != systemTenant {
				return fmt.Errorf("%w: %s must belong to tenant 'system', got %q",
					ErrRoleTenantMismatch, in.Role, in.TenantSlug)
			}
		default: // tenant_admin / member：必须属于真实租户（不可住保留的 'system'）
			if in.TenantSlug == systemTenant {
				return fmt.Errorf("%w: %s cannot belong to reserved tenant 'system'",
					ErrRoleTenantMismatch, in.Role)
			}
		}
		if tenantStatus != "active" {
			return fmt.Errorf("%w: tenant %s is %s", ErrTenantSuspended, in.TenantSlug, tenantStatus)
		}

		// 唯一性预检：username/cluster_user/uid/account 四列（表上均有 UNIQUE 兜底）。
		for _, c := range []struct {
			col string
			val any
		}{
			{"username", in.Username}, {"cluster_user", clusterUser},
			{"uid", uid}, {"account", account},
		} {
			var one int
			q := `SELECT 1 FROM users WHERE ` + c.col + ` = ?`
			err := tx.QueryRowContext(ctx, q, c.val).Scan(&one)
			if err == nil {
				return fmt.Errorf("%w: %s %v already taken", ErrDuplicateUser, c.col, c.val)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO users (username, password_hash, role, tenant_id, cluster_user,
			                   uid, gid, account, display_name, email, status)
			SELECT ?, ?, ?, t.id, ?, ?, ?, ?, ?, ?, 'active'
			FROM tenants t WHERE t.slug = ?`,
			in.Username, string(hash), in.Role, clusterUser, uid, gid, account,
			in.DisplayName, in.Email, in.TenantSlug); err != nil {
			return fmt.Errorf("store: insert user %s: %w", in.Username, err)
		}

		*out = auth.User{
			Username: in.Username, PasswordHash: string(hash), Role: in.Role,
			OrgSlug: in.TenantSlug, TenantSlug: in.TenantSlug,
			ClusterUser: clusterUser, UID: uid, GID: gid, Account: account,
			Status: "active", TokenVersion: 0,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateUserStatus 置账号 active|disabled。disable 时 token_version+1 → 在途 JWT 即刻
// 失效（中间件按请求比对 claims.Ver）；重新启用不回退版本——被吊销的旧令牌不复活，
// 用户须重新登录换取新版本令牌。
func (s *sqliteStore) UpdateUserStatus(ctx context.Context, username, status string) error {
	if status != "active" && status != "disabled" {
		return fmt.Errorf("%w: %q (want active|disabled)", ErrInvalidStatus, status)
	}
	q := `UPDATE users SET status = ?, updated_at = datetime('now')`
	if status == "disabled" {
		q += `, token_version = token_version + 1`
	}
	q += ` WHERE username = ?`
	res, err := s.db.ExecContext(ctx, q, status, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: user %s", ErrNotFound, username)
	}
	return nil
}

// ResetUserPassword 以调用方生成好的 bcrypt 哈希替换密码并 token_version+1（吊销在途
// 令牌）。自助改密走 auth.UserStore.SetPassword，本方法供管理员重置。
func (s *sqliteStore) ResetUserPassword(ctx context.Context, username, newHash string) error {
	if !strings.HasPrefix(newHash, "$2") { // $2a$/$2b$/$2y$
		return fmt.Errorf("%w: %q is not a bcrypt hash", ErrInvalidHash, newHash)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET password_hash = ?, token_version = token_version + 1, updated_at = datetime('now')
		WHERE username = ?`, newHash, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: user %s", ErrNotFound, username)
	}
	return nil
}

// ListTenantUsers 列出租户全部用户（JOIN tenants by slug，按 username 排序）。
// 返回值不含密码哈希（auth.User 对 hash 本就 json:"-"，这里再置空一道防线）。
// 租户不存在时返回空列表（存在性由调用方按需经 TenantBySlug 判 404）。
func (s *sqliteStore) ListTenantUsers(ctx context.Context, tenantSlug string) ([]auth.User, error) {
	rows, err := s.db.QueryContext(ctx, userSelect+` WHERE t.slug = ? ORDER BY u.username`, tenantSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []auth.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		u.PasswordHash = ""
		out = append(out, *u)
	}
	return out, rows.Err()
}

// NextUID 返回下一个可分配 uid：max(uid)+1，下限抬到带宽起点（库内遗留低 uid 不致
// 分配出带宽外的值），上限 2999——满则 ErrUIDExhausted（运维需迁移/清理后重试）。
func (s *sqliteStore) NextUID(ctx context.Context) (int, error) {
	var maxUID int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(uid), 0) FROM users`).Scan(&maxUID); err != nil {
		return 0, err
	}
	next := maxUID + 1
	if next < uidMin {
		next = uidMin
	}
	if next > uidMax {
		return 0, fmt.Errorf("%w: band %d..%d is full", ErrUIDExhausted, uidMin, uidMax)
	}
	return next, nil
}

// WriteAudit 落一条审计记录（设计 §5：所有管理变更写 audit_log）。
// detail 为 JSON 文本（空默认 '{}'）；request_id 贯穿中间件请求号。
func (s *sqliteStore) WriteAudit(ctx context.Context, actor, action, target, requestID, detail string) error {
	if detail == "" {
		detail = "{}"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log (actor, action, target, detail, request_id) VALUES (?, ?, ?, ?, ?)`,
		actor, action, target, detail, requestID)
	return err
}
