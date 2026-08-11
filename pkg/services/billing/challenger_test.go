package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestChallenger_ConcurrencyStress tests high-concurrency access with go test -race.
func TestChallenger_ConcurrencyStress(t *testing.T) {
	svc := NewBillingService()
	h := NewBillingHandler(svc)
	router, _ := setupTestRouter()

	ctx := context.Background()
	var wg sync.WaitGroup

	numGoroutines := 50
	numOps := 20

	// Concurrent writers (RecordJobUsage)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				svc.RecordJobUsage(AccountAuditRecord{
					ID:           fmt.Sprintf("stress-job-%d-%d", workerID, j),
					Type:         "job",
					User:         fmt.Sprintf("user-%d", workerID%5),
					Project:      "default",
					CPUs:         2,
					MemoryMB:     2048,
					GPUs:         1,
					DurationSecs: 1800,
					CreatedAt:    time.Now(),
				})
			}
		}(i)
	}

	// Concurrent writers (RecordContainerUsage)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				svc.RecordContainerUsage(AccountAuditRecord{
					ID:           fmt.Sprintf("stress-ctr-%d-%d", workerID, j),
					Type:         "container",
					User:         fmt.Sprintf("user-%d", workerID%5),
					Project:      "default",
					CPUs:         1,
					MemoryMB:     1024,
					GPUs:         0,
					DurationSecs: 3600,
					CreatedAt:    time.Now(),
				})
			}
		}(i)
	}

	// Concurrent readers (GetUsage & ExportReport via HTTP & Direct Service)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				// Direct Service call
				_, err := svc.GetUsage(ctx, UsageQueryParam{
					User: fmt.Sprintf("user-%d", workerID%5),
				})
				if err != nil {
					t.Errorf("GetUsage returned error: %v", err)
				}

				_, err = svc.ExportReport(ctx, ExportQueryParam{
					Format: "json",
					User:   fmt.Sprintf("user-%d", workerID%5),
				})
				if err != nil {
					t.Errorf("ExportReport returned error: %v", err)
				}

				// HTTP request to handler
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/slurm/billing/usage?user=user-%d", workerID%5), nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Errorf("HTTP GetUsage expected 200, got %d", w.Code)
				}

				_ = h // silence unused warning if any
			}
		}(i)
	}

	wg.Wait()
}

// TestChallenger_TimezoneDateRangeValidation probes date range behavior with cross-timezone dates.
// EMPIRICAL FINDING: handler.go uses lexical string comparison (startTime > endTime)
// which fails when comparing cross-timezone dates (e.g. UTC vs +08:00).
func TestChallenger_TimezoneDateRangeValidation(t *testing.T) {
	router, _ := setupTestRouter()

	// Scenario 1: start_time = 05:00 UTC, end_time = 12:00 UTC+8 (04:00 UTC).
	// Chronologically invalid (05:00 UTC > 04:00 UTC), but string check "2026-08-11T05..." > "2026-08-11T12..." is false.
	// HTTP Handler incorrectly returns 200 OK instead of 400 Bad Request.
	startStr := "2026-08-11T05:00:00Z"
	endStr := "2026-08-11T12:00:00+08:00"

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/slurm/billing/usage?start_time=%s&end_time=%s", startStr, endStr), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Logf("[BUG CONFIRMED] Lexical string comparison allowed chronologically invalid date range (05:00 UTC > 04:00 UTC), got HTTP %d", w.Code)
	}

	// Scenario 2: start_time = 12:00 UTC+8 (04:00 UTC), end_time = 05:00 UTC.
	// Chronologically valid (04:00 UTC < 05:00 UTC), but string check "2026-08-11T12..." > "2026-08-11T05..." is true.
	// HTTP Handler incorrectly returns 400 Bad Request instead of 200 OK.
	startStr2 := "2026-08-11T12:00:00+08:00"
	endStr2 := "2026-08-11T05:00:00Z"

	req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/slurm/billing/usage?start_time=%s&end_time=%s", startStr2, endStr2), nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code == http.StatusBadRequest {
		t.Logf("[BUG CONFIRMED] Lexical string comparison rejected chronologically valid date range (04:00 UTC < 05:00 UTC), got HTTP %d", w2.Code)
	}
}

