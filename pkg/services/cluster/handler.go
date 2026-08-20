package cluster

import (
	"log"
	"net/http"

	"ails-hpc/pkg/httpx"

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
		// 503 是面向客户端的"集群不可达"信号 + status:"DOWN"。P2：真实错误（含
		// slurmrestd 响应细节）只落服务端日志，客户端文案固定。
		log.Printf("cluster ping failed: %v", err)
		httpx.ServiceUnavailable(c, "slurmrestd unreachable", httpx.Extra{"status": "DOWN"})
		return
	}
	c.JSON(http.StatusOK, res)
}

// GetPartitions GET /api/v1/slurm/partitions — 集群分区定义与分配信息
func (h *ClusterHandler) GetPartitions(c *gin.Context) {
	res, err := h.service.ListPartitions(c.Request.Context())
	if err != nil {
		httpx.Internal(c, "GetPartitions", err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// Readyz GET /readyz —— readiness 探针，免鉴权（与 /healthz 同在 engine 根级）。
// liveness /healthz 只看"进程在跑"；readiness 探 slurmrestd 可达性，反映"能否提供完整功能"：
// 可达 → 200 {"status":"ready"}；不可达 → 503 {"status":"degraded",...}。供 future LB/k8s 探活。
func (h *ClusterHandler) Readyz(c *gin.Context) {
	if _, err := h.service.Ping(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "error": "slurmrestd unreachable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
