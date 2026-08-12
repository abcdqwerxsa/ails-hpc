package nodes

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

type NodeHandler struct {
	service NodeService
}

func NewNodeHandler(service NodeService) *NodeHandler {
	return &NodeHandler{service: service}
}

func (h *NodeHandler) GetNodes(c *gin.Context) {
	nodes, err := h.service.ListNodes(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, NodesListResponse{Nodes: nodes})
}

func (h *NodeHandler) UpdateNodeState(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "node name parameter is required"})
		return
	}

	var req NodeStateUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body or state payload required"})
		return
	}

	if req.State == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "state payload required"})
		return
	}

	res, err := h.service.UpdateNodeState(c.Request.Context(), name, &req)
	if err != nil {
		if errors.Is(err, ErrNodeNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrInvalidState) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}
