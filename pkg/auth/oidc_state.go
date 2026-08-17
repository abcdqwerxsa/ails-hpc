package auth

// OIDC 会话状态存储（S1）与账号关联令牌（S4）。
//
// stateStore：授权码流的 state ↔ PKCE verifier 映射，10 分钟 TTL、严格一次性
// （回调取出即删——重放/CSRF 直接失败）。进程内单实例存储（apiserver 单写者部署形态）。

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// oidcSession 是一次 /auth/oidc/login 发起的待完成流程。
type oidcSession struct {
	verifier  string
	issuedAt  time.Time
	// bindUsername 非空 = 账号关联流程（S4：已登录用户绑定 OIDC sub）
	bindUsername string
}

type stateStore struct {
	mu       sync.Mutex
	sessions map[string]*oidcSession
}

var oidcStates = &stateStore{sessions: map[string]*oidcSession{}}

const oidcStateTTL = 10 * time.Minute

// PutState 登记 state（返回前清理过期项）。
func PutState(state string, sess *oidcSession) {
	oidcStates.mu.Lock()
	defer oidcStates.mu.Unlock()
	now := time.Now()
	for k, v := range oidcStates.sessions {
		if now.Sub(v.issuedAt) > oidcStateTTL {
			delete(oidcStates.sessions, k)
		}
	}
	if sess.issuedAt.IsZero() {
		sess.issuedAt = now
	}
	oidcStates.sessions[state] = sess
}

// TakeState 取出并删除（一次性）。不存在/过期 → ok=false。
func TakeState(state string) (*oidcSession, bool) {
	oidcStates.mu.Lock()
	defer oidcStates.mu.Unlock()
	s, ok := oidcStates.sessions[state]
	if ok {
		delete(oidcStates.sessions, state)
	}
	if !ok || time.Since(s.issuedAt) > oidcStateTTL {
		return nil, false
	}
	return s, true
}

// BindSession 构造账号关联用途的会话（S4）。
func BindSession(username string) *oidcSession {
	return &oidcSession{bindUsername: username}
}

// IsBind 报告会话是否为账号关联流程。
func (s *oidcSession) IsBind() bool  { return s != nil && s.bindUsername != "" }
func (s *oidcSession) BindUser() string { return s.bindUsername }

// --- 关联令牌（S4 撞名确认流） ---
//
// SSO 首登撞本地用户名时，回跳前端携带 linkToken（10 分钟有效、单次使用语义由
// 完成端点强制——绑定即失效）。令牌为 HMAC(jwtSecret) 签名的小载荷：
// {sub, username, exp}。持有它 + 本地密码正确 → 绑定 oidc_sub 并签发门户 JWT。

// ErrLinkTokenInvalid 关联令牌无效/过期/验签失败。
var ErrLinkTokenInvalid = errors.New("auth: invalid or expired link token")

type linkTokenClaims struct {
	Sub      string `json:"sub"`
	Username string `json:"username"`
	Exp      int64  `json:"exp"`
}

// MintLinkToken 为撞名确认流铸造一次性关联令牌。
func MintLinkToken(sub, username string) (string, error) {
	if len(jwtSecret) == 0 {
		return "", errors.New("jwt secret not configured")
	}
	buf, _ := json.Marshal(linkTokenClaims{
		Sub: sub, Username: username, Exp: time.Now().Add(10 * time.Minute).Unix(),
	})
	return signHMAC(buf), nil
}

// VerifyLinkToken 验证关联令牌，返回 (sub, username)。
func VerifyLinkToken(token string) (string, string, error) {
	if len(jwtSecret) == 0 {
		return "", "", errors.New("jwt secret not configured")
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", "", ErrLinkTokenInvalid
	}
	if len(raw) < sha256.Size {
		return "", "", ErrLinkTokenInvalid
	}
	body, mac := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	h := hmac.New(sha256.New, jwtSecret)
	h.Write(body)
	if !hmac.Equal(mac, h.Sum(nil)) {
		return "", "", ErrLinkTokenInvalid
	}
	var cl linkTokenClaims
	if err := json.Unmarshal(body, &cl); err != nil {
		return "", "", ErrLinkTokenInvalid
	}
	if time.Now().Unix() >= cl.Exp || cl.Sub == "" || cl.Username == "" {
		return "", "", ErrLinkTokenInvalid
	}
	return cl.Sub, cl.Username, nil
}

// signHMAC 载荷+尾部 MAC，整体 base64url。
func signHMAC(payload []byte) string {
	h := hmac.New(sha256.New, jwtSecret)
	h.Write(payload)
	return base64.RawURLEncoding.EncodeToString(append(payload, h.Sum(nil)...))
}
