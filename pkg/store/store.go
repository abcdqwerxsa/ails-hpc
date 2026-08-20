// Package store 是多租户用户库的持久层（Phase 1：sqlite + users.yaml 导入，读路径）。
//
// 设计（docs/multi-tenant-design.md §4.1）：DB 库与内存/yaml 库同走 auth.UserStore 读面，
// apiserver 的租户解析（tenantResolver）与登录路径对两种后端无感知。写面（AdminStore：
// 建/改/禁用用户、租户管理、审计）Phase 3 随管理 API 落地——仅 sqlite 实现，yaml 种子只读。
package store

import (
	"context"
	"errors"
	"time"

	"ails-hpc/pkg/auth"
)

var (
	// ErrNotFound 目标（用户/租户）不存在。
	ErrNotFound = errors.New("store: not found")
	// ErrTenantExists 创建租户时 slug 已存在。
	ErrTenantExists = errors.New("store: tenant already exists")
)

// Tenant 是租户记录（映射 tenants 表；设计 §3）。JSON 形状与前端 admin 页契约对齐。
type Tenant struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"` // 唯一；保留 'system'（admin/ops_admin 所属）
	Name          string `json:"name"`
	ParentAccount string `json:"parentAccount"` // Slurm 父账号（Phase 5 起用于 fairshare 层级）
	Status        string `json:"status"`        // active | suspended
}

// Store 是 sqlite 用户库的读面（Phase 1）。满足 auth.UserStore，可直接替换 yaml 库。
type Store interface {
	auth.UserStore // Lookup / Verify / ListUsers

	// Tenants 列出全部租户（导入与后续管理页用）。
	Tenants(ctx context.Context) ([]Tenant, error)
	// TenantBySlug 按 slug 查租户。
	TenantBySlug(ctx context.Context, slug string) (*Tenant, error)
	// ClusterUsersOfTenant 返回租户成员的 clusterUser 清单（tenantResolver 的 DB 实现）。
	ClusterUsersOfTenant(ctx context.Context, tenantSlug string) ([]string, error)

	// Close 关闭底层句柄。
	Close() error
}

// AdminStore 是用户库写面（管理 API 用；sqlite 实现。yaml 种子只读——写操作走 db 库）。
// Open 返回 Store；db 模式下调用方经类型断言取本面（yaml 库不实现 → 断言失败即"只读"）。
type AdminStore interface {
	Store

	// CreateTenant 建租户（parent_account=slug，Phase 5 起 fairshare 层级用）。
	// slug 'system' 不可建（保留给 admin/ops_admin）；重复返回 ErrTenantExists。
	CreateTenant(ctx context.Context, slug, name string) (*Tenant, error)
	// SetTenantStatus 置租户状态（active|suspended）；'system' 不可挂起（防平台自锁）。
	SetTenantStatus(ctx context.Context, slug, status string) error
	// CreateUser 建用户（校验 + bcrypt + 唯一性；细节见 admin.go）。
	CreateUser(ctx context.Context, u NewUser) (*auth.User, error)
	// UpdateUserStatus 置账号状态（active|disabled）；disabled 时 token_version+1
	// （吊销在途令牌；重新启用不回退版本，旧令牌不复活）。
	UpdateUserStatus(ctx context.Context, username, status string) error
	// ResetUserPassword 重置密码哈希并 token_version+1（吊销在途令牌）。
	ResetUserPassword(ctx context.Context, username, newHash string) error
	// UpdateUserDisplayName 置显示名（v3-U4；空串=清除，上限 64 字符）。
	UpdateUserDisplayName(ctx context.Context, username, displayName string) error
	// ListPlatformUsers 全平台用户目录（v3-U1；跨租户，按 username 排序，不含哈希）。
	ListPlatformUsers(ctx context.Context) ([]auth.User, error)
	// ListTenantUsers 列出租户全部用户（按 username 排序；不含密码哈希）。
	ListTenantUsers(ctx context.Context, tenantSlug string) ([]auth.User, error)
	// NextUID 分配下一个 uid（max(uid)+1，带宽 2001..2999 避让节点既存账号；满则错误）。
	NextUID(ctx context.Context) (int, error)
	// WriteAudit 落一条审计记录（actor/action/target/request_id/detail）。
	WriteAudit(ctx context.Context, actor, action, target, requestID, detail string) error
	// ListAudit 审计日志读取（时间倒序；actor/action 过滤可选；limit 1..500 默认 100）。
	ListAudit(ctx context.Context, actor, action string, limit int) ([]AuditEntry, error)

	// --- R3 角色管理（自定义角色；子集防提权校验在 service 层，store 管语法与归属） ---
	// ListRoles 列角色（tenantSlug="" → 平台；否则该租户）。
	ListRoles(ctx context.Context, tenantSlug string) ([]Role, error)
	// RoleByName 按名查角色（作用域同 ListRoles）。
	RoleByName(ctx context.Context, tenantSlug, name string) (*Role, error)
	// CreateRole 建自定义角色（base_role 作用域规则见 roles.go）。
	CreateRole(ctx context.Context, in NewRole) (*Role, error)
	// UpdateRole 改权限/描述（系统角色拒绝；nil=不改）。
	UpdateRole(ctx context.Context, roleID int64, permissions []string, desc *string) (*Role, error)
	// DeleteRole 删自定义角色（系统角色/在用角色拒绝）。
	DeleteRole(ctx context.Context, roleID int64) error
	// SetUserRole 改派用户角色（角色-租户归属校验；改派即刻生效）。
	SetUserRole(ctx context.Context, username string, roleID int64) error
	// MoveUserTenant 迁移用户到目标租户并同事务改派角色（最终组合做归属校验——
	// 两步合一，因单步迁移会被对方不变量拒绝）。
	MoveUserTenant(ctx context.Context, username, tenantSlug, roleName string) error

	// --- OIDC 账号关联（S1/S4；service 层补 Slurm 供给与审计） ---
	// UserByOIDCSub 按绑定的 SSO 身份查用户。
	UserByOIDCSub(sub string) (*auth.User, bool)
	// LinkOIDC 绑定 sub 到本地账号（auth_source 保持 local——并行登录）。
	LinkOIDC(username, sub string) error
	// UnlinkOIDC 解绑（auth_source=oidc 账号拒绝——无本地密码会自锁）。
	UnlinkOIDC(username string) error
	// ProvisionOIDCUser JIT 开户（S2 映射；随机本地密码 + 角色/租户归属校验）。
	ProvisionOIDCUser(username, email, displayName, roleName, tenantSlug, sub string) (*auth.User, error)

	// --- A1 密码与会话策略 ---
	// CheckPasswordHistory 新密码与最近 N 次重复 → ErrPasswordReused。
	CheckPasswordHistory(ctx context.Context, username, newPassword string) error
	// SetPasswordWithHistory 自助改密落库（清 must_change + 旧哈希入历史 + bump 版本）。
	SetPasswordWithHistory(ctx context.Context, username, newHash string) error
	// RecordLogin 台账一条会话（登录成功时）。
	RecordLogin(ctx context.Context, username, ip, userAgent string, expiresAt time.Time)
	// ListSessions 当前有效会话（未过期且 token_version 对齐）。
	ListSessions(ctx context.Context, username string) ([]auth.SessionEntry, error)
	// LogoutAll 全设备登出（token_version+1 + 台账清理）。
	LogoutAll(ctx context.Context, username string) error
}
