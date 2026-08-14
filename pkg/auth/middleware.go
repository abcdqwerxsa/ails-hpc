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

// JWTAuthMiddleware 校验 Authorization: Bearer <token>，将 *Claims 注入 gin.Context。
//
// 无令牌或令牌无效/过期一律 401 —— fail-closed，绝不默认授予任何角色
// （历史版本在无 Authorization 头时默认下发 admin claims，已移除）。
func JWTAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
		tokenStr := ""
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		}

		// /ide/ 反代路径：浏览器导航/iframe 无法带 Authorization 头。凭证取用顺序：
		// Authorization 头 > ?token= 查询参数 > cookie（首跳 ?token= 种下）。
		// 仅限 /api/v1/ide/（Web-IDE 会话），避免把宽松凭证方式放宽到所有 API。
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
			return
		}

		claims, err := VerifyToken(tokenStr)
		if err != nil {
			// 固定文案：不外泄 JWT 校验内部细节（签名/解析错误等）
			httpx.Unauthorized(c, "invalid or expired token")
			return
		}

		// 首跳 ?token= 验证通过 → 种 cookie，让 IDE 的重定向/XHR/WebSocket 后续自动携带。
		// HttpOnly 防 JS 读取；Path 限定仅 /api/v1/ide/；SameSite=Lax 阻跨站发送。
		if isIDE && fromQuery {
			http.SetCookie(c.Writer, &http.Cookie{
				Name:     ideCookieName,
				Value:    tokenStr,
				Path:     ideCookiePath,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   24 * 3600, // 与 token TTL 对齐；过期后重新从门户打开即刷新
			})
		}

		c.Set("claims", claims)
		c.Next()
	}
}

// RequireRole 仅允许指定角色通过，其余 403。角色来自 JWT claims（服务端权威）。
//
// admin 不再隐式短路 —— 矩阵即权威：admin 若需访问某路由，必须显式列入 allowedRoles。
// （历史版本中 admin 会在任何 RequireRole 中放行，已移除。）
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
