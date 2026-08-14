package common

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MockJob represents a job object stored in memory by MockSlurmServer
type MockJob struct {
	JobID      int    `json:"job_id"`
	Name       string `json:"name"`
	Partition  string `json:"partition"`
	JobState   string `json:"job_state"` // PENDING, RUNNING, HELD, CANCELLED, COMPLETED
	Nodes      string `json:"nodes"`
	TimeLimit  int    `json:"time_limit"`
	SubmitTime int64  `json:"submit_time"`
	Script     string `json:"script"`
	Account    string `json:"account"`   // 提交时写入的 Slurm account（== clusterUser）
	User       string `json:"user_name"` // submit 请求的 X-SLURM-USER-NAME（per-user 身份）
}

// MockSlurmServer encapsulates an in-memory SlurmREST v0.0.37 HTTP mock server
type MockSlurmServer struct {
	Server    *httptest.Server
	URL       string
	mu        sync.RWMutex
	jobs      map[int]*MockJob
	nextJobID int

	lastControlUser string // 最近一次控制操作(cancel/hold/requeue)的 X-SLURM-USER-NAME
}

// NewMockSlurmServer initializes and starts a new MockSlurmServer instance
func NewMockSlurmServer() *MockSlurmServer {
	mock := &MockSlurmServer{
		jobs:      make(map[int]*MockJob),
		nextJobID: 1001,
	}

	mux := http.NewServeMux()

	// Ping endpoint
	mux.HandleFunc("/slurm/v0.0.37/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"meta": map[string]interface{}{
				"Slurm": map[string]string{"release": "20.11.7"},
			},
			"errors": []interface{}{},
			"pings": []map[string]interface{}{
				{
					"hostname": "slurmctld",
					"ping":     "UP",
					"status":   0,
					"mode":     "primary",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Get Nodes endpoint
	mux.HandleFunc("/slurm/v0.0.37/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"errors": []interface{}{},
			"nodes": []map[string]interface{}{
				{
					"name":        "node1",
					"state":       "IDLE",
					"cpus":        64,
					"real_memory": 128000,
					"cores":       32,
					"gres":        "gpu:1",
					"gres_used":   "gpu:0",
				},
				{
					"name":        "node2",
					"state":       "IDLE",
					"cpus":        64,
					"real_memory": 128000,
					"cores":       32,
				},
				{
					"name":        "node3",
					"state":       "IDLE",
					"cpus":        64,
					"real_memory": 128000,
					"cores":       32,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Get Partitions endpoint
	mux.HandleFunc("/slurm/v0.0.37/partitions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"errors": []interface{}{},
			"partitions": []map[string]interface{}{
				{"name": "standard", "nodes": "node1,node2,node3", "total_cpus": 192, "total_nodes": 3},
				{"name": "debug", "nodes": "node1", "total_cpus": 64, "total_nodes": 1},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// JobSubmit or GetJobs or JobControl handler router
	mux.HandleFunc("/slurm/v0.0.37/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			mock.handleGetJobs(w, r)
			return
		}
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/slurm/v0.0.37/job/submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mock.handleSubmitJob(w, r)
			return
		}
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})

	// Single job path: /slurm/v0.0.37/job/{job_id}
	mux.HandleFunc("/slurm/v0.0.37/job/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// Extract job_id from /slurm/v0.0.37/job/{job_id}
		trimPath := strings.TrimPrefix(path, "/slurm/v0.0.37/job/")
		if trimPath == "" {
			http.Error(w, "Job ID missing", http.StatusBadRequest)
			return
		}

		jobID, err := strconv.Atoi(trimPath)
		if err != nil {
			http.Error(w, "Invalid Job ID", http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodDelete:
			mock.handleCancelJob(w, r, jobID)
		case http.MethodPost:
			mock.handleControlJob(w, r, jobID)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	server := httptest.NewServer(mux)
	mock.Server = server
	mock.URL = server.URL
	return mock
}

// Close shuts down the underlying httptest server
func (m *MockSlurmServer) Close() {
	m.Server.Close()
}

// LastControlUser 返回最近一次控制操作的执行身份（X-SLURM-USER-NAME），L4 集成测试断言用。
func (m *MockSlurmServer) LastControlUser() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastControlUser
}

// GetJobs returns a slice of all stored jobs
func (m *MockSlurmServer) GetJobs() []*MockJob {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]*MockJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		res = append(res, j)
	}
	return res
}

// GetJob fetches a specific job by ID
func (m *MockSlurmServer) GetJob(jobID int) (*MockJob, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, exists := m.jobs[jobID]
	if !exists {
		return nil, false
	}
	return j, true
}

// SetJobState updates the state of a job in memory
func (m *MockSlurmServer) SetJobState(jobID int, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, exists := m.jobs[jobID]; exists {
		j.JobState = state
	}
}

// AddJob manually inserts a job into mock storage
func (m *MockSlurmServer) AddJob(j *MockJob) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.JobID] = j
}

func (m *MockSlurmServer) handleGetJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	m.mu.RLock()
	jobList := make([]map[string]interface{}, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobList = append(jobList, map[string]interface{}{
			"job_id":      j.JobID,
			"name":        j.Name,
			"partition":   j.Partition,
			"job_state":   j.JobState,
			"nodes":       j.Nodes,
			"time_limit":  j.TimeLimit,
			"submit_time": j.SubmitTime,
			"account":     j.Account,
			"user_name":   j.User,
		})
	}
	m.mu.RUnlock()

	resp := map[string]interface{}{
		"errors": []interface{}{},
		"jobs":   jobList,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (m *MockSlurmServer) handleSubmitJob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []string{"failed to read request body"},
		})
		return
	}

	var reqPayload struct {
		Script string `json:"script"`
		Job    struct {
			Name      string `json:"name"`
			Partition string `json:"partition"`
			Nodes     []int  `json:"nodes"`
			TimeLimit int    `json:"time_limit"`
			Account   string `json:"account"`
		} `json:"job"`
	}

	if err := json.Unmarshal(bodyBytes, &reqPayload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []string{"invalid JSON payload"},
		})
		return
	}

	m.mu.Lock()
	jobID := m.nextJobID
	m.nextJobID++

	nodesStr := "1"
	if len(reqPayload.Job.Nodes) > 0 {
		nodesStr = strconv.Itoa(reqPayload.Job.Nodes[0])
	}
	partition := reqPayload.Job.Partition
	if partition == "" {
		partition = "debug"
	}
	name := reqPayload.Job.Name
	if name == "" {
		name = fmt.Sprintf("job_%d", jobID)
	}
	timeLimit := reqPayload.Job.TimeLimit
	if timeLimit <= 0 {
		timeLimit = 3600
	}

	mockJob := &MockJob{
		JobID:      jobID,
		Name:       name,
		Partition:  partition,
		JobState:   "PENDING",
		Nodes:      nodesStr,
		TimeLimit:  timeLimit,
		SubmitTime: time.Now().Unix(),
		Script:     reqPayload.Script,
		Account:    reqPayload.Job.Account,
		User:       r.Header.Get("X-SLURM-USER-NAME"),
	}
	m.jobs[jobID] = mockJob
	m.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	resp := map[string]interface{}{
		"errors": []interface{}{},
		"job_id": jobID,
		"step_id": "batch",
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// l4Allowed 模拟 Slurm 的控制鉴权（L4）：令牌身份必须是作业属主或 root；
// 属主为空（遗留/手工注入作业）放行。不匹配返回 false（调用方回 403）。
func l4Allowed(jobUser, acting string) bool {
	return acting == "root" || jobUser == "" || jobUser == acting
}

func (m *MockSlurmServer) handleCancelJob(w http.ResponseWriter, r *http.Request, jobID int) {
	w.Header().Set("Content-Type", "application/json")

	m.mu.Lock()
	job, exists := m.jobs[jobID]
	if exists {
		m.lastControlUser = r.Header.Get("X-SLURM-USER-NAME")
	}
	if exists && !l4Allowed(job.User, r.Header.Get("X-SLURM-USER-NAME")) {
		m.mu.Unlock()
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []string{fmt.Sprintf("only root or job owner may cancel job %d", jobID)},
		})
		return
	}
	if !exists {
		m.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []string{fmt.Sprintf("Job %d not found", jobID)},
		})
		return
	}

	job.JobState = "CANCELLED"
	m.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"errors": []interface{}{},
	})
}

