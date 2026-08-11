package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Global job & resource counter for atomic ID generation
var (
	globalJobID int64 = 1000
)

// In-Memory state for opaque-box test server
type TestServerState struct {
	mu         sync.Mutex
	Jobs       map[int64]*JobState
	Nodes      map[string]*NodeState
	Containers map[string]*ContainerState
}

type JobState struct {
	ID        int64     `json:"job_id"`
	Name      string    `json:"name"`
	Script    string    `json:"script"`
	Status    string    `json:"status"` // SUBMITTED, PENDING, RUNNING, HELD, CANCELLED, COMPLETED
	Nodes     int       `json:"nodes"`
	CPUs      int       `json:"cpus"`
	Partition string    `json:"partition"`
	CreatedAt time.Time `json:"created_at"`
}

type NodeState struct {
	Name     string `json:"name"`
	State    string `json:"state"` // IDLE, ALLOCATED, DRAIN, RESUME, DOWN
	CPUs     int    `json:"cpus"`
	MemoryMB int    `json:"memory_mb"`
}

type ContainerState struct {
	ID        string    `json:"container_id"`
	EnvType   string    `json:"env_type"` // vscode, jupyter
	Status    string    `json:"status"`   // RUNNING, TERMINATED
	WebURL    string    `json:"web_url"`
	Token     string    `json:"token"`
	Nodes     int       `json:"nodes"`
	CPUs      int       `json:"cpus"`
	MemoryMB  int       `json:"memory_mb"`
	CreatedAt time.Time `json:"created_at"`
}

func NewTestServerState() *TestServerState {
	return &TestServerState{
		Jobs: make(map[int64]*JobState),
		Nodes: map[string]*NodeState{
			"node1": {Name: "node1", State: "IDLE", CPUs: 64, MemoryMB: 128000},
			"node2": {Name: "node2", State: "IDLE", CPUs: 64, MemoryMB: 128000},
			"node3": {Name: "node3", State: "IDLE", CPUs: 64, MemoryMB: 128000},
		},
		Containers: make(map[string]*ContainerState),
	}
}

// TestHarness encapsulates target URL and HTTP client for E2E testing
type TestHarness struct {
	BaseURL    string
	Client     *http.Client
	TestServer *httptest.Server
	State      *TestServerState
}

