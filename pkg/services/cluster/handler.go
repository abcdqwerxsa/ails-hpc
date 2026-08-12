package cluster

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ClusterHandler 集群只读状态的 HTTP 处理器
type ClusterHandler struct {
	service ClusterService
}

// NewClusterHandler 构造集群状态处理器
func NewClusterHandler(service ClusterService) *ClusterHandler {
	return &ClusterHandler{service: service}
}

// GetStatus GET /api/v1/slurm/ping — 测试 slurmrestd 可达性与 Slurm 控制节点状态。
// 成功返回 *slurmrest.PingResponse（含 pings[].ping="UP"，前端 fetchSlurmStatus 依赖此字段）；
// 不可达时返回 {status:"DOWN", error} 以保持与前端兼容。
func (h *ClusterHandler) GetStatus(c *gin.Context) {
	res, err := h.service.Ping(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "DOWN",
			"error":  err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, res)
}

// GetPartitions GET /api/v1/slurm/partitions — 集群分区定义与分配信息
func (h *ClusterHandler) GetPartitions(c *gin.Context) {
	res, err := h.service.ListPartitions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}
