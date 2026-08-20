package auth

// T1 个人 API token：长期可撤销凭证，供脚本/cron 调面板 API（免用户密码登录）。
// 令牌形态 `ailspat_<43 字符 base64url>`（32 字节 crypto/rand）；库内只存
// sha256 hex（本包定义窄接口防 import 环——sqlite 实现在 pkg/store/tokens.go）。
// 明文仅签发响应出现一次；吊销/过期/禁用/挂起租户即刻失效（活体校验同 JWT）。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrTokenQuota 单用户活跃令牌配额满（store 层同义错误的映射锚）。
var ErrTokenQuota = errors.New("auth: token quota exceeded")

// PATPrefix 令牌明文前缀（区分 JWT：JWT 恒为三段点分；PAT 恒有此前缀）。
const PATPrefix = "ailspat_"

// PATDisplayPrefixLen 明文回显前缀长度（核对用，如 "ailspat_Ab3kF…"）。
const PATDisplayPrefixLen = 14

// PATRecord 中间件按哈希直查所需的行。
type PATRecord struct {
	ID         int64
	Username   string
	Revoked    bool
	ExpiresAt  string // SQLite UTC "2006-01-02 15:04:05"；空=长期
	LastUsedAt string // 空=从未使用
}

// PATInfo 令牌的安全投影（列表/签发响应共用；永不包含哈希与明文）。
type PATInfo struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	CreatedAt  string `json:"createdAt"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	ExpiresAt  string `json:"expiresAt,omitempty"`
	Revoked    bool   `json:"revoked"`
}

// PATStore 是认证路径的查取面（中间件用；sqlite 实现满足，yaml/内存库不满足 → 503）。
type PATStore interface {
	LookupAPIToken(tokenHash string) (PATRecord, error)
	TouchAPIToken(id int64) error
}

// PATManager 在查取面之上加自助管理面（签发/列表/吊销；auth handler 用）。
type PATManager interface {
	PATStore
	CreateAPIToken(ctx context.Context, username, name, tokenHash, prefix, expiresAt string) (int64, error)
	ListAPITokens(ctx context.Context, username string) ([]PATInfo, error)
	RevokeAPIToken(ctx context.Context, username string, id int64) error
}

// patExpiryLayout 与 store 侧 Touch/查询一致的 SQLite UTC 时间格式。
const patExpiryLayout = "2006-01-02 15:04:05"

// PATExpired 判断令牌是否已过期（空=长期；解析失败按过期处理——fail-closed）。
func PATExpired(expiresAt string) bool {
	if expiresAt == "" {
		return false
	}
	t, err := time.Parse(patExpiryLayout, expiresAt)
	if err != nil {
		return true
	}
	return time.Now().UTC().After(t)
}

// GeneratePAT 生成 (明文令牌, sha256 hex 哈希, 展示前缀)。
func GeneratePAT() (token, hash, prefix string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", "", err
	}
	token = PATPrefix + base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	hash = hex.EncodeToString(sum[:])
	return token, hash, token[:min(len(token), PATDisplayPrefixLen)], nil
}

// PATHash 令牌明文 → sha256 hex（中间件查取键）。
func PATHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SanitizePATName 令牌标签清洗：去空白、截 64 字符、剔除控制字符；空 → 默认名。
func SanitizePATName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if len(name) > 64 {
		name = name[:64]
	}
	if name == "" {
		return "api-token"
	}
	return name
}

// --- last_used_at 节流（每进程内存；≥5 分钟才落一次库，避免热路径逐请求写） ---

var patTouch struct {
	sync.Mutex
	last map[int64]time.Time
}

// patTouchDue 记录并判断是否到达节流窗口（true=调用方应 Touch）。
func patTouchDue(id int64) bool {
	patTouch.Lock()
	defer patTouch.Unlock()
	if patTouch.last == nil {
		patTouch.last = map[int64]time.Time{}
	}
	if t, ok := patTouch.last[id]; ok && time.Since(t) < 5*time.Minute {
		return false
	}
	patTouch.last[id] = time.Now()
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
