package containers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"ails-hpc/pkg/httpx"

	"github.com/gin-gonic/gin"
)

// ideRoutePrefix 是 /api/v1/ide/:session 路由的前缀；code-server（根路径启动）需剥掉它再转发。
const ideRoutePrefix = "/api/v1/ide/"

type ContainerHandler struct {
	service ContainerService
}

func NewContainerHandler(service ContainerService) *ContainerHandler {
	return &ContainerHandler{service: service}
}

func (h *ContainerHandler) LaunchContainer(c *gin.Context) {
	var req ContainerLaunchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid request payload")
		return
	}

	res, err := h.service.LaunchContainer(c.Request.Context(), &req)
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

	c.JSON(http.StatusOK, ContainerListResponse{Containers: list})
}

func (h *ContainerHandler) RecycleContainer(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		httpx.BadRequest(c, "container ID is required")
		return
	}

	res, err := h.service.RecycleContainer(c.Request.Context(), id)
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
