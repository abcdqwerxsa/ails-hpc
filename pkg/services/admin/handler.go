package admin

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

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
	case errors.Is(err, ErrRoleNotAllowed), errors.Is(err, ErrRoleEscalation),
		errors.Is(err, ErrSelfDisable):
		httpx.BadRequest(c, err.Error())
	case errors.Is(err, store.ErrNotFound):
		httpx.NotFound(c, "not found")
	case errors.Is(err, store.ErrTenantExists), errors.Is(err, store.ErrDuplicateUser),
		errors.Is(err, store.ErrTenantReserved), errors.Is(err, store.ErrTenantSuspended),
		errors.Is(err, store.ErrUIDExhausted),
		errors.Is(err, store.ErrRoleExists), errors.Is(err, store.ErrRoleSystem),
		errors.Is(err, store.ErrRoleInUse):
		httpx.Error(c, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrInvalidUsername), errors.Is(err, store.ErrInvalidSlug),
		errors.Is(err, store.ErrInvalidRole), errors.Is(err, store.ErrInvalidStatus),
		errors.Is(err, store.ErrInvalidDisplayName),
		errors.Is(err, store.ErrRoleTenantMismatch), errors.Is(err, store.ErrWeakPassword),
		errors.Is(err, store.ErrInvalidClusterUser), errors.Is(err, store.ErrInvalidAccount),
		errors.Is(err, store.ErrInvalidUID), errors.Is(err, store.ErrInvalidHash),
		errors.Is(err, store.ErrRoleReserved), errors.Is(err, store.ErrInvalidPermission),
		errors.Is(err, store.ErrInvalidBaseRole):
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
		Name      string `json:"name"`
		Status    string `json:"status"`
		GrpTRES   string `json:"grpTRES"`
		Fairshare string `json:"fairshare"`
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
	if err := auth.ValidatePasswordPolicy(req.Password); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	actor, _ := actorAndTenant(c)
	u, err := h.service.CreatePlatformUser(c.Request.Context(), actor, store.NewUser{
		Username: req.Username, Password: req.Password, Role: req.Role, TenantSlug: req.TenantSlug,
		MustChangePassword: true, // A1：初始密码首登强制改密
	}, requestID(c))
	if err != nil {
		mapErr(c, err, "admin.CreatePlatformUser")
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": u})
}

// --- v3-U 平台用户生命周期（users:manage） ---

// ListPlatformUsers GET /api/v1/admin/users?tenant=&q= —— 平台用户目录（跨租户）。
// tenant=精确过滤；q=子串命中 username 或显示名（目录规模几十~几百，内存过滤足够）。
func (h *AdminHandler) ListPlatformUsers(c *gin.Context) {
	us, err := h.service.ListPlatformUsers(c.Request.Context())
	if err != nil {
		mapErr(c, err, "admin.ListPlatformUsers")
		return
	}
	if tenant, q := c.Query("tenant"), strings.ToLower(c.Query("q")); tenant != "" || q != "" {
		out := make([]auth.User, 0, len(us))
		for _, u := range us {
			if tenant != "" && u.TenantSlug != tenant {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(u.Username), q) &&
				!strings.Contains(strings.ToLower(u.DisplayName), q) {
				continue
			}
			out = append(out, u)
		}
		us = out
	}
	c.JSON(http.StatusOK, gin.H{"users": us})
}

// UpdatePlatformUser PATCH /api/v1/admin/users/:username {displayName?,status?}
// 显示名编辑（U4）与禁用/启用（U2）；空串=不变更；自禁用 400（防自锁）。
func (h *AdminHandler) UpdatePlatformUser(c *gin.Context) {
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
	if len(req.DisplayName) > 64 {
		httpx.BadRequest(c, "displayName too long (max 64)")
		return
	}
	actor, _ := actorAndTenant(c)
	if err := h.service.UpdatePlatformUser(c.Request.Context(), actor, c.Param("username"),
		req.DisplayName, req.Status, requestID(c)); err != nil {
		mapErr(c, err, "admin.UpdatePlatformUser")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "user updated"})
}

