package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"ails-hpc/pkg/httpx"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
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
	Username    string `json:"username"`
	Role        string `json:"role"` // 基角色（scope 语义）
	RoleName    string `json:"roleName,omitempty"` // 实际角色名（自定义角色 ≠ role）
	Permissions []string `json:"permissions,omitempty"` // 权限点清单（R4 前端能力驱动）
	OrgSlug     string `json:"orgSlug"`
	TenantNS    string `json:"tenantNs"`
	ClusterUser string `json:"clusterUser"`
	Account     string `json:"account"`
	TenantSlug  string `json:"tenantSlug"`
	// MustChangePassword A1：首次登录/被重置后须改密（前端引导到设置页）。
	MustChangePassword bool `json:"mustChangePassword,omitempty"`
	// AuthSource 凭证来源（local|oidc）；OIDCLinked 供 S4 绑定/解绑 UI 判定。
	AuthSource string `json:"authSource,omitempty"`
	OIDCLinked bool   `json:"oidcLinked,omitempty"`
}

// LoginResponse 严格匹配 React login.tsx 解析的结构：{token, user:{username,role,orgSlug,tenantNs}}。
type LoginResponse struct {
	Token string   `json:"token"`
	User  UserInfo `json:"user"`
}

// AuditSink 是认证域的审计出口（A2：登录成功/失败、改密入库）。
// 生产装配为 sqlite AdminStore（store.WriteAudit 同面）；nil=不落审计（测试）。
type AuditSink interface {
	WriteAudit(ctx context.Context, actor, action, target, requestID, detail string) error
}

// AuthHandler 负责登录与令牌签发。Rate(2.1 登录防爆破)可为 nil=不限速（测试）。
type AuthHandler struct {
	store UserStore
	Rate  *RateLimiter
	audit AuditSink
}

// NewAuthHandler 构造登录处理器（带默认限速器）。
func NewAuthHandler(store UserStore) *AuthHandler {
	return &AuthHandler{store: store, Rate: NewRateLimiter()}
}

// SetAuditSink 注入审计出口（main 装配时调用；写失败只记日志不影响主流程）。
func (h *AuthHandler) SetAuditSink(s AuditSink) { h.audit = s }

// writeAudit 尽力写入（失败只记日志不影响响应；request_id 从 gin 上下文取）。
func (h *AuthHandler) writeAudit(c *gin.Context, actor, action, target, detail string) {
	if h.audit == nil {
		return
	}
	rid, _ := c.Get("request_id")
	ridStr, _ := rid.(string)
	if err := h.audit.WriteAudit(c.Request.Context(), actor, action, target, ridStr, detail); err != nil {
		log.Printf("AUDIT write failed action=%s actor=%s err=%v", action, actor, err)
	}
}

// NewAuthHandlerNoRate 构造不限速的登录处理器（测试用）。
func NewAuthHandlerNoRate(store UserStore) *AuthHandler {
	return &AuthHandler{store: store}
}

