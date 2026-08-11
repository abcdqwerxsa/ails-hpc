// Package config 集中解析进程启动所需的运行时配置（环境变量驱动，带安全默认值）。
// 取代此前散落在 cmd/apiserver 与各 service 里的硬编码 IP / 密钥 / 路径。
package config

import (
	"errors"
	"os"
	"strings"
	"time"
)

// Config 持有进程启动所需的全部运行时参数。
type Config struct {
	ListenPort         string        // AILS_PORT，默认 "8090"（可被 -port flag 覆盖）
	JWTSecret          []byte        // AILS_JWT_SECRET，必填（空则 Load 报错，fail-closed）
	TokenTTL           time.Duration // AILS_TOKEN_TTL，默认 24h
	SlurmRESTDURL      string        // SLURMRESTD_URL，默认 "http://192.168.20.226:6820"
	SlurmUserName      string        // AILS_SLURM_USER，默认 "hpcuser"
	UsersFile          string        // AILS_USERS_FILE，默认 "config/users.yaml"
	DeployHost         string        // AILS_DEPLOY_HOST，默认 "192.168.20.226"（容器 IDE 入口 URL 主机）
	ContainerJWTSecret []byte        // AILS_CONTAINER_JWT_SECRET，可选（容器代理令牌密钥；空则用包内兜底）
}

// Load 从环境变量读取配置。
//
// JWTSecret 为空时返回错误 —— fail-closed：拒绝在无签名密钥的状态下启动，
// 以免签发出可被伪造的令牌。
func Load() (*Config, error) {
	cfg := &Config{
		ListenPort:         envOr("AILS_PORT", "8090"),
		JWTSecret:          []byte(os.Getenv("AILS_JWT_SECRET")),
		TokenTTL:           envDurOr("AILS_TOKEN_TTL", 24*time.Hour),
		SlurmRESTDURL:      envOr("SLURMRESTD_URL", "http://192.168.20.226:6820"),
		SlurmUserName:      envOr("AILS_SLURM_USER", "hpcuser"),
		UsersFile:          envOr("AILS_USERS_FILE", "config/users.yaml"),
		DeployHost:         envOr("AILS_DEPLOY_HOST", "192.168.20.226"),
		ContainerJWTSecret: []byte(os.Getenv("AILS_CONTAINER_JWT_SECRET")),
	}
	if len(cfg.JWTSecret) == 0 {
		return nil, errors.New("AILS_JWT_SECRET is required (set it to a random >=32-byte string)")
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envDurOr(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
