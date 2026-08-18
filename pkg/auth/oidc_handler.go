package auth

// OIDC HTTP 处理器（S1 授权码流 + S2 claim 映射 + S4 账号关联）。
//
// 端点（前两个公开，后三个需登录）：
//   GET  /auth/oidc/config    —— 前端判断是否显示 SSO 按钮
//   GET  /auth/oidc/login     —— 302 → IdP authorize（state+PKCE；?bind=1 为关联流程）
//   GET  /auth/oidc/callback  —— IdP 回跳：换 token、验签、映射、签发/关联/拒绝
//   POST /auth/oidc/link      —— 撞名确认：linkToken + 本地密码 → 绑定并签发
//   POST /auth/oidc/unlink    —— 解绑当前账号的 OIDC sub

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"ails-hpc/pkg/httpx"

	"github.com/gin-gonic/gin"
)

// OIDCMappingConfig 是 S2 的 claim→角色/租户映射（env 解析后注入）。
type OIDCMappingConfig struct {
	RolesClaim  string            // 解析角色/组的 ID token claim（默认 "roles"）
	TenantClaim string            // 解析租户的 claim（空 = 不按 claim 映射租户）
	RoleMap     map[string]string // claim 值 → 角色名（内置或平台/租户自定义）
	TenantMap   map[string]string // claim 值 → 租户 slug
	// UnmappedPolicy 无映射命中时的处置："deny"（默认，拒绝）/ "default"（用 Default*）
	UnmappedPolicy string
	DefaultRole    string // default 策略下的角色（默认 member）
	DefaultTenant  string // default 策略下的租户 slug（空 = JIT 不可用，拒绝）
}

// OIDCProvisioner 是 SSO 需要的用户库写面（生产由 services/admin.Service 实现：
// 含 Slurm 供给与审计；store 只读装配为 nil → JIT/绑定不可用，仅已绑定账号可 SSO 登录）。
type OIDCProvisioner interface {
	// UserByOIDCSub 按 sub 查已绑定用户。
	UserByOIDCSub(sub string) (*User, bool)
	// LinkOIDC 绑定 sub 到本地账号（撞名确认流/已登录绑定共用）。
	LinkOIDC(username, sub string) error
	// UnlinkOIDC 解绑（auth_source=oidc 的账号拒绝——无本地密码会自锁）。
	UnlinkOIDC(username string) error
	// ProvisionOIDCUser JIT 开户（S2：按映射角色/租户；自定义角色按 base 建后改派）。
	ProvisionOIDCUser(username, email, displayName, roleName, tenantSlug, sub string) (*User, error)
}

// OIDCHandler 承载全部 OIDC 端点。
type OIDCHandler struct {
	store   UserStore
	prov    OIDCProvisioner
	audit   AuditSink
	mapping OIDCMappingConfig
	// PortalURL 回跳前端地址（默认 /portal/，hash 路由 #/login/oidc/callback）
	PortalURL string
}

// NewOIDCHandler 构造。store 必填；prov/audit 可为 nil（降级语义见各端点注释）。
func NewOIDCHandler(store UserStore, prov OIDCProvisioner, mapping OIDCMappingConfig) *OIDCHandler {
	h := &OIDCHandler{store: store, prov: prov, mapping: mapping, PortalURL: "/portal/"}
	if h.mapping.RolesClaim == "" {
		h.mapping.RolesClaim = "roles"
	}
	if h.mapping.UnmappedPolicy == "" {
		h.mapping.UnmappedPolicy = "deny"
	}
	if h.mapping.DefaultRole == "" {
		h.mapping.DefaultRole = RoleMember
	}
	return h
}

// SetAuditSink 注入审计出口（与 AuthHandler 同面）。
func (h *OIDCHandler) SetAuditSink(s AuditSink) { h.audit = s }

