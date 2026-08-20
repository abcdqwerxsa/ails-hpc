package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"ails-hpc/pkg/auth"

	"github.com/gin-gonic/gin"
)

// requestIDMiddleware 为每个请求生成或透传 X-Request-ID，便于跨日志/跨服务追踪。
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-ID")
		if rid == "" {
			rid = newRequestID()
		}
		c.Set("request_id", rid)
		c.Header("X-Request-ID", rid)
		c.Next()
	}
}

// accessLogMiddleware 结构化访问日志（JSON），含 request-id、用户、状态码、延迟。
// 测试模式下静默，避免污染测试输出。
func accessLogMiddleware() gin.HandlerFunc {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	return func(c *gin.Context) {
		if gin.Mode() == gin.TestMode {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()

		user := ""
		if v, ok := c.Get("claims"); ok {
			if claims, ok := v.(*auth.Claims); ok {
				user = claims.Username
			}
		}
		logger.Info("request",
			"rid", c.GetString("request_id"),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"user", user,
			"ip", c.ClientIP(),
		)
	}
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// auditSink 是审计写入面（生产 = sqlite AdminStore.WriteAudit 同面）。
type auditSink interface {
	WriteAudit(ctx context.Context, actor, action, target, requestID, detail string) error
}

// slurmAuditActions 路由模式 → 审计动作名（A2：作业提交/控制、IDE 会话操作入库；
// 管理面变更已由 service 层逐操作落审计，此处补齐 /slurm/** 的用户侧变更）。
var slurmAuditActions = map[string]string{
	"/api/v1/slurm/nodes/:name/state":     "nodes.state",
	"/api/v1/slurm/jobs/submit":           "jobs.submit",
	"/api/v1/slurm/jobs/:id/cancel":       "jobs.cancel",
	"/api/v1/slurm/jobs/:id/hold":         "jobs.hold",
	"/api/v1/slurm/jobs/:id/requeue":      "jobs.requeue",
	"/api/v1/slurm/containers/launch":     "ide.launch",
	"/api/v1/slurm/containers/:id":        "ide.recycle",
	"/api/v1/slurm/containers/:id/extend": "ide.extend",
	// P2（安全审计 2026-08-19）：IDE 反代内的写操作（非 GET——terminal 执行/文件写）
	// 此前不落审计；GET 资产加载按既有规则跳过。
	"/api/v1/ide/:session/*any": "ide.proxy",
}

// slurmAuditMiddleware 把 /slurm/** 的变更操作（非 GET）落 audit_log。
// sink 为 nil（测试装配）时 no-op；写失败只记日志不影响响应。
func slurmAuditMiddleware(sink auditSink) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if sink == nil || c.Request.Method == http.MethodGet || c.Request.Method == http.MethodOptions {
			return
		}
		action, ok := slurmAuditActions[c.FullPath()]
		if !ok {
			return
		}
		actor := ""
		if cl := auth.ClaimsFromCtx(c); cl != nil {
			actor = cl.Username
		}
		target := c.Request.URL.Path
		if v := c.Param("id"); v != "" {
			target = v
		} else if v := c.Param("name"); v != "" {
			target = v
		}
		detail := fmt.Sprintf(`{"status":%d,"ip":%q}`, c.Writer.Status(), c.ClientIP())
		rid := c.GetString("request_id")
		if err := sink.WriteAudit(c.Request.Context(), actor, action, target, rid, detail); err != nil {
			slog.Warn("audit write failed", "action", action, "actor", actor, "err", err)
		}
	}
}
