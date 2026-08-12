package nodes

import (
	"errors"
	"net/http"

	"ails-hpc/pkg/httpx"

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
		httpx.Internal(c, "GetNodes", err)
		return
	}

	c.JSON(http.StatusOK, NodesListResponse{Nodes: nodes})
}

func (h *NodeHandler) UpdateNodeState(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		httpx.BadRequest(c, "node name parameter is required")
		return
	}

	var req NodeStateUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid request body or state payload required")
		return
	}

	if req.State == "" {
		httpx.BadRequest(c, "state payload required")
		return
	}

	res, err := h.service.UpdateNodeState(c.Request.Context(), name, &req)
	if err != nil {
		if errors.Is(err, ErrNodeNotFound) {
			httpx.NotFound(c, err.Error())
			return
		}
		if errors.Is(err, ErrInvalidState) {
			httpx.BadRequest(c, err.Error())
			return
		}
		httpx.Internal(c, "UpdateNodeState", err)
		return
	}

	c.JSON(http.StatusOK, res)
}