// Login POST /api/v1/auth/login —— 校验凭证，签发 JWT，返回前端契约响应。
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "username and password are required")
		return
	}

	ip := c.ClientIP()
	if ok, wait := h.Rate.Allow(req.Username, ip); !ok {
		// 锁定中：不比对密码（防在线穷举），文案与普通失败一致（防枚举/不泄露锁定态）
		log.Printf("AUDIT login.locked username=%s ip=%s wait=%s", req.Username, ip, wait.Truncate(time.Second))
		h.writeAudit(c, req.Username, "auth.login.locked", "user:"+req.Username,
			fmt.Sprintf(`{"ip":%q}`, ip))
		httpx.Unauthorized(c, "invalid username or password")
		return
	}

	user, err := h.store.Verify(req.Username, req.Password)
	if err != nil {
		locked := h.Rate.RecordFailure(req.Username, ip)
		log.Printf("AUDIT login.fail username=%s ip=%s locked=%v", req.Username, ip, locked)
		h.writeAudit(c, req.Username, "auth.login.fail", "user:"+req.Username,
			fmt.Sprintf(`{"ip":%q,"locked":%v}`, ip, locked))
		// 用户不存在/密码错同一文案，避免用户名枚举
		httpx.Unauthorized(c, "invalid username or password")
		return
	}
	h.Rate.RecordSuccess(req.Username, ip)
	h.writeAudit(c, user.Username, "auth.login", "user:"+user.Username,
		fmt.Sprintf(`{"ip":%q,"auth_source":%q,"role":%q}`, ip, "local", user.Role))
	// A1 会话台账（可选面：DB 库支持；内存/yaml 库跳过）
	if sp, ok := h.store.(SessionSink); ok {
		sp.RecordLogin(c.Request.Context(), user.Username, ip, c.Request.UserAgent(),
			time.Now().Add(tokenTTL))
	}

	// Phase 2：整 Claims 签发——tid（租户）+ ver（令牌版本，改密/禁用即吊销在途令牌）。
	// R2：rid/rn/perms 携带实际角色（自定义角色时代 Role=基角色仅作 scope 推导）。
	rn := user.RoleName
	if rn == "" || rn == user.Role {
		rn = "" // 内置角色不重复携带（零噪音，旧客户端无感）
	}
	token, err := GenerateTokenClaims(Claims{
		Username:    user.Username,
		Role:        user.Role,
		Rid:         user.RoleID,
		Rn:          rn,
		Perms:       user.Permissions,
		OrgSlug:     user.OrgSlug,
		TenantNS:    user.TenantNS,
		ClusterUser: user.ClusterUser,
		Account:     user.Account,
		TID:         user.TenantSlug,
		Ver:         user.TokenVersion,
	})
	if err != nil {
		httpx.Internal(c, "Login.GenerateToken", err)
		return
	}

	c.JSON(http.StatusOK, LoginResponse{
		Token: token,
		User: UserInfo{
			Username:           user.Username,
			Role:               user.Role,
			RoleName:           rn,
			Permissions:        user.Permissions,
			OrgSlug:            user.OrgSlug,
			TenantNS:           user.TenantNS,
			ClusterUser:        user.ClusterUser,
			Account:            user.Account,
			TenantSlug:         user.TenantSlug,
			MustChangePassword: user.MustChangePassword,
			AuthSource:         user.AuthSource,
			OIDCLinked:         user.OIDCSub != "",
		},
	})
}

// ChangePasswordRequest POST /api/v1/auth/password 请求体（自助改密）。
type ChangePasswordRequest struct {
	OldPassword string `json:"oldPassword" binding:"required"`
	NewPassword string `json:"newPassword" binding:"required"`
}

// ChangePassword POST /api/v1/auth/password —— 自助改密（任何已认证角色）。
// A1 起执行复杂度策略（大小写/数字/符号 + ≥8）与历史 N 次不可重用；成功后
// TokenVersion+1：本人所有在途 JWT 即刻失效，需重新登录（must_change 标记同时清除）。
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "oldPassword and newPassword are required")
		return
	}
	if err := ValidatePasswordPolicy(req.NewPassword); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	if req.NewPassword == req.OldPassword {
		httpx.BadRequest(c, "newPassword must differ from oldPassword")
		return
	}

	username, _, _, _ := claimsFromCtx(c)
	if _, err := h.store.Verify(username, req.OldPassword); err != nil {
		httpx.Unauthorized(c, "invalid username or password")
		return
	}
	// A1 历史 N 次不可重用（DB 库可选面；内存库跳过）
	if ps, ok := h.store.(PolicyStore); ok {
		if err := ps.CheckPasswordHistory(c.Request.Context(), username, req.NewPassword); err != nil {
			httpx.BadRequest(c, err.Error())
			return
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		httpx.Internal(c, "ChangePassword.hash", err)
		return
	}
	if ps, ok := h.store.(PolicyStore); ok {
		if err := ps.SetPasswordWithHistory(c.Request.Context(), username, string(hash)); err != nil {
			httpx.Internal(c, "ChangePassword.SetPassword", err)
			return
		}
	} else if err := h.store.SetPassword(username, string(hash)); err != nil {
		if errors.Is(err, ErrUserStoreReadOnly) {
			// yaml 文件库落在只读文件系统（systemd ProtectSystem）等：明确拒绝，
			// 密码未变化；DB 用户库（AILS_USER_STORE=db）可写。
			httpx.ServiceUnavailable(c, "password change is not supported by the active (read-only) user store; AILS_USER_STORE=db required", nil)
			return
		}
		httpx.Internal(c, "ChangePassword.SetPassword", err)
		return
	}
	h.writeAudit(c, username, "auth.password.change", "user:"+username, `{}`)
	c.JSON(http.StatusOK, gin.H{"message": "password updated; please log in again"})
}

