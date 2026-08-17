package jobs_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"ails-hpc/pkg/services/jobs"
	"ails-hpc/pkg/slurmrest"

	"github.com/gin-gonic/gin"
)

// TestAdversarial_StateTransitions tests full state lifecycle and invalid state transitions
func TestAdversarial_StateTransitions(t *testing.T) {
	mockServer, router := setupTestRouter()
	defer mockServer.Close()

	// 1. Submit job (SUBMITTED / PENDING)
	reqBody := jobs.SubmitJobRequest{
		Name:      "lifecycle_job",
		Partition: "normal",
		Nodes:     2,
		Tasks:     4,
		TimeLimit: "1800",
		Script:    "#!/bin/bash\nsleep 10",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/submit", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Submit failed: expected 200, got %d", w.Code)
	}

	var submitResp jobs.SubmitJobResponse
	_ = json.Unmarshal(w.Body.Bytes(), &submitResp)
	jobID := submitResp.JobID

	// Verify state is PENDING
	job, exists := mockServer.GetJob(jobID)
	if !exists || job.JobState != "PENDING" {
		t.Fatalf("Expected initial state PENDING, got %s (exists=%v)", job.JobState, exists)
	}

	// 2. Hold job -> state HELD
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", fmt.Sprintf("/api/v1/slurm/jobs/%d/hold", jobID), nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Hold failed: expected 200, got %d", w.Code)
	}
	job, _ = mockServer.GetJob(jobID)
	if job.JobState != "HELD" {
		t.Fatalf("Expected state HELD after hold, got %s", job.JobState)
	}

	// 3. Requeue job -> state PENDING
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", fmt.Sprintf("/api/v1/slurm/jobs/%d/requeue", jobID), nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Requeue failed: expected 200, got %d", w.Code)
	}
	job, _ = mockServer.GetJob(jobID)
	if job.JobState != "PENDING" {
		t.Fatalf("Expected state PENDING after requeue, got %s", job.JobState)
	}

	// 4. Cancel job -> state CANCELLED
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", fmt.Sprintf("/api/v1/slurm/jobs/%d/cancel", jobID), nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Cancel failed: expected 200, got %d", w.Code)
	}
	job, _ = mockServer.GetJob(jobID)
	if job.JobState != "CANCELLED" {
		t.Fatalf("Expected state CANCELLED after cancel, got %s", job.JobState)
	}
}

// TestAdversarial_InvalidInputs tests malformed, missing, or out-of-range inputs
func TestAdversarial_InvalidInputs(t *testing.T) {
	mockServer, router := setupTestRouter()
	defer mockServer.Close()

	t.Run("Non-numeric Job ID in Cancel", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/abc/cancel", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected 400 Bad Request for non-numeric job ID, got %d", w.Code)
		}
	})

	t.Run("Negative Job ID in Hold", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/-5/hold", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected 400 Bad Request for negative job ID, got %d", w.Code)
		}
	})

	t.Run("Zero Job ID in Requeue", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/0/requeue", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected 400 Bad Request for job ID 0, got %d", w.Code)
		}
	})

	t.Run("Malformed JSON on Submission", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/submit", bytes.NewBufferString("{invalid_json}"))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected 400 Bad Request for malformed JSON, got %d", w.Code)
		}
	})

	t.Run("Missing Required Fields (Name & Script)", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/submit", bytes.NewBufferString("{}"))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected 400 Bad Request when missing required fields name and script, got %d", w.Code)
		}
	})

	t.Run("Invalid TimeLimit String Defaults Gracefully", func(t *testing.T) {
		reqBody := jobs.SubmitJobRequest{
			Name:      "test_job_invalid_time",
			Script:    "#!/bin/bash\necho test",
			TimeLimit: "invalid_string",
		}
		bodyBytes, _ := json.Marshal(reqBody)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/submit", bytes.NewBuffer(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Expected 200 OK, got %d", w.Code)
		}
	})
}

