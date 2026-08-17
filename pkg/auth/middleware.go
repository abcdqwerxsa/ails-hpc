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
// 验签 →（store 非空时）活体校验 → IDE 首跳种 cookie。失败时已写 401 响应。
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
	claims, err := VerifyToken(tokenStr)
	if err != nil {
		// 固定文案：不外泄 JWT 校验内部细节（签名/解析错误等）
		httpx.Unauthorized(c, "invalid or expired token")
		return nil, false
	}
	if store != nil {
		u, ok := store.Lookup(claims.Username)
		if !ok || u.Status != "active" {
			httpx.Unauthorized(c, "invalid or expired token")
			return nil, false
		}
		if ver, ok := store.UserVersion(claims.Username); !ok || ver != claims.Ver {
			httpx.Unauthorized(c, "invalid or expired token")
			return nil, false
		}
		// R2 角色表化：按库内当前值刷新角色面（角色改派/角色权限调整即刻生效，无需
		// 重登）。Role 恒为"基角色"（scope 推导）；Rn/Perms/Rid 携带实际角色信息。
		// 内存/yaml 库这些字段为零值 → 刷新为空 → 解析器回退内置映射，行为不变。
		claims.Role = u.Role
		claims.Rn = u.RoleName
		claims.Perms = u.Permissions
		claims.Rid = u.RoleID

		// A1 强制改密：must_change_password=1 时只放行自助面（改密/自描述/登出全部/
		// SSO 关联），其余业务端点一律 403——防初始/被重置密码被长期使用。
		if u.MustChangePassword && !mustChangeAllowed(c.Request.URL.Path) {
			httpx.Error(c, http.StatusForbidden,
				"password change required before using this service",
				httpx.Extra{"code": "must_change_password"})
			return nil, false
		}
	}
	if isIDE && fromQuery {
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     ideCookieName,
			Value:    tokenStr,
			Path:     ideCookiePath,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   24 * 3600,
		})
	}
	return claims, true
}

// mustChangeAllowed 是 must_change_password=1 时的端点白名单（自助面）。
func mustChangeAllowed(path string) bool {
	for _, p := range []string{
		"/api/v1/auth/password",
		"/api/v1/auth/me",
		"/api/v1/auth/logout-all",
		"/api/v1/auth/oidc/",
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