// SetupHarness initializes E2E test harness connecting to APISERVER_URL or spinning up httptest.Server
func SetupHarness(t *testing.T) *TestHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	targetURL := os.Getenv("APISERVER_URL")
	if targetURL != "" {
		return &TestHarness{
			BaseURL: targetURL,
			Client:  &http.Client{Timeout: 10 * time.Second},
			State:   NewTestServerState(),
		}
	}

	state := NewTestServerState()
	router := gin.New()
	router.Use(gin.Recovery())

	// Configure REST Endpoints per PROJECT.md interface contracts
	slurm := router.Group("/api/v1/slurm")
	{
		// Jobs Endpoints
		slurm.POST("/jobs/submit", func(c *gin.Context) {
			var body struct {
				Name      string `json:"name"`
				Script    string `json:"script"`
				Nodes     int    `json:"nodes"`
				CPUs      int    `json:"cpus"`
				Partition string `json:"partition"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
				return
			}
			if body.Script == "" && body.Name == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Job script or name is required"})
				return
			}
			if body.Nodes < 0 || body.CPUs < 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Resource limits must be non-negative"})
				return
			}
			if body.CPUs > 1000 || body.Nodes > 100 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Resource request exceeds maximum cluster limit"})
				return
			}

			if body.Nodes <= 0 {
				body.Nodes = 1
			}
			if body.CPUs <= 0 {
				body.CPUs = 1
			}
			if body.Partition == "" {
				body.Partition = "standard"
			}

			id := atomic.AddInt64(&globalJobID, 1)
			job := &JobState{
				ID:        id,
				Name:      body.Name,
				Script:    body.Script,
				Status:    "SUBMITTED",
				Nodes:     body.Nodes,
				CPUs:      body.CPUs,
				Partition: body.Partition,
				CreatedAt: time.Now(),
			}

			state.mu.Lock()
			state.Jobs[id] = job
			state.mu.Unlock()

			c.JSON(http.StatusOK, gin.H{
				"job_id":    job.ID,
				"name":      job.Name,
				"status":    job.Status,
				"nodes":     job.Nodes,
				"cpus":      job.CPUs,
				"partition": job.Partition,
			})
		})

		slurm.POST("/jobs/:id/cancel", func(c *gin.Context) {
			idStr := c.Param("id")
			var id int64
			if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Job ID"})
				return
			}

			state.mu.Lock()
			job, exists := state.Jobs[id]
			if !exists {
				state.mu.Unlock()
				c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
				return
			}
			job.Status = "CANCELLED"
			state.mu.Unlock()

			c.JSON(http.StatusOK, gin.H{
				"job_id":  job.ID,
				"status":  "CANCELLED",
				"message": fmt.Sprintf("Job %d cancelled successfully", job.ID),
			})
		})

		slurm.POST("/jobs/:id/hold", func(c *gin.Context) {
			idStr := c.Param("id")
			var id int64
			if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Job ID"})
				return
			}

			state.mu.Lock()
			job, exists := state.Jobs[id]
			if !exists {
				state.mu.Unlock()
				c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
				return
			}
			if job.Status == "CANCELLED" {
				state.mu.Unlock()
				c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot hold cancelled job"})
				return
			}
			job.Status = "HELD"
			state.mu.Unlock()

			c.JSON(http.StatusOK, gin.H{
				"job_id":  job.ID,
				"status":  "HELD",
				"message": fmt.Sprintf("Job %d placed on hold", job.ID),
			})
		})

		slurm.POST("/jobs/:id/requeue", func(c *gin.Context) {
			idStr := c.Param("id")
			var id int64
			if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Job ID"})
				return
			}

			state.mu.Lock()
			job, exists := state.Jobs[id]
			if !exists {
				state.mu.Unlock()
				c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
				return
			}
			job.Status = "PENDING"
			state.mu.Unlock()

			c.JSON(http.StatusOK, gin.H{
				"job_id":  job.ID,
				"status":  "PENDING",
				"message": fmt.Sprintf("Job %d requeued successfully", job.ID),
			})
		})

		slurm.GET("/jobs", func(c *gin.Context) {
			state.mu.Lock()
			list := make([]*JobState, 0, len(state.Jobs))
			for _, j := range state.Jobs {
				list = append(list, j)
			}
			state.mu.Unlock()
			c.JSON(http.StatusOK, gin.H{"jobs": list})
		})

		// Nodes Endpoints
		slurm.GET("/nodes", func(c *gin.Context) {
			state.mu.Lock()
			list := make([]*NodeState, 0, len(state.Nodes))
			for _, n := range state.Nodes {
				list = append(list, n)
			}
			state.mu.Unlock()
			c.JSON(http.StatusOK, gin.H{"nodes": list})
		})

		slurm.POST("/nodes/:name/state", func(c *gin.Context) {
			name := c.Param("name")
			if name == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Node name required"})
				return
			}

			var body struct {
				State string `json:"state"`
			}
			if err := c.ShouldBindJSON(&body); err != nil || body.State == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "State payload required"})
				return
			}

			if body.State != "DRAIN" && body.State != "RESUME" && body.State != "IDLE" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid node state. Supported: DRAIN, RESUME, IDLE"})
				return
			}

			state.mu.Lock()
			node, exists := state.Nodes[name]
			if !exists {
				state.mu.Unlock()
				c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Node %s not found", name)})
				return
			}
			if body.State == "RESUME" {
				node.State = "IDLE"
			} else {
				node.State = body.State
			}
			currentState := node.State
			state.mu.Unlock()

			c.JSON(http.StatusOK, gin.H{
				"node_name": name,
				"state":     currentState,
				"message":   fmt.Sprintf("Node %s state updated to %s", name, currentState),
			})
		})

		// Containers Endpoints
		slurm.POST("/containers/launch", func(c *gin.Context) {
			var body struct {
				EnvType  string `json:"env_type"`
				Nodes    int    `json:"nodes"`
				CPUs     int    `json:"cpus"`
				MemoryMB int    `json:"memory_mb"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
				return
			}

			if body.EnvType != "vscode" && body.EnvType != "jupyter" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported env_type. Expected 'vscode' or 'jupyter'"})
				return
			}

			if body.CPUs < 0 || body.MemoryMB < 0 || body.Nodes < 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Resource amounts cannot be negative"})
				return
			}

			if body.CPUs > 512 || body.MemoryMB > 1000000 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Requested resources exceed workspace quota"})
				return
			}

			if body.Nodes <= 0 {
				body.Nodes = 1
			}
			if body.CPUs <= 0 {
				body.CPUs = 2
			}
			if body.MemoryMB <= 0 {
				body.MemoryMB = 4096
			}

			containerID := fmt.Sprintf("c-%d", time.Now().UnixNano()%1000000)
			jwtToken := fmt.Sprintf("jwt-auth-token-%s-%d", body.EnvType, time.Now().UnixNano()%100000)
			webURL := ""
			if body.EnvType == "vscode" {
				webURL = fmt.Sprintf("http://192.168.20.226:8080/vscode/?token=%s&cpus=%d", jwtToken, body.CPUs)
			} else {
				webURL = fmt.Sprintf("http://192.168.20.226:8888/lab?token=%s&cpus=%d", jwtToken, body.CPUs)
			}

			ctr := &ContainerState{
				ID:        containerID,
				EnvType:   body.EnvType,
				Status:    "RUNNING",
				WebURL:    webURL,
				Token:     jwtToken,
				Nodes:     body.Nodes,
				CPUs:      body.CPUs,
				MemoryMB:  body.MemoryMB,
				CreatedAt: time.Now(),
			}

			state.mu.Lock()
			state.Containers[containerID] = ctr
			state.mu.Unlock()

			c.JSON(http.StatusOK, gin.H{
				"container_id": ctr.ID,
				"env_type":     ctr.EnvType,
				"status":       ctr.Status,
				"web_url":      ctr.WebURL,
				"token":        ctr.Token,
				"allocated":    ctr,
			})
		})

		slurm.GET("/containers/list", func(c *gin.Context) {
			state.mu.Lock()
			list := make([]*ContainerState, 0)
			for _, ctr := range state.Containers {
				if ctr.Status == "RUNNING" {
					list = append(list, ctr)
				}
			}
			state.mu.Unlock()
			c.JSON(http.StatusOK, gin.H{"containers": list})
		})

		slurm.DELETE("/containers/:id", func(c *gin.Context) {
			id := c.Param("id")
			if id == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Container ID required"})
				return
			}

			state.mu.Lock()
			ctr, exists := state.Containers[id]
			if !exists || ctr.Status == "TERMINATED" {
				state.mu.Unlock()
				c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Container %s not found or already recycled", id)})
				return
			}
			ctr.Status = "TERMINATED"
			state.mu.Unlock()

			c.JSON(http.StatusOK, gin.H{
				"container_id": id,
				"status":       "TERMINATED",
				"message":      fmt.Sprintf("Container %s recycled successfully", id),
			})
		})

		// Billing Endpoints
		slurm.GET("/billing/usage", func(c *gin.Context) {
			user := c.DefaultQuery("user", "hpcuser")
			project := c.DefaultQuery("project", "default")
			format := c.Query("format")
			if format != "" && format != "json" && format != "chart" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid format query parameter"})
				return
			}

			startTimeStr := c.Query("start_time")
			endTimeStr := c.Query("end_time")
			if startTimeStr != "" && endTimeStr != "" && startTimeStr > endTimeStr {
				c.JSON(http.StatusBadRequest, gin.H{"error": "start_time cannot be greater than end_time"})
				return
			}

			limitStr := c.Query("limit")
			if limitStr != "" && (limitStr == "-1" || limitStr[0] == '-') {
				c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be non-negative"})
				return
			}

			state.mu.Lock()
			totalCPUs := 0
			for _, j := range state.Jobs {
				totalCPUs += j.CPUs
			}
			for _, ctr := range state.Containers {
				totalCPUs += ctr.CPUs
			}
			state.mu.Unlock()

			cpuHours := float64(totalCPUs) * 1.5
			memGBHours := cpuHours * 4.0
			gpuHours := float64(len(state.Containers)) * 0.5

			c.JSON(http.StatusOK, gin.H{
				"user":                  user,
				"project":               project,
				"total_cpu_hours":       cpuHours,
				"total_memory_gb_hours": memGBHours,
				"total_gpu_hours":       gpuHours,
				"job_count":             len(state.Jobs),
				"container_count":       len(state.Containers),
			})
		})

		slurm.GET("/billing/export", func(c *gin.Context) {
			format := c.DefaultQuery("format", "json")
			user := c.DefaultQuery("user", "hpcuser")

			if format != "json" && format != "chart" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid export format. Supported: json, chart"})
				return
			}

			state.mu.Lock()
			jobCount := len(state.Jobs)
			containerCount := len(state.Containers)
			state.mu.Unlock()

			if format == "chart" {
				c.JSON(http.StatusOK, gin.H{
					"format": "chart",
					"labels": []string{"Jobs", "Containers", "GPU Workloads"},
					"series": []float64{float64(jobCount * 2), float64(containerCount * 4), 1.5},
				})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"format":      "json",
				"user":        user,
				"timestamp":   time.Now().Format(time.RFC3339),
				"total_cost":  float64(jobCount)*0.5 + float64(containerCount)*1.2,
				"currency":    "CNY",
				"job_count":   jobCount,
				"ctr_count":   containerCount,
				"exported_by": "slurm-billing-auditor",
			})
		})
	}

	ts := httptest.NewServer(router)
	t.Cleanup(func() {
		ts.Close()
	})

	return &TestHarness{
		BaseURL:    ts.URL,
		Client:     ts.Client(),
		TestServer: ts,
		State:      state,
	}
}

