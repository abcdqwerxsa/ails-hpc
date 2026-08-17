package containers

import "time"

// ContainerInstance 表示一个作为 Slurm 作业运行的交互式开发会话
// （Jupyter Lab / code-server）。"Container" 为历史命名，实际是 Slurm 交互会话。
type ContainerInstance struct {
	ID        string    `json:"container_id"` // 会话 uuid（apiserver 生成）
	EnvType   string    `json:"env_type"`      // jupyter | vscode
	Status    string    `json:"status"`        // STARTING | RUNNING | STOPPED
	WebURL    string    `json:"web_url"`       // /api/v1/ide/<session>/
	JobID     int       `json:"job_id"`
	Node      string    `json:"node,omitempty"`
	Owner     string    `json:"owner,omitempty"` // 会话属主 clusterUser（Phase 4 列表租户过滤）
	Nodes     int       `json:"nodes"`
	CPUs      int       `json:"cpus"`
	MemoryMB  int       `json:"memory_mb"`
	CreatedAt time.Time `json:"created_at"`
}

// SessionMeta 是作业脚本写到 /shared/sessions/<id>.json 的连接信息，
// apiserver 据此反向代理到计算节点上的应用 server。
type SessionMeta struct {
	SessionID string `json:"session_id"`
	JobID     int    `json:"job_id"`
	Node      string `json:"node"`
	NodeIP    string `json:"node_ip"`
	Port      int    `json:"port"`
	EnvType   string `json:"env_type"`
	CPUs      int    `json:"cpus"`
	MemoryMB  int    `json:"memory_mb"`
	Nodes     int    `json:"nodes"`
	Owner     string `json:"owner"` // 归属隔离：提交者 clusterUser（unix 身份，launch 时写入）
}

// ContainerLaunchRequest defines the request body for launching an interactive session
type ContainerLaunchRequest struct {
	EnvType  string `json:"env_type" binding:"required"` // jupyter | vscode
	Nodes    int    `json:"nodes"`                       // 默认 1
	CPUs     int    `json:"cpus"`                        // 默认 2
	MemoryMB int    `json:"memory_mb"`                   // 默认 4096
}

// ContainerLaunchResponse defines the response payload for a launched session
type ContainerLaunchResponse struct {
	ContainerID string             `json:"container_id"`
	EnvType     string             `json:"env_type"`
	Status      string             `json:"status"`
	WebURL      string             `json:"web_url"`
	Allocated   *ContainerInstance `json:"allocated"`
}

// ContainerListResponse defines the response payload for listing active sessions
type ContainerListResponse struct {
	Containers []*ContainerInstance `json:"containers"`
}

// ContainerRecycleResponse defines the response payload for recycling a session
type ContainerRecycleResponse struct {
	ContainerID string `json:"container_id"`
	Status      string `json:"status"`
	Message     string `json:"message"`
}
