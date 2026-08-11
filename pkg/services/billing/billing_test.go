package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func setupTestRouter() (*gin.Engine, BillingService) {
	gin.SetMode(gin.TestMode)
	svc := NewBillingService()
	h := NewBillingHandler(svc)

	r := gin.New()
	rg := r.Group("/api/v1/slurm")
	h.RegisterRoutes(rg)

	return r, svc
}

func TestGetUsage_Success(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/usage", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var resp UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.User != "hpcuser" {
		t.Errorf("Expected user hpcuser, got %s", resp.User)
	}
	if resp.TotalCPUHours <= 0 {
		t.Errorf("Expected positive total_cpu_hours, got %f", resp.TotalCPUHours)
	}
	if resp.JobCount == 0 {
		t.Errorf("Expected job_count > 0, got %d", resp.JobCount)
	}
}

func TestGetUsage_UserFilter(t *testing.T) {
	router, svc := setupTestRouter()

	svc.RecordJobUsage(AccountAuditRecord{
		ID:           "test-job-1",
		Type:         "job",
		User:         "testuser1",
		Project:      "ai-lab",
		CPUs:         4,
		MemoryMB:     8192,
		GPUs:         2,
		DurationSecs: 3600,
		CreatedAt:    time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/usage?user=testuser1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var resp UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.User != "testuser1" {
		t.Errorf("Expected user testuser1, got %s", resp.User)
	}
	if resp.TotalCPUHours != 4.0 {
		t.Errorf("Expected 4.0 CPU hours, got %f", resp.TotalCPUHours)
	}
}

func TestGetUsage_ProjectFilter(t *testing.T) {
	router, svc := setupTestRouter()

	svc.RecordJobUsage(AccountAuditRecord{
		ID:           "test-job-proj",
		Type:         "job",
		User:         "hpcuser",
		Project:      "ai-lab",
		CPUs:         8,
		MemoryMB:     16384,
		GPUs:         1,
		DurationSecs: 3600,
		CreatedAt:    time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/usage?project=ai-lab", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var resp UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Project != "ai-lab" {
		t.Errorf("Expected project ai-lab, got %s", resp.Project)
	}
	if resp.TotalCPUHours != 8.0 {
		t.Errorf("Expected 8.0 CPU hours, got %f", resp.TotalCPUHours)
	}
}

func TestGetUsage_NonExistentUser(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/usage?user=ghost_user_9999", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for non-existent user, got %d", w.Code)
	}

	var resp UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.User != "ghost_user_9999" {
		t.Errorf("Expected user ghost_user_9999, got %s", resp.User)
	}
	if resp.TotalCPUHours != 0.0 || resp.JobCount != 0 {
		t.Errorf("Expected zero stats for ghost user, got CPU: %f, Jobs: %d", resp.TotalCPUHours, resp.JobCount)
	}
}

func TestGetUsage_InvalidDateRange(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/usage?start_time=2026-12-31&end_time=2026-01-01", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for invalid date range, got %d", w.Code)
	}
}

func TestGetUsage_NegativeLimit(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/usage?limit=-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for negative limit, got %d", w.Code)
	}
}

func TestGetUsage_MalformedFormat(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/usage?format=invalid_fmt", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for malformed format, got %d", w.Code)
	}
}

func TestExportReport_JSON(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/export?format=json&user=hpcuser", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var resp ExportJSONResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Format != "json" {
		t.Errorf("Expected format json, got %s", resp.Format)
	}
	if resp.Currency != "CNY" {
		t.Errorf("Expected currency CNY, got %s", resp.Currency)
	}
	if resp.ExportedBy != "slurm-billing-auditor" {
		t.Errorf("Expected exported_by slurm-billing-auditor, got %s", resp.ExportedBy)
	}
	if resp.TotalCost <= 0 {
		t.Errorf("Expected positive total_cost, got %f", resp.TotalCost)
	}
}

func TestExportReport_Chart(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/export?format=chart&user=hpcuser", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var resp ExportChartResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Format != "chart" {
		t.Errorf("Expected format chart, got %s", resp.Format)
	}
	if len(resp.Labels) == 0 || len(resp.Series) == 0 {
		t.Errorf("Expected non-empty labels and series, got labels: %v, series: %v", resp.Labels, resp.Series)
	}
}

func TestExportReport_UnsupportedFormat(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/export?format=xml_unsupported", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for unsupported export format, got %d", w.Code)
	}
}

func TestRecordUsage_DynamicUpdates(t *testing.T) {
	_, svc := setupTestRouter()

	ctx := context.Background()
	initUsage, err := svc.GetUsage(ctx, UsageQueryParam{User: "hpcuser"})
	if err != nil {
		t.Fatalf("Failed to get initial usage: %v", err)
	}

	svc.RecordJobUsage(AccountAuditRecord{
		ID:           "dyn-job-1",
		Type:         "job",
		User:         "hpcuser",
		Project:      "default",
		CPUs:         4,
		MemoryMB:     8192,
		GPUs:         1,
		DurationSecs: 3600,
		CreatedAt:    time.Now(),
	})

	newUsage, err := svc.GetUsage(ctx, UsageQueryParam{User: "hpcuser"})
	if err != nil {
		t.Fatalf("Failed to get updated usage: %v", err)
	}

	if newUsage.JobCount != initUsage.JobCount+1 {
		t.Errorf("Expected job count to increase by 1, got init %d, new %d", initUsage.JobCount, newUsage.JobCount)
	}
	if newUsage.TotalCPUHours != initUsage.TotalCPUHours+4.0 {
		t.Errorf("Expected total CPU hours to increase by 4.0, got init %f, new %f", initUsage.TotalCPUHours, newUsage.TotalCPUHours)
	}
}
