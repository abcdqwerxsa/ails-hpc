package auth

import "github.com/gin-gonic/gin"

// 数据可见范围（多租户 Phase 0：统一租户策略的唯一出处）。
//
// Mode 语义：
//   - ScopeSelf   member：只能看自己（ClusterUser 维度）
//   - ScopeTenant tenant_admin：本租户用户集合（TenantSlug 维度，成员清单由调用方注入解析）
//   - ScopeAll    ops_admin/admin：平台级全量
//
// 迁移期：租户标识暂用 OrgSlug（users.yaml 时代即租户）；Phase 2 引入 Claims.TID 后
// tenantOf 优先取 TID。
type ScopeMode int

const (
	ScopeSelf ScopeMode = iota
	ScopeTenant
	ScopeAll
)

// Scope 是从 JWT claims 推导的数据可见范围。
type Scope struct {
	Mode        ScopeMode
	TenantSlug  string // ScopeTenant 时非空
	ClusterUser string // ScopeSelf 时的本人集群身份
	Username    string
}

// ScopeFromClaims 按权威角色推导可见范围。claims 为 nil 时返回零值（最保守：Self 且
// 无人匹配——配合 fail-closed 中间件实际不可达，防御性兜底）。
func ScopeFromClaims(cl *Claims) Scope {
	if cl == nil {
		return Scope{}
	}
	s := Scope{Username: cl.Username, ClusterUser: cl.ClusterUser, TenantSlug: tenantOf(cl)}
	switch cl.Role {
	case RoleSystemAdmin, RoleOpsAdmin:
		s.Mode = ScopeAll
	case RoleTenantAdmin:
		s.Mode = ScopeTenant
	default: // member 及一切未知角色：最保守
		s.Mode = ScopeSelf
	}
	return s
}

// tenantOf 取租户标识：Claims.TID（Phase 2 起）优先，迁移期回退 OrgSlug。
func tenantOf(cl *Claims) string {
	if cl.TID != "" {
		return cl.TID
	}
	return cl.OrgSlug
}

// AllowsUser 判断某 clusterUser 的数据是否可见。
// ScopeSelf：仅本人；ScopeTenant/ScopeAll 恒真——租户收紧由调用方拿租户成员清单
// 做 AllowedUsers 列表过滤（清单来源 DB/users.yaml，auth 包不持有）。
func (s Scope) AllowsUser(clusterUser string) bool {
	if s.Mode == ScopeSelf {
		return clusterUser == s.ClusterUser
	}
	return true
}

// TenantResolver 返回某租户成员的 clusterUser 清单（users.yaml 时代由 main 按 orgSlug
// 派生，DB 时代由 store.ClusterUsersOfTenant 实现；billing/jobs/containers 共用）。
type TenantResolver func(tenantSlug string) ([]string, error)

// RowFilter 返回"数据行属主是否可见"的谓词，列表过滤与控制门共用同一语义：
//   - ScopeSelf   member：仅本人
//   - ScopeTenant tenant_admin：本租户成员清单（resolver 为 nil 时不收紧——仅限测试/旧装配）
//   - ScopeAll    ops_admin/admin：恒真
//
// owner 为空 = 遗留数据（squeue 兜底/迁移期作业），全员可见——与归属控制的历史豁免一致。
func (s Scope) RowFilter(resolve TenantResolver) (func(owner string) bool, error) {
	switch s.Mode {
	case ScopeSelf:
		return func(owner string) bool { return owner == "" || owner == s.ClusterUser }, nil
	case ScopeTenant:
		if resolve == nil {
			return func(string) bool { return true }, nil
		}
		members, err := resolve(s.TenantSlug)
		if err != nil {
			return nil, err
		}
		set := make(map[string]bool, len(members))
		for _, m := range members {
			set[m] = true
		}
		return func(owner string) bool { return owner == "" || set[owner] }, nil
	default:
		return func(string) bool { return true }, nil
	}
}

// ClaimsFromCtx 从 gin 上下文取 JWT claims（各 service handler 共用的读取口）。
func ClaimsFromCtx(c *gin.Context) *Claims {
	if v, ok := c.Get("claims"); ok {
		if cl, ok := v.(*Claims); ok {
			return cl
		}
	}
	return nil
}
