package nodes

import "time"

// NodeStateInfo represents a single Slurm compute node status and resource specification
type NodeStateInfo struct {
	Name       string    `json:"name"`
	State      string    `json:"state"` // IDLE, ALLOCATED, DRAIN, RESUME, DOWN, MIXED
	CPUs       int       `json:"cpus"`
	RealMemory int       `json:"real_memory"`
	Cores      int       `json:"cores"`
	Reason     string    `json:"reason,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

// NodesListResponse defines the API response payload for listing nodes
type NodesListResponse struct {
	Nodes []*NodeStateInfo `json:"nodes"`
}

// NodeStateUpdateRequest defines the request body for modifying node state
type NodeStateUpdateRequest struct {
	State  string `json:"state" binding:"required"` // DRAIN, RESUME, IDLE
	Reason string `json:"reason,omitempty"`         // Optional reason for state change
}

// NodeStateUpdateResponse defines the API response payload for node state modification
type NodeStateUpdateResponse struct {
	NodeName string `json:"node_name"`
	State    string `json:"state"`
	Message  string `json:"message"`
}
