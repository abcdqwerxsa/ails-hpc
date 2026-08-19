package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// jwtSecret 由 cmd/apiserver 在启动时通过 SetSecret 注入（源自 AILS_JWT_SECRET）。
// 留空即视为未配置：GenerateToken/VerifyToken 会返回错误，杜绝在无密钥状态下
// 签发或信任令牌。
var jwtSecret []byte

// tokenTTL access token 生命周期，默认 24h；可由 SetTokenTTL 覆盖（Phase D 从 config 注入）。
var tokenTTL = 24 * time.Hour

const (
	jwtIssuer   = "ails-hpc"
	jwtAudience = "ails-hpc"
)

// SetSecret 注入 JWT 签名密钥（HS256）。必须在服务启动、签发或校验任何令牌前调用一次。
func SetSecret(b []byte) {
	jwtSecret = make([]byte, len(b))
	copy(jwtSecret, b)
}

// SetTokenTTL 覆盖默认 token 生命周期（仅正数生效）。
func SetTokenTTL(d time.Duration) {
	if d > 0 {
		tokenTTL = d
	}
}

// Claims 描述 access token 的载荷。Role 为权威角色（admin/ops_admin/tenant_admin/member）。
// ClusterUser/Account 携带 Slurm 集群身份（L1+L3 隔离）：apiserver 提交作业时按 ClusterUser
// 铸造 slurmrestd JWT、按 Account 写入 Slurm account，使作业以该真实 unix 身份运行。
type Claims struct {
	Username    string `json:"username"`
	Role        string `json:"role"` // admin / ops_admin / tenant_admin / member
	OrgSlug     string `json:"orgSlug"`
	TenantNS    string `json:"tenantNs"`
	ClusterUser string `json:"clusterUser"`
	Account     string `json:"account"`
	// TID 租户 slug（多租户 Phase 2 起签发；空=迁移期旧 token，scope 回退 OrgSlug）
	TID string `json:"tid,omitempty"`
	// Ver token_version：签发时用户库中的版本号。改密/禁用会 bump，中间件按请求比对，
	// 使在途 token 即刻失效（不必等 24h TTL）。旧 token 无此字段=0。
	Ver int `json:"ver,omitempty"`
	// Rid 实际角色 id（R2 角色表化起签发；0=旧令牌，中间件按用户库刷新）。
	Rid int64 `json:"rid,omitempty"`
	// Rn 实际角色名（自定义角色 ≠ Role；空=内置角色）。带 store 的中间件每请求按库刷新。
	Rn string `json:"rn,omitempty"`
	// Perms 权限点快照（签发时来自角色表；带 store 的中间件每请求按库刷新——角色
	// 权限调整即刻生效）。空 = 回退 BuiltinRolePermissions[Role]（旧令牌/内存库）。
	Perms []string `json:"perms,omitempty"`
	Iss   string   `json:"iss"`
	Aud   string   `json:"aud"`
	Exp   int64    `json:"exp"`
}

// GenerateToken 用当前 tokenTTL 签发一个新的 access token（兼容包装，不带 tid/ver）。
func GenerateToken(username, role, orgSlug, tenantNs, clusterUser, account string) (string, error) {
	return GenerateTokenWithTTL(username, role, orgSlug, tenantNs, clusterUser, account, tokenTTL)
}

// GenerateTokenClaims 以完整 Claims 签发（Phase 2 起 Login 用：携带 tid/ver）。
// Exp 由本函数按当前 tokenTTL 填充，其余字段原样签入。
func GenerateTokenClaims(cl Claims) (string, error) {
	cl.Iss = jwtIssuer
	cl.Aud = jwtAudience
	cl.Exp = time.Now().Add(tokenTTL).Unix()
	return signClaims(cl)
}

// signClaims 序列化+签名（GenerateTokenWithTTL 与 GenerateTokenClaims 共用）。
func signClaims(cl Claims) (string, error) {
	if len(jwtSecret) == 0 {
		return "", errors.New("jwt secret not configured")
	}
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsJSON, _ := json.Marshal(cl)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	unsigned := fmt.Sprintf("%s.%s", headerB64, claimsB64)
	h := hmac.New(sha256.New, jwtSecret)
	h.Write([]byte(unsigned))
	return fmt.Sprintf("%s.%s", unsigned, base64.RawURLEncoding.EncodeToString(h.Sum(nil))), nil
}

// GenerateTokenWithTTL 用显式 TTL 签发 access token（测试用于构造过期/将过期令牌）。
func GenerateTokenWithTTL(username, role, orgSlug, tenantNs, clusterUser, account string, ttl time.Duration) (string, error) {
	if len(jwtSecret) == 0 {
		return "", errors.New("jwt secret not configured")
	}

	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	claims := Claims{
		Username:    username,
		Role:        role,
		OrgSlug:     orgSlug,
		TenantNS:    tenantNs,
		ClusterUser: clusterUser,
		Account:     account,
		Iss:         jwtIssuer,
		Aud:         jwtAudience,
		Exp:         time.Now().Add(ttl).Unix(),
	}
	claimsJSON, _ := json.Marshal(claims)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	unsignedToken := fmt.Sprintf("%s.%s", headerB64, claimsB64)
	h := hmac.New(sha256.New, jwtSecret)
	h.Write([]byte(unsignedToken))
	signatureB64 := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	return fmt.Sprintf("%s.%s", unsignedToken, signatureB64), nil
}

// VerifyToken 校验签名、过期时间与签发方/受众，返回 Claims。
func VerifyToken(tokenStr string) (*Claims, error) {
	if len(jwtSecret) == 0 {
		return nil, errors.New("jwt secret not configured")
	}

	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token format")
	}

	unsignedToken := fmt.Sprintf("%s.%s", parts[0], parts[1])
	h := hmac.New(sha256.New, jwtSecret)
	h.Write([]byte(unsignedToken))
	expectedSig := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	// P2（安全审计 2026-08-19）：恒时比较（防时序侧信道逐字节探签名；对齐 oidc_state
	// 的 hmac.Equal 用法）。
	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return nil, errors.New("invalid signature")
	}

	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}

	var claims Claims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return nil, err
	}

	if time.Now().Unix() > claims.Exp {
		return nil, errors.New("token expired")
	}

	// 校验签发方/受众，防止跨服务令牌混用（旧令牌无 iss/aud 时放行）
	if claims.Iss != "" && claims.Iss != jwtIssuer {
		return nil, errors.New("invalid token issuer")
	}
	if claims.Aud != "" && claims.Aud != jwtAudience {
		return nil, errors.New("invalid token audience")
	}

	return &claims, nil
}
