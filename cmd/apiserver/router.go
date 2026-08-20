package main

import (
	"net/http"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/services/admin"
	"ails-hpc/pkg/services/billing"
	"ails-hpc/pkg/services/cluster"
	"ails-hpc/pkg/services/containers"
	"ails-hpc/pkg/services/jobs"
	"ails-hpc/pkg/services/monitor"
	"ails-hpc/pkg/services/nodes"

	"github.com/gin-gonic/gin"
)

// Handlers 聚合所有领域 HTTP 处理器，供 NewRouter 装配路由。
type Handlers struct {
	Auth       *auth.AuthHandler
	OIDC       *auth.OIDCHandler // 未配置 OIDC 时也要构造（端点自查 enabled）
	Cluster    *cluster.ClusterHandler
	Nodes      *nodes.NodeHandler
	Jobs       *jobs.JobHandler
	Containers *containers.ContainerHandler
	Billing    *billing.BillingHandler
	Monitor    *monitor.MonitorHandler
	Admin      *admin.AdminHandler // yaml 模式可为 nil（端点统一 503）
	// Audit 是 /slurm/** 变更操作的审计出口（A2；nil=不落，测试装配用）。
	Audit auditSink
}

// NewRouter 装配整套路由：CORS、公开登录端点、JWT 保护的 /api/v1 组，
// 并按权限点在每个写/读敏感路由上挂 auth.RequirePermission（R1 起）。
//
// 历史角色矩阵（内置角色经 BuiltinRolePermissions 映射到权限点，行为零变化）：
//   - 读集群状态（ping/nodes/jobs/partitions）：cluster:read（所有已认证角色）
//   - 节点 DRAIN/RESUME：nodes:manage（admin 独占；member/tenant_admin/ops 不可）
//   - 作业提交/控制 + 容器 IDE：jobs:submit|jobs:control|ide:*（member + tenant_admin；
//     admin 是纯监控角色，不提交作业）
//   - 计费读取：billing:read（member(自己)/tenant_admin(租户)/ops_admin(全部)；
//     admin 纯硬件监控不含计费）
func NewRouter(h Handlers) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), requestIDMiddleware(), accessLogMiddleware(), corsMiddleware())

	// 公开路由：登录 + liveness/readiness 探针。其余 /api/v1/** 一律需 Bearer JWT。
	r.POST("/api/v1/auth/login", h.Auth.Login)
	// OIDC SSO（S1/S3；未配置时 /config 返回 enabled=false，其余端点 400）
	r.GET("/api/v1/auth/oidc/config", h.OIDC.Config)
	r.GET("/api/v1/auth/oidc/login", h.OIDC.Login)
	r.GET("/api/v1/auth/oidc/callback", h.OIDC.Callback)
	r.POST("/api/v1/auth/oidc/link", h.OIDC.Link)
	r.GET("/healthz", healthHandler)   // 免鉴权 liveness：进程在跑即可
	r.GET("/readyz", h.Cluster.Readyz) // 免鉴权 readiness：探 slurmrestd 可达性

	api := r.Group("/api/v1")
	// Phase 2：带用户库实校——禁用/改密即刻吊销在途令牌（claims.Ver 比对）。
	api.Use(auth.JWTAuthMiddlewareWithStore(h.Auth.Store()))
	// A2：作业提交/控制、IDE 会话操作落 audit_log（变更面审计补齐；登录审计在 handler）。
	// P2：映射含 /ide/**（反代内写操作）。中间件挂在 api 组——/ide/ 路由同组生效。
	api.Use(slurmAuditMiddleware(h.Audit))
	{
		// 自助改密（任何已认证角色；成功后本人令牌全部失效）
		api.POST("/auth/password", h.Auth.ChangePassword)
		// 权限自描述（R4 前端能力驱动：角色 + 权限点清单 + 集群身份）
		api.GET("/auth/me", h.Auth.Me)
		// A1 会话策略：会话台账 + 全设备登出（token_version+1）
		api.GET("/auth/me/sessions", h.Auth.MySessions)
		api.POST("/auth/logout-all", h.Auth.LogoutAll)
		// OIDC 账号关联（S4；需登录）
		api.GET("/auth/oidc/bind", h.OIDC.BindLogin)
		api.POST("/auth/oidc/unlink", h.OIDC.Unlink)
		slurm := api.Group("/slurm")

		// 读：集群状态（cluster:read——安全审计 2026-08-19 P1-7：权限点此前声明
		// 未执行，剥掉它的自定义角色仍可读全部集群面；内置四角色全持有点，行为不变）
		clusterRead := slurm.Group("", auth.RequirePermission(auth.PermClusterRead))
		clusterRead.GET("/ping", h.Cluster.GetStatus)
		clusterRead.GET("/nodes", h.Nodes.GetNodes)
		clusterRead.GET("/jobs", h.Jobs.ListJobs)
		clusterRead.GET("/jobs/:id/detail", h.Jobs.GetJobDetail)
		clusterRead.GET("/jobs/history", h.Jobs.ListHistory)
		clusterRead.GET("/partitions", h.Cluster.GetPartitions)
		clusterRead.GET("/monitor/snapshot", h.Monitor.GetSnapshot)
		clusterRead.GET("/monitor/history", h.Monitor.GetHistory)

		// 管理员独占：节点 DRAIN/RESUME
		slurm.POST("/nodes/:name/state", auth.RequirePermission(auth.PermNodesManage), h.Nodes.UpdateNodeState)

		// member + tenant_admin：作业提交与控制
		slurm.POST("/jobs/submit", auth.RequirePermission(auth.PermJobsSubmit), h.Jobs.SubmitJob)
		slurm.POST("/jobs/:id/cancel", auth.RequirePermission(auth.PermJobsControl), h.Jobs.CancelJob)
		slurm.POST("/jobs/:id/hold", auth.RequirePermission(auth.PermJobsControl), h.Jobs.HoldJob)
		slurm.POST("/jobs/:id/requeue", auth.RequirePermission(auth.PermJobsControl), h.Jobs.RequeueJob)

		// member + tenant_admin：交互式开发环境（Web-IDE）
		slurm.POST("/containers/launch", auth.RequirePermission(auth.PermIdeManage), h.Containers.LaunchContainer)
		slurm.GET("/containers/list", auth.RequirePermission(auth.PermIdeList), h.Containers.ListContainers)
		slurm.DELETE("/containers/:id", auth.RequirePermission(auth.PermIdeManage), h.Containers.RecycleContainer)
		slurm.POST("/containers/:id/extend", auth.RequirePermission(auth.PermIdeManage), h.Containers.ExtendSession)

		// member(自己)/tenant_admin(租户)/ops_admin(全部)：计费读取
		billingRead := auth.RequirePermission(auth.PermBillingRead)
		slurm.GET("/billing/usage", billingRead, h.Billing.GetUsage)
		slurm.GET("/billing/export", billingRead, h.Billing.ExportReport)
		// v4-W3 租户配额可见性：GrpTRES 上限（sacctmgr 权威读数；scope 收口在 handler）
		slurm.GET("/billing/quota", billingRead, h.Admin.GetBillingQuota)

		// 平台管理（admin 独占；sqlite 库未启用时端点统一 503）。
		// R1 起按权限点逐路由挂（原 RequireRole(admin) 组门面的等价拆分——admin 持有
		// 全部平台权限点，鉴权结果不变；自定义角色获得细粒度准入）。
		platformAdmin := api.Group("/admin")
		platformAdmin.GET("/tenants", auth.RequirePermission(auth.PermTenantsRead), h.Admin.ListTenants)
		platformAdmin.POST("/tenants", auth.RequirePermission(auth.PermTenantsManage), h.Admin.CreateTenant)
		platformAdmin.PATCH("/tenants/:slug", auth.RequirePermission(auth.PermTenantsManage), h.Admin.UpdateTenant)
		platformAdmin.GET("/tenants/:slug/users", auth.RequirePermission(auth.PermTenantsRead), h.Admin.ListTenantUsers)
		// v4-W3 租户配额总览（平台侧入口——admin 无 billing:read，配额经 tenants:read）
		platformAdmin.GET("/tenants/quotas", auth.RequirePermission(auth.PermTenantsRead), h.Admin.GetTenantQuotas)
		platformAdmin.POST("/users", auth.RequirePermission(auth.PermUsersCreate), h.Admin.CreatePlatformUser)
		// v3-U 平台用户生命周期：目录/状态/显示名/重置（users:manage——与建号 users:create 分权）
		usersManage := auth.RequirePermission(auth.PermUsersManage)
		platformAdmin.GET("/users", usersManage, h.Admin.ListPlatformUsers)
		platformAdmin.PATCH("/users/:username", usersManage, h.Admin.UpdatePlatformUser)
		platformAdmin.POST("/users/:username/password", usersManage, h.Admin.ResetPlatformUserPassword)
		platformAdmin.GET("/audit", auth.RequirePermission(auth.PermAuditRead), h.Admin.ListAudit)
		// 4.2 预约 / QOS 管理（admin 直通 scontrol/sacctmgr）
		platformAdmin.GET("/reservations", auth.RequirePermission(auth.PermReservationsManage), h.Admin.ListReservations)
		platformAdmin.POST("/reservations", auth.RequirePermission(auth.PermReservationsManage), h.Admin.CreateReservation)
		platformAdmin.DELETE("/reservations/:name", auth.RequirePermission(auth.PermReservationsManage), h.Admin.DeleteReservation)
		platformAdmin.GET("/qos", auth.RequirePermission(auth.PermQosManage), h.Admin.ListQOS)
		platformAdmin.POST("/qos", auth.RequirePermission(auth.PermQosManage), h.Admin.CreateQOS)
		platformAdmin.PATCH("/tenants/:slug/qos", auth.RequirePermission(auth.PermQosManage), h.Admin.SetTenantQOS)
		// 分区属性查看/修改（v2 增量；scontrol 直通）
		partitionsManage := auth.RequirePermission(auth.PermPartitionsManage)
		platformAdmin.GET("/partitions/:name", partitionsManage, h.Admin.GetPartition)
		platformAdmin.PATCH("/partitions/:name", partitionsManage, h.Admin.UpdatePartition)
		// R3 角色管理：平台自定义角色 CRUD + 角色指派
		rolesManage := auth.RequirePermission(auth.PermRolesManage)
		platformAdmin.GET("/roles", rolesManage, h.Admin.ListPlatformRoles)
		platformAdmin.GET("/tenants/:slug/roles", rolesManage, h.Admin.ListTenantRoles)
		platformAdmin.POST("/roles", rolesManage, h.Admin.CreatePlatformRole)
		platformAdmin.PATCH("/roles/:name", rolesManage, h.Admin.UpdatePlatformRole)
		platformAdmin.DELETE("/roles/:name", rolesManage, h.Admin.DeletePlatformRole)
		platformAdmin.PATCH("/users/:username/role", rolesManage, h.Admin.AssignPlatformRole)

		// 租户管理（tenant_admin；租户归属以 claims 为权威，不信任请求体）
		tenants := api.Group("/tenants")
		tenants.GET("/me/users", auth.RequirePermission(auth.PermTenantUsersRead), h.Admin.ListMyUsers)
		tenants.POST("/me/users", auth.RequirePermission(auth.PermTenantUsersManage), h.Admin.CreateTenantUser)
		tenants.PATCH("/me/users/:username", auth.RequirePermission(auth.PermTenantUsersManage), h.Admin.UpdateMyUser)
		tenants.POST("/me/users/:username/password", auth.RequirePermission(auth.PermTenantUsersResetPassword), h.Admin.ResetMyUserPassword)
		// R3 租户自定义角色 CRUD + 指派（权限 ⊆ 调用者——防提权在 service 层）
		tenantRoles := auth.RequirePermission(auth.PermTenantRolesManage)
		tenants.GET("/me/roles", tenantRoles, h.Admin.ListMyRoles)
		tenants.POST("/me/roles", tenantRoles, h.Admin.CreateMyRole)
		tenants.PATCH("/me/roles/:name", tenantRoles, h.Admin.UpdateMyRole)
		tenants.DELETE("/me/roles/:name", tenantRoles, h.Admin.DeleteMyRole)
		tenants.PATCH("/me/users/:username/role", auth.RequirePermission(auth.PermTenantUsersManage), h.Admin.AssignMyRole)

		// Web-IDE 反向代理：/api/v1/ide/<session>/* → 计算节点上的 Jupyter/code-server
		api.Any("/ide/:session/*any", auth.RequirePermission(auth.PermIdeManage), h.Containers.ProxyIDE)
	}

	// 静态门户：React 构建产物（apps/web/dist）。SPA 用 hash 路由，gin.Static 即可（无需 fallback）。
	r.Static("/portal", "./apps/web/dist")
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/portal/")
	})

	return r
}

// healthHandler GET /healthz —— liveness 探针，免鉴权。
// 仅表示进程在跑且 gin 能服务 HTTP（不探 slurmrestd 可达性，避免后端抖动触发重启风暴）。
func healthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// corsMiddleware 处理浏览器跨域预检与简单请求。
//
// 登录改用 Authorization 头（非 cookie），故使用 Allow-Origin:* 且不启用 credentials，
// 避免 "*" + Allow-Credentials:true 并存被浏览器拒绝。OPTIONS 预检返回 204
// （历史版本误返回 24，已修正）。
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, PATCH, DELETE")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
