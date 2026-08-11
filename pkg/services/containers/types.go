package containers

import "time"

// ContainerInstance represents an active or terminated Web IDE container workspace instance
type ContainerInstance struct {
	ID        string    `json:"container_id"`
	EnvType   string    `json:"env_type"` // "vscode" or "jupyter"
	Status    string    `json:"status"`   // "RUNNING" or "TERMINATED"
	WebURL    string    `json:"web_url"`
	Token     string    `json:"token"`
	Nodes     int       `json:"nodes"`
	CPUs      int       `json:"cpus"`
	MemoryMB  int       `json:"memory_mb"`
	CreatedAt time.Time `json:"created_at"`
}

// ContainerLaunchRequest defines the request body for launching a new workspace container
type ContainerLaunchRequest struct {
	EnvType  string `json:"env_type" binding:"required"` // "vscode" or "jupyter"
	Nodes    int    `json:"nodes"`                       // Default: 1
	CPUs     int    `json:"cpus"`                        // Default: 2
	MemoryMB int    `json:"memory_mb"`                   // Default: 4096
}

// ContainerLaunchResponse defines the response payload for a launched workspace container
type ContainerLaunchResponse struct {
	ContainerID string             `json:"container_id"`
	EnvType     string             `json:"env_type"`
	Status      string             `json:"status"`
	WebURL      string             `json:"web_url"`
	Token       string             `json:"token"`
	Allocated   *ContainerInstance `json:"allocated"`
}

// ContainerListResponse defines the response payload for listing active containers
type ContainerListResponse struct {
	Containers []*ContainerInstance `json:"containers"`
}

// ContainerRecycleResponse defines the response payload for recycling a container
type ContainerRecycleResponse struct {
	ContainerID string `json:"container_id"`
	Status      string `json:"status"`
	Message     string `json:"message"`
}
