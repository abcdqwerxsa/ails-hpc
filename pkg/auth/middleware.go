package auth

import (
	"net/http"
	"strings"

	"ails-hpc/pkg/httpx"

	"github.com/gin-gonic/gin"
)

// 权威角色常量（来自 JWT claims，由登录后服务端下发）。
const (
	RoleSystemAdmin = "admin"
	RoleOpsAdmin    = "ops_admin"
	RoleTenantAdmin = "tenant_admin"
	RoleMember      = "member"
)

// ideCookieName 是 Web-IDE 反代路径的凭证 cookie。
// 背景：浏览器打开 IDE 后，Jupyter 的 302 重定向（丢 query）、XHR 与 code-server 的
// 静态资源/WebSocket 请求都无法携带 Authorization 头，也带不上 ?token=。首跳
// ?token= 验证通过后种下此 cookie（Path 限定 /api/v1/ide/、HttpOnly、SameSite=Lax），
// 后续 IDE 子资源请求由浏览器自动携带。
const ideCookieName = "ails_ide_token"
const ideCookiePath = "/api/v1/ide/"

// JWTAuthMiddleware 校验 Authorization: Bearer <token>（纯签名校验形态，等价于
// JWTAuthMiddlewareWithStore(nil)）。生产路由请用 WithStore 形态（活体校验）。
func JWTAuthMiddleware() gin.HandlerFunc {
	return JWTAuthMiddlewareWithStore(nil)
}

// JWTAuthMiddlewareWithStore 带用户库实校的 JWT 中间件（生产形态：NewRouter 挂载）。
// 在签名校验通过后追加两条活体检查（Lookup/UserVersion 各一次，内存 map 与 sqlite
// 均为微秒级）：
//  1. 用户存在且 status=active（禁用即刻踢出，不等 24h TTL）
//  2. claims.Ver == 用户当前 TokenVersion（改密后旧令牌即刻失效）
//
// 旧格式令牌（无 ver，Ver=0）与初始版本 0 天然兼容——迁移期不强制重登。
// store 为 nil 时等价于 JWTAuthMiddleware（纯签名校验）。
func JWTAuthMiddlewareWithStore(store UserStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := authenticate(c, store)
		if !ok {
			return // 响应已在 authenticate 写好
		}
		c.Set("claims", claims)
		c.Next()
	}
}

// authenticate 是两种中间件的共享内核：取凭证（头>?token=>cookie，IDE 路径）→
// 验签（JWT）或查取（PAT，T1）→（store 非空时）活体校验 → IDE 首跳种 cookie。
// 失败时已写 401 响应。
func authenticate(c *gin.Context, store UserStore) (*Claims, bool) {
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	tokenStr := ""
	if strings.HasPrefix(authHeader, "Bearer ") {
		tokenStr = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	}
	isIDE := strings.HasPrefix(c.Request.URL.Path, "/api/v1/ide/")
	fromQuery := false
	if tokenStr == "" && isIDE {
		tokenStr = strings.TrimSpace(c.Query("token"))
		fromQuery = tokenStr != ""
	}
	if tokenStr == "" && isIDE {
		if ck, err := c.Cookie(ideCookieName); err == nil {
			tokenStr = strings.TrimSpace(ck)
		}
	}
	if tokenStr == "" {
		httpx.Unauthorized(c, "missing or invalid Authorization header")
		return nil, false
	}
	// T1：PAT 前缀分流（JWT 恒为三段点分，与 PAT 前缀互斥）。
	if strings.HasPrefix(tokenStr, PATPrefix) {
		claims, ok := authenticatePAT(c, store, tokenStr)
		if ok && isIDE && fromQuery {
			seedIDECookie(c, tokenStr)
		}
		return claims, ok
	}
	claims, err := VerifyToken(tokenStr)
	if err != nil {
		// 固定文案：不外泄 JWT 校验内部细节（签名/解析错误等）
		httpx.Unauthorized(c, "invalid or expired token")
		return nil, false
	}
	if store != nil {
		u, ok := store.Lookup(claims.Username)
		if !liveGate(c, store, u, ok, claims) {
			return nil, false
		}
	}
	if isIDE && fromQuery {
		seedIDECookie(c, tokenStr)
	}
	return claims, true
}

