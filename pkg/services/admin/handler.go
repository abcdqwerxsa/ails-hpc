package admin

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/httpx"
	"ails-hpc/pkg/store"

	"github.com/gin-gonic/gin"
)

// AdminHandler 暴露租户/用户管理端点（设计 §5）。
// 平台组 /api/v1/admin/**：admin 独占；租户组 /api/v1/tenants/me/**：tenant_admin
// （越权角色由路由层 RequireRole 拦截；租户归属在 service 层以 claims 为权威）。
type AdminHandler struct {
	service *Service
}

// NewAdminHandler 构造。service 为 nil（yaml 模式）时全部端点 503。
func NewAdminHandler(service *Service) *AdminHandler {
	return &AdminHandler{service: service}
}

// actorAndTenant 从 claims 取 (actor, tenantSlug, ok)。
func actorAndTenant(c *gin.Context) (string, string) {
	sc := scopeOf(c)
	return sc.Username, sc.TenantSlug
}

func scopeOf(c *gin.Context) auth.Scope {
	if v, ok := c.Get("claims"); ok {
		if cl, ok := v.(*auth.Claims); ok {
			return auth.ScopeFromClaims(cl)
		}
	}
	return auth.Scope{}
}

func requestID(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// mapErr 把 store/service 错误映射为 HTTP 语义（sentinel 与 pkg/store/admin.go 对齐）。
func mapErr(c *gin.Context, err error, op string) {
	switch {
	case err == nil:
		return
	case errors.Is(err, ErrReadOnlyStore):
		httpx.ServiceUnavailable(c, "admin API requires AILS_USER_STORE=db (yaml seed is read-only)", nil)
	case errors.Is(err, ErrProvisionFailed):
		// DB 已提交、Slurm 供给失败：502 + 明确文案（重试幂等）
		httpx.Error(c, http.StatusBadGateway, err.Error())
	case errors.Is(err, ErrRoleNotAllowed):
		httpx.BadRequest(c, err.Error())
	case errors.Is(err, store.ErrNotFound):
		httpx.NotFound(c, "not found")
	case errors.Is(err, store.ErrTenantExists), errors.Is(err, store.ErrDuplicateUser),
		errors.Is(err, store.ErrTenantReserved), errors.Is(err, store.ErrTenantSuspended),
		errors.Is(err, store.ErrUIDExhausted):
		httpx.Error(c, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrInvalidUsername), errors.Is(err, store.ErrInvalidSlug),
		errors.Is(err, store.ErrInvalidRole), errors.Is(err, store.ErrInvalidStatus),
		errors.Is(err, store.ErrRoleTenantMismatch), errors.Is(err, store.ErrWeakPassword),
		errors.Is(err, store.ErrInvalidClusterUser), errors.Is(err, store.ErrInvalidAccount),
		errors.Is(err, store.ErrInvalidUID), errors.Is(err, store.ErrInvalidHash):
		httpx.BadRequest(c, err.Error())
	default:
		httpx.Internal(c, op, err)
	}
}

// --- 平台级 ---

// ListTenants GET /api/v1/admin/tenants（附每租户用户数，前端契约含 userCount）
func (h *AdminHandler) ListTenants(c *gin.Context) {
	ts, err := h.service.ListTenants(c.Request.Context())
	if err != nil {
		mapErr(c, err, "admin.ListTenants")
		return
	}
	type tenantWithCount struct {
		store.Tenant
		UserCount int `json:"userCount"`
	}
	out := make([]tenantWithCount, 0, len(ts))
	for _, t := range ts {
		us, err := h.service.ListTenantUsers(c.Request.Context(), t.Slug)
		n := 0
		if err == nil {
			n = len(us)
		}
		out = append(out, tenantWithCount{Tenant: t, UserCount: n})
	}
	c.JSON(http.StatusOK, gin.H{"tenants": out})
}

// CreateTenant POST /api/v1/admin/tenants {slug,name}
func (h *AdminHandler) CreateTenant(c *gin.Context) {
	var req struct {
		Slug string `json:"slug" binding:"required"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "slug is required")
		return
	}
	actor, _ := actorAndTenant(c)
	t, err := h.service.CreateTenant(c.Request.Context(), actor, req.Slug, req.Name, requestID(c))
	if err != nil {
		mapErr(c, err, "admin.CreateTenant")
		return
	}
	c.JSON(http.StatusOK, gin.H{"tenant": t})
}

// UpdateTenant PATCH /api/v1/admin/tenants/:slug {name?,status?}
func (h *AdminHandler) UpdateTenant(c *gin.Context) {
	var req struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		GrpTRES    string `json:"grpTRES"`
		Fairshare  string `json:"fairshare"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid payload")
		return
	}
	if req.Status != "" && req.Status != "active" && req.Status != "suspended" {
		httpx.BadRequest(c, "status must be active or suspended")
		return
	}
	// 限额值白名单（防注入 sacctmgr）：TRES 值字符集 / Fairshare 数字
	for _, v := range []string{req.GrpTRES, req.Fairshare} {
		if v != "" && !limitRE.MatchString(v) {
			httpx.BadRequest(c, "grpTRES/fairshare contains illegal characters")
			return
		}
	}
	actor, _ := actorAndTenant(c)
	if err := h.service.UpdateTenant(c.Request.Context(), actor, c.Param("slug"), req.Status, req.GrpTRES, req.Fairshare, requestID(c)); err != nil {
		mapErr(c, err, "admin.UpdateTenant")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "tenant updated"})
}

