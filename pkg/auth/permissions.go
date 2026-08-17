package auth

import (
	"fmt"
	"sort"

	"ails-hpc/pkg/httpx"

	"github.com/gin-gonic/gin"
)

// 权限点词汇表（维护期 R1）：路由与前端只认权限点，角色是权限的命名集合。
// 本文件把既有四角色×路由矩阵翻译为 16 个权限点常量；RequireRole(...) 全部替换为
// RequirePermission("...")，内部暂由内置角色映射满足——行为零变化（矩阵测试护航）。

// 权限点常量。命名约定 `<域>:<动作>`（租户域三段）。这是 API 权威词汇，新增权限点
// 必须同步 AllPermissions / docs/rbac-matrix.md。
const (
	// PermClusterRead 读集群状态：ping/nodes/jobs/detail/history/partitions/monitor。
	// 历史矩阵：所有已认证角色。
	PermClusterRead = "cluster:read"
	// PermNodesManage 节点 DRAIN/RESUME。历史矩阵：admin 独占。
	PermNodesManage = "nodes:manage"
	// PermJobsSubmit 作业提交。历史矩阵：member + tenant_admin。
	PermJobsSubmit = "jobs:submit"
	// PermJobsControl 作业取消/挂起/重排。历史矩阵：member + tenant_admin。
	PermJobsControl = "jobs:control"
	// PermIdeList Web-IDE 会话列表。历史矩阵：member + tenant_admin。
	PermIdeList = "ide:list"
	// PermIdeManage Web-IDE 启动/回收/延时/反代。历史矩阵：member + tenant_admin。
	PermIdeManage = "ide:manage"
	// PermBillingRead 计费读取（数据范围 self/tenant/all 由角色 scope 另行收口，
	// 权限点只管路由准入）。历史矩阵：member + tenant_admin + ops_admin。
	PermBillingRead = "billing:read"

	// PermTenantsRead 平台租户清单/租户成员查看。历史矩阵：admin 独占。
	PermTenantsRead = "tenants:read"
	// PermTenantsManage 平台租户创建/修改（含 QOS 绑定）。历史矩阵：admin 独占。
	PermTenantsManage = "tenants:manage"
	// PermUsersCreate 平台用户创建。历史矩阵：admin 独占。
	PermUsersCreate = "users:create"
	// PermAuditRead 平台审计日志查看。历史矩阵：admin 独占。
	PermAuditRead = "audit:read"
	// PermReservationsManage 预约查看/创建/删除（admin 直通 scontrol）。历史矩阵：admin 独占。
	PermReservationsManage = "reservations:manage"
	// PermQosManage QOS 查看/创建/绑定（admin 直通 sacctmgr）。历史矩阵：admin 独占。
	PermQosManage = "qos:manage"

	// PermTenantUsersRead 本租户成员查看。历史矩阵：tenant_admin。
	PermTenantUsersRead = "tenant:users:read"
	// PermTenantUsersManage 本租户成员创建/修改。历史矩阵：tenant_admin。
	PermTenantUsersManage = "tenant:users:manage"
	// PermTenantUsersResetPassword 本租户成员密码重置。历史矩阵：tenant_admin。
	PermTenantUsersResetPassword = "tenant:users:reset_password"
)

// AllPermissions 权威权限点清单（R3 子集校验与矩阵测试对照用）。
// 与本文件 const 块一一对应——新增权限点两处同步。
var AllPermissions = []string{
	PermClusterRead,
	PermNodesManage,
	PermJobsSubmit,
	PermJobsControl,
	PermIdeList,
	PermIdeManage,
	PermBillingRead,
	PermTenantsRead,
	PermTenantsManage,
	PermUsersCreate,
	PermAuditRead,
	PermReservationsManage,
	PermQosManage,
	PermTenantUsersRead,
	PermTenantUsersManage,
	PermTenantUsersResetPassword,
}

// BuiltinRolePermissions 内置四角色 → 权限集合。与替换前的 RequireRole 路由矩阵
// 逐条对照（cmd/apiserver/router.go 注释保留历史矩阵说明）：
//   - admin：集群读 + 节点/租户/用户/审计/预约/QOS 管理（纯硬件监控，无作业/IDE/计费）
//   - ops_admin：集群读 + 计费读（全部维度）
//   - tenant_admin：集群读 + 作业提交/控制 + IDE + 计费（租户维度）+ 本租户用户管理
//   - member：集群读 + 作业提交/控制 + IDE + 计费（本人维度）
var BuiltinRolePermissions = map[string][]string{
	RoleSystemAdmin: {
		PermClusterRead, PermNodesManage,
		PermTenantsRead, PermTenantsManage, PermUsersCreate,
		PermAuditRead, PermReservationsManage, PermQosManage,
	},
	RoleOpsAdmin: {
		PermClusterRead, PermBillingRead,
	},
	RoleTenantAdmin: {
		PermClusterRead, PermJobsSubmit, PermJobsControl,
		PermIdeList, PermIdeManage, PermBillingRead,
		PermTenantUsersRead, PermTenantUsersManage, PermTenantUsersResetPassword,
	},
	RoleMember: {
		PermClusterRead, PermJobsSubmit, PermJobsControl,
		PermIdeList, PermIdeManage, PermBillingRead,
	},
}

// claimPermissionResolver 把 claims 解析为权限集合。优先级：
//  1. claims.Perms（登录签发/带 store 的中间件每请求按 roles 表刷新——R2 起 DB 权威）
//  2. BuiltinRolePermissions[claims.Role]（内存/yaml 库与迁移期旧令牌的回退——零行为变化）
var claimPermissionResolver = func(cl *Claims) map[string]bool {
	if cl == nil {
		return map[string]bool{}
	}
	if len(cl.Perms) > 0 {
		return permSetOf(cl.Perms...)
	}
	return permSetOf(BuiltinRolePermissions[cl.Role]...)
}

// PermissionsOf 返回 claims 持有者的权限集合。
func PermissionsOf(cl *Claims) map[string]bool {
	return claimPermissionResolver(cl)
}

// permSetOf 构造权限点集合。
func permSetOf(perms ...string) map[string]bool {
	set := make(map[string]bool, len(perms))
	for _, p := range perms {
		set[p] = true
	}
	return set
}

// ValidPermission 判断权限点在权威词汇表内（R3 建角色时的白名单校验）。
func ValidPermission(p string) bool {
	return permSetOf(AllPermissions...)[p]
}

// RequirePermission 校验 claims 持有全部所列权限点（AND 语义）。403 的 required extra
// 携带权限点清单（RequireRole 时代携带角色清单——门面切换，四角色鉴权结果零变化）。
func RequirePermission(perms ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		val, exists := c.Get("claims")
		if !exists {
			httpx.Unauthorized(c, "missing authenticated context")
			return
		}
		claims, ok := val.(*Claims)
		if !ok {
			httpx.Unauthorized(c, "invalid authenticated context")
			return
		}

		held := PermissionsOf(claims)
		missing := []string{}
		for _, p := range perms {
			if !held[p] {
				missing = append(missing, p)
			}
		}
		if len(missing) > 0 {
			httpx.Forbidden(c,
				fmt.Sprintf("forbidden: missing permission %v", missing), perms)
			return
		}
		c.Next()
	}
}

// SortedPermissions 供 /auth/me 与角色管理 API 输出稳定排序的权限清单。
func SortedPermissions(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
