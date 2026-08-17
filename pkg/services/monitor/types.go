package monitor

// Resource 是某类资源（CPU/内存/GPU）的已分配/总量。
type Resource struct {
	Alloc int `json:"alloc"`
	Total int `json:"total"`
}

// Pct 返回分配百分比（总量为 0 时返回 0，避免除零）。
func (r Resource) Pct() int {
	if r.Total <= 0 {
		return 0
	}
	p := r.Alloc * 100 / r.Total
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// Disk 是共享文件系统（/shared）的用量。
type Disk struct {
	UsedKB  int `json:"used_kb"`
	TotalKB int `json:"total_kb"`
	Percent int `json:"percent"` // 0-100
}

// Snapshot 是监控页一次采样的资源聚合快照（CPU/内存/GPU/磁盘/队列深度）。
type Snapshot struct {
	CPU   Resource `json:"cpu"`
	Mem   Resource `json:"mem"`
	GPU   Resource `json:"gpu"`
	Disk  Disk     `json:"disk"`
	Queue int      `json:"queue"` // PENDING 作业数（队列深度，3.3）
}

// MonitorHistory 是监控趋势历史（oldest→newest，最多 360 个采样点；持久化时跨重启保留）。
type MonitorHistory struct {
	Timestamps []int64 `json:"timestamps"` // unix 秒
	CPU        []int   `json:"cpu"`        // 0-100
	Mem        []int   `json:"mem"`
	GPU        []int   `json:"gpu"`
	Disk       []int   `json:"disk"`
	Queue      []int   `json:"queue"` // PENDING 作业数（计数，非百分比）
}

// SnapshotResponse 是 GET /api/v1/slurm/monitor/snapshot 的响应体。
type SnapshotResponse struct {
	CPU   Resource `json:"cpu"`
	Mem   Resource `json:"mem"`
	GPU   Resource `json:"gpu"`
	Disk  Disk     `json:"disk"`
	Queue int      `json:"queue"`
}
