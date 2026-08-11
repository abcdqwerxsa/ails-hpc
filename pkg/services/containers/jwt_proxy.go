package containers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

var defaultJWTSecret = []byte("ails-hpc-containers-jwt-secret-key-2026")

type JWTClaims struct {
	ContainerID string `json:"cid"`
	EnvType     string `json:"env"`
	Exp         int64  `json:"exp"`
}

// GenerateJWTToken generates a signed JWT-like token for container auth proxy
func GenerateJWTToken(containerID, envType string, secret []byte) (string, error) {
	if len(secret) == 0 {
		secret = defaultJWTSecret
	}

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

// BuildWebURL returns the complete access URL with JWT auth token and cpus query param
func BuildWebURL(envType, token string, cpus int) string {
	baseIP := "192.168.20.226"
	if envType == "vscode" {
		return fmt.Sprintf("http://%s:8080/vscode/?token=%s&cpus=%d", baseIP, token, cpus)
	}
	return fmt.Sprintf("http://%s:8888/lab?token=%s&cpus=%d", baseIP, token, cpus)
}
