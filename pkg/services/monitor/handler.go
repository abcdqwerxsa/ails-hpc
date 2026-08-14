package monitor

import (
	"net/http"

	"ails-hpc/pkg/httpx"

	"github.com/gin-gonic/gin"
)

// MonitorHandler 暴露监控快照端点。
type MonitorHandler struct {
	service Service
}

// NewMonitorHandler 构造监控处理器。
func NewMonitorHandler(service Service) *MonitorHandler {
	return &MonitorHandler{service: service}
}

// GetSnapshot GET /api/v1/slurm/monitor/snapshot —— 返回当前 CPU/内存/GPU/磁盘 资源聚合。
func (h *MonitorHandler) GetSnapshot(c *gin.Context) {
	snap, err := h.service.Snapshot(c.Request.Context())
	if err != nil {
		httpx.Internal(c, "monitor.Snapshot", err)
		return
	}
	c.JSON(http.StatusOK, SnapshotResponse{
		CPU:  snap.CPU,
		Mem:  snap.Mem,
		GPU:  snap.GPU,
		Disk: snap.Disk,
	})
}

// GetHistory GET /api/v1/slurm/monitor/history —— 服务端滚动趋势（≤360 点，进程内存态）。
// 供监控页刷新后播种趋势图，不因页面刷新丢失窗口。
func (h *MonitorHandler) GetHistory(c *gin.Context) {
	c.JSON(http.StatusOK, h.service.History())
}
