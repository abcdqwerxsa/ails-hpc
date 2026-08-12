package main

import (
	"net/http"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/services/billing"
	"ails-hpc/pkg/services/cluster"
	"ails-hpc/pkg/services/containers"
	"ails-hpc/pkg/services/jobs"
	"ails-hpc/pkg/services/nodes"

	"github.com/gin-gonic/gin"
)

// Handlers 聚合所有领域 HTTP 处理器，供 NewRouter 装配路由。
type Handlers struct {
	Auth       *auth.AuthHandler
	Cluster    *cluster.ClusterHandler
	Nodes      *nodes.NodeHandler
	Jobs       *jobs.JobHandler
	Containers *containers.ContainerHandler
	Billing    *billing.BillingHandler
}

// NewRouter 装配整套路由：CORS、公开登录端点、JWT 保护的 /api/v1 组，
// 并按四角色矩阵在每个写/读敏感路由上挂 auth.RequireRole。
//
// 角色矩阵（与系统角色边界一致）：
//   - 读集群状态（ping/nodes/jobs/partitions）：所有已认证角色
//   - 节点 DRAIN/RESUME：admin 独占（member/tenant_admin/ops 不可）
//   - 作业提交/控制 + 容器 IDE：member + tenant_admin（admin 是纯监控角色，不提交作业）
//   - 计费读取：member(自己)/tenant_admin(租户)/ops_admin(全部)（admin 纯硬件监控不含计费）
func NewRouter(h Handlers) *gin.Engine {
	r := gin.Default()
	r.Use(corsMiddleware())

	// 公开路由：仅登录。其余 /api/v1/** 一律需 Bearer JWT。
	r.POST("/api/v1/auth/login", h.Auth.Login)

	api := r.Group("/api/v1")
	api.Use(auth.JWTAuthMiddleware())
	{
		slurm := api.Group("/slurm")

		// 读：集群状态（所有已认证角色）
		slurm.GET("/ping", h.Cluster.GetStatus)
		slurm.GET("/nodes", h.Nodes.GetNodes)
		slurm.GET("/jobs", h.Jobs.ListJobs)
		slurm.GET("/partitions", h.Cluster.GetPartitions)

		// 管理员独占：节点 DRAIN/RESUME
		slurm.POST("/nodes/:name/state", auth.RequireRole(auth.RoleSystemAdmin), h.Nodes.UpdateNodeState)

		// member + tenant_admin：作业提交与控制
		memberWrite := auth.RequireRole(auth.RoleMember, auth.RoleTenantAdmin)
		slurm.POST("/jobs/submit", memberWrite, h.Jobs.SubmitJob)
		slurm.POST("/jobs/:id/cancel", memberWrite, h.Jobs.CancelJob)
		slurm.POST("/jobs/:id/hold", memberWrite, h.Jobs.HoldJob)
		slurm.POST("/jobs/:id/requeue", memberWrite, h.Jobs.RequeueJob)

		// member + tenant_admin：交互式开发环境（Web-IDE）
		slurm.POST("/containers/launch", memberWrite, h.Containers.LaunchContainer)
		slurm.GET("/containers/list", memberWrite, h.Containers.ListContainers)
		slurm.DELETE("/containers/:id", memberWrite, h.Containers.RecycleContainer)

		// member(自己)/tenant_admin(租户)/ops_admin(全部)：计费读取
		billingRead := auth.RequireRole(auth.RoleMember, auth.RoleTenantAdmin, auth.RoleOpsAdmin)
		slurm.GET("/billing/usage", billingRead, h.Billing.GetUsage)
		slurm.GET("/billing/export", billingRead, h.Billing.ExportReport)

		// Web-IDE 反向代理：/api/v1/ide/<session>/* → 计算节点上的 Jupyter/code-server
		api.Any("/ide/:session/*any", memberWrite, h.Containers.ProxyIDE)
	}

	// 静态门户
	r.Static("/portal", "./apps/web")
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/portal/")
	})

	return r
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
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
