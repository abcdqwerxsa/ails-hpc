package apis

import (
	"net/http"

	"ails-hpc/pkg/auth"

	"github.com/gin-gonic/gin"
)

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	OrgSlug  string `json:"orgSlug"`
}

type AuthHandler struct{}

func (a *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	role := "member"
	tenantNs := "default"
	orgSlug := req.OrgSlug

	if req.Username == "admin" || req.Username == "root" {
		role = "admin"
		tenantNs = "default"
		if orgSlug == "" {
			orgSlug = "hpc-lab"
		}
	} else if req.OrgSlug != "" {
		tenantNs = "hpc-tenant-" + req.OrgSlug
	}

	token, err := auth.GenerateToken(req.Username, role, orgSlug, tenantNs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "SSO Login Successful",
		"token":   token,
		"user": gin.H{
			"username": req.Username,
			"role":     role,
			"orgSlug":  orgSlug,
			"tenantNs": tenantNs,
		},
	})
}
