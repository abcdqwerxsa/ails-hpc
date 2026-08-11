package e2e

import (
	"fmt"
	"net/http"
	"testing"
)

// Tier 2: Boundary & Corner Cases (>=5 test cases per feature group)

// Jobs Domain Boundary Tests
func TestTier2_Jobs_EmptySubmissionPayload(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for empty payload, got %d", resp.StatusCode)
	}
}

func TestTier2_Jobs_InvalidJobIDCancel(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoPost("/api/v1/slurm/jobs/999999999/cancel", nil)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 404 Not Found or 400 Bad Request for invalid job ID, got %d", resp.StatusCode)
	}
}

func TestTier2_Jobs_HoldCancelledJob(t *testing.T) {
	harness := SetupHarness(t)

	subResp, subJson, err := harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{
		"name":   "job-cancel-then-hold",
		"script": "echo test",
	})
	if err != nil || subResp.StatusCode != http.StatusOK {
		t.Fatalf("Job submission failed: %v", err)
	}
	jobID := int64(subJson["job_id"].(float64))

	_, _, _ = harness.DoPost(fmt.Sprintf("/api/v1/slurm/jobs/%d/cancel", jobID), nil)

	resp, _, err := harness.DoPost(fmt.Sprintf("/api/v1/slurm/jobs/%d/hold", jobID), nil)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request when holding a cancelled job, got %d", resp.StatusCode)
	}
}

func TestTier2_Jobs_RequeueNonExistentJob(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoPost("/api/v1/slurm/jobs/88888888/requeue", nil)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Expected 404 Not Found for non-existent job requeue, got %d", resp.StatusCode)
	}
}

func TestTier2_Jobs_ExcessiveCPUsRequest(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{
		"name":   "huge-job",
		"script": "echo high",
		"cpus":   999999,
	})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for excessive CPU limit request, got %d", resp.StatusCode)
	}
}

// Nodes Domain Boundary Tests
func TestTier2_Nodes_NonExistentNodeStateUpdate(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoPost("/api/v1/slurm/nodes/non_existent_node_xyz/state", map[string]interface{}{
		"state": "DRAIN",
	})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Expected 404 Not Found for non-existent node name, got %d", resp.StatusCode)
	}
}

func TestTier2_Nodes_InvalidStateValue(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoPost("/api/v1/slurm/nodes/node1/state", map[string]interface{}{
		"state": "UNSUPPORTED_NODE_STATE",
	})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for invalid state value, got %d", resp.StatusCode)
	}
}

func TestTier2_Nodes_EmptyStatePayload(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoPost("/api/v1/slurm/nodes/node1/state", map[string]interface{}{})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for empty state payload, got %d", resp.StatusCode)
	}
}

func TestTier2_Nodes_RepeatDrainStateIdempotency(t *testing.T) {
	harness := SetupHarness(t)

	_, _, _ = harness.DoPost("/api/v1/slurm/nodes/node1/state", map[string]interface{}{"state": "DRAIN"})

	resp, jsonResult, err := harness.DoPost("/api/v1/slurm/nodes/node1/state", map[string]interface{}{"state": "DRAIN"})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK for repeat DRAIN action, got %d", resp.StatusCode)
	}
	state, _ := jsonResult["state"].(string)
	if state != "DRAIN" {
		t.Fatalf("Expected state DRAIN, got %s", state)
	}
}

func TestTier2_Nodes_NegativeNodeIDPath(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoPost("/api/v1/slurm/nodes/-1/state", map[string]interface{}{"state": "DRAIN"})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 404 or 400 for negative node path parameter, got %d", resp.StatusCode)
	}
}

// Containers Domain Boundary Tests
func TestTier2_Containers_UnsupportedEnvType(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{
		"env_type": "eclipse-ide",
	})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for unsupported env_type, got %d", resp.StatusCode)
	}
}

func TestTier2_Containers_NegativeResources(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{
		"env_type":  "vscode",
		"cpus":      -10,
		"memory_mb": -4096,
	})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for negative resources, got %d", resp.StatusCode)
	}
}

func TestTier2_Containers_ExceedingQuotaLimit(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{
		"env_type":  "vscode",
		"cpus":      9999,
		"memory_mb": 99999999,
	})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for resources exceeding quota, got %d", resp.StatusCode)
	}
}

func TestTier2_Containers_RecycleNonExistentContainer(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoDelete("/api/v1/slurm/containers/non_existent_ctr_999")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Expected 404 Not Found when recycling non-existent container, got %d", resp.StatusCode)
	}
}

func TestTier2_Containers_RecycleAlreadyRecycledContainer(t *testing.T) {
	harness := SetupHarness(t)

	lResp, lJson, err := harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{"env_type": "vscode"})
	if err != nil || lResp.StatusCode != http.StatusOK {
		t.Fatalf("Launch failed: %v", err)
	}
	ctrID := lJson["container_id"].(string)
	_, _, _ = harness.DoDelete(fmt.Sprintf("/api/v1/slurm/containers/%s", ctrID))

	resp, _, err := harness.DoDelete(fmt.Sprintf("/api/v1/slurm/containers/%s", ctrID))
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Expected 404 Not Found when recycling an already recycled container, got %d", resp.StatusCode)
	}
}

// Billing Domain Boundary Tests
func TestTier2_Billing_InvalidDateRange(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoGet("/api/v1/slurm/billing/usage?start_time=2026-12-31&end_time=2026-01-01")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for start_time > end_time, got %d", resp.StatusCode)
	}
}

func TestTier2_Billing_NegativeLimitQuery(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoGet("/api/v1/slurm/billing/usage?limit=-1")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for negative limit parameter, got %d", resp.StatusCode)
	}
}

func TestTier2_Billing_UnsupportedExportFormat(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoGet("/api/v1/slurm/billing/export?format=xml_unsupported")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for unsupported export format, got %d", resp.StatusCode)
	}
}

func TestTier2_Billing_NonExistentUserUsageQuery(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoGet("/api/v1/slurm/billing/usage?user=ghost_user_9999")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK for non-existent user (returning zero stats), got %d", resp.StatusCode)
	}
	user, _ := jsonResult["user"].(string)
	if user != "ghost_user_9999" {
		t.Fatalf("Expected user ghost_user_9999, got %s", user)
	}
}

func TestTier2_Billing_MalformedQueryFormatParameter(t *testing.T) {
	harness := SetupHarness(t)

	resp, _, err := harness.DoGet("/api/v1/slurm/billing/usage?format=invalid_fmt")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for invalid query format, got %d", resp.StatusCode)
	}
}
