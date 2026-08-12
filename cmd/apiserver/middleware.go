package main

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
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