// Config GET /api/v1/auth/oidc/config —— 公开端点：前端据此显示 SSO 按钮（S3）。
func (h *OIDCHandler) Config(c *gin.Context) {
	if !OIDCEnabled() {
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": true, "issuer": oidcClient.Config().Issuer})
}

// Login GET /api/v1/auth/oidc/login（公开）—— 302 到 IdP 发起普通 SSO 登录。
func (h *OIDCHandler) Login(c *gin.Context) {
	if !OIDCEnabled() {
		httpx.BadRequest(c, "OIDC is not configured")
		return
	}
	h.beginFlow(c, &oidcSession{})
}

// BindLogin GET /api/v1/auth/oidc/bind（需登录）—— S4 账号关联流程：state 关联当前
// 用户名，回调完成后绑定 sub。返回 JSON {authorizeUrl}（前端认证 XHR 取 URL 后再
// 导航——浏览器普通导航带不上 Authorization 头）。
func (h *OIDCHandler) BindLogin(c *gin.Context) {
	if !OIDCEnabled() {
		httpx.BadRequest(c, "OIDC is not configured")
		return
	}
	cl := ClaimsFromCtx(c)
	if cl == nil {
		httpx.Unauthorized(c, "login required for account binding")
		return
	}
	sess := BindSession(cl.Username)
	state, err := NewState()
	if err != nil {
		httpx.Internal(c, "OIDC.Bind.state", err)
		return
	}
	verifier, challenge, err := NewPKCE()
	if err != nil {
		httpx.Internal(c, "OIDC.Bind.pkce", err)
		return
	}
	sess.verifier = verifier
	PutState(state, sess)
	authURL, err := oidcClient.AuthCodeURL(state, challenge)
	if err != nil {
		httpx.BadGateway(c, "OIDC provider unreachable: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"authorizeUrl": authURL})
}

// beginFlow 生成 state+PKCE 并 302 到 IdP authorize 端点。
func (h *OIDCHandler) beginFlow(c *gin.Context, sess *oidcSession) {
	state, err := NewState()
	if err != nil {
		httpx.Internal(c, "OIDC.Login.state", err)
		return
	}
	verifier, challenge, err := NewPKCE()
	if err != nil {
		httpx.Internal(c, "OIDC.Login.pkce", err)
		return
	}
	sess.verifier = verifier
	PutState(state, sess)
	authURL, err := oidcClient.AuthCodeURL(state, challenge)
	if err != nil {
		httpx.BadGateway(c, "OIDC provider unreachable: "+err.Error())
		return
	}
	c.Redirect(http.StatusFound, authURL)
}

// Callback GET /api/v1/auth/oidc/callback —— IdP 回跳。结果一律 302 回前端
// #/login/oidc/callback?status=...（token 只出现在 URL 片段，不进服务端访问日志）。
func (h *OIDCHandler) Callback(c *gin.Context) {
	if !OIDCEnabled() {
		h.redirectResult(c, "error", "oidc not configured", "")
		return
	}
	if errMsg := c.Query("error"); errMsg != "" {
		h.redirectResult(c, "error", errMsg, "")
		return
	}
	state := c.Query("state")
	code := c.Query("code")
	sess, ok := TakeState(state) // 一次性：重放/伪造 state 在此终结
	if !ok {
		h.redirectResult(c, "error", "invalid or expired state", "")
		return
	}
	tokens, err := oidcClient.ExchangeCode(code, sess.verifier)
	if err != nil {
		h.redirectResult(c, "error", "token exchange failed", "")
		return
	}
	idCl, err := oidcClient.VerifyIDToken(tokens.IDToken)
	if err != nil {
		h.auditSSO(c, idCl, "auth.oidc.verify_fail", err.Error())
		h.redirectResult(c, "error", "id token verification failed", "")
		return
	}

	// 1) S4 绑定流程：已登录用户发起 → 绑定 sub 并回跳
	if sess.IsBind() {
		if h.prov == nil {
			h.redirectResult(c, "error", "account binding is not available", "")
			return
		}
		if err := h.prov.LinkOIDC(sess.BindUser(), idCl.Sub); err != nil {
			h.auditSSO(c, idCl, "auth.oidc.bind_fail", err.Error())
			h.redirectResult(c, "error", "bind failed: "+err.Error(), "")
			return
		}
		h.auditSSO(c, idCl, "auth.oidc.bind", "user:"+sess.BindUser())
		h.redirectResult(c, "bound", "", "")
		return
	}

	// 2) 已绑定的 SSO 身份 → 签发门户 JWT
	if h.prov != nil {
		if u, ok := h.prov.UserByOIDCSub(idCl.Sub); ok && u.Status == "active" {
			h.issueAndRedirect(c, u)
			return
		}
	}

	// 3) 撞名确认流（S4）：IdP 用户名命中本地未绑定账号 → 前端引导输本地密码确认
	username := sanitizeUsername(idCl.PreferredUsername)
	if username != "" {
		if u, ok := h.store.Lookup(username); ok && u.OIDCSub == "" {
			lt, err := MintLinkToken(idCl.Sub, username)
			if err != nil {
				h.redirectResult(c, "error", "internal error", "")
				return
			}
			h.auditSSO(c, idCl, "auth.oidc.conflict", "user:"+username)
			h.redirectResult(c, "link", "", lt)
			return
		}
	}

	// 4) JIT 开户（S2 映射；未启用/映射不出 → 拒绝）
	if u := h.tryProvision(c, idCl); u != nil {
		h.issueAndRedirect(c, u)
		return
	}
	h.auditSSO(c, idCl, "auth.oidc.jit_denied", "sub:"+idCl.Sub)
	h.redirectResult(c, "error", "no local account matches this identity and JIT provisioning is not permitted", "")
}

// tryProvision 按 S2 映射 JIT 开户；不可用返回 nil。
func (h *OIDCHandler) tryProvision(c *gin.Context, idCl *IDTokenClaims) *User {
	if h.prov == nil {
		return nil
	}
	username := sanitizeUsername(idCl.PreferredUsername)
	if username == "" {
		return nil
	}
	roleName, tenantSlug := h.resolveMapping(idCl)
	if roleName == "" || tenantSlug == "" {
		return nil
	}
	u, err := h.prov.ProvisionOIDCUser(username, idCl.Email, idCl.Name, roleName, tenantSlug, idCl.Sub)
	if err != nil {
		h.auditSSO(c, idCl, "auth.oidc.jit_fail", err.Error())
		return nil
	}
	h.auditSSO(c, idCl, "auth.oidc.jit", "user:"+u.Username)
	return u
}

// resolveMapping S2：claim 值 → (角色名, 租户 slug)。命中任一映射即用之；
// 未命中按 UnmappedPolicy（deny → 空 = 拒绝）。
func (h *OIDCHandler) resolveMapping(idCl *IDTokenClaims) (role, tenant string) {
	m := h.mapping
	role = firstMapped(idCl.ParseRoles(m.RolesClaim), m.RoleMap)
	if m.TenantClaim != "" && m.TenantClaim != m.RolesClaim {
		tenant = firstMapped(idCl.ParseRoles(m.TenantClaim), m.TenantMap)
	} else {
		tenant = firstMapped(idCl.ParseRoles(m.RolesClaim), m.TenantMap)
	}
	if role == "" || tenant == "" {
		if m.UnmappedPolicy != "default" {
			return "", ""
		}
		if role == "" {
			role = m.DefaultRole
		}
		if tenant == "" {
			tenant = m.DefaultTenant
		}
	}
	return role, tenant
}

func firstMapped(values []string, m map[string]string) string {
	for _, v := range values {
		if mapped, ok := m[v]; ok && mapped != "" {
			return mapped
		}
	}
	return ""
}

// Link POST /api/v1/auth/oidc/link —— 撞名确认：linkToken + 本地密码 → 绑定 + 签发。
func (h *OIDCHandler) Link(c *gin.Context) {
	var req struct {
		LinkToken string `json:"linkToken" binding:"required"`
		Username  string `json:"username" binding:"required"`
		Password  string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "linkToken, username and password are required")
		return
	}
	sub, tokUser, err := VerifyLinkToken(req.LinkToken)
	if err != nil || tokUser != req.Username {
		httpx.BadRequest(c, "invalid or expired link token")
		return
	}
	if h.prov == nil {
		httpx.ServiceUnavailable(c, "account binding is not available", nil)
		return
	}
	// 本地密码确认（身份证明）后才绑定
	if _, err := h.store.Verify(req.Username, req.Password); err != nil {
		httpx.Unauthorized(c, "invalid username or password")
		return
	}
	if err := h.prov.LinkOIDC(req.Username, sub); err != nil {
		httpx.Error(c, http.StatusConflict, err.Error())
		return
	}
	u, ok := h.store.Lookup(req.Username)
	if !ok {
		httpx.Internal(c, "OIDC.Link.lookup", err)
		return
	}
	h.auditSSOPlain(c, req.Username, "auth.oidc.link", "user:"+req.Username)
	token, err := GenerateTokenClaims(claimsOfUser(u))
	if err != nil {
		httpx.Internal(c, "OIDC.Link.token", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "username": u.Username, "roleName": u.RoleName})
}

// Unlink POST /api/v1/auth/oidc/unlink —— 已登录用户解绑 OIDC（auth_source=oidc 拒绝）。
func (h *OIDCHandler) Unlink(c *gin.Context) {
	cl := ClaimsFromCtx(c)
	if cl == nil {
		httpx.Unauthorized(c, "login required")
		return
	}
	if h.prov == nil {
		httpx.ServiceUnavailable(c, "account binding is not available", nil)
		return
	}
	if err := h.prov.UnlinkOIDC(cl.Username); err != nil {
		httpx.Error(c, http.StatusConflict, err.Error())
		return
	}
	h.auditSSOPlain(c, cl.Username, "auth.oidc.unlink", "user:"+cl.Username)
	c.JSON(http.StatusOK, gin.H{"message": "OIDC identity unlinked"})
}

// issueAndRedirect 为已就绪的用户签发门户 JWT 并 302 回前端。
func (h *OIDCHandler) issueAndRedirect(c *gin.Context, u *User) {
	// 活体校验口径与 JWTAuthMiddlewareWithStore 一致
	fresh, ok := h.store.Lookup(u.Username)
	if !ok || fresh.Status != "active" {
		h.redirectResult(c, "error", "account is not active", "")
		return
	}
	token, err := GenerateTokenClaims(claimsOfUser(fresh))
	if err != nil {
		h.redirectResult(c, "error", "token signing failed", "")
		return
	}
	// A1 会话台账（SSO 签发路径与密码登录同账）
	if sp, ok := h.store.(SessionSink); ok {
		sp.RecordLogin(c.Request.Context(), fresh.Username, c.ClientIP(), c.Request.UserAgent(),
			time.Now().Add(tokenTTL))
	}
	h.auditSSOPlain(c, fresh.Username, "auth.login", "user:"+fresh.Username)
	h.redirectResult(c, "ok", "", token)
}

// claimsOfUser 组装门户 Claims（与 Login 同构：tid/ver/rid/rn/perms）。
func claimsOfUser(u *User) Claims {
	rn := u.RoleName
	if rn == u.Role {
		rn = ""
	}
	return Claims{
		Username: u.Username, Role: u.Role, Rid: u.RoleID, Rn: rn, Perms: u.Permissions,
		OrgSlug: u.OrgSlug, TenantNS: u.TenantNS, ClusterUser: u.ClusterUser,
		Account: u.Account, TID: u.TenantSlug, Ver: u.TokenVersion,
	}
}

// redirectResult 302 到前端 hash 回调：#/login/oidc/callback?status=..&token=..
// token 只在 URL 片段里（浏览器不把 fragment 发给服务器——不出现在访问日志/Referer）。
func (h *OIDCHandler) redirectResult(c *gin.Context, status, errMsg, token string) {
	q := url.Values{}
	q.Set("status", status)
	if errMsg != "" {
		q.Set("error", errMsg)
	}
	if token != "" {
		q.Set("token", token)
	}
	base := h.PortalURL
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	c.Redirect(http.StatusFound, base+"#/login/oidc/callback?"+q.Encode())
}

func (h *OIDCHandler) auditSSO(c *gin.Context, cl *IDTokenClaims, action, detail string) {
	actor := cl.PreferredUsername
	if actor == "" {
		actor = "sub:" + cl.Sub
	}
	h.auditSSOPlain(c, actor, action, detail)
}

func (h *OIDCHandler) auditSSOPlain(c *gin.Context, actor, action, target string) {
	if h.audit == nil {
		return
	}
	rid := c.GetString("request_id")
	detail := `{"ip":` + jsonString(c.ClientIP()) + `}`
	_ = h.audit.WriteAudit(c.Request.Context(), actor, action, target, rid, detail)
}

// sanitizeUsername 把 IdP preferred_username 收敛到平台安全字符集（^[a-z_][a-z0-9_-]{0,31}$）。
func sanitizeUsername(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// 常见 IdP 邮箱形态 → 取本地部分
	if i := strings.IndexByte(s, '@'); i > 0 {
		s = s[:i]
	}
	s = strings.ReplaceAll(s, ".", "_")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	out := b.String()
	out = strings.TrimLeft(out, "-") // 首字符须 [a-z_]（unixSafeRE）
	if len(out) > 32 {
		out = out[:32]
		out = strings.TrimRight(out, "-_")
	}
	if out == "" {
		return ""
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "u" + out // 首字符不可为数字 → 前缀对齐
	}
	return out
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
