package main

import (
	"flag"
	"log"
	"net/http"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/config"
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
// 运行所需环境变量（pkg/config）：
//   - AILS_JWT_SECRET       （必填）登录 JWT 签名密钥，空则拒绝启动
//   - AILS_USERS_FILE       用户库 YAML，默认 config/users.yaml
//   - SLURMRESTD_URL        slurmrestd 地址，默认 http://192.168.20.226:6820
//   - AILS_SLURM_USER       slurm 用户名，默认 hpcuser
//   - AILS_DEPLOY_HOST      容器 IDE 入口主机，默认 192.168.20.226
//   - AILS_CONTAINER_JWT_SECRET （可选）容器代理令牌密钥
//   - AILS_TOKEN_TTL / AILS_PORT 可选
//
// 安全债（后续 Phase 修复）：
//   - /api/v1/slurm 组尚无 JWT 门禁、写接口处于无鉴权状态 —— Phase E 挂
//     JWTAuthMiddleware + RequireRole 角色矩阵并接入真登录路由。
//   - CORS OPTIONS 仍返回 24、Allow-Origin:* 与 Allow-Credentials:true 并存 —— Phase E 修正。
func main() {
	portFlag := flag.String("port", "", "Port for API server (overrides AILS_PORT; default 8090)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	// 注入签名密钥与 TTL（fail-closed：无密钥已在 config.Load 阶段拒绝启动）。
	// 真登录路由与 JWT 组在 Phase E 接入。
	auth.SetSecret(cfg.JWTSecret)
	auth.SetTokenTTL(cfg.TokenTTL)

	// 容器 IDE 入口主机与代理密钥（替代 jwt_proxy.go 历史硬编码）
	containers.SetDeployHost(cfg.DeployHost)
	containers.SetContainerJWTSecret(cfg.ContainerJWTSecret)

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

	// 共享单个 slurmrestd 客户端（懒加载 token、401/403 自动续期）
	slurmClient := slurmrest.NewClient(cfg.SlurmRESTDURL, cfg.SlurmUserName, "")

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
		slurmGroup.POST("/nodes/:name/state", nodesHandler.UpdateNodeState)

		slurmGroup.GET("/jobs", jobsHandler.ListJobs)
		slurmGroup.POST("/jobs/submit", jobsHandler.SubmitJob)
		slurmGroup.POST("/jobs/:id/cancel", jobsHandler.CancelJob)
		slurmGroup.POST("/jobs/:id/hold", jobsHandler.HoldJob)
		slurmGroup.POST("/jobs/:id/requeue", jobsHandler.RequeueJob)

		slurmGroup.POST("/containers/launch", containersHandler.LaunchContainer)
		slurmGroup.GET("/containers/list", containersHandler.ListContainers)
		slurmGroup.DELETE("/containers/:id", containersHandler.RecycleContainer)

		slurmGroup.GET("/partitions", clusterHandler.GetPartitions)
		billingHandler.RegisterRoutes(slurmGroup)
	}

	// Serve Neumorphic Web Dashboard Static Portal
	r.Static("/portal", "./apps/web")
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/portal/")
	})

	port := *portFlag
	if port == "" {
		port = cfg.ListenPort
	}
	r.Run(":" + port)
}
