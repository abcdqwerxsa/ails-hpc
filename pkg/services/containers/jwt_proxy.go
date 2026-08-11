package containers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// fallbackJWTSecret 仅在未通过 SetContainerJWTSecret 注入时的兜底密钥。
// 生产环境应通过 AILS_CONTAINER_JWT_SECRET 注入独立密钥。
var fallbackJWTSecret = []byte("ails-hpc-containers-jwt-secret-key-2026")

// containerJWTSecret 由 cmd/apiserver 启动时注入（源自 AILS_CONTAINER_JWT_SECRET）。
var containerJWTSecret []byte

// deployHost 容器 IDE 入口 URL 的主机（由 AILS_DEPLOY_HOST 注入；历史上硬编码为 192.168.20.226）。
var deployHost = "192.168.20.226"

// SetContainerJWTSecret 注入容器代理令牌签名密钥（仅正数长度生效）。
func SetContainerJWTSecret(b []byte) {
	if len(b) > 0 {
		containerJWTSecret = make([]byte, len(b))
		copy(containerJWTSecret, b)
	}
}

// SetDeployHost 注入容器 IDE 入口 URL 的主机（空串忽略）。
func SetDeployHost(host string) {
	if h := strings.TrimSpace(host); h != "" {
		deployHost = h
	}
}

type JWTClaims struct {
	ContainerID string `json:"cid"`
	EnvType     string `json:"env"`
	Exp         int64  `json:"exp"`
}

func resolveSecret(secret []byte) []byte {
	if len(secret) > 0 {
		return secret
	}
	if len(containerJWTSecret) > 0 {
		return containerJWTSecret
	}
	return fallbackJWTSecret
}

// GenerateJWTToken 为容器鉴权代理生成签名 JWT-like 令牌。
func GenerateJWTToken(containerID, envType string, secret []byte) (string, error) {
	secret = resolveSecret(secret)

	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	headerB64 := base64.RawURLEncoding.EncodeToString(headerBytes)

	claims := JWTClaims{
		ContainerID: containerID,
		EnvType:     envType,
		Exp:         time.Now().Add(24 * time.Hour).Unix(),
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsBytes)

	unsignedToken := fmt.Sprintf("%s.%s", headerB64, claimsB64)

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(unsignedToken))
	signature := mac.Sum(nil)
	signatureB64 := base64.RawURLEncoding.EncodeToString(signature)

	return fmt.Sprintf("%s.%s", unsignedToken, signatureB64), nil
}

// BuildWebURL 返回带 JWT 鉴权 token 与 cpus 查询参数的完整入口 URL。
// 主机取自配置的 deployHost（原为硬编码 192.168.20.226）。
func BuildWebURL(envType, token string, cpus int) string {
	host := deployHost
	if host == "" {
		host = "192.168.20.226"
	}
	if envType == "vscode" {
		return fmt.Sprintf("http://%s:8080/vscode/?token=%s&cpus=%d", host, token, cpus)
	}
	return fmt.Sprintf("http://%s:8888/lab?token=%s&cpus=%d", host, token, cpus)
}
