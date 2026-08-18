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
	"strings"

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
	// ErrPartitionNotFound 分区不存在（scontrol show partition 无该名字）。
	ErrPartitionNotFound = errors.New("admin: partition not found")
	// ErrSelfDisable 自禁用守卫：平台管理员不可禁用自己的账号（防自锁门外）。
	ErrSelfDisable = errors.New("admin: cannot disable your own account")
)

// SlurmProvisioner 是 Slurm 侧供给面（默认 sacctmgr via docker exec；测试注入假实现）。
type SlurmProvisioner interface {
	// ProvisionAccount 建账号（租户父账号 / 用户叶子账号；幂等）。
	ProvisionAccount(account, parentAccount string) error
	// ProvisionUser 建用户叶子账号（parent=租户父账号）+ association + POSIX 账号
	// （slurmctld 与全部计算节点 useradd——sudo -u sbatch 与 sacct 都要容器内
	// 系统账号；entrypoint 只在容器启动时按 seed 文件建，API 建号必须在线补）。
	ProvisionUser(clusterUser string, uid, gid int, account, parentAccount string) error
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

func (sacctmgrProvisioner) ProvisionUser(clusterUser string, uid, gid int, account, parentAccount string) error {
	if _, err := slurmrest.RunInSlurmctld("sh", "-c",
		fmt.Sprintf("sacctmgr -i add account %s parent=%s || true; sacctmgr -i add user %s account=%s || true",
			account, parentAccount, clusterUser, account)); err != nil {
		return err
	}
	// POSIX 账号：slurmctld（CLI sbatch/sacct 面）+ 全部计算节点（作业执行面）。
	// 节点清单从 sinfo 枚举（单一真源——不与部署拓扑硬编码耦合）。幂等（useradd || true）。
	nodes, err := slurmrest.RunInSlurmctld("sinfo", "-N", "-h", "-o", "%N")
	if err != nil {
		nodes = nil // sinfo 失败不阻断建号——POSIX 补齐退化为仅 slurmctld（可重试）
	}
	targets := []string{"slurmctld"}
	for _, n := range strings.Fields(string(nodes)) {
		if n = strings.TrimSpace(n); n != "" {
			targets = append(targets, n)
		}
	}
	useradd := fmt.Sprintf("groupadd -g %d ailshpc 2>/dev/null; useradd -m -u %d -g %d %s 2>/dev/null || true",
		gid, uid, gid, clusterUser)
	for _, t := range targets {
		if _, err := slurmrest.RunInContainer(t, "sh", "-c", useradd); err != nil {
			return fmt.Errorf("posix provision on %s: %w", t, err)
		}
	}
	return nil
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
	if err := s.provision.ProvisionUser(u.ClusterUser, u.UID, u.GID, u.Account, t.ParentAccount); err != nil {
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

// ListPlatformUsers 全平台用户目录（v3-U1；跨租户，yaml 模式 503——目录依赖用户库）。
func (s *Service) ListPlatformUsers(ctx context.Context) ([]auth.User, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	return s.st.ListPlatformUsers(ctx)
}

// UpdatePlatformUser 平台改用户（v3-U2/U4：显示名与/或状态；空串=不变更）。
// 自禁用拒绝（防自锁）；禁用即 token_version+1 吊销在途令牌（store 语义）。
func (s *Service) UpdatePlatformUser(ctx context.Context, actor, username, displayName, status, rid string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if status == "disabled" && actor == username {
		return ErrSelfDisable
	}
	if status != "" {
		if err := s.st.UpdateUserStatus(ctx, username, status); err != nil {
			return err
		}
	}
	if displayName != "" {
		if err := s.st.UpdateUserDisplayName(ctx, username, displayName); err != nil {
			return err
		}
	}
	_ = s.st.WriteAudit(ctx, actor, "user.update", "user:"+username, rid,
		`{"status":"`+status+`","displayName":"`+displayName+`"}`)
	return nil
}

// ResetPlatformUserPassword 平台重置任意用户密码（v3-U3——tenant_admin 忘密时无人可解的
// 死锁由此消除）。重置后强制首登改密 + 在途令牌全部吊销（ResetUserPassword 语义）。
func (s *Service) ResetPlatformUserPassword(ctx context.Context, actor, username, newPassword, rid string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.st.ResetUserPassword(ctx, username, string(hash)); err != nil {
		return err
	}
	_ = s.st.WriteAudit(ctx, actor, "user.reset_password", "user:"+username, rid, "{}")
	return nil
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
	// v3-U4：显示名落地（空串=不变更——与 status 同语义）
	if displayName != "" {
		if err := s.st.UpdateUserDisplayName(ctx, username, displayName); err != nil {
			return err
		}
	}
	_ = s.st.WriteAudit(ctx, actor, "user.update", "user:"+username, rid,
		`{"status":"`+status+`","displayName":"`+displayName+`"}`)
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
	if err := s.st.ResetUserPassword(ctx, username, string(hash)); err != nil {
		return err
	}
	_ = s.st.WriteAudit(ctx, actor, "user.reset_password", "user:"+username, rid, "{}")
	return nil
}
