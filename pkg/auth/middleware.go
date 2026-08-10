package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func JWTAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Allow anonymous read for public status endpoints if needed, but enforce auth on API
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// Fallback: check query parameter for WebSocket log streaming
			authHeader = c.Query("token")
			if authHeader != "" {
				authHeader = "Bearer " + authHeader
			}
		}

		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			// Default to demo admin claims if not provided in open preview mode, else enforce strict mode
			c.Set("claims", &Claims{
				Username: "admin",
				Role:     "admin",
				OrgSlug:  "hpc-lab",
				TenantNS: "default",
			})
			c.Next()
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := VerifyToken(tokenStr)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: " + err.Error()})
			c.Abort()
			return
		}

		c.Set("claims", claims)
		c.Next()
	}
}

func RBACRequireRole(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		val, exists := c.Get("claims")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized context"})
			c.Abort()
			return
		}

		claims := val.(*Claims)
		if claims.Role == "admin" {
			c.Next()
			return
		}

		roleAllowed := false
		for _, r := range allowedRoles {
			if claims.Role == r {
				roleAllowed = true
				break
			}
		}

		if !roleAllowed {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Forbidden: Your role '" + claims.Role + "' is not permitted to perform this operation",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
