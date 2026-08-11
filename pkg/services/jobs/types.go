package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrInvalidResourceLimit = errors.New("Resource request exceeds maximum cluster limit")
	ErrNegativeResources    = errors.New("Resource limits must be non-negative")
	ErrJobNotFound          = errors.New("Job not found")
	ErrCannotHoldCancelled  = errors.New("Cannot hold cancelled job")
)

// FlexTimeLimit 支持 JSON 解包时兼容 string ("3600") 和 int (3600)
type FlexTimeLimit string

func (f *FlexTimeLimit) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = FlexTimeLimit(s)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err == nil {
		*f = FlexTimeLimit(fmt.Sprintf("%d", n))
		return nil
	}
	return nil
}

func (f FlexTimeLimit) String() string {
	return string(f)
}

// SubmitJobRequest 统一的作业提交 HTTP 请求体
type SubmitJobRequest struct {
	Name                    string        `json:"name"`
	Partition               string        `json:"partition"`
	Nodes                   int           `json:"nodes"`
	Tasks                   int           `json:"tasks"`
	CPUs                    int           `json:"cpus"`
	CpusPerTask             int           `json:"cpus_per_task"`
	TimeLimit               FlexTimeLimit `json:"time_limit"`
	Script                  string        `json:"script"`
	CurrentWorkingDirectory string        `json:"current_working_directory"`
}

// SubmitJobResponse 作业提交成功响应
type SubmitJobResponse struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	JobID     int    `json:"job_id"`
	Name      string `json:"name,omitempty"`
	Status    string `json:"status,omitempty"`
	Nodes     int    `json:"nodes,omitempty"`
	CPUs      int    `json:"cpus,omitempty"`
	Partition string `json:"partition,omitempty"`
}

// JobControlResponse 作业控制（Cancel/Hold/Requeue）响应
type JobControlResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	JobID   int    `json:"job_id"`
	Action  string `json:"action"`
	Status  string `json:"status,omitempty"`
}

// JobSummary 单个作业概览
type JobSummary struct {
	JobID      int    `json:"job_id"`
	Name       string `json:"name"`
	Partition  string `json:"partition"`
	JobState   string `json:"job_state"`
	Nodes      string `json:"nodes"`
	TimeLimit  int    `json:"time_limit"`
	SubmitTime int64  `json:"submit_time"`
}

// JobListResponse 作业列表响应
type JobListResponse struct {
	Code int          `json:"code"`
	Jobs []JobSummary `json:"jobs"`
}
