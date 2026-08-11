package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	RoleSystemAdmin = "admin"
	RoleOpsAdmin    = "ops_admin"
	RoleTenantAdmin = "tenant_admin"
	RoleMember      = "member"
)

// RequireRole 检查 Header 或 Context 中的角色权限
func RequireRole(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole := c.GetHeader("X-User-Role")
		if userRole == "" {
			userRole = RoleMember // 默认普通成员角色
		}

		userRole = strings.ToLower(strings.TrimSpace(userRole))

		hasPermission := false
		for _, role := range allowedRoles {
			if userRole == strings.ToLower(role) || userRole == RoleSystemAdmin {
				hasPermission = true
				break
			}
		}

		if !hasPermission {
			c.JSON(http.StatusForbidden, gin.H{
				"error":     "Forbidden: insufficient permissions for this operation",
				"user_role": userRole,
				"required":  allowedRoles,
			})
			c.Abort()
			return
		}

		c.Set("user_role", userRole)
		c.Next()
	}
}
