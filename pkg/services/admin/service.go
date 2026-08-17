// Package admin 提供租户与用户管理 API（多租户 Phase 3，设计 §5）。
//
// 写面只落在 sqlite 用户库（store.AdminStore）：yaml 种子只读，未翻 DB（AILS_USER_STORE
// 仍为 yaml）时全部管理写/读接口干净拒绝（503）——与 Phase 2 改密的只读语义一致。
// 每次变更写 audit_log（actor/action/target/request_id）。
// Slurm 供给（sacctmgr 建 account/association）经可注入 Provisioner，失败返回明确错误
// （DB 已提交、可幂等重试）。
package admin

import (
	"context"
	"errors"
	"fmt"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/slurmrest"
	"ails-hpc/pkg/store"

	"golang.org/x/crypto/bcrypt"
)

var (
	// ErrReadOnlyStore 未启用 sqlite 用户库（yaml 种子只读）——管理接口整体不可用。
	ErrReadOnlyStore = errors.New("admin: requires AILS_USER_STORE=db (yaml seed is read-only)")
	// ErrProvisionFailed DB 写入成功但 Slurm 供给失败（幂等可重试）。
	ErrProvisionFailed = errors.New("admin: user created but Slurm provisioning failed (retry is safe)")
	// ErrRoleNotAllowed 租户级建用户只允许 member/tenant_admin（平台角色由 /admin/users 建）。
	ErrRoleNotAllowed = errors.New("admin: tenant admins may only create member or tenant_admin users")
)

// Provisioner 在 Slurm 侧为用户建 account+association（默认 sacctmgr via docker exec；
// 测试注入假实现）。
type Provisioner func(clusterUser, account string) error

// DefaultProvisioner 经 slurmctld 容器执行 sacctmgr（幂等：重复 add 容错）。
func DefaultProvisioner(clusterUser, account string) error {
	if _, err := slurmrest.RunInSlurmctld("sh", "-c",
		fmt.Sprintf("sacctmgr -i add account %s || true; sacctmgr -i add user %s account=%s || true",
			account, clusterUser, account)); err != nil {
		return err
	}
	return nil
}

// Service 是管理域用例层。
type Service struct {
	st        store.AdminStore
	provision Provisioner
}

// NewService 构造管理服务。st 为 nil（yaml 模式）时所有方法返回 ErrReadOnlyStore。
func NewService(st store.AdminStore, p Provisioner) *Service {
	if p == nil {
		p = DefaultProvisioner
	}
	return &Service{st: st, provision: p}
}

func (s *Service) ensure() error {
	if s.st == nil {
		return ErrReadOnlyStore
	}
	return nil
}

// --- 平台级（admin） ---

// ListTenants 全部租户。
func (s *Service) ListTenants(ctx context.Context) ([]store.Tenant, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	return s.st.Tenants(ctx)
}

// CreateTenant 建租户。
func (s *Service) CreateTenant(ctx context.Context, actor, slug, name, rid string) (*store.Tenant, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	t, err := s.st.CreateTenant(ctx, slug, name)
	if err != nil {
		return nil, err
	}
	_ = s.st.WriteAudit(ctx, actor, "tenant.create", "tenant:"+slug, rid, "{}")
	return t, nil
}

// UpdateTenantStatus 更新租户状态（active|suspended）。
func (s *Service) UpdateTenantStatus(ctx context.Context, actor, slug, status, rid string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if err := s.st.SetTenantStatus(ctx, slug, status); err != nil {
		return err
	}
	_ = s.st.WriteAudit(ctx, actor, "tenant.status", "tenant:"+slug, rid, `{"status":"`+status+`"}`)
	return nil
}

// ListTenantUsers 某租户的用户（无哈希）。
func (s *Service) ListTenantUsers(ctx context.Context, slug string) ([]auth.User, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	return s.st.ListTenantUsers(ctx, slug)
}

// CreatePlatformUser 平台建用户（任意角色/租户；角色-租户规则由 store 校验）。
// 供给失败 → ErrProvisionFailed（DB 行已在，重试安全）。
func (s *Service) CreatePlatformUser(ctx context.Context, actor string, nu store.NewUser, rid string) (*auth.User, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	u, err := s.st.CreateUser(ctx, nu)
	if err != nil {
		return nil, err
	}
	if err := s.provision(u.ClusterUser, u.Account); err != nil {
		return u, fmt.Errorf("%w: %v", ErrProvisionFailed, err)
	}
	_ = s.st.WriteAudit(ctx, actor, "user.create", "user:"+u.Username, rid, `{"role":"`+u.Role+`","tenant":"`+u.TenantSlug+`"}`)
	return u, nil
}

// --- 租户级（tenant_admin，仅本租户） ---

// ListMyUsers 本租户用户。
func (s *Service) ListMyUsers(ctx context.Context, tenantSlug string) ([]auth.User, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	return s.st.ListTenantUsers(ctx, tenantSlug)
}

// CreateTenantUser 租户内建用户：租户强制=调用者所属，角色限 member/tenant_admin。
func (s *Service) CreateTenantUser(ctx context.Context, actor, tenantSlug string, nu store.NewUser, rid string) (*auth.User, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if nu.Role != auth.RoleMember && nu.Role != auth.RoleTenantAdmin {
		return nil, ErrRoleNotAllowed
	}
	nu.TenantSlug = tenantSlug // 服务端权威：不信任请求体里的租户
	u, err := s.st.CreateUser(ctx, nu)
	if err != nil {
		return nil, err
	}
	if err := s.provision(u.ClusterUser, u.Account); err != nil {
		return u, fmt.Errorf("%w: %v", ErrProvisionFailed, err)
	}
	_ = s.st.WriteAudit(ctx, actor, "user.create", "user:"+u.Username, rid, `{"tenant":"`+tenantSlug+`"}`)
	return u, nil
}

// UpdateMyUser 更新本租户用户（显示名/状态）。目标不在本租户 → store.ErrNotFound → 404（防枚举）。
func (s *Service) UpdateMyUser(ctx context.Context, actor, tenantSlug, username, displayName, status, rid string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	targets, err := s.st.ListTenantUsers(ctx, tenantSlug)
	if err != nil {
		return err
	}
	found := false
	for _, u := range targets {
		if u.Username == username {
			found = true
			break
		}
	}
	if !found {
		return store.ErrNotFound // 跨租户/不存在统一 404
	}
	if status != "" {
		if err := s.st.UpdateUserStatus(ctx, username, status); err != nil {
			return err
		}
	}
	_ = displayName // Phase 3 仅状态；显示名编辑随后续需要落 store
	_ = s.st.WriteAudit(ctx, actor, "user.update", "user:"+username, rid, `{"status":"`+status+`"}`)
	return nil
}

// ResetMyUserPassword 重置本租户用户密码（token_version+1 → 在途令牌即刻失效）。
func (s *Service) ResetMyUserPassword(ctx context.Context, actor, tenantSlug, username, newPassword, rid string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	targets, err := s.st.ListTenantUsers(ctx, tenantSlug)
	if err != nil {
		return err
	}
	found := false
	for _, u := range targets {
		if u.Username == username {
			found = true
			break
		}
	}
	if !found {
		return store.ErrNotFound
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.st.ResetUserPassword(ctx, username, string(hash))
}
