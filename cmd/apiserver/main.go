package main

import (
	"flag"
	"log"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/config"
	"ails-hpc/pkg/services/billing"
	"ails-hpc/pkg/services/cluster"
	"ails-hpc/pkg/services/containers"
	"ails-hpc/pkg/services/jobs"
	"ails-hpc/pkg/services/nodes"
	"ails-hpc/pkg/slurmrest"
)

// main 是 AILS HPC Slurm 管理平台的唯一运行入口（纯 SlurmRESTd 单体）。
//
// 运行所需环境变量（pkg/config）：
//   - AILS_JWT_SECRET            （必填）登录 JWT 签名密钥，空则拒绝启动
//   - AILS_USERS_FILE            用户库 YAML，默认 config/users.yaml
//   - SLURMRESTD_URL             slurmrestd 地址，默认 http://192.168.20.226:6820
//   - AILS_SLURM_USER            slurm 用户名，默认 hpcuser
//   - AILS_DEPLOY_HOST           容器 IDE 入口主机，默认 192.168.20.226
//   - AILS_CONTAINER_JWT_SECRET  （可选）容器代理令牌密钥
//   - AILS_TOKEN_TTL / AILS_PORT 可选
func main() {
	portFlag := flag.String("port", "", "Port for API server (overrides AILS_PORT; default 8090)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	// 注入签名密钥与 TTL（fail-closed：无密钥已在 config.Load 阶段拒绝启动）
	auth.SetSecret(cfg.JWTSecret)
	auth.SetTokenTTL(cfg.TokenTTL)

	// 容器 IDE 入口主机与代理密钥（替代 jwt_proxy.go 历史硬编码）
	containers.SetDeployHost(cfg.DeployHost)
	containers.SetContainerJWTSecret(cfg.ContainerJWTSecret)

	userStore, err := auth.LoadUserStore(cfg.UsersFile)
	if err != nil {
		log.Fatalf("load users from %s: %v", cfg.UsersFile, err)
	}

	// 共享单个 slurmrestd 客户端（懒加载 token、401/403 自动续期）
	slurmClient := slurmrest.NewClient(cfg.SlurmRESTDURL, cfg.SlurmUserName, "")
	billingService := billing.NewBillingService(slurmClient)

	handlers := Handlers{
		Auth:       auth.NewAuthHandler(userStore),
		Cluster:    cluster.NewClusterHandler(cluster.NewClusterService(slurmClient)),
		Nodes:      nodes.NewNodeHandler(nodes.NewNodeService(slurmClient)),
		Jobs:       jobs.NewJobHandler(jobs.NewJobService(slurmClient)),
		Containers: containers.NewContainerHandler(containers.NewContainerService()),
		Billing:    billing.NewBillingHandler(billingService),
	}

	r := NewRouter(handlers)

	port := *portFlag
	if port == "" {
		port = cfg.ListenPort
	}
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