func (m *MockSlurmServer) handleControlJob(w http.ResponseWriter, r *http.Request, jobID int) {
	w.Header().Set("Content-Type", "application/json")

	m.mu.Lock()
	job, exists := m.jobs[jobID]
	if exists {
		m.lastControlUser = r.Header.Get("X-SLURM-USER-NAME")
	}
	if exists && !l4Allowed(job.User, r.Header.Get("X-SLURM-USER-NAME")) {
		m.mu.Unlock()
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []string{fmt.Sprintf("only root or job owner may modify job %d", jobID)},
		})
		return
	}
	if !exists {
		m.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []string{fmt.Sprintf("Job %d not found", jobID)},
		})
		return
	}

	bodyBytes, _ := io.ReadAll(r.Body)
	var ctrlReq struct {
		Hold     bool   `json:"hold"`
		Requeue  bool   `json:"requeue"`
		JobState string `json:"job_state"`
	}
	_ = json.Unmarshal(bodyBytes, &ctrlReq)

	if ctrlReq.Hold || ctrlReq.JobState == "HELD" {
		job.JobState = "HELD"
	} else if ctrlReq.Requeue || ctrlReq.JobState == "PENDING" {
		job.JobState = "PENDING"
	}

	m.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"errors": []interface{}{},
	})
}
