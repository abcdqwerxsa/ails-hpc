package e2e

import (
	"fmt"
	"net/http"
	"testing"
)

// Tier 3: Cross-Feature Combinations (pairwise interactions)

func TestTier3_JobSubmission_ContainerLaunch_BillingTracking(t *testing.T) {
	harness := SetupHarness(t)

	initResp, initUsage, err := harness.DoGet("/api/v1/slurm/billing/usage")
	if err != nil || initResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to fetch initial billing usage: %v", err)
	}
	initCPUs := initUsage["total_cpu_hours"].(float64)

	subResp, _, err := harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{
		"name":   "combo-job-1",
		"script": "echo 'Job 1'",
		"cpus":   4,
	})
	if err != nil || subResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed job submission: %v", err)
	}

	ctrResp, _, err := harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{
		"env_type":  "vscode",
		"cpus":      8,
		"memory_mb": 16384,
	})
	if err != nil || ctrResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed container launch: %v", err)
	}

	afterResp, afterUsage, err := harness.DoGet("/api/v1/slurm/billing/usage")
	if err != nil || afterResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to fetch updated billing usage: %v", err)
	}
	afterCPUs := afterUsage["total_cpu_hours"].(float64)

	if afterCPUs <= initCPUs {
		t.Fatalf("Expected total_cpu_hours to increase after job + container launch. Init: %f, After: %f", initCPUs, afterCPUs)
	}
}

func TestTier3_NodeDrain_JobRequeue_BillingAccounting(t *testing.T) {
	harness := SetupHarness(t)

	drainResp, drainJson, err := harness.DoPost("/api/v1/slurm/nodes/node1/state", map[string]interface{}{"state": "DRAIN"})
	if err != nil || drainResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed node drain: %v", err)
	}
	if drainJson["state"] != "DRAIN" {
		t.Fatalf("Expected node1 state DRAIN, got %v", drainJson)
	}

	subResp, subJson, err := harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{
		"name": "node1-job",
		"cpus": 2,
	})
	if err != nil || subResp.StatusCode != http.StatusOK {
		t.Fatalf("Job submit failed: %v", err)
	}
	jobID := int64(subJson["job_id"].(float64))

	reqResp, reqJson, err := harness.DoPost(fmt.Sprintf("/api/v1/slurm/jobs/%d/requeue", jobID), nil)
	if err != nil || reqResp.StatusCode != http.StatusOK {
		t.Fatalf("Job requeue failed: %v", err)
	}
	if reqJson["status"] != "PENDING" {
		t.Fatalf("Expected job status PENDING after requeue, got %v", reqJson)
	}

	resResp, _, err := harness.DoPost("/api/v1/slurm/nodes/node1/state", map[string]interface{}{"state": "RESUME"})
	if err != nil || resResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed node resume: %v", err)
	}

	billResp, billJson, err := harness.DoGet("/api/v1/slurm/billing/usage")
	if err != nil || billResp.StatusCode != http.StatusOK {
		t.Fatalf("Billing usage query failed: %v", err)
	}
	if billJson["job_count"].(float64) < 1 {
		t.Fatalf("Expected billing job_count to record requeued job: %v", billJson)
	}
}

