package auth

// OIDC 授权码流 + PKCE 客户端（S1，纯 stdlib 实现——依赖面与部署体积最小化）。
//
// 流程：/auth/oidc/login 生成 state+code_verifier 并 302 到 IdP authorize 端点 →
// IdP 回跳 /auth/oidc/callback?code&state → 用 code+verifier 换 token → 验签 ID token
// （JWKS，RS*/ES*/PS*/EdDSA）→ 取 sub/preferred_username/roles 映射本地身份。

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OIDCConfig 是 SSO 提供方配置（env 驱动；Issuer 为空 = 功能整体禁用）。
type OIDCConfig struct {
	Issuer       string // AILS_OIDC_ISSUER，如 https://sso.example.com/realms/hpc
	ClientID     string // AILS_OIDC_CLIENT_ID
	ClientSecret string // AILS_OIDC_CLIENT_SECRET
	RedirectURL  string // AILS_OIDC_REDIRECT，如 https://portal.example.com/api/v1/auth/oidc/callback
}

// Enabled 判定 OIDC 是否可用（Issuer/ClientID/Redirect 三者齐备才启用）。
func (c OIDCConfig) Enabled() bool {
	return c.Issuer != "" && c.ClientID != "" && c.RedirectURL != ""
}

// discovery 是 /.well-known/openid-configuration 的关心字段。
type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// OIDCClient 封装与 IdP 的交互（discovery 懒加载并缓存；JWKS 按 kid 缓存）。
type OIDCClient struct {
	cfg OIDCConfig
	hc  *http.Client

	mu         sync.Mutex
	disc       *discovery
	jwksCache  map[string]crypto.PublicKey
	jwksFetched time.Time
}

// NewOIDCClient 构造（cfg.Enabled()==false 时仍可构造，调用方负责门面）。
func NewOIDCClient(cfg OIDCConfig) *OIDCClient {
	return &OIDCClient{
		cfg:  cfg,
		hc:   &http.Client{Timeout: 10 * time.Second},
		jwksCache: map[string]crypto.PublicKey{},
	}
}

// Config 返回提供方配置（handler 判定启用与拼回调用）。
func (c *OIDCClient) Config() OIDCConfig { return c.cfg }

var oidcClient *OIDCClient

// SetOIDCClient 注入进程级 OIDC 客户端（main 装配；nil=禁用）。
func SetOIDCClient(cl *OIDCClient) { oidcClient = cl }

// OIDCEnabled 报告 SSO 是否启用（handler 门面）。
func OIDCEnabled() bool { return oidcClient != nil && oidcClient.cfg.Enabled() }

// discover 拉取并缓存 IdP 元数据（含 issuer 一致性校验——防元数据被指向他域）。
func (c *OIDCClient) discover() (*discovery, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disc != nil {
		return c.disc, nil
	}
	wellKnown := strings.TrimSuffix(c.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	resp, err := c.hc.Get(wellKnown)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc: discovery: HTTP %d", resp.StatusCode)
	}
	var d discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return nil, fmt.Errorf("oidc: discovery decode: %w", err)
	}
	// issuer 必须与配置一致（OIDC Core §4.3——元数据可信锚）
	if d.Issuer != c.cfg.Issuer {
		return nil, fmt.Errorf("oidc: discovery issuer mismatch: got %q want %q", d.Issuer, c.cfg.Issuer)
	}
	c.disc = &d
	return c.disc, nil
}

// AuthCodeURL 拼带 state 与 PKCE(S256) 的 authorize URL。
func (c *OIDCClient) AuthCodeURL(state, codeChallenge string) (string, error) {
	d, err := c.discover()
	if err != nil {
		return "", err
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.cfg.ClientID},
		"redirect_uri":          {c.cfg.RedirectURL},
		"scope":                 {"openid profile email"},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return d.AuthorizationEndpoint + "?" + q.Encode(), nil
}

// TokenResponse 是 token 端点响应（只关心 id_token/access_token）。
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// ExchangeCode 用授权码 + PKCE verifier 换 token。
func (c *OIDCClient) ExchangeCode(code, codeVerifier string) (*TokenResponse, error) {
	d, err := c.discover()
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.cfg.RedirectURL},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"code_verifier": {codeVerifier},
	}
	resp, err := c.hc.PostForm(d.TokenEndpoint, form)
	if err != nil {
		return nil, fmt.Errorf("oidc: token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc: token exchange: HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}
	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("oidc: token decode: %w", err)
	}
	if tr.IDToken == "" {
		return nil, errors.New("oidc: token response missing id_token")
	}
	return &tr, nil
}

