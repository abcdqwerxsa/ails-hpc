package auth

import (
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

// JWTAuthMiddleware 校验 Authorization: Bearer <token>，将 *Claims 注入 gin.Context。
//
// 无令牌或令牌无效/过期一律 401 —— fail-closed，绝不默认授予任何角色
// （历史版本在无 Authorization 头时默认下发 admin claims，已移除）。
func JWTAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			httpx.Unauthorized(c, "missing or invalid Authorization header")
			return
		}

		tokenStr := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		claims, err := VerifyToken(tokenStr)
		if err != nil {
			// 固定文案：不外泄 JWT 校验内部细节（签名/解析错误等）
			httpx.Unauthorized(c, "invalid or expired token")
			return
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
