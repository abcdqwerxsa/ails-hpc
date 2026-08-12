package auth

import (
	"net/http"

	"ails-hpc/pkg/httpx"

	"github.com/gin-gonic/gin"
)

// LoginRequest 与前端契约对齐：apps/web/app/routes/login.tsx 提交 {username,password,orgSlug}。
// orgSlug 由 React 端发送，本轮服务端不消费（多租户隔离在路线图中）。
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	OrgSlug  string `json:"orgSlug"`
}

// UserInfo 登录响应中的用户可见信息（不含密码哈希）。
type UserInfo struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	OrgSlug  string `json:"orgSlug"`
	TenantNS string `json:"tenantNs"`
}

// LoginResponse 严格匹配 React login.tsx 解析的结构：{token, user:{username,role,orgSlug,tenantNs}}。
type LoginResponse struct {
	Token string   `json:"token"`
	User  UserInfo `json:"user"`
}

// AuthHandler 负责登录与令牌签发。
type AuthHandler struct {
	store UserStore
}

// NewAuthHandler 构造登录处理器。
func NewAuthHandler(store UserStore) *AuthHandler {
	return &AuthHandler{store: store}
}

// Login POST /api/v1/auth/login —— 校验凭证，签发 JWT，返回前端契约响应。
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "username and password are required")
		return
	}

	user, err := h.store.Verify(req.Username, req.Password)
	if err != nil {
		// 用户不存在/密码错同一文案，避免用户名枚举
		httpx.Unauthorized(c, "invalid username or password")
		return
	}

	token, err := GenerateToken(user.Username, user.Role, user.OrgSlug, user.TenantNS)
	if err != nil {
		httpx.Internal(c, "Login.GenerateToken", err)
		return
	}

	c.JSON(http.StatusOK, LoginResponse{
		Token: token,
		User: UserInfo{
			Username: user.Username,
			Role:     user.Role,
			OrgSlug:  user.OrgSlug,
			TenantNS: user.TenantNS,
		},
	})
}