// IDTokenClaims 是验签后的 ID token 载荷（标准 claim + S2 的角色/组 claim）。
type IDTokenClaims struct {
	Sub                string   `json:"sub"`
	PreferredUsername  string   `json:"preferred_username"`
	Email              string   `json:"email"`
	Name               string   `json:"name"`
	Aud                audClaim `json:"aud"`
	Iss                string   `json:"iss"`
	Exp                int64    `json:"exp"`
	RawRoles           []string `json:"-"` // 由 RolesClaim 解析（见 ParseRoles）
	raw                map[string]json.RawMessage
}

// audClaim 兼容 aud 的字符串与数组两种形态（OIDC Core 允许两者）。
type audClaim []string

func (a *audClaim) UnmarshalJSON(b []byte) error {
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		*a = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*a = []string{s}
		return nil
	}
	return errors.New("oidc: aud claim is neither string nor array")
}

// VerifyIDToken 验签并校验 ID token（签名 via JWKS、iss、aud、exp），返回载荷。
func (c *OIDCClient) VerifyIDToken(idToken string) (*IDTokenClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("oidc: id_token: invalid format")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("oidc: id_token header: %w", err)
	}
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return nil, fmt.Errorf("oidc: id_token header decode: %w", err)
	}
	// 不支持对称签名（HS*）——客户端不应共享 IdP 私钥，JWKS 公钥签名才可信
	if strings.HasPrefix(hdr.Alg, "HS") || hdr.Alg == "none" {
		return nil, fmt.Errorf("oidc: id_token alg %q not accepted (asymmetric required)", hdr.Alg)
	}

	pub, err := c.jwksKey(hdr.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("oidc: id_token signature: %w", err)
	}
	signingInput := parts[0] + "." + parts[1]
	if err := verifyAsymmetric(pub, hdr.Alg, []byte(signingInput), sig); err != nil {
		return nil, fmt.Errorf("oidc: id_token signature verify: %w", err)
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("oidc: id_token payload: %w", err)
	}
	var cl IDTokenClaims
	if err := json.Unmarshal(payload, &cl); err != nil {
		return nil, fmt.Errorf("oidc: id_token claims decode: %w", err)
	}
	_ = json.Unmarshal(payload, &cl.raw)

	if cl.Iss != c.cfg.Issuer {
		return nil, fmt.Errorf("oidc: id_token iss mismatch: got %q", cl.Iss)
	}
	if time.Now().Unix() >= cl.Exp {
		return nil, errors.New("oidc: id_token expired")
	}
	audOK := false
	for _, a := range cl.Aud { // audClaim 已归一化 string|array → []string
		if a == c.cfg.ClientID {
			audOK = true
			break
		}
	}
	if !audOK {
		return nil, errors.New("oidc: id_token aud does not contain client_id")
	}
	if cl.Sub == "" {
		return nil, errors.New("oidc: id_token missing sub")
	}
	return &cl, nil
}

// ParseRoles 从 ID token 载荷解析角色/组 claim（rolesClaim 如 "roles" / "groups"；
// 兼容数组与逗号分隔字符串两种形态）。
func (cl *IDTokenClaims) ParseRoles(rolesClaim string) []string {
	if rolesClaim == "" {
		rolesClaim = "roles"
	}
	raw, ok := cl.raw[rolesClaim]
	if !ok {
		return nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil
		}
		out := []string{}
		for _, part := range strings.Split(s, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	// 数组内元素非字符串（如对象数组）——尽力提取 name/value 字段
	var objs []map[string]any
	if err := json.Unmarshal(raw, &objs); err == nil {
		out := []string{}
		for _, o := range objs {
			for _, k := range []string{"name", "value", "id"} {
				if v, ok := o[k].(string); ok && v != "" {
					out = append(out, v)
					break
				}
			}
		}
		return out
	}
	return nil
}

// --- PKCE ---

// NewPKCE 生成 (verifier, challenge)。verifier 43 字符（base64url(32B)），S256 challenge。
func NewPKCE() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	challenge = PKCEChallenge(verifier)
	return verifier, challenge, nil
}

// PKCEChallenge 计算 S256 code_challenge。
func PKCEChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// NewState 生成加密随机 state（16B hex）。
func NewState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}

// --- JWKS ---

type jwksDoc struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Crv string `json:"crv"`
}

