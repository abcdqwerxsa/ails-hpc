package jobs_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ails-hpc/pkg/services/common"
	"ails-hpc/pkg/services/jobs"
	"ails-hpc/pkg/slurmrest"

	"github.com/gin-gonic/gin"
)

func setupTestRouter() (*common.MockSlurmServer, *gin.Engine) {
	gin.SetMode(gin.TestMode)
	mockServer := common.NewMockSlurmServer()

	client := slurmrest.NewClient(mockServer.URL, "hpcuser", "test-token")
	service := jobs.NewJobService(client)
	handler := jobs.NewJobHandler(service)

	router := gin.New()
	router.Use(gin.Recovery())

	slurm := router.Group("/api/v1/slurm")
	{
		slurm.POST("/jobs/submit", handler.SubmitJob)
		slurm.GET("/jobs", handler.ListJobs)
		slurm.POST("/jobs/:id/cancel", handler.CancelJob)
		slurm.POST("/jobs/:id/hold", handler.HoldJob)
		slurm.POST("/jobs/:id/requeue", handler.RequeueJob)
	}

	return mockServer, router
}

func TestSubmitJob(t *testing.T) {
	mockServer, router := setupTestRouter()
	defer mockServer.Close()

	reqBody := jobs.SubmitJobRequest{
		Name:      "test_job_1",
		Partition: "debug",
		Nodes:     1,
		Tasks:     2,
		TimeLimit: "3600",
		Script:    "#!/bin/bash\necho hello",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/submit", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	var resp jobs.SubmitJobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.JobID <= 0 {
		t.Errorf("expected positive job_id, got %d", resp.JobID)
	}

	// Verify mock state
	job, exists := mockServer.GetJob(resp.JobID)
	if !exists {
		t.Fatalf("job %d not found in mock server", resp.JobID)
	}
	if job.Name != "test_job_1" {
		t.Errorf("expected job name 'test_job_1', got '%s'", job.Name)
	}
	if job.JobState != "PENDING" {
		t.Errorf("expected job state 'PENDING', got '%s'", job.JobState)
	}
}

func TestListJobs(t *testing.T) {
	mockServer, router := setupTestRouter()
	defer mockServer.Close()

	// Insert mock job directly
	mockServer.AddJob(&common.MockJob{
		JobID:     1001,
		Name:      "job_1001",
		Partition: "debug",
		JobState:  "RUNNING",
		Nodes:     "node1",
		TimeLimit: 3600,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/slurm/jobs", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp jobs.JobListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(resp.Jobs))
	}
	if resp.Jobs[0].JobID != 1001 {
		t.Errorf("expected job_id 1001, got %d", resp.Jobs[0].JobID)
	}
}

func TestHoldJob(t *testing.T) {
	mockServer, router := setupTestRouter()
	defer mockServer.Close()

	mockServer.AddJob(&common.MockJob{
		JobID:    1001,
		Name:     "job_1001",
		JobState: "PENDING",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/1001/hold", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp jobs.JobControlResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Action != "hold" {
		t.Errorf("expected action 'hold', got '%s'", resp.Action)
	}

	job, _ := mockServer.GetJob(1001)
	if job.JobState != "HELD" {
		t.Errorf("expected mock job state 'HELD', got '%s'", job.JobState)
	}
}

func TestRequeueJob(t *testing.T) {
	mockServer, router := setupTestRouter()
	defer mockServer.Close()

	mockServer.AddJob(&common.MockJob{
		JobID:    1001,
		Name:     "job_1001",
		JobState: "HELD",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/1001/requeue", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp jobs.JobControlResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Action != "requeue" {
		t.Errorf("expected action 'requeue', got '%s'", resp.Action)
	}

	job, _ := mockServer.GetJob(1001)
	if job.JobState != "PENDING" {
		t.Errorf("expected mock job state 'PENDING', got '%s'", job.JobState)
	}
}

func TestCancelJob(t *testing.T) {
	mockServer, router := setupTestRouter()
	defer mockServer.Close()

	mockServer.AddJob(&common.MockJob{
		JobID:    1001,
		Name:     "job_1001",
		JobState: "RUNNING",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/1001/cancel", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp jobs.JobControlResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Action != "cancel" {
		t.Errorf("expected action 'cancel', got '%s'", resp.Action)
	}

	job, _ := mockServer.GetJob(1001)
	if job.JobState != "CANCELLED" {
		t.Errorf("expected mock job state 'CANCELLED', got '%s'", job.JobState)
	}
}

func TestSubmitJob_WithQOS_MockServer(t *testing.T) {
	mockServer, router := setupTestRouter()
	defer mockServer.Close()

	reqBody := jobs.SubmitJobRequest{
		Name:      "test_job_qos",
		Partition: "standard",
		Nodes:     1,
		Tasks:     1,
		TimeLimit: "3600",
		Script:    "#!/bin/bash\necho hello",
		QOS:       "vip",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/submit", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	var resp jobs.SubmitJobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	job, exists := mockServer.GetJob(resp.JobID)
	if !exists {
		t.Fatalf("job %d not found in mock server", resp.JobID)
	}
	if job.QOS != "vip" {
		t.Errorf("expected mock job QOS 'vip', got %q", job.QOS)
	}
}

func TestListJobs_WithQOS(t *testing.T) {
	mockServer, router := setupTestRouter()
	defer mockServer.Close()

	mockServer.AddJob(&common.MockJob{
		JobID:     1002,
		Name:      "job_qos_1002",
		Partition: "standard",
		JobState:  "RUNNING",
		Nodes:     "node2",
		TimeLimit: 3600,
		QOS:       "gpu-short",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/slurm/jobs", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp jobs.JobListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(resp.Jobs))
	}
	if resp.Jobs[0].QOS != "gpu-short" {
		t.Errorf("expected job QOS 'gpu-short', got %q", resp.Jobs[0].QOS)
	}
}
