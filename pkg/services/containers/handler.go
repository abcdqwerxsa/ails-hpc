package containers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
)

type ContainerHandler struct {
	service ContainerService
}

func NewContainerHandler(service ContainerService) *ContainerHandler {
	return &ContainerHandler{service: service}
}

func (h *ContainerHandler) LaunchContainer(c *gin.Context) {
	var req ContainerLaunchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request payload"})
		return
	}

	res, err := h.service.LaunchContainer(c.Request.Context(), &req)
	if err != nil {
		if errors.Is(err, ErrUnsupportedEnvType) || errors.Is(err, ErrInvalidResources) || errors.Is(err, ErrQuotaExceeded) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}

func (h *ContainerHandler) ListContainers(c *gin.Context) {
	list, err := h.service.ListActiveContainers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, ContainerListResponse{Containers: list})
}

func (h *ContainerHandler) RecycleContainer(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "container ID is required"})
		return
	}

	res, err := h.service.RecycleContainer(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrContainerNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}

// ProxyIDE 反向代理 /api/v1/ide/<session>/* 到计算节点上的 IDE 应用 server。
// 访问已由路由层的 RequireRole(member,tenant_admin) 守门；应用自身 auth 关闭。
func (h *ContainerHandler) ProxyIDE(c *gin.Context) {
	session := c.Param("session")
	nodeIP, port, status, err := h.service.ProxyTarget(c.Request.Context(), session)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session not reachable", "status": status})
		return
	}
	if status != "RUNNING" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session still starting", "status": status})
		return
	}
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("%s:%d", nodeIP, port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // 支持 WebSocket / 流式（Jupyter kernel、code-server terminal）
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		http.Error(w, "session backend unreachable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}