// ResetPlatformUserPassword POST /api/v1/admin/users/:username/password {newPassword}
// 平台重置（U3）：策略校验同建号；重置后强制首登改密+在途令牌吊销。
func (h *AdminHandler) ResetPlatformUserPassword(c *gin.Context) {
	var req struct {
		NewPassword string `json:"newPassword" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "newPassword is required")
		return
	}
	if err := auth.ValidatePasswordPolicy(req.NewPassword); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	actor, _ := actorAndTenant(c)
	if err := h.service.ResetPlatformUserPassword(c.Request.Context(), actor, c.Param("username"),
		req.NewPassword, requestID(c)); err != nil {
		mapErr(c, err, "admin.ResetPlatformUserPassword")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "password reset; must change on next login"})
}

// GetTenantQuotas GET /api/v1/admin/tenants/quotas（v4-W3）：全部租户配额——
// 平台侧入口（tenants:read）。admin 不持 billing:read（纯硬件监控教义），
// 平台视角的配额总览走本端点而非 billing 面。
func (h *AdminHandler) GetTenantQuotas(c *gin.Context) {
	quotas, err := h.service.ListTenantQuotas(c.Request.Context())
	if err != nil {
		mapErr(c, err, "admin.ListTenantQuotas")
		return
	}
	c.JSON(http.StatusOK, gin.H{"quotas": quotas})
}

// GetBillingQuota GET /api/v1/slurm/billing/quota（v4-W3；billing:read 门 + scope 收口）。
// 读数走 sacctmgr 权威。scope：ops(scope all)=全部租户；tenant_admin/member=仅本租户。
func (h *AdminHandler) GetBillingQuota(c *gin.Context) {
	quotas, err := h.service.ListTenantQuotas(c.Request.Context())
	if err != nil {
		mapErr(c, err, "admin.ListTenantQuotas")
		return
	}
	sc := scopeOf(c)
	if sc.Mode != auth.ScopeAll {
		own := make([]TenantQuota, 0, 1)
		for _, q := range quotas {
			if q.TenantSlug == sc.TenantSlug {
				own = append(own, q)
			}
		}
		quotas = own
	}
	c.JSON(http.StatusOK, gin.H{"quotas": quotas})
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
// v3-X1：成功经 service 落审计（reservations.create）。
func (h *AdminHandler) CreateReservation(c *gin.Context) {
	var req struct {
		Name            string `json:"name" binding:"required"`
		StartTime       string `json:"startTime"` // 空=now+1min；YYYY-MM-DDTHH:MM
		DurationMinutes int    `json:"durationMinutes" binding:"required"`
		Nodes           string `json:"nodes"`
		Users           string `json:"users"`
		Partition       string `json:"partition"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "name and durationMinutes are required")
		return
	}
	actor, _ := actorAndTenant(c)
	r, err := h.service.CreateReservation(c.Request.Context(), actor, req.Name, req.StartTime, req.DurationMinutes, req.Nodes, req.Users, req.Partition, requestID(c))
	if err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"reservation": r})
}

// DeleteReservation DELETE /api/v1/admin/reservations/:name
func (h *AdminHandler) DeleteReservation(c *gin.Context) {
	actor, _ := actorAndTenant(c)
	if err := h.service.DeleteReservation(c.Request.Context(), actor, c.Param("name"), requestID(c)); err != nil {
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
	actor, _ := actorAndTenant(c)
	q, err := h.service.CreateQOS(c.Request.Context(), actor, req.Name, req.GrpTRES, requestID(c))
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
	actor, _ := actorAndTenant(c)
	if err := h.service.SetTenantQOS(c.Request.Context(), actor, c.Param("slug"), req.Name, requestID(c)); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "tenant qos updated"})
}

// --- 分区管理（partitions:manage；scontrol 直通，同 4.2 教义） ---

// GetPartition GET /api/v1/admin/partitions/:name —— 编辑弹层当前值（scontrol show partition）。
func (h *AdminHandler) GetPartition(c *gin.Context) {
	p, err := h.service.GetPartition(c.Request.Context(), c.Param("name"))
	if errors.Is(err, ErrPartitionNotFound) {
		httpx.NotFound(c, "partition not found")
		return
	}
	if err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"partition": p})
}

