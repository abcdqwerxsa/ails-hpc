package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/config"
	"ails-hpc/pkg/services/billing"
	"ails-hpc/pkg/services/cluster"
	"ails-hpc/pkg/services/containers"
	"ails-hpc/pkg/services/jobs"
	"ails-hpc/pkg/services/monitor"
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

	userStore, err := auth.LoadUserStore(cfg.UsersFile)
	if err != nil {
		log.Fatalf("load users from %s: %v", cfg.UsersFile, err)
	}

	// 共享单个 slurmrestd 客户端（懒加载 token、401/403 自动续期）
	slurmClient := slurmrest.NewClient(cfg.SlurmRESTDURL, cfg.SlurmUserName, "")
	billingService := billing.NewBillingService(slurmClient)

	// 多租户 Phase 0：租户成员解析器（users.yaml 时代按 orgSlug=租户 派生 clusterUser 清单）
	tenantResolver := func(tenantSlug string) ([]string, error) {
		var members []string
		for _, u := range userStore.ListUsers() {
			if u.OrgSlug == tenantSlug {
				members = append(members, u.ClusterUser)
			}
		}
		return members, nil
	}

	handlers := Handlers{
		Auth:       auth.NewAuthHandler(userStore),
		Cluster:    cluster.NewClusterHandler(cluster.NewClusterService(slurmClient)),
		Nodes:      nodes.NewNodeHandler(nodes.NewNodeService(slurmClient)),
		Jobs:       jobs.NewJobHandler(jobs.NewJobService(slurmClient)),
		Containers: containers.NewContainerHandler(containers.NewContainerService(slurmClient)),
		Billing:    billing.NewBillingHandlerWithScope(billingService, tenantResolver),
		Monitor:    monitor.NewMonitorHandler(monitor.NewMonitorService(slurmClient)),
	}

	r := NewRouter(handlers)

	port := *portFlag
	if port == "" {
		port = cfg.ListenPort
	}

	// systemd Type=notify：等服务能响应 /healthz 后发 READY=1，之后每 10s 发 WATCHDOG=1。
	// unit 里 WatchdogSec=30：若 apiserver 挂死、停止心跳，systemd 自动重启（覆盖"进程在但僵死"）。
	// 非 systemd 运行（NOTIFY_SOCKET 未设）时 sdNotify 为 no-op，不影响本地开发。
	go func() {
		healthz := "http://127.0.0.1:" + port + "/healthz"
		for i := 0; i < 100; i++ { // ~10s 内等服务就绪
			if resp, err := http.Get(healthz); err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		sdNotify("READY=1")
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			sdNotify("WATCHDOG=1")
		}
	}()

	if err := r.Run(":" + port); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

// sdNotify 通过 NOTIFY_SOCKET 向 systemd 发送 sd_notify 协议消息（READY=1 / WATCHDOG=1）。
// 非 systemd 环境（NOTIFY_SOCKET 未设）静默 no-op。
func sdNotify(state string) {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return
	}
	// user 会话用抽象 socket（@ 前缀 → abstract namespace，NUL 起始）
	if strings.HasPrefix(socket, "@") {
		socket = "\x00" + socket[1:]
	}
	conn, err := net.Dial("unixgram", socket)
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.Write([]byte(state))
}