// ListTenantUsers GET /api/v1/admin/tenants/:slug/users
func (h *AdminHandler) ListTenantUsers(c *gin.Context) {
	us, err := h.service.ListTenantUsers(c.Request.Context(), c.Param("slug"))
	if err != nil {
		mapErr(c, err, "admin.ListTenantUsers")
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": us})
}

// CreatePlatformUser POST /api/v1/admin/users {username,role,tenantSlug,password}
func (h *AdminHandler) CreatePlatformUser(c *gin.Context) {
	var req struct {
		Username   string `json:"username" binding:"required"`
		Role       string `json:"role" binding:"required"`
		TenantSlug string `json:"tenantSlug" binding:"required"`
		Password   string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "username, role, tenantSlug, password are required")
		return
	}
	actor, _ := actorAndTenant(c)
	u, err := h.service.CreatePlatformUser(c.Request.Context(), actor, store.NewUser{
		Username: req.Username, Password: req.Password, Role: req.Role, TenantSlug: req.TenantSlug,
	}, requestID(c))
	if err != nil {
		mapErr(c, err, "admin.CreatePlatformUser")
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": u})
}

// --- 4.2 预约 / QOS ---

// ListReservations GET /api/v1/admin/reservations
func (h *AdminHandler) ListReservations(c *gin.Context) {
	rs, err := h.service.ListReservations(c.Request.Context())
	if err != nil {
		httpx.Internal(c, "admin.ListReservations", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"reservations": rs})
}

// CreateReservation POST /api/v1/admin/reservations {name,startTime,durationMinutes,nodes,users,partition}
func (h *AdminHandler) CreateReservation(c *gin.Context) {
	var req struct {
		Name           string `json:"name" binding:"required"`
		StartTime      string `json:"startTime"` // 空=now+1min；YYYY-MM-DDTHH:MM
		DurationMinutes int   `json:"durationMinutes" binding:"required"`
		Nodes          string `json:"nodes"`
		Users          string `json:"users"`
		Partition      string `json:"partition"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "name and durationMinutes are required")
		return
	}
	r, err := h.service.CreateReservation(c.Request.Context(), req.Name, req.StartTime, req.DurationMinutes, req.Nodes, req.Users, req.Partition)
	if err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"reservation": r})
}

// DeleteReservation DELETE /api/v1/admin/reservations/:name
func (h *AdminHandler) DeleteReservation(c *gin.Context) {
	if err := h.service.DeleteReservation(c.Request.Context(), c.Param("name")); err != nil {
		httpx.Internal(c, "admin.DeleteReservation", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "reservation deleted"})
}

// ListQOS GET /api/v1/admin/qos
func (h *AdminHandler) ListQOS(c *gin.Context) {
	qs, err := h.service.ListQOS(c.Request.Context())
	if err != nil {
		httpx.Internal(c, "admin.ListQOS", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"qos": qs})
}

// CreateQOS POST /api/v1/admin/qos {name,grpTRES?}
func (h *AdminHandler) CreateQOS(c *gin.Context) {
	var req struct {
		Name    string `json:"name" binding:"required"`
		GrpTRES string `json:"grpTRES"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "name is required")
		return
	}
	q, err := h.service.CreateQOS(c.Request.Context(), req.Name, req.GrpTRES)
	if err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"qos": q})
}