// UpdatePartition PATCH /api/v1/admin/partitions/:name {state?,maxTime?,default?,...}
// 可改字段白名单与逐字段值校验见 cluster_admin.go（空串=不变更；全空 400）。
func (h *AdminHandler) UpdatePartition(c *gin.Context) {
	var req PartitionUpdates
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid payload")
		return
	}
	if err := ValidatePartitionUpdates(req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	actor, _ := actorAndTenant(c)
	if err := h.service.UpdatePartition(c.Request.Context(), actor, c.Param("name"), req, requestID(c)); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "partition updated"})
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
	if err := auth.ValidatePasswordPolicy(req.Password); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	actor, tenant := actorAndTenant(c)
	u, err := h.service.CreateTenantUser(c.Request.Context(), actor, tenant, store.NewUser{
		Username: req.Username, Password: req.Password, Role: req.Role,
		MustChangePassword: true, // A1：初始密码首登强制改密
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
	if err := auth.ValidatePasswordPolicy(req.NewPassword); err != nil {
		httpx.BadRequest(c, err.Error())
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

// --- 角色管理（R3 自定义角色；子集防提权校验在 service 层） ---

// roleRequest 是建/改角色的请求体。baseRole 缺省按作用域取最小面（平台=ops_admin、
// 租户=member）；permissions 为白名单词汇表内的子集（服务端校验 ⊆ 调用者权限）。
type roleRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	BaseRole    string   `json:"baseRole"`
}

// actorPermissions 从 claims 取调用者有效权限集合（中间件已按库刷新——DB 权威）。
func actorPermissions(c *gin.Context) []string {
	return auth.SortedPermissions(auth.PermissionsOf(auth.ClaimsFromCtx(c)))
}

// ListPlatformRoles GET /api/v1/admin/roles —— 平台角色（内置+自定义）。
func (h *AdminHandler) ListPlatformRoles(c *gin.Context) {
	rs, err := h.service.ListRoles(c.Request.Context(), "")
	if err != nil {
		mapErr(c, err, "admin.ListPlatformRoles")
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": rs})
}

// ListTenantRoles GET /api/v1/admin/tenants/:slug/roles —— 某租户的自定义角色（admin 查看）。
func (h *AdminHandler) ListTenantRoles(c *gin.Context) {
	rs, err := h.service.ListRoles(c.Request.Context(), c.Param("slug"))
	if err != nil {
		mapErr(c, err, "admin.ListTenantRoles")
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": rs})
}

// CreatePlatformRole POST /api/v1/admin/roles —— 建平台自定义角色（admin）。
func (h *AdminHandler) CreatePlatformRole(c *gin.Context) {
	var req roleRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		httpx.BadRequest(c, "name is required")
		return
	}
	if req.BaseRole == "" {
		req.BaseRole = auth.RoleOpsAdmin // 平台默认基角色（scope=all 的最小面）
	}
	actor, _ := actorAndTenant(c)
	r, err := h.service.CreateRole(c.Request.Context(), actor, actorPermissions(c), store.NewRole{
		Name: req.Name, Description: req.Description,
		Permissions: req.Permissions, BaseRole: req.BaseRole, TenantSlug: "",
	}, requestID(c))
	if err != nil {
		mapErr(c, err, "admin.CreatePlatformRole")
		return
	}
	c.JSON(http.StatusOK, gin.H{"role": r})
}

// UpdatePlatformRole PATCH /api/v1/admin/roles/:name {description?, permissions?}
func (h *AdminHandler) UpdatePlatformRole(c *gin.Context) {
	var req struct {
		Description *string  `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid payload")
		return
	}
	actor, _ := actorAndTenant(c)
	r, err := h.service.UpdateRole(c.Request.Context(), actor, actorPermissions(c), "",
		c.Param("name"), req.Permissions, req.Description, requestID(c))
	if err != nil {
		mapErr(c, err, "admin.UpdatePlatformRole")
		return
	}
	c.JSON(http.StatusOK, gin.H{"role": r})
}

// DeletePlatformRole DELETE /api/v1/admin/roles/:name（系统角色/在用角色 → 409）。
func (h *AdminHandler) DeletePlatformRole(c *gin.Context) {
	actor, _ := actorAndTenant(c)
	if err := h.service.DeleteRole(c.Request.Context(), actor, "", c.Param("name"), requestID(c)); err != nil {
		mapErr(c, err, "admin.DeletePlatformRole")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "role deleted"})
}

// AssignPlatformRole PATCH /api/v1/admin/users/:username/role {role}
func (h *AdminHandler) AssignPlatformRole(c *gin.Context) {
	var req struct {
		Role string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "role is required")
		return
	}
	actor, _ := actorAndTenant(c)
	if err := h.service.AssignRole(c.Request.Context(), actor, "", c.Param("username"), req.Role, requestID(c)); err != nil {
		mapErr(c, err, "admin.AssignPlatformRole")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "role assigned"})
}

// --- 租户级角色管理（tenant_admin，仅本租户） ---

// ListMyRoles GET /api/v1/tenants/me/roles —— 本租户的自定义角色。
func (h *AdminHandler) ListMyRoles(c *gin.Context) {
	_, tenant := actorAndTenant(c)
	rs, err := h.service.ListRoles(c.Request.Context(), tenant)
	if err != nil {
		mapErr(c, err, "admin.ListMyRoles")
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": rs})
}

// CreateMyRole POST /api/v1/tenants/me/roles —— 建本租户自定义角色（权限 ⊆ 自身）。
func (h *AdminHandler) CreateMyRole(c *gin.Context) {
	var req roleRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		httpx.BadRequest(c, "name is required")
		return
	}
	if req.BaseRole == "" {
		req.BaseRole = auth.RoleMember // 租户默认基角色（scope=self）
	}
	actor, tenant := actorAndTenant(c)
	r, err := h.service.CreateRole(c.Request.Context(), actor, actorPermissions(c), store.NewRole{
		Name: req.Name, Description: req.Description,
		Permissions: req.Permissions, BaseRole: req.BaseRole, TenantSlug: tenant,
	}, requestID(c))
	if err != nil {
		mapErr(c, err, "admin.CreateMyRole")
		return
	}
	c.JSON(http.StatusOK, gin.H{"role": r})
}

// UpdateMyRole PATCH /api/v1/tenants/me/roles/:name（跨租户同名角色不可达 → 404）。
func (h *AdminHandler) UpdateMyRole(c *gin.Context) {
	var req struct {
		Description *string  `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid payload")
		return
	}
	actor, tenant := actorAndTenant(c)
	r, err := h.service.UpdateRole(c.Request.Context(), actor, actorPermissions(c), tenant,
		c.Param("name"), req.Permissions, req.Description, requestID(c))
	if err != nil {
		mapErr(c, err, "admin.UpdateMyRole")
		return
	}
	c.JSON(http.StatusOK, gin.H{"role": r})
}

// DeleteMyRole DELETE /api/v1/tenants/me/roles/:name（在用 → 409 须先改派）。
func (h *AdminHandler) DeleteMyRole(c *gin.Context) {
	actor, tenant := actorAndTenant(c)
	if err := h.service.DeleteRole(c.Request.Context(), actor, tenant, c.Param("name"), requestID(c)); err != nil {
		mapErr(c, err, "admin.DeleteMyRole")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "role deleted"})
}

// AssignMyRole PATCH /api/v1/tenants/me/users/:username/role {role}
// 角色解析限本租户作用域（+内置 member/tenant_admin）；跨租户角色不可达。
func (h *AdminHandler) AssignMyRole(c *gin.Context) {
	var req struct {
		Role string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "role is required")
		return
	}
	actor, tenant := actorAndTenant(c)
	if err := h.service.AssignRole(c.Request.Context(), actor, tenant, c.Param("username"), req.Role, requestID(c)); err != nil {
		mapErr(c, err, "admin.AssignMyRole")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "role assigned"})
}