// TestAdversarial_NonExistentJob tests operations on job IDs that do not exist
func TestAdversarial_NonExistentJob(t *testing.T) {
	mockServer, router := setupTestRouter()
	defer mockServer.Close()

	nonExistentID := 99999

	// Phase 4 起 forbidIfNotOwner 先做 JobOwner 预检：不存在的作业 → 404（此前 500）。
	notFound := func(name, path string) {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", fmt.Sprintf(path, nonExistentID), nil)
			router.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound {
				t.Errorf("Expected 404 for non-existent job, got %d", w.Code)
			}
		})
	}
	notFound("Cancel Non-Existent Job", "/api/v1/slurm/jobs/%d/cancel")
	notFound("Hold Non-Existent Job", "/api/v1/slurm/jobs/%d/hold")
	notFound("Requeue Non-Existent Job", "/api/v1/slurm/jobs/%d/requeue")
}

// TestAdversarial_BackendFailure tests resilience when SlurmRESTd is unreachable
func TestAdversarial_BackendFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Client pointing to an unlistenable port
	deadClient := slurmrest.NewClient("http://127.0.0.1:59999", "user", "token")
	service := jobs.NewJobService(deadClient)
	handler := jobs.NewJobHandler(service)

	router := gin.New()
	router.POST("/api/v1/slurm/jobs/submit", handler.SubmitJob)
	router.GET("/api/v1/slurm/jobs", handler.ListJobs)
	router.POST("/api/v1/slurm/jobs/:id/cancel", handler.CancelJob)

	t.Run("Submit Job Backend Unreachable", func(t *testing.T) {
		reqBody := jobs.SubmitJobRequest{
			Name:   "fail_job",
			Script: "#!/bin/bash\necho fail",
		}
		bodyBytes, _ := json.Marshal(reqBody)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/submit", bytes.NewBuffer(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("Expected 500 when backend unreachable, got %d", w.Code)
		}
	})

	t.Run("List Jobs Backend Unreachable", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/api/v1/slurm/jobs", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("Expected 500 when backend unreachable, got %d", w.Code)
		}
	})

	t.Run("Cancel Job Backend Unreachable", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/1001/cancel", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("Expected 500 when backend unreachable, got %d", w.Code)
		}
	})
}

// TestAdversarial_Concurrency tests high concurrency operations
func TestAdversarial_Concurrency(t *testing.T) {
	mockServer, router := setupTestRouter()
	defer mockServer.Close()

	const numGoroutines = 30
	var wg sync.WaitGroup

	// 1. Concurrent job submissions
	jobIDs := make([]int, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			reqBody := jobs.SubmitJobRequest{
				Name:      fmt.Sprintf("concurrent_job_%d", idx),
				Partition: "debug",
				Script:    "#!/bin/bash\necho test",
			}
			bodyBytes, _ := json.Marshal(reqBody)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/submit", bytes.NewBuffer(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			if w.Code == http.StatusOK {
				var resp jobs.SubmitJobResponse
				_ = json.Unmarshal(w.Body.Bytes(), &resp)
				jobIDs[idx] = resp.JobID
			} else {
				t.Errorf("Concurrent submit failed with status %d: %s", w.Code, w.Body.String())
			}
		}(i)
	}
	wg.Wait()

	// Verify all jobs were created with unique IDs
	uniqueIDs := make(map[int]bool)
	for _, id := range jobIDs {
		if id <= 0 {
			t.Errorf("Invalid job ID created during concurrent submission: %d", id)
		}
		if uniqueIDs[id] {
			t.Errorf("Duplicate job ID generated under concurrency: %d", id)
		}
		uniqueIDs[id] = true
	}

	// 2. Concurrent holds, requeues, lists, cancels
	for i := 0; i < numGoroutines; i++ {
		wg.Add(4)
		id := jobIDs[i]

		// List jobs concurrently
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/api/v1/slurm/jobs", nil)
			router.ServeHTTP(w, req)
		}()

		// Hold job
		go func(jID int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/slurm/jobs/%d/hold", jID), nil)
			router.ServeHTTP(w, req)
		}(id)

		// Requeue job
		go func(jID int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/slurm/jobs/%d/requeue", jID), nil)
			router.ServeHTTP(w, req)
		}(id)

		// Cancel job
		go func(jID int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/slurm/jobs/%d/cancel", jID), nil)
			router.ServeHTTP(w, req)
		}(id)
	}
	wg.Wait()
}
