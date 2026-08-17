package containers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/httpx"

	"github.com/gin-gonic/gin"
)

// ideRoutePrefix 是 /api/v1/ide/:session 路由的前缀；code-server（根路径启动）需剥掉它再转发。
const ideRoutePrefix = "/api/v1/ide/"

type ContainerHandler struct {
	service ContainerService
	// tenants 租户成员解析（Phase 4）；nil = 不收紧（仅测试/旧装配）。
	tenants auth.TenantResolver
}

func NewContainerHandler(service ContainerService) *ContainerHandler {
	return &ContainerHandler{service: service}
}

// NewContainerHandlerScoped 注入租户成员解析（Phase 4：member 只见/只控自己的会话，
// tenant_admin 限本租户，ops/admin 全量）。
func NewContainerHandlerScoped(service ContainerService, tenants auth.TenantResolver) *ContainerHandler {
	return &ContainerHandler{service: service, tenants: tenants}
}

// callerFromCtx 从 JWT claims 取 (username, role, clusterUser, account)。
// clusterUser/account 为真·每用户 Slurm 隔离所需：会话以 clusterUser 真实身份提交、account 写入 Slurm。
func callerFromCtx(c *gin.Context) (username, role, clusterUser, account string) {
	if v, ok := c.Get("claims"); ok {
		if cl, ok := v.(*auth.Claims); ok {
			return cl.Username, cl.Role, cl.ClusterUser, cl.Account
		}
	}
	return "", "", "", ""
}

// forbidIfNotSessionOwner 归属隔离：member 只能回收自己的会话（owner==clusterUser 或遗留空 owner 放行）；
// tenant_admin 越权。已写响应（403/404）时返回 true，调用方应 return。
func (h *ContainerHandler) forbidIfNotSessionOwner(c *gin.Context, id string) bool {
	owner, err := h.service.SessionOwner(c.Request.Context(), id)
	if err != nil {
		httpx.NotFound(c, "session not found")
		return true
	}
	// Phase 4：member 只控自己的；tenant_admin 限本租户（此前全局通配，按设计 §6 收紧）；
	// 空属主=遗留会话，放行。
	allow, err := auth.ScopeFromClaims(auth.ClaimsFromCtx(c)).RowFilter(h.tenants)
	if err != nil {
		httpx.Internal(c, "forbidIfNotSessionOwner.scope", err)
		return true
	}
	if !allow(owner) {
		httpx.Error(c, http.StatusForbidden, "forbidden: not the session owner")
		return true
	}
	return false
}


// controlActAs 决定回收操作的下发身份（L4）：member 用自己的 clusterUser（Slurm 层
// 强制令牌身份==会话作业属主）；tenant_admin 越权走 root。
func controlActAs(c *gin.Context) string {
	_, role, clusterUser, _ := callerFromCtx(c)
	if role == auth.RoleMember {
		return clusterUser
	}
	return ""
}

func (h *ContainerHandler) LaunchContainer(c *gin.Context) {
	var req ContainerLaunchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid request payload")
		return
	}

	_, _, clusterUser, account := callerFromCtx(c)
	res, err := h.service.LaunchContainer(c.Request.Context(), &req, clusterUser, account)
	if err != nil {
		if errors.Is(err, ErrUnsupportedEnvType) || errors.Is(err, ErrInvalidResources) || errors.Is(err, ErrQuotaExceeded) {
			httpx.BadRequest(c, err.Error())
			return
		}
		httpx.Internal(c, "LaunchContainer", err)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (h *ContainerHandler) ListContainers(c *gin.Context) {
	list, err := h.service.ListActiveContainers(c.Request.Context())
	if err != nil {
		httpx.Internal(c, "ListContainers", err)
		return
	}

	// Phase 4 租户隔离：member 只见自己的会话；tenant_admin 见本租户；ops/admin 全量。
	allow, err := auth.ScopeFromClaims(auth.ClaimsFromCtx(c)).RowFilter(h.tenants)
	if err != nil {
		httpx.Internal(c, "ListContainers.scope", err)
		return
	}
	scoped := make([]*ContainerInstance, 0, len(list))
	for _, ct := range list {
		if allow(ct.Owner) {
			scoped = append(scoped, ct)
		}
	}

	c.JSON(http.StatusOK, ContainerListResponse{Containers: scoped})
}

func (h *ContainerHandler) RecycleContainer(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		httpx.BadRequest(c, "container ID is required")
		return
	}

	if h.forbidIfNotSessionOwner(c, id) {
		return
	}

	res, err := h.service.RecycleContainer(c.Request.Context(), id, controlActAs(c))
	if err != nil {
		if errors.Is(err, ErrContainerNotFound) {
			httpx.NotFound(c, err.Error())
			return
		}
		httpx.Internal(c, "RecycleContainer", err)
		return
	}

	c.JSON(http.StatusOK, res)
}

// ProxyIDE 反向代理 /api/v1/ide/<session>/* 到计算节点上的 IDE 应用 server。
// 访问已由路由层的 RequireRole(member,tenant_admin) 守门；应用自身 auth 关闭。
func (h *ContainerHandler) ProxyIDE(c *gin.Context) {
	session := c.Param("session")
	nodeIP, port, status, envType, err := h.service.ProxyTarget(c.Request.Context(), session)
	if err != nil {
		httpx.ServiceUnavailable(c, "session not reachable", httpx.Extra{"status": status})
		return
	}
	if status != "RUNNING" {
		httpx.ServiceUnavailable(c, "session still starting", httpx.Extra{"status": status})
		return
	}
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("%s:%d", nodeIP, port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // 支持 WebSocket / 流式（Jupyter kernel、code-server terminal）
	// code-server（standalone）无 base-path，根路径启动；要在 /api/v1/ide/<session>/ 子路径下
	// 服务，需剥掉该前缀再转发（Jupyter 有 base_url 对齐，保持原样不剥）。
	if strings.EqualFold(envType, "vscode") {
		prefix := ideRoutePrefix + session
		baseDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			baseDirector(req)
			if rest := strings.TrimPrefix(req.URL.Path, prefix); rest != req.URL.Path {
				if rest == "" {
					rest = "/"
				}
				req.URL.Path = rest
				req.URL.RawPath = ""
			}
		}
	}
	// 下方 ErrorHandler 是 stdlib 反向代理自身的裸文本 502（绕过 gin/JSON 信封），保留原状。
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		http.Error(w, "session backend unreachable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}
