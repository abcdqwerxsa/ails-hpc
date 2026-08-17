// Package store 是多租户用户库的持久层（Phase 1：sqlite + users.yaml 导入，读路径）。
//
// 设计（docs/multi-tenant-design.md §4.1）：DB 库与内存/yaml 库同走 auth.UserStore 读面，
// apiserver 的租户解析（tenantResolver）与登录路径对两种后端无感知。写面（AdminStore：
// 建/改/禁用用户、租户管理、审计）Phase 3 随管理 API 落地——仅 sqlite 实现，yaml 种子只读。
package store

import (
	"context"
	"errors"

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
	Slug          string `json:"slug"`          // 唯一；保留 'system'（admin/ops_admin 所属）
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
	// ListTenantUsers 列出租户全部用户（按 username 排序；不含密码哈希）。
	ListTenantUsers(ctx context.Context, tenantSlug string) ([]auth.User, error)
	// NextUID 分配下一个 uid（max(uid)+1，带宽 2001..2999 避让节点既存账号；满则错误）。
	NextUID(ctx context.Context) (int, error)
	// WriteAudit 落一条审计记录（actor/action/target/request_id/detail）。
	WriteAudit(ctx context.Context, actor, action, target, requestID, detail string) error
}
