package admin

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/store"
)

// ErrRoleEscalation 请求的权限超出本次调用允许授予的集合（防提权核心：服务端取子集
// 校验，不信任请求体声明的任何权限）。允许集合按作用域定：租户作用域=创建者自身权限
// （角色链不放大）；平台作用域=全目录（平台管理员持 roles:manage，本就管理全部角色——
// ⊆ 自身会让"纯监控角色"永远造不出含作业权限的角色，2026-08-19 产品决策放开）。
var ErrRoleEscalation = errors.New("admin: requested permissions exceed the allowed set")

// ensureSubset 校验 requested ⊆ allowed（子集规则；allowed 的语义见 ErrRoleEscalation）。
func ensureSubset(allowed, requested []string) error {
	set := make(map[string]bool, len(allowed))
	for _, p := range allowed {
		set[p] = true
	}
	for _, p := range requested {
		if !set[p] {
			return fmt.Errorf("%w: %q", ErrRoleEscalation, p)
		}
	}
	return nil
}

// ListRoles 列角色。tenantSlug="" → 平台角色（admin）；否则该租户的自定义角色。
func (s *Service) ListRoles(ctx context.Context, tenantSlug string) ([]store.Role, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	return s.st.ListRoles(ctx, tenantSlug)
}

// CreateRole 建自定义角色。tenantSlug 非空时为租户角色（归属以服务端为准）；
// permissions 必须是 allowedPerms 子集（平台=全目录，租户=创建者自身——防提权）。
func (s *Service) CreateRole(ctx context.Context, actor string, allowedPerms []string, in store.NewRole, rid string) (*store.Role, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if err := ensureSubset(allowedPerms, in.Permissions); err != nil {
		return nil, err
	}
	r, err := s.st.CreateRole(ctx, in)
	if err != nil {
		return nil, err
	}
	_ = s.st.WriteAudit(ctx, actor, "role.create", roleTarget(tenantWord(in.TenantSlug), r.Name), rid,
		fmt.Sprintf(`{"baseRole":%q,"permissions":%v}`, r.BaseRole, r.Permissions))
	return r, nil
}

// UpdateRole 改角色权限/描述（作用域内按名解析；跨作用域/不存在统一 404——防枚举）。
// 新权限集合同样必须是 allowedPerms 子集（平台=全目录，租户=创建者自身）——收缩后再
// 放大也被拦截。
func (s *Service) UpdateRole(ctx context.Context, actor string, allowedPerms []string, tenantSlug, name string, permissions []string, desc *string, rid string) (*store.Role, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	cur, err := s.st.RoleByName(ctx, tenantSlug, name)
	if err != nil {
		return nil, err
	}
	next := cur.Permissions
	if permissions != nil {
		if err := ensureSubset(allowedPerms, permissions); err != nil {
			return nil, err
		}
		next = permissions
	}
	r, err := s.st.UpdateRole(ctx, cur.ID, permissions, desc)
	if err != nil {
		return nil, err
	}
	_ = s.st.WriteAudit(ctx, actor, "role.update", roleTarget(tenantWord(tenantSlug), name), rid,
		fmt.Sprintf(`{"permissions":%v}`, next))
	return r, nil
}

// DeleteRole 删自定义角色（在用 → ErrRoleInUse 409，须先改派；系统角色 → 409）。
func (s *Service) DeleteRole(ctx context.Context, actor, tenantSlug, name, rid string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	cur, err := s.st.RoleByName(ctx, tenantSlug, name)
	if err != nil {
		return err
	}
	if err := s.st.DeleteRole(ctx, cur.ID); err != nil {
		return err
	}
	_ = s.st.WriteAudit(ctx, actor, "role.delete", roleTarget(tenantWord(tenantSlug), name), rid, "{}")
	return nil
}

// AssignRole 把用户改派到角色。角色按作用域解析：内置角色名（member/tenant_admin/
// admin/ops_admin）→ 系统角色；否则限定在 tenantSlug 作用域内找自定义角色（跨租户
// 同名角色不可达——解析不到即 404）。租户调用方（tenantSlug 非空）目标用户必须属于
// 本租户（越权改派外租户用户 → 404，与 UpdateMyUser 同语义）；归属规则与落库在
// store.SetUserRole。
// 安全审计 2026-08-19 P1-5：租户作用域增加"目标角色权限 ⊆ 调用者"校验——指派即
// 授予权限，弱角色持有者（路由门=tenant:users:manage）不可把内置 tenant_admin 或
// 任何超集角色派给自己/他人。平台作用域跳过（与建/改角色同决策：roles:manage 持有
// 者管理全部角色指派）。
func (s *Service) AssignRole(ctx context.Context, actor string, actorPerms []string, tenantSlug, username, roleName, rid string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	// P2 守卫（安全审计 2026-08-19，与 UpdatePlatformUser 同面）：非 admin 基角色
	// 调用者不可动内置 admin 基角色账号——否则持 roles:manage 的自定义角色可把
	// 平台管理员降级为 member（接管/瘫痪）。
	if err := s.protectBuiltinAdmin(ctx, actor, username); err != nil {
		return err
	}
	r, err := s.resolveRole(ctx, tenantSlug, roleName)
	if err != nil {
		return err
	}
	if tenantSlug != "" {
		if err := ensureSubset(actorPerms, r.Permissions); err != nil {
			return fmt.Errorf("%w: role %s", err, r.Name)
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
			return store.ErrNotFound // 跨租户/不存在统一 404（防枚举）
		}
	}
	if err := s.st.SetUserRole(ctx, username, r.ID); err != nil {
		return err
	}
	_ = s.st.WriteAudit(ctx, actor, "user.role", "user:"+username, rid,
		fmt.Sprintf(`{"role":%q,"baseRole":%q}`, r.Name, r.BaseRole))
	return nil
}

