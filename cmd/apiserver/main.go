package main

import (
	"flag"
	"net/http"
	"os"

	"ails-hpc/pkg/services/billing"
	"ails-hpc/pkg/services/cluster"
	"ails-hpc/pkg/services/containers"
	"ails-hpc/pkg/services/jobs"
	"ails-hpc/pkg/services/nodes"
	"ails-hpc/pkg/slurmrest"

	"github.com/gin-gonic/gin"
)

// main 是 AILS HPC Slurm 管理平台的唯一运行入口（纯 SlurmRESTd 单体）。
//
// 注意（安全债，后续 Phase 修复）：
//   - 当前 /api/v1/slurm 组尚无 JWT 门禁，写接口处于无鉴权状态 —— Phase C 引入真登录、
//     Phase E 给整组挂 JWTAuthMiddleware + RequireRole 角色矩阵。
//   - CORS 的 OPTIONS 仍返回 24、Allow-Origin:* 与 Allow-Credentials:true 并存 —— Phase E 修正。
func main() {
	port := flag.String("port", "8090", "Port for API server")
	flag.Parse()

	r := gin.Default()

	// Enable CORS for Web Portal（Phase E 将修正 OPTIONS 状态码与 credentials 冲突）
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(24) // TODO(Phase E): http.StatusNoContent (204)
			return
		}
		c.Next()
	})

	slurmRESTURL := os.Getenv("SLURMRESTD_URL")
	if slurmRESTURL == "" {
		slurmRESTURL = "http://192.168.20.226:6820"
	}

	// 共享单个 slurmrestd 客户端（懒加载 token、401/403 自动续期）
	slurmClient := slurmrest.NewClient(slurmRESTURL, "hpcuser", "")

	billingService := billing.NewBillingService()
	billingHandler := billing.NewBillingHandler(billingService)

	clusterHandler := cluster.NewClusterHandler(cluster.NewClusterService(slurmClient))
	jobsHandler := jobs.NewJobHandler(jobs.NewJobServiceWithBilling(slurmClient, billingService))
	nodesHandler := nodes.NewNodeHandler(nodes.NewNodeService(slurmClient))
	containersHandler := containers.NewContainerHandler(containers.NewContainerServiceWithBilling(billingService))

	// Direct Slurm Portal REST APIs（Phase E 将整组挂 JWT + 角色矩阵）
	slurmGroup := r.Group("/api/v1/slurm")
	{
		slurmGroup.GET("/ping", clusterHandler.GetStatus)
		slurmGroup.GET("/nodes", nodesHandler.GetNodes)
		slurmGroup.POST("/nodes/:name/state", nodesHandler.UpdateNodeState) // Phase E: RequireRole(admin)

		slurmGroup.GET("/jobs", jobsHandler.ListJobs)
		slurmGroup.POST("/jobs/submit", jobsHandler.SubmitJob) // Phase E: RequireRole(member,tenant_admin)
		slurmGroup.POST("/jobs/:id/cancel", jobsHandler.CancelJob)
		slurmGroup.POST("/jobs/:id/hold", jobsHandler.HoldJob)
		slurmGroup.POST("/jobs/:id/requeue", jobsHandler.RequeueJob)

		slurmGroup.POST("/containers/launch", containersHandler.LaunchContainer) // Phase E: RequireRole(member,tenant_admin)
		slurmGroup.GET("/containers/list", containersHandler.ListContainers)
		slurmGroup.DELETE("/containers/:id", containersHandler.RecycleContainer)

		slurmGroup.GET("/partitions", clusterHandler.GetPartitions)
		billingHandler.RegisterRoutes(slurmGroup) // Phase E: 拆为显式注册并挂 RequireRole(member,tenant_admin,ops)
	}

	// Serve Neumorphic Web Dashboard Static Portal
	r.Static("/portal", "./apps/web")
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/portal/")
	})

	r.Run(":" + *port)
}