// SetTenantQOS PATCH /api/v1/admin/tenants/:slug/qos {name}
func (h *AdminHandler) SetTenantQOS(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "name is required")
		return
	}
	if err := h.service.SetTenantQOS(c.Request.Context(), c.Param("slug"), req.Name); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "tenant qos updated"})
}

// ListAudit GET /api/v1/admin/audit?actor=&action=&limit=（平台审计查看器，admin 独占）。
func (h *AdminHandler) ListAudit(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	entries, err := h.service.ListAudit(c.Request.Context(), c.Query("actor"), c.Query("action"), limit)
	if err != nil {
		mapErr(c, err, "admin.ListAudit")
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

// --- 租户级 ---

// ListMyUsers GET /api/v1/tenants/me/users
func (h *AdminHandler) ListMyUsers(c *gin.Context) {
	_, tenant := actorAndTenant(c)
	us, err := h.service.ListMyUsers(c.Request.Context(), tenant)
	if err != nil {
		mapErr(c, err, "admin.ListMyUsers")
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": us})
}

// CreateTenantUser POST /api/v1/tenants/me/users
func (h *AdminHandler) CreateTenantUser(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
		Role     string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "username, password, role are required")
		return
	}
	actor, tenant := actorAndTenant(c)
	u, err := h.service.CreateTenantUser(c.Request.Context(), actor, tenant, store.NewUser{
		Username: req.Username, Password: req.Password, Role: req.Role,
	}, requestID(c))
	if err != nil {
		mapErr(c, err, "admin.CreateTenantUser")
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": u})
}

// UpdateMyUser PATCH /api/v1/tenants/me/users/:username {displayName?,status?}
func (h *AdminHandler) UpdateMyUser(c *gin.Context) {
	var req struct {
		DisplayName string `json:"displayName"`
		Status      string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid payload")
		return
	}
	if req.Status != "" && req.Status != "active" && req.Status != "disabled" {
		httpx.BadRequest(c, "status must be active or disabled")
		return
	}
	actor, tenant := actorAndTenant(c)
	if err := h.service.UpdateMyUser(c.Request.Context(), actor, tenant, c.Param("username"), req.DisplayName, req.Status, requestID(c)); err != nil {
		mapErr(c, err, "admin.UpdateMyUser")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "user updated"})
}

// ResetMyUserPassword POST /api/v1/tenants/me/users/:username/password {newPassword}
func (h *AdminHandler) ResetMyUserPassword(c *gin.Context) {
	var req struct {
		NewPassword string `json:"newPassword" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "newPassword is required")
		return
	}
	if len(req.NewPassword) < 8 {
		httpx.BadRequest(c, "newPassword must be at least 8 characters")
		return
	}
	actor, tenant := actorAndTenant(c)
	if err := h.service.ResetMyUserPassword(c.Request.Context(), actor, tenant, c.Param("username"), req.NewPassword, requestID(c)); err != nil {
		mapErr(c, err, "admin.ResetMyUserPassword")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "password reset; the user's sessions are revoked"})
}

// limitRE 限额值白名单：TRES（cpu=4,mem=2g,gres/gpu=1 逗号分隔）或 Fairshare 数字。
var limitRE = regexp.MustCompile(`^[0-9A-Za-z/=,:+. _-]{1,64}$`)
