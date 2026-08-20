// Package config 集中解析进程启动所需的运行时配置（环境变量驱动，带安全默认值）。
// 取代此前散落在 cmd/apiserver 与各 service 里的硬编码 IP / 密钥 / 路径。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ails-hpc/pkg/auth"
)

// Config 持有进程启动所需的全部运行时参数。
type Config struct {
	ListenPort    string        // AILS_PORT，默认 "8090"（可被 -port flag 覆盖）
	JWTSecret     []byte        // AILS_JWT_SECRET，必填（空则 Load 报错，fail-closed）
	TokenTTL      time.Duration // AILS_TOKEN_TTL，默认 24h
	SlurmRESTDURL string        // SLURMRESTD_URL，默认 "http://192.168.20.226:6820"
	SlurmUserName string        // AILS_SLURM_USER，默认 "hpcuser"
	UsersFile     string        // AILS_USERS_FILE，默认 "config/users.yaml"
	UserStoreKind string        // AILS_USER_STORE，"yaml"|"db"（多租户 Phase 1 双模，默认 yaml）
	CookieSecure  bool          // AILS_COOKIE_SECURE=1——TLS 部署后给 IDE cookie 加 Secure（P2）
	DBPath        string        // AILS_DB_PATH，sqlite 用户库路径（UserStoreKind=db 时生效）

	// OIDC SSO（S1/S2；Issuer 空 = 功能整体禁用，本地密码登录不受影响）
	OIDC auth.OIDCConfig
	// OIDCMapping claim→角色/租户映射（S2）
	OIDCMapping auth.OIDCMappingConfig
	// OIDCPortalURL 回跳前端基址（默认 /portal/）
	OIDCPortalURL string
}

// Load 从环境变量读取配置。
//
// JWTSecret 为空时返回错误 —— fail-closed：拒绝在无签名密钥的状态下启动，
// 以免签发出可被伪造的令牌。
func Load() (*Config, error) {
	cfg := &Config{
		ListenPort:    envOr("AILS_PORT", "8090"),
		JWTSecret:     []byte(os.Getenv("AILS_JWT_SECRET")),
		TokenTTL:      envDurOr("AILS_TOKEN_TTL", 24*time.Hour),
		SlurmRESTDURL: envOr("SLURMRESTD_URL", "http://192.168.20.226:6820"),
		SlurmUserName: envOr("AILS_SLURM_USER", "hpcuser"),
		UsersFile:     envOr("AILS_USERS_FILE", "config/users.yaml"),

		DBPath: envOr("AILS_DB_PATH", "var/lib/ails/ails.db"),

		OIDC: auth.OIDCConfig{
			Issuer:       envOr("AILS_OIDC_ISSUER", ""),
			ClientID:     envOr("AILS_OIDC_CLIENT_ID", ""),
			ClientSecret: os.Getenv("AILS_OIDC_CLIENT_SECRET"),
			RedirectURL:  envOr("AILS_OIDC_REDIRECT", ""),
		},
		OIDCPortalURL: envOr("AILS_OIDC_PORTAL_URL", "/portal/"),
		CookieSecure:  os.Getenv("AILS_COOKIE_SECURE") == "1",
	}

	if err := parseOIDCMapping(cfg); err != nil {
		return nil, err
	}

	// P2（安全审计 2026-08-19）：最小长度强制——此前只查非空，短 secret 直接降低
	// HS256 暴力破解成本。生产 .env 为 openssl rand -hex 32（64 字符）不受影响。
	if len(cfg.JWTSecret) < 32 {
		return nil, errors.New("AILS_JWT_SECRET must be at least 32 bytes (use: openssl rand -hex 32)")
	}
	return cfg, nil
}

// parseOIDCMapping 解析 S2 映射 env：
//
//	AILS_OIDC_ROLES_CLAIM   解析角色/组的 claim 名（默认 "roles"）
//	AILS_OIDC_TENANT_CLAIM  解析租户的 claim 名（默认同 roles claim）
//	AILS_OIDC_ROLE_MAP      JSON：claim 值 → 角色名（如 {"hpc-admin":"admin","dev":"dev"}）
//	AILS_OIDC_TENANT_MAP    JSON：claim 值 → 租户 slug（如 {"lab-a":"hpc-lab"}）
//	AILS_OIDC_UNMAPPED      deny（默认，未命中映射即拒绝）| default（用 DEFAULT_* 兜底）
//	AILS_OIDC_DEFAULT_ROLE  default 策略角色（默认 member）
//	AILS_OIDC_DEFAULT_TENANT default 策略租户 slug（空 = JIT 不可用）
func parseOIDCMapping(cfg *Config) error {
	m := auth.OIDCMappingConfig{
		RolesClaim:     envOr("AILS_OIDC_ROLES_CLAIM", "roles"),
		TenantClaim:    envOr("AILS_OIDC_TENANT_CLAIM", ""),
		UnmappedPolicy: envOr("AILS_OIDC_UNMAPPED", "deny"),
		DefaultRole:    envOr("AILS_OIDC_DEFAULT_ROLE", auth.RoleMember),
		DefaultTenant:  envOr("AILS_OIDC_DEFAULT_TENANT", ""),
	}
	var err error
	if m.RoleMap, err = parseJSONMap("AILS_OIDC_ROLE_MAP"); err != nil {
		return err
	}
	if m.TenantMap, err = parseJSONMap("AILS_OIDC_TENANT_MAP"); err != nil {
		return err
	}
	if m.UnmappedPolicy != "deny" && m.UnmappedPolicy != "default" {
		return fmt.Errorf("AILS_OIDC_UNMAPPED must be deny|default, got %q", m.UnmappedPolicy)
	}
	if m.TenantClaim == "" {
		m.TenantClaim = m.RolesClaim
	}
	cfg.OIDCMapping = m
	return nil
}

// parseJSONMap 解析 env 里的 JSON 字符串映射（空 → nil）。
func parseJSONMap(key string) (map[string]string, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil, nil
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON map: %w", key, err)
	}
	return out, nil
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
