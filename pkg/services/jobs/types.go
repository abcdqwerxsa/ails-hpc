package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrInvalidResourceLimit = errors.New("Resource request exceeds maximum cluster limit")
	ErrNegativeResources    = errors.New("Resource limits must be non-negative")
	// ErrGPUPartition GPU 作业必须提交到 performance 分区（唯一 GPU 节点 node1 所在）。
	ErrGPUPartition = errors.New("GPU jobs require the performance partition")
	// ErrInvalidSpec 数组/依赖等高级选项语法非法（白名单外字符）。
	ErrInvalidSpec         = errors.New("invalid array/dependency spec")
	ErrJobNotFound         = errors.New("Job not found")
	ErrCannotHoldCancelled = errors.New("Cannot hold cancelled job")
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
	// MemoryMB 显式内存申请（MB，0=缺省 DefMemPerCPU 350/核；上限=节点 RealMemory）。
	MemoryMB int `json:"memory_mb"`
	// Gpus 申请 GPU 卡数（0=不申请）。GPU 仅在 performance 分区（node1）；>0 时分区
	// 必须 performance。slurm 21.08 REST 无 gres 提交字段（实测），走 CLI sbatch。
	Gpus int `json:"gpus"`
	// ArraySpec 作业数组（4.1）：sbatch --array 语法，如 "1-4"、"1-10%2"。非空走 CLI。
	ArraySpec string `json:"array_spec"`
	// Dependency 依赖（4.1）：sbatch --dependency 语法，如 "afterok:123"、"afterany:120:121"。非空走 CLI。
	Dependency              string        `json:"dependency"`
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

// JobDetail 作业详情（GET /jobs/:id/detail）：sacct 生命期数据 + 输出尾部。
type JobDetail struct {
	JobID      int    `json:"job_id"`
	Name       string `json:"name"`
	Owner      string `json:"owner"` // clusterUser（sacct User）
	Account    string `json:"account"`
	Partition  string `json:"partition"`
	State      string `json:"state"`
	ElapsedSec int    `json:"elapsed_sec"`
	ExitCode   string `json:"exit_code"` // "0:0" 形态
	Start      string `json:"start"`
	End        string `json:"end"`
	Submit     string `json:"submit"`
	// StdoutTail 输出文件尾部（/shared/jobs/<id>.out tail 200 行；空=尚无输出）。
	StdoutTail string `json:"stdout_tail"`
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
	Owner      string `json:"owner,omitempty"` // 归属隔离：提交者（slurm account 回填）
}

// JobListResponse 作业列表响应
type JobListResponse struct {
	Code int          `json:"code"`
	Jobs []JobSummary `json:"jobs"`
}