// jwksKey 按 kid 取公钥（缓存 15 分钟；未知 kid 触发一次刷新后重试）。
func (c *OIDCClient) jwksKey(kid string) (crypto.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fetch := func() error {
		d, err := c.discoverLocked()
		if err != nil {
			return err
		}
		if d.JWKSURI == "" {
			return errors.New("oidc: discovery missing jwks_uri")
		}
		resp, err := c.hc.Get(d.JWKSURI)
		if err != nil {
			return fmt.Errorf("oidc: jwks fetch: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("oidc: jwks fetch: HTTP %d", resp.StatusCode)
		}
		var doc jwksDoc
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
			return fmt.Errorf("oidc: jwks decode: %w", err)
		}
		c.jwksCache = map[string]crypto.PublicKey{}
		c.jwksFetched = time.Now()
		for _, k := range doc.Keys {
			if pk, err := k.publicKey(); err == nil {
				c.jwksCache[k.Kid] = pk
			}
		}
		return nil
	}

	if key, ok := c.jwksCache[kid]; ok && time.Since(c.jwksFetched) < 15*time.Minute {
		return key, nil
	}
	if err := fetch(); err != nil {
		return nil, err
	}
	if key, ok := c.jwksCache[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("oidc: jwks: kid %q not found", kid)
}

// discoverLocked 在已持锁上下文里做 discovery（jwksKey 调用路径复用锁内逻辑）。
func (c *OIDCClient) discoverLocked() (*discovery, error) {
	if c.disc != nil {
		return c.disc, nil
	}
	wellKnown := strings.TrimSuffix(c.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	resp, err := c.hc.Get(wellKnown)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc: discovery: HTTP %d", resp.StatusCode)
	}
	var d discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return nil, fmt.Errorf("oidc: discovery decode: %w", err)
	}
	if d.Issuer != c.cfg.Issuer {
		return nil, fmt.Errorf("oidc: discovery issuer mismatch: got %q want %q", d.Issuer, c.cfg.Issuer)
	}
	c.disc = &d
	return c.disc, nil
}

// publicKey 把 JWK 换成 crypto.PublicKey（RSA / EC / OKP）。
func (k *jwk) publicKey() (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		nB, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, err
		}
		eB, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, err
		}
		e := 0
		for _, b := range eB {
			e = e<<8 | int(b)
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nB), E: e}, nil
	case "EC":
		xB, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		yB, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, err
		}
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("oidc: unsupported EC curve %q", k.Crv)
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(xB), Y: new(big.Int).SetBytes(yB)}, nil
	case "OKP":
		kb, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		if k.Crv != "Ed25519" {
			return nil, fmt.Errorf("oidc: unsupported OKP curve %q", k.Crv)
		}
		return ed25519.PublicKey(kb), nil
	default:
		return nil, fmt.Errorf("oidc: unsupported jwk kty %q", k.Kty)
	}
}

// verifyAsymmetric 按 alg 验签。
func verifyAsymmetric(pub crypto.PublicKey, alg string, msg, sig []byte) error {
	var hash crypto.Hash
	switch {
	case strings.HasPrefix(alg, "RS"), strings.HasPrefix(alg, "PS"):
		hash = hashForAlg(alg)
	case strings.HasPrefix(alg, "ES"):
		hash = hashForAlg(alg)
	default:
		if alg == "EdDSA" {
			if ed, ok := pub.(ed25519.PublicKey); ok {
				if ed25519.Verify(ed, msg, sig) {
					return nil
				}
				return errors.New("ed25519 mismatch")
			}
			return errors.New("pubkey not ed25519")
		}
		return fmt.Errorf("oidc: unsupported alg %q", alg)
	}
	digest := hash.New()
	digest.Write(msg)

	switch p := pub.(type) {
	case *rsa.PublicKey:
		if strings.HasPrefix(alg, "PS") {
			return rsa.VerifyPSS(p, hash, digest.Sum(nil), sig, nil)
		}
		return rsa.VerifyPKCS1v15(p, hash, digest.Sum(nil), sig)
	case *ecdsa.PublicKey:
		// JOSE ES* 签名是 raw r||s（各半签名长度）
		size := (p.Curve.Params().BitSize + 7) / 8
		if len(sig) != 2*size {
			return fmt.Errorf("ecdsa sig length %d, want %d", len(sig), 2*size)
		}
		r := new(big.Int).SetBytes(sig[:size])
		s := new(big.Int).SetBytes(sig[size:])
		if ecdsa.Verify(p, digest.Sum(nil), r, s) {
			return nil
		}
		return errors.New("ecdsa mismatch")
	default:
		return errors.New("pubkey type not usable for alg")
	}
}

func hashForAlg(alg string) crypto.Hash {
	switch {
	case strings.HasSuffix(alg, "256"):
		return crypto.SHA256
	case strings.HasSuffix(alg, "384"):
		return crypto.SHA384
	case strings.HasSuffix(alg, "512"):
		return crypto.SHA512
	}
	return crypto.SHA256
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
