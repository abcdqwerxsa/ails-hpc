package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/config"
	"ails-hpc/pkg/services/admin"
	"ails-hpc/pkg/services/billing"
	"ails-hpc/pkg/services/cluster"
	"ails-hpc/pkg/services/containers"
	"ails-hpc/pkg/services/jobs"
	"ails-hpc/pkg/services/monitor"
	"ails-hpc/pkg/services/nodes"
	"ails-hpc/pkg/slurmrest"
	"ails-hpc/pkg/store"
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
	importUsersFlag := flag.String("import-users", "", "Import a users.yaml into the sqlite user store (AILS_DB_PATH), then exit")
	exportSeedsFlag := flag.String("export-seeds", "", "Export the sqlite user store as cluster seed JSON (tenants+users), then exit")
	backupDbFlag := flag.String("backup-db", "", "Online-snapshot the sqlite user store (VACUUM INTO) to the given path, then exit")
	flag.Parse()

	// -backup-db：用户库在线快照（systemd timer 每日调用；宿主无 sqlite3 CLI）。
	if *backupDbFlag != "" {
		dbPath := os.Getenv("AILS_DB_PATH")
		if dbPath == "" {
			dbPath = "var/lib/ails/ails.db"
		}
		st, err := store.Open(dbPath)
		if err != nil {
			log.Fatalf("open sqlite store %s: %v", dbPath, err)
		}
		if err := store.BackupTo(st, *backupDbFlag); err != nil {
			log.Fatalf("backup: %v", err)
		}
		fmt.Printf("backup written to %s\n", *backupDbFlag)
		return
	}

	// -export-seeds：把用户库导出为集群供给种子（entrypoint 重建集群时消费；db 真相源）。
	if *exportSeedsFlag != "" {
		dbPath := os.Getenv("AILS_DB_PATH")
		if dbPath == "" {
			dbPath = "var/lib/ails/ails.db"
		}
		st, err := store.Open(dbPath)
		if err != nil {
			log.Fatalf("open sqlite store %s: %v", dbPath, err)
		}
		f, err := os.Create(*exportSeedsFlag)
		if err != nil {
			log.Fatalf("create seeds file: %v", err)
		}
		if err := store.WriteSeedsJSON(f, st); err != nil {
			log.Fatalf("export seeds: %v", err)
		}
		_ = f.Close()
		fmt.Printf("seeds exported to %s\n", *exportSeedsFlag)
		return
	}

	// -import-users：一次性迁移工具（多租户 Phase 1）。不启动服务。
	if *importUsersFlag != "" {
		dbPath := os.Getenv("AILS_DB_PATH")
		if dbPath == "" {
			dbPath = "var/lib/ails/ails.db"
		}
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
			log.Fatalf("mkdir db dir: %v", err)
		}
		st, err := store.Open(dbPath)
		if err != nil {
			log.Fatalf("open sqlite store %s: %v", dbPath, err)
		}
		n, err := store.ImportYaml(st, *importUsersFlag)
		if err != nil {
			log.Fatalf("import: %v", err)
		}
		fmt.Printf("imported %d users into %s\n", n, dbPath)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	// 注入签名密钥与 TTL（fail-closed：无密钥已在 config.Load 阶段拒绝启动）
	auth.SetSecret(cfg.JWTSecret)
	auth.SetTokenTTL(cfg.TokenTTL)
	auth.SetIdeCookieSecure(cfg.CookieSecure)

	// 用户库 = sqlite（Phase 6 起 db 唯一；users.yaml 仅作 -import-users 导入源）。
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open sqlite user store %s: %v (fresh install? seed it: apiserver -import-users config/users.yaml)", cfg.DBPath, err)
	}
	defer st.Close()
	userStore := auth.UserStore(st)
	adminStore, ok := st.(store.AdminStore)
	if !ok {
		log.Fatalf("sqlite store does not implement AdminStore (internal error)")
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

	authHandler := auth.NewAuthHandler(userStore)
	authHandler.SetAuditSink(adminStore) // A2：登录成功/失败、改密入库

	// OIDC SSO（S1/S2；未配置时端点自报 enabled=false，本地密码登录不受影响）。
	adminService := admin.NewService(adminStore, admin.DefaultProvisioner)
	oidcHandler := auth.NewOIDCHandler(userStore, adminService, cfg.OIDCMapping)
	oidcHandler.SetAuditSink(adminStore)
	oidcHandler.PortalURL = cfg.OIDCPortalURL
	if cfg.OIDC.Enabled() {
		auth.SetOIDCClient(auth.NewOIDCClient(cfg.OIDC))
		log.Printf("OIDC SSO enabled: issuer=%s redirect=%s unmapped=%s",
			cfg.OIDC.Issuer, cfg.OIDC.RedirectURL, cfg.OIDCMapping.UnmappedPolicy)
	}

	containerService := containers.NewContainerService(slurmClient)

	// 空闲容器自动回收守护协程（默认 60 分钟，AILS_IDE_IDLE_TIMEOUT_MIN 可调；0=关闭）
	idleTimeoutMin := 60
	if envTimeout := os.Getenv("AILS_IDE_IDLE_TIMEOUT_MIN"); envTimeout != "" {
		if v, err := strconv.Atoi(envTimeout); err == nil && v >= 0 {
			idleTimeoutMin = v
		}
	}
	if idleTimeoutMin > 0 {
		containerService.StartIdleReaper(context.Background(), 1*time.Minute, idleTimeoutMin, func(sessionID string, jobID int, owner string, idleMin int) {
			log.Printf("[IdleReaper] Auto-reclaimed idle session %s (job %d, owner %s, idle %d min)", sessionID, jobID, owner, idleMin)
			_ = adminStore.WriteAudit(context.Background(), "system", "container.idle_reclaim", "session:"+sessionID, "", fmt.Sprintf(`{"job_id":%d,"owner":%q,"idle_min":%d}`, jobID, owner, idleMin))
		})
		log.Printf("IDE IdleReaper started: idle_timeout=%d min", idleTimeoutMin)
	}

	handlers := Handlers{
		Auth:       authHandler,
		OIDC:       oidcHandler,
		Cluster:    cluster.NewClusterHandler(cluster.NewClusterService(slurmClient)),
		Nodes:      nodes.NewNodeHandler(nodes.NewNodeService(slurmClient)),
		Jobs:       jobs.NewJobHandlerScoped(jobs.NewJobService(slurmClient), tenantResolver),
		Containers: containers.NewContainerHandlerScoped(containerService, tenantResolver),
		Billing:    billing.NewBillingHandlerWithScope(billingService, tenantResolver),
		Monitor: monitor.NewMonitorHandler(
			monitor.NewMonitorServicePersistent(slurmClient, filepath.Join(filepath.Dir(cfg.DBPath), "monitor.db"))),
		Admin: admin.NewAdminHandler(adminService),
		Audit: adminStore, // A2：/slurm/** 变更操作审计出口
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
