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
	// ErrReservationNotFound 预约不存在。
	ErrReservationNotFound = errors.New("admin: reservation not found")
)

// SlurmProvisioner 是 Slurm 侧供给面（默认 sacctmgr via docker exec；测试注入假实现）。
type SlurmProvisioner interface {
	// ProvisionAccount 建账号（租户父账号 / 用户叶子账号；幂等）。
	ProvisionAccount(account, parentAccount string) error
	// ProvisionUser 建用户叶子账号（parent=租户父账号）+ association。
	ProvisionUser(clusterUser, account, parentAccount string) error
	// SetAccountLimits 设置账号级限额（如 "GrpTRES=cpu=4" / "Fairshare=10"；幂等）。
	SetAccountLimits(account, setting string) error
}

// sacctmgrProvisioner 是默认实现：经 slurmctld 容器执行 sacctmgr（重复 add 容错）。
type sacctmgrProvisioner struct{}

// DefaultProvisioner 生产供给实现。
var DefaultProvisioner SlurmProvisioner = sacctmgrProvisioner{}

func (sacctmgrProvisioner) ProvisionAccount(account, parentAccount string) error {
	add := fmt.Sprintf("sacctmgr -i add account %s", account)
	if parentAccount != "" {
		add += fmt.Sprintf(" parent=%s", parentAccount)
	}
	_, err := slurmrest.RunInSlurmctld("sh", "-c", add+" || true")
	return err
}

func (sacctmgrProvisioner) ProvisionUser(clusterUser, account, parentAccount string) error {
	_, err := slurmrest.RunInSlurmctld("sh", "-c",
		fmt.Sprintf("sacctmgr -i add account %s parent=%s || true; sacctmgr -i add user %s account=%s || true",
			account, parentAccount, clusterUser, account))
	return err
}

func (sacctmgrProvisioner) SetAccountLimits(account, setting string) error {
	_, err := slurmrest.RunInSlurmctld("sh", "-c",
		fmt.Sprintf("sacctmgr -i modify account %s set %s", account, setting))
	return err
}

// Service 是管理域用例层。
type Service struct {
	st        store.AdminStore
	provision SlurmProvisioner
	runner    clusterRunner // 集群管理命令（4.2 预约/QOS）；nil=默认 slurmctld CLI
}

// NewService 构造管理服务。st 为 nil（yaml 模式）时所有方法返回 ErrReadOnlyStore。
func NewService(st store.AdminStore, p SlurmProvisioner) *Service {
	if p == nil {
		p = DefaultProvisioner
	}
	return &Service{st: st, provision: p}
}

// provisionUser 供给用户叶子账号（parent=租户父账号）+ association。
func (s *Service) provisionUser(ctx context.Context, u *auth.User) error {
	t, err := s.st.TenantBySlug(ctx, u.TenantSlug)
	if err != nil {
		return fmt.Errorf("%w: resolve tenant %s: %v", ErrProvisionFailed, u.TenantSlug, err)
	}
	if err := s.provision.ProvisionUser(u.ClusterUser, u.Account, t.ParentAccount); err != nil {
		return fmt.Errorf("%w: %v", ErrProvisionFailed, err)
	}
	return nil
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
	// Phase 5：租户=Slurm 父账号（fairshare/GrpTRES 层级载体）；建租户即建父账号。
	if err := s.provision.ProvisionAccount(t.ParentAccount, ""); err != nil {
		return t, fmt.Errorf("%w: tenant account provisioning: %v", ErrProvisionFailed, err)
	}
	_ = s.st.WriteAudit(ctx, actor, "tenant.create", "tenant:"+slug, rid, "{}")
	return t, nil
}

// UpdateTenant 更新租户（状态与/或 Slurm 限额——GrpTRES/Fairshare 落在父账号上）。
// grpTRES/fairshare 为空串表示不变更。
func (s *Service) UpdateTenant(ctx context.Context, actor, slug, status, grpTRES, fairshare, rid string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	t, err := s.st.TenantBySlug(ctx, slug)
	if err != nil {
		return err
	}
	if status != "" {
		if err := s.st.SetTenantStatus(ctx, slug, status); err != nil {
			return err
		}
	}
	// 限额变更：逐条 set（幂等；Slurm 语法由调用方保证——handler 只做字符集白名单）
	for _, kv := range []struct{ setting, val string }{
		{"GrpTRES", grpTRES}, {"Fairshare", fairshare},
	} {
		if kv.val == "" {
			continue
		}
		if err := s.provision.SetAccountLimits(t.ParentAccount, kv.setting+"="+kv.val); err != nil {
			return fmt.Errorf("%w: set %s on %s: %v", ErrProvisionFailed, kv.setting, t.ParentAccount, err)
		}
	}
	_ = s.st.WriteAudit(ctx, actor, "tenant.update", "tenant:"+slug, rid,
		`{"status":"`+status+`","grpTRES":"`+grpTRES+`","fairshare":"`+fairshare+`"}`)
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
	if err := s.provisionUser(ctx, u); err != nil {
		return u, err
	}
	_ = s.st.WriteAudit(ctx, actor, "user.create", "user:"+u.Username, rid, `{"role":"`+u.Role+`","tenant":"`+u.TenantSlug+`"}`)
	return u, nil
}

// ListAudit 审计日志（admin）。
func (s *Service) ListAudit(ctx context.Context, actor, action string, limit int) ([]store.AuditEntry, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	return s.st.ListAudit(ctx, actor, action, limit)
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
	if err := s.provisionUser(ctx, u); err != nil {
		return u, err
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