// authenticatePAT T1 PAT 认证路径：哈希直查 → 吊销/过期拒绝 → 活体门（同 JWT）→
// 由用户库当值重建 claims（每请求刷新，角色改派/权限调整即刻生效）。
func authenticatePAT(c *gin.Context, store UserStore, token string) (*Claims, bool) {
	if store == nil {
		httpx.Unauthorized(c, "invalid or expired token")
		return nil, false
	}
	ts, ok := store.(PATStore)
	if !ok {
		httpx.ServiceUnavailable(c, "API tokens require AILS_USER_STORE=db", nil)
		return nil, false
	}
	rec, err := ts.LookupAPIToken(PATHash(token))
	if err != nil || rec.Revoked || PATExpired(rec.ExpiresAt) {
		// 与 JWT 同文案——吊销/过期/未知统一"无效令牌"（不泄露具体状态）
		httpx.Unauthorized(c, "invalid or expired token")
		return nil, false
	}
	u, ok := store.Lookup(rec.Username)
	claims := &Claims{Username: rec.Username}
	if !liveGate(c, store, u, ok, claims) {
		return nil, false
	}
	if patTouchDue(rec.ID) {
		_ = ts.TouchAPIToken(rec.ID) // best-effort：失败不影响请求
	}
	return claims, true
}

// liveGate 活体校验 + 角色面刷新 + must-change 门（JWT/PAT 共用）。claims 为
// JWT 解析结果或 PAT 的空白构造（由 u 重建）。返回 false 时已写响应。
func liveGate(c *gin.Context, store UserStore, u *User, ok bool, claims *Claims) bool {
	if !ok || u.Status != "active" || u.TenantSuspended {
		httpx.Unauthorized(c, "invalid or expired token")
		return false
	}
	if claims.Ver != 0 || claims.Exp != 0 { // JWT 路径才比对版本（PAT 每请求重建，无 Ver）
		if ver, vok := store.UserVersion(claims.Username); !vok || ver != claims.Ver {
			httpx.Unauthorized(c, "invalid or expired token")
			return false
		}
	}
	// R2 角色表化：按库内当前值刷新角色面（角色改派/角色权限调整即刻生效，无需
	// 重登）。Role 恒为"基角色"（scope 推导）；Rn/Perms/Rid 携带实际角色信息。
	// 内存/yaml 库这些字段为零值 → 刷新为空 → 解析器回退内置映射，行为不变。
	claims.Role = u.Role
	claims.Rn = u.RoleName
	claims.Perms = u.Permissions
	claims.Rid = u.RoleID
	claims.OrgSlug = u.OrgSlug
	claims.TenantNS = u.TenantNS
	claims.ClusterUser = u.ClusterUser
	claims.Account = u.Account
	claims.TID = u.TenantSlug

	// A1 强制改密：must_change_password=1 时只放行自助面（改密/自描述/登出全部/
	// SSO 关联），其余业务端点一律 403——防初始/被重置密码被长期使用。
	if u.MustChangePassword && !mustChangeAllowed(c.Request.URL.Path) {
		httpx.Error(c, http.StatusForbidden,
			"password change required before using this service",
			httpx.Extra{"code": "must_change_password"})
		return false
	}
	return true
}

// seedIDECookie IDE 首跳种 cookie（JWT/PAT 共用）。
func seedIDECookie(c *gin.Context, tokenStr string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     ideCookieName,
		Value:    tokenStr,
		Path:     ideCookiePath,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   ideCookieSecure, // P2：TLS 部署后置 AILS_COOKIE_SECURE=1（http 下置位会静默丢 cookie）
		MaxAge:   24 * 3600,
	})
}

// ideCookieSecure 由 main 按 AILS_COOKIE_SECURE 注入（默认 false——当前 http 部署；
// 上 TLS 后开启，cookie 只经 https 传输。安全审计 2026-08-19 P2）。
var ideCookieSecure bool

// SetIdeCookieSecure 设置 IDE 凭证 cookie 的 Secure 属性。
func SetIdeCookieSecure(v bool) { ideCookieSecure = v }

// mustChangeAllowed 是 must_change_password=1 时的端点白名单（自助面）。
func mustChangeAllowed(path string) bool {
	// 安全审计 2026-08-19 P1-6：不再前缀放行 /auth/oidc/*——改密锁定窗口期若可调
	// bind，持初始/重置密码方可把自己的 IdP sub 绑上账号（绑定不 bump token_version
	// → 改密后仍可 SSO 登录的持久后门）。锁定用户先改密再绑定/解绑。
	for _, p := range []string{
		"/api/v1/auth/password",
		"/api/v1/auth/me",
		"/api/v1/auth/logout-all",
	} {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// RequireRole 按角色白名单放行（R1 前的路由门面）。
//
// Deprecated: 生产路由已全部切换到 RequirePermission（权限点为权威，角色是权限的命名
// 集合——自定义角色时代按角色名放行无法表达）。保留供旧测试装配与迁移期引用。
func RequireRole(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		val, exists := c.Get("claims")
		if !exists {
			httpx.Unauthorized(c, "missing authenticated context")
			return
		}
		claims, ok := val.(*Claims)
		if !ok {
			httpx.Unauthorized(c, "invalid authenticated context")
			return
		}

		for _, r := range allowedRoles {
			if claims.Role == r {
				c.Next()
				return
			}
		}

		httpx.Forbidden(c, "forbidden: role '"+claims.Role+"' is not permitted", allowedRoles)
	}
}