// MoveUserTenant 平台作用域：迁移用户到目标租户并同事务改派角色。角色限定平台
// 作用域解析（内置四角色/平台自定义）——最终 (角色, 租户) 组合的归属校验在 store 层
// （单步迁移会被角色-租户不变量互斥拒绝，故两步合一）。审计独立于 AssignRole
// （user.tenant：跨租户身份变更是高敏操作）。
func (s *Service) MoveUserTenant(ctx context.Context, actor, username, tenantSlug, roleName, rid string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	// P2 守卫：同 AssignRole——非 admin 基角色调用者不可迁移内置 admin 账号
	// （迁出租户 + 降级角色比禁用更彻底，必须同防）。
	if err := s.protectBuiltinAdmin(ctx, actor, username); err != nil {
		return err
	}
	// 账号名迁移前后不变（clusterUser/account 与租户解耦）；快照兼作审计的"from"值
	// （用户名不可改名，Lookup 与落库之间的 TOCTOU 无实际危害）。
	before, ok := s.st.Lookup(username)
	if !ok {
		return store.ErrNotFound
	}
	if err := s.st.MoveUserTenant(ctx, username, tenantSlug, roleName); err != nil {
		return err
	}
	// fairshare 层级对齐：叶子账号 re-parent 到新租户父账号（root └ <租户> └ <用户>）。
	// DB 已提交，供给失败按 CreateUser 同语义报 502（ErrProvisionFailed）——重试幂等
	// （store 侧同值 UPDATE no-op，sacctmgr re-parent 到原 parent 亦 no-op）。
	t, err := s.st.TenantBySlug(ctx, tenantSlug)
	if err != nil {
		// 租户在 store 迁移事务里刚校验过存在且 active；到这里失败属异常态——绝不可
		// 静默跳过 re-parent（否则 DB 与 Slurm 记账层级静默漂移），按供给失败上报。
		return fmt.Errorf("%w: resolve tenant %s for reparent: %v", ErrProvisionFailed, tenantSlug, err)
	}
	if err := s.provision.ReparentAccount(before.Account, t.ParentAccount); err != nil {
		return fmt.Errorf("%w: reparent %s to %s: %v", ErrProvisionFailed, before.Account, t.ParentAccount, err)
	}
	_ = s.st.WriteAudit(ctx, actor, "user.tenant", "user:"+username, rid,
		fmt.Sprintf(`{"from":%q/%q,"to":%q/%q}`, before.TenantSlug, before.Role, tenantSlug, roleName))
	return nil
}

// resolveRole 作用域内解析角色名：自定义角色优先（租户作用域内），内置四角色名解析为
// 系统角色（其 base 决定 users.role 回写）。
func (s *Service) resolveRole(ctx context.Context, tenantSlug, name string) (*store.Role, error) {
	if r, err := s.st.RoleByName(ctx, tenantSlug, name); err == nil {
		return r, nil
	} else if err != store.ErrNotFound {
		return nil, err
	}
	// 内置角色名 → 平台系统角色（tenant 作用域调用方仅可解析 member/tenant_admin，
	// 越界组合由 store.SetUserRole 的归属校验兜底拒绝）
	if auth.BuiltinRolePermissions[name] != nil {
		return s.st.RoleByName(ctx, "", name)
	}
	return nil, fmt.Errorf("%w: role %s", store.ErrNotFound, name)
}

func tenantWord(tenantSlug string) string {
	if tenantSlug == "" {
		return ""
	}
	return tenantSlug
}

func roleTarget(tenantSlug, name string) string {
	if tenantSlug == "" {
		return "role:" + name
	}
	return "role:" + tenantSlug + "/" + name
}

// sortedPerms 供 handler 输出稳定排序（显示与测试断言友好）。
func sortedPerms(perms []string) []string {
	out := append([]string(nil), perms...)
	sort.Strings(out)
	return out
}