// SessionSink 是 A1 会话台账写面（生产 = sqlite store；内存库不实现 → 跳过）。
type SessionSink interface {
	RecordLogin(ctx context.Context, username, ip, userAgent string, expiresAt time.Time)
	ListSessions(ctx context.Context, username string) ([]SessionEntry, error)
	LogoutAll(ctx context.Context, username string) error
}

// SessionEntry 是会话台账行（auth 侧形状；store.SessionInfo 对齐）。
type SessionEntry struct {
	ID        int64  `json:"id"`
	IssuedAt  string `json:"issuedAt"`
	ExpiresAt string `json:"expiresAt"`
	IP        string `json:"ip"`
	UserAgent string `json:"userAgent"`
}

// MySessions GET /api/v1/auth/me/sessions —— 本人当前有效会话清单。
func (h *AuthHandler) MySessions(c *gin.Context) {
	username, _, _, _ := claimsFromCtx(c)
	sp, ok := h.store.(SessionSink)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"sessions": []SessionEntry{}})
		return
	}
	out, err := sp.ListSessions(c.Request.Context(), username)
	if err != nil {
		httpx.Internal(c, "MySessions", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": out})
}

// LogoutAll POST /api/v1/auth/logout-all —— 全设备登出：token_version+1，
// 本人所有在途 JWT 即刻失效；台账清理。
func (h *AuthHandler) LogoutAll(c *gin.Context) {
	username, _, _, _ := claimsFromCtx(c)
	if sp, ok := h.store.(SessionSink); ok {
		if err := sp.LogoutAll(c.Request.Context(), username); err != nil {
			httpx.Internal(c, "LogoutAll", err)
			return
		}
	}
	h.writeAudit(c, username, "auth.logout_all", "user:"+username, `{}`)
	c.JSON(http.StatusOK, gin.H{"message": "all sessions revoked; please log in again"})
}

// claimsFromCtx 从 gin 上下文取 JWT claims（中间件注入）。
func claimsFromCtx(c *gin.Context) (username, role, clusterUser, account string) {
	if v, ok := c.Get("claims"); ok {
		if cl, ok := v.(*Claims); ok {
			return cl.Username, cl.Role, cl.ClusterUser, cl.Account
		}
	}
	return "", "", "", ""
}

// Store 暴露用户库（NewRouter 挂带 store 的 JWT 中间件用）。
func (h *AuthHandler) Store() UserStore { return h.store }

// Me GET /api/v1/auth/me —— 权限自描述（R4 前端能力驱动的数据源）。
// 返回本人基角色/实际角色名/权限点清单 + 集群身份；claims 由带 store 的中间件
// 每请求按库刷新——角色改派/权限调整后前端无需重登即可感知（刷新页面或重拉本端点）。
func (h *AuthHandler) Me(c *gin.Context) {
	cl := ClaimsFromCtx(c)
	if cl == nil {
		httpx.Unauthorized(c, "missing authenticated context")
		return
	}
	roleName := cl.Rn
	if roleName == cl.Role {
		roleName = "" // 内置角色不重复携带（与 Login 的 rn 归一化一致）
	}
	mustChange, authSource, oidcLinked := false, "local", false
	if u, ok := h.store.Lookup(cl.Username); ok {
		mustChange = u.MustChangePassword
		if u.AuthSource != "" {
			authSource = u.AuthSource
		}
		oidcLinked = u.OIDCSub != ""
	}
	c.JSON(http.StatusOK, LoginResponse{
		Token: "",
		User: UserInfo{
			Username:           cl.Username,
			Role:               cl.Role,
			RoleName:           roleName,
			Permissions:        SortedPermissions(PermissionsOf(cl)),
			OrgSlug:            cl.OrgSlug,
			TenantNS:           cl.TenantNS,
			ClusterUser:        cl.ClusterUser,
			Account:            cl.Account,
			TenantSlug:         tenantOfPublic(cl),
			MustChangePassword: mustChange,
			AuthSource:         authSource,
			OIDCLinked:         oidcLinked,
		},
	})
}

// tenantOfPublic 对齐 scope.tenantOf 的可见语义（TID 优先，迁移期回退 OrgSlug）。
func tenantOfPublic(cl *Claims) string {
	if cl.TID != "" {
		return cl.TID
	}
	return cl.OrgSlug
}
