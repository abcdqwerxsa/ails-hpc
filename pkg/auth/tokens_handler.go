package auth

// T1 个人 API token 的自助管理端点（均需登录；PAT 亦可管理本人令牌，配额封顶）：
//
//	POST   /auth/tokens       {name, expiresInDays?} → {token(明文仅此一次), ...}
//	GET    /auth/tokens       → 本人的令牌列表（无哈希/明文）
//	DELETE /auth/tokens/:id   → 吊销
//
// 审计：token.create / token.revoke。must_change_password 锁定期被中间件 403（不在
// mustChangeAllowed 白名单——锁定期不该发长期凭证）。

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"ails-hpc/pkg/httpx"

	"github.com/gin-gonic/gin"
)

// patManagerOf 取 PAT 管理面（sqlite 库满足；yaml/内存库 → 503）。
func (h *AuthHandler) patManagerOf(c *gin.Context) (PATManager, bool) {
	pm, ok := h.store.(PATManager)
	if !ok {
		httpx.ServiceUnavailable(c, "API tokens require AILS_USER_STORE=db", nil)
		return nil, false
	}
	return pm, true
}

// CreateAPIToken POST /api/v1/auth/tokens。
func (h *AuthHandler) CreateAPIToken(c *gin.Context) {
	cl := ClaimsFromCtx(c)
	if cl == nil {
		httpx.Unauthorized(c, "login required")
		return
	}
	pm, ok := h.patManagerOf(c)
	if !ok {
		return
	}
	var req struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expiresInDays"` // 0=长期；1-3650
	}
	// 空体/nil 体合法（全默认值）；仅真正的畸形 JSON 拒绝。
	if c.Request.Body != nil {
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			httpx.BadRequest(c, "invalid payload")
			return
		}
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 3650 {
		httpx.BadRequest(c, "expiresInDays must be 0 (no expiry) or 1-3650")
		return
	}
	expires := ""
	if req.ExpiresInDays > 0 {
		expires = time.Now().UTC().AddDate(0, 0, req.ExpiresInDays).Format("2006-01-02 15:04:05")
	}
	token, hash, prefix, err := GeneratePAT()
	if err != nil {
		httpx.Internal(c, "Auth.CreateAPIToken", err)
		return
	}
	name := SanitizePATName(req.Name)
	id, err := pm.CreateAPIToken(c.Request.Context(), cl.Username, name, hash, prefix, expires)
	if err != nil {
		if errors.Is(err, ErrTokenQuota) {
			httpx.Error(c, http.StatusConflict, "token quota exceeded (max 10 active per user)")
			return
		}
		httpx.Internal(c, "Auth.CreateAPIToken", err)
		return
	}
	h.writeAudit(c, cl.Username, "token.create", "token:"+strconv.FormatInt(id, 10),
		`{"name":"`+name+`"}`)
	c.JSON(http.StatusOK, gin.H{
		"id": id, "name": name, "prefix": prefix,
		"token":     token, // 明文仅此一次
		"expiresAt": expires, "message": "保存此令牌——关闭后不再显示",
	})
}

// ListAPITokens GET /api/v1/auth/tokens。
func (h *AuthHandler) ListAPITokens(c *gin.Context) {
	cl := ClaimsFromCtx(c)
	if cl == nil {
		httpx.Unauthorized(c, "login required")
		return
	}
	pm, ok := h.patManagerOf(c)
	if !ok {
		return
	}
	toks, err := pm.ListAPITokens(c.Request.Context(), cl.Username)
	if err != nil {
		httpx.Internal(c, "Auth.ListAPITokens", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tokens": toks})
}

// RevokeAPIToken DELETE /api/v1/auth/tokens/:id。
func (h *AuthHandler) RevokeAPIToken(c *gin.Context) {
	cl := ClaimsFromCtx(c)
	if cl == nil {
		httpx.Unauthorized(c, "login required")
		return
	}
	pm, ok := h.patManagerOf(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.BadRequest(c, "invalid token id")
		return
	}
	if err := pm.RevokeAPIToken(c.Request.Context(), cl.Username, id); err != nil {
		httpx.NotFound(c, "token not found")
		return
	}
	h.writeAudit(c, cl.Username, "token.revoke", "token:"+strconv.FormatInt(id, 10), "{}")
	c.JSON(http.StatusOK, gin.H{"message": "token revoked"})
}
