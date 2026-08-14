package billing

// UsageQueryParam contains request query filters for /api/v1/slurm/billing/usage
type UsageQueryParam struct {
	User      string `form:"user"`
	Project   string `form:"project"`
	StartTime string `form:"start_time"`
	EndTime   string `form:"end_time"`
	Limit     int    `form:"limit"`
	Format    string `form:"format"`
}

// UsageResponse represents the payload returned by GET /api/v1/slurm/billing/usage
type UsageResponse struct {
	User               string           `json:"user"`
	Project            string           `json:"project"`
	TotalCPUHours      float64          `json:"total_cpu_hours"`
	TotalMemoryGBHours float64          `json:"total_memory_gb_hours"`
	TotalGPUHours      float64          `json:"total_gpu_hours"`
	JobCount           int              `json:"job_count"`
	ContainerCount     int              `json:"container_count"`
	Breakdown          []UsageBreakdown `json:"breakdown"`
}

// UsageBreakdown 是按 (用户, Slurm account) 聚合的用量明细。
type UsageBreakdown struct {
	User       string  `json:"user"`
	Account    string  `json:"account"`
	CpuHours   float64 `json:"cpu_hours"`
	MemGBHours float64 `json:"mem_gb_hours"`
	GpuHours   float64 `json:"gpu_hours"`
	JobCount   int     `json:"job_count"`
}

// ExportQueryParam contains query parameters for /api/v1/slurm/billing/export
type ExportQueryParam struct {
	Format  string `form:"format"` // "json" or "chart"
	User    string `form:"user"`
	Project string `form:"project"`
}

// ExportJSONResponse represents the JSON export report payload
type ExportJSONResponse struct {
	Format     string  `json:"format"` // Always "json"
	User       string  `json:"user"`
	Timestamp  string  `json:"timestamp"`   // RFC3339 format
	TotalCost  float64 `json:"total_cost"`  // Total calculated cost
	Currency   string  `json:"currency"`    // "CNY"
	JobCount   int     `json:"job_count"`   // Recorded job count
	CtrCount   int     `json:"ctr_count"`   // Recorded container count
	ExportedBy string  `json:"exported_by"` // "slurm-billing-auditor" (Required by E2E assertion)
}

// ExportChartResponse represents chart series data payload
type ExportChartResponse struct {
	Format string    `json:"format"` // Always "chart"
	Labels []string  `json:"labels"` // e.g. ["Jobs", "Containers", "GPU Workloads"]
	Series []float64 `json:"series"` // e.g. [10.0, 5.0, 1.5]
}

// SacctRow 是解析后的一条 sacct 作业记录（来自 --parsable2 --noheader 输出），
// 字段顺序与 service.go 的 sacctFormat 一致。
type SacctRow struct {
	JobID      string
	User       string
	Account    string
	Partition  string
	JobName    string
	State      string
	ElapsedRaw int64 // 秒
	AllocCPUS  int
	AllocTRES  string // 如 "cpu=4,mem=...,gres/gpu=1,node=1"
	ReqMem     string // 如 "3000M"、"4G"、"0"
	Start      string
	End        string
}
