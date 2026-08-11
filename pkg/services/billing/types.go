package billing

import "time"

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
	User               string  `json:"user"`
	Project            string  `json:"project"`
	TotalCPUHours      float64 `json:"total_cpu_hours"`
	TotalMemoryGBHours float64 `json:"total_memory_gb_hours"`
	TotalGPUHours      float64 `json:"total_gpu_hours"`
	JobCount           int     `json:"job_count"`
	ContainerCount     int     `json:"container_count"`
}

// ExportQueryParam contains query parameters for /api/v1/slurm/billing/export
type ExportQueryParam struct {
	Format  string `form:"format"` // "json" or "chart"
	User    string `form:"user"`
	Project string `form:"project"`
}

// ExportJSONResponse represents the JSON export report payload
type ExportJSONResponse struct {
	Format     string  `json:"format"`      // Always "json"
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

// AccountAuditRecord tracks individual job or workspace billing audit items
type AccountAuditRecord struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"` // "job" or "container"
	User         string    `json:"user"`
	Project      string    `json:"project"`
	CPUs         int       `json:"cpus"`
	MemoryMB     int       `json:"memory_mb"`
	GPUs         int       `json:"gpus"`
	DurationSecs float64   `json:"duration_secs"`
	CreatedAt    time.Time `json:"created_at"`
}
