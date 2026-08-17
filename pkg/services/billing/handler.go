package billing

import (
	"net/http"
	"strconv"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/httpx"

	"github.com/gin-gonic/gin"
)

// TenantResolver 统一复用 pkg/auth 的注入面（users.yaml 时代由 main 按 orgSlug 派生，
// DB 时代由 store.ClusterUsersOfTenant 实现）。
type TenantResolver = auth.TenantResolver

type BillingHandler struct {
	service  BillingService
	tenants  TenantResolver // 可为 nil：tenant_admin 退化为无租户约束（仅测试场景）
}

func NewBillingHandler(service BillingService) *BillingHandler {
	return &BillingHandler{service: service}
}

// NewBillingHandlerWithScope 注入租户成员解析器（多租户 Phase 0：billing 读写按 scope 收口）。
func NewBillingHandlerWithScope(service BillingService, tenants TenantResolver) *BillingHandler {
	return &BillingHandler{service: service, tenants: tenants}
}

// scopeParam 按登录者角色收紧计费查询参数（在信任 query 之前）：
//   - member：无视 ?user=，强制本人（修复：此前 member 可读任意用户账单）
//   - tenant_admin：?user= 必须属本租户（否则 403）；缺省查全量后按租户成员后过滤
//   - ops_admin/admin：不限制（billing 路由本身 admin 不可达，见 router 矩阵）
//
// 返回收紧后的 (user, allowedUsers)；拒绝时已写响应并返回 ok=false。
func (h *BillingHandler) scopeParam(c *gin.Context, requested string) (user string, allowed []string, ok bool) {
	val, _ := c.Get("claims")
	cl, _ := val.(*auth.Claims)
	sc := auth.ScopeFromClaims(cl)

	switch sc.Mode {
	case auth.ScopeSelf:
		return sc.ClusterUser, nil, true // 强制本人，query 被无视
	case auth.ScopeTenant:
		if h.tenants == nil {
			return requested, nil, true // 无解析器（仅测试）：维持原行为
		}
		members, err := h.tenants(sc.TenantSlug)
		if err != nil {
			httpx.Internal(c, "billing.resolveTenant", err)
			return "", nil, false
		}
		if requested != "" {
			for _, m := range members {
				if m == requested {
					return requested, nil, true // 本租户成员，放行单查
				}
			}
			httpx.Error(c, http.StatusForbidden, "forbidden: user is outside your tenant")
			return "", nil, false
		}
		return "", members, true // 全量拉取 + 租户成员后过滤
	default: // ScopeAll
		return requested, nil, true
	}
}

func (h *BillingHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/billing/usage", h.GetUsage)
	rg.GET("/billing/export", h.ExportReport)
}

func (h *BillingHandler) GetUsage(c *gin.Context) {
	format := c.Query("format")
	if format != "" && format != "json" && format != "chart" {
		httpx.BadRequest(c, "Invalid format query parameter")
		return
	}

	startTime := c.Query("start_time")
	endTime := c.Query("end_time")
	if startTime != "" && endTime != "" && startTime > endTime {
		httpx.BadRequest(c, "start_time cannot be greater than end_time")
		return
	}

	limitStr := c.Query("limit")
	limit := 0
	if limitStr != "" {
		var err error
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit < 0 {
			httpx.BadRequest(c, "limit must be non-negative")
			return
		}
	}

	user, allowed, ok := h.scopeParam(c, c.Query("user"))
	if !ok {
		return
	}

	param := UsageQueryParam{
		User:         user,
		Project:      c.Query("project"),
		StartTime:    startTime,
		EndTime:      endTime,
		Limit:        limit,
		Format:       format,
		AllowedUsers: allowed,
	}

	resp, err := h.service.GetUsage(c.Request.Context(), param)
	if err != nil {
		httpx.Internal(c, "GetUsage", err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *BillingHandler) ExportReport(c *gin.Context) {
	format := c.DefaultQuery("format", "json")
	if format != "json" && format != "chart" {
		httpx.BadRequest(c, "Invalid export format. Supported: json, chart")
		return
	}

	user, allowed, ok := h.scopeParam(c, c.Query("user"))
	if !ok {
		return
	}

	param := ExportQueryParam{
		Format:       format,
		User:         user,
		Project:      c.Query("project"),
		AllowedUsers: allowed, // tenant_admin 缺省导出 = 本租户成员后过滤
	}

	report, err := h.service.ExportReport(c.Request.Context(), param)
	if err != nil {
		httpx.Internal(c, "ExportReport", err)
		return
	}

	c.JSON(http.StatusOK, report)
}