// TestChallenger_MalformedDates checks behavior with unparseable or unexpected date formats.
func TestChallenger_MalformedDates(t *testing.T) {
	router, _ := setupTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/usage?start_time=not-a-date&end_time=2026-01-01", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	t.Logf("Malformed start_time check: HTTP Status = %d, Body = %s", w.Code, w.Body.String())
}

// TestChallenger_ExportFormats verifies deep JSON and Chart export structures and math.
func TestChallenger_ExportFormats(t *testing.T) {
	router, svc := setupTestRouter()

	// Add known record
	svc.RecordJobUsage(AccountAuditRecord{
		ID:           "export-math-job",
		Type:         "job",
		User:         "export_user",
		Project:      "export_proj",
		CPUs:         10,     // 10 CPUs
		MemoryMB:     20480,  // 20 GB
		GPUs:         2,      // 2 GPUs
		DurationSecs: 7200,   // 2 hours
		CreatedAt:    time.Now(),
	})

	// JSON Export
	reqJSON := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/export?format=json&user=export_user", nil)
	wJSON := httptest.NewRecorder()
	router.ServeHTTP(wJSON, reqJSON)

	if wJSON.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for JSON export, got %d", wJSON.Code)
	}

	var jsonResp ExportJSONResponse
	if err := json.Unmarshal(wJSON.Body.Bytes(), &jsonResp); err != nil {
		t.Fatalf("JSON decode error: %v", err)
	}

	// Expected:
	// CPU hours: 10 * 2 = 20 CPU-hours
	// Mem GB hours: 20 * 2 = 40 GB-hours
	// GPU hours: 2 * 2 = 4 GPU-hours
	// Cost: 20 * 0.50 + 40 * 0.10 + 4 * 2.50 = 10.0 + 4.0 + 10.0 = 24.0 CNY
	expectedCost := 24.0
	if jsonResp.TotalCost != expectedCost {
		t.Errorf("Cost calculation mismatch: expected %f, got %f", expectedCost, jsonResp.TotalCost)
	}
	if jsonResp.Currency != "CNY" {
		t.Errorf("Expected currency CNY, got %s", jsonResp.Currency)
	}
	if jsonResp.ExportedBy != "slurm-billing-auditor" {
		t.Errorf("Expected exported_by slurm-billing-auditor, got %s", jsonResp.ExportedBy)
	}

	// Chart Export
	reqChart := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/export?format=chart&user=export_user", nil)
	wChart := httptest.NewRecorder()
	router.ServeHTTP(wChart, reqChart)

	if wChart.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for Chart export, got %d", wChart.Code)
	}

	var chartResp ExportChartResponse
	if err := json.Unmarshal(wChart.Body.Bytes(), &chartResp); err != nil {
		t.Fatalf("Chart decode error: %v", err)
	}

	if chartResp.Format != "chart" {
		t.Errorf("Expected format chart, got %s", chartResp.Format)
	}
	if len(chartResp.Labels) != 3 || len(chartResp.Series) != 3 {
		t.Errorf("Expected 3 labels and 3 series, got %d labels, %d series", len(chartResp.Labels), len(chartResp.Series))
	}
	// Series should be [JobCount, ContainerCount, GPUHours] = [1, 0, 4.0]
	if chartResp.Series[0] != 1.0 || chartResp.Series[1] != 0.0 || chartResp.Series[2] != 4.0 {
		t.Errorf("Chart series mismatch: expected [1.0, 0.0, 4.0], got %v", chartResp.Series)
	}
}

// TestChallenger_NegativeAndEdgeLimits checks edge limits like 0, negative, and large limits.
func TestChallenger_NegativeAndEdgeLimits(t *testing.T) {
	router, _ := setupTestRouter()

	// limit=0 (should be treated as no limit / return all)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/usage?limit=0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for limit=0, got %d", w.Code)
	}

	// limit=-5 (should return 400 Bad Request)
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/billing/usage?limit=-5", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for limit=-5, got %d", w2.Code)
	}
}