// HTTP Helper Methods for opaque-box test calls

func (h *TestHarness) DoPost(path string, body interface{}) (*http.Response, map[string]interface{}, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		bodyReader = bytes.NewBuffer(b)
	}

	req, err := http.NewRequest(http.MethodPost, h.BaseURL+path, bodyReader)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, nil, err
	}

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return resp, nil, err
	}

	var jsonResult map[string]interface{}
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &jsonResult)
	}
	return resp, jsonResult, nil
}

func (h *TestHarness) DoGet(path string) (*http.Response, map[string]interface{}, error) {
	req, err := http.NewRequest(http.MethodGet, h.BaseURL+path, nil)
	if err != nil {
		return nil, nil, err
	}

	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, nil, err
	}

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return resp, nil, err
	}

	var jsonResult map[string]interface{}
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &jsonResult)
	}
	return resp, jsonResult, nil
}

func (h *TestHarness) DoDelete(path string) (*http.Response, map[string]interface{}, error) {
	req, err := http.NewRequest(http.MethodDelete, h.BaseURL+path, nil)
	if err != nil {
		return nil, nil, err
	}

	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, nil, err
	}

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return resp, nil, err
	}

	var jsonResult map[string]interface{}
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &jsonResult)
	}
	return resp, jsonResult, nil
}