func TestTier3_ContainerJWTValidation_Recycle_BillingPersistence(t *testing.T) {
	harness := SetupHarness(t)

	launchResp, launchJson, err := harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{
		"env_type": "jupyter",
		"cpus":     4,
	})
	if err != nil || launchResp.StatusCode != http.StatusOK {
		t.Fatalf("Container launch failed: %v", err)
	}

	ctrID := launchJson["container_id"].(string)
	jwtToken := launchJson["token"].(string)
	if jwtToken == "" {
		t.Fatalf("JWT proxy token missing: %v", launchJson)
	}

	delResp, delJson, err := harness.DoDelete(fmt.Sprintf("/api/v1/slurm/containers/%s", ctrID))
	if err != nil || delResp.StatusCode != http.StatusOK {
		t.Fatalf("Container recycle failed: %v", err)
	}
	if delJson["status"] != "TERMINATED" {
		t.Fatalf("Expected status TERMINATED, got %v", delJson)
	}

	listResp, listJson, err := harness.DoGet("/api/v1/slurm/containers/list")
	if err != nil || listResp.StatusCode != http.StatusOK {
		t.Fatalf("Container list failed: %v", err)
	}
	activeList := listJson["containers"].([]interface{})
	for _, item := range activeList {
		cMap := item.(map[string]interface{})
		if cMap["container_id"] == ctrID {
			t.Fatalf("Recycled container %s still present in active list!", ctrID)
		}
	}

	expResp, expJson, err := harness.DoGet("/api/v1/slurm/billing/export?format=json")
	if err != nil || expResp.StatusCode != http.StatusOK {
		t.Fatalf("Billing export failed: %v", err)
	}
	if expJson["ctr_count"].(float64) < 1 {
		t.Fatalf("Expected billing export to retain historical container record count: %v", expJson)
	}
}

func TestTier3_MultiJob_NodeToggle_BillingExport(t *testing.T) {
	harness := SetupHarness(t)

	for i := 1; i <= 3; i++ {
		_, _, err := harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{
			"name": fmt.Sprintf("batch-job-%d", i),
			"cpus": i * 2,
		})
		if err != nil {
			t.Fatalf("Job submission %d failed: %v", i, err)
		}
	}

	_, _, _ = harness.DoPost("/api/v1/slurm/nodes/node2/state", map[string]interface{}{"state": "DRAIN"})
	_, _, _ = harness.DoPost("/api/v1/slurm/nodes/node2/state", map[string]interface{}{"state": "RESUME"})

	jsonResp, jsonExport, err := harness.DoGet("/api/v1/slurm/billing/export?format=json")
	if err != nil || jsonResp.StatusCode != http.StatusOK {
		t.Fatalf("JSON export failed: %v", err)
	}
	chartResp, chartExport, err := harness.DoGet("/api/v1/slurm/billing/export?format=chart")
	if err != nil || chartResp.StatusCode != http.StatusOK {
		t.Fatalf("Chart export failed: %v", err)
	}

	if jsonExport["job_count"].(float64) < 3 {
		t.Fatalf("Expected at least 3 jobs in billing JSON export, got %v", jsonExport)
	}
	if chartExport["format"] != "chart" {
		t.Fatalf("Expected format 'chart', got %v", chartExport)
	}
}

func TestTier3_QuotaLimit_JobCancel_ResourceRecovery(t *testing.T) {
	harness := SetupHarness(t)

	subResp, subJson, err := harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{
		"name": "held-job",
		"cpus": 4,
	})
	if err != nil || subResp.StatusCode != http.StatusOK {
		t.Fatalf("Job submit failed: %v", err)
	}
	jobID := int64(subJson["job_id"].(float64))

	_, _, _ = harness.DoPost(fmt.Sprintf("/api/v1/slurm/jobs/%d/hold", jobID), nil)

	lResp, lJson, err := harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{
		"env_type": "vscode",
		"cpus":     4,
	})
	if err != nil || lResp.StatusCode != http.StatusOK {
		t.Fatalf("Container launch failed: %v", err)
	}
	ctrID := lJson["container_id"].(string)

	_, _, _ = harness.DoPost(fmt.Sprintf("/api/v1/slurm/jobs/%d/cancel", jobID), nil)
	_, _, _ = harness.DoDelete(fmt.Sprintf("/api/v1/slurm/containers/%s", ctrID))

	nResp, nJson, err := harness.DoGet("/api/v1/slurm/nodes")
	if err != nil || nResp.StatusCode != http.StatusOK {
		t.Fatalf("Node list failed: %v", err)
	}
	nodes := nJson["nodes"].([]interface{})
	if len(nodes) == 0 {
		t.Fatalf("Expected active cluster nodes: %v", nJson)
	}
}
