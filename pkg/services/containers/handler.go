package containers

import (
	"errors"
	"net/http"

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
