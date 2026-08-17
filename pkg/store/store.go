// Package store 是多租户用户库的持久层（Phase 1：sqlite + users.yaml 导入，读路径）。
//
// 设计（docs/multi-tenant-design.md §4.1）：DB 库与内存/yaml 库同走 auth.UserStore 读面，
// apiserver 的租户解析（tenantResolver）与登录路径对两种后端无感知。CRUD（建/改/禁用
// 用户、租户管理）在 Phase 3 随管理 API 落地；本包先提供读路径 + 导入。
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

// Tenant 是租户记录（映射 tenants 表；设计 §3）。
type Tenant struct {
	ID            int64
	Slug          string // 唯一；保留 'system'（admin/ops_admin 所属）
	Name          string
	ParentAccount string // Slurm 父账号（Phase 5 起用于 fairshare 层级）
	Status        string // active | suspended
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
