package e2e

import (
	"fmt"
	"net/http"
	"testing"
)

// Tier 4: Real-World Scenarios

func TestTier4_MultiNodeBatchSubmissionScenario(t *testing.T) {
	harness := SetupHarness(t)

	jobIDs := make([]int64, 0, 10)
	partitions := []string{"standard", "gpu-a100", "high-mem"}

	for i := 1; i <= 10; i++ {
		part := partitions[i%len(partitions)]
		subResp, subJson, err := harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{
			"name":      fmt.Sprintf("real-world-batch-job-%02d", i),
			"script":    fmt.Sprintf("#!/bin/bash\necho 'Running batch task %d'", i),
			"nodes":     (i % 3) + 1,
			"cpus":      i * 2,
			"partition": part,
		})
		if err != nil || subResp.StatusCode != http.StatusOK {
			t.Fatalf("Failed batch submission for job %d: %v", i, err)
		}
		id := int64(subJson["job_id"].(float64))
		jobIDs = append(jobIDs, id)
	}

	for i, id := range jobIDs {
		if i%2 == 1 {
			holdResp, holdJson, err := harness.DoPost(fmt.Sprintf("/api/v1/slurm/jobs/%d/hold", id), nil)
			if err != nil || holdResp.StatusCode != http.StatusOK {
				t.Fatalf("Failed to hold job %d: %v", id, err)
			}
			if holdJson["status"] != "HELD" {
				t.Fatalf("Expected job %d status HELD, got %v", id, holdJson)
			}
		}
	}

	for i := 0; i < 2; i++ {
		id := jobIDs[i]
		canResp, canJson, err := harness.DoPost(fmt.Sprintf("/api/v1/slurm/jobs/%d/cancel", id), nil)
		if err != nil || canResp.StatusCode != http.StatusOK {
			t.Fatalf("Failed to cancel job %d: %v", id, err)
		}
		if canJson["status"] != "CANCELLED" {
			t.Fatalf("Expected job %d status CANCELLED, got %v", id, canJson)
		}
	}

	reqResp, reqJson, err := harness.DoPost(fmt.Sprintf("/api/v1/slurm/jobs/%d/requeue", jobIDs[2]), nil)
	if err != nil || reqResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to requeue job %d: %v", jobIDs[2], err)
	}
	if reqJson["status"] != "PENDING" {
		t.Fatalf("Expected job %d status PENDING after requeue, got %v", jobIDs[2], reqJson)
	}

	listResp, listJson, err := harness.DoGet("/api/v1/slurm/jobs")
	if err != nil || listResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to query jobs queue: %v", err)
	}

	jobsList, ok := listJson["jobs"].([]interface{})
	if !ok || len(jobsList) < 10 {
		t.Fatalf("Expected 10 jobs in queue listing, got %v", listJson)
	}

	_, _, _ = harness.DoPost("/api/v1/slurm/nodes/node1/state", map[string]interface{}{"state": "DRAIN"})
	nodeListResp, nodeListJson, err := harness.DoGet("/api/v1/slurm/nodes")
	if err != nil || nodeListResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed node grid query: %v", err)
	}
	nodes := nodeListJson["nodes"].([]interface{})
	if len(nodes) < 3 {
		t.Fatalf("Expected 3 cluster nodes, got %v", nodeListJson)
	}

	_, _, _ = harness.DoPost("/api/v1/slurm/nodes/node1/state", map[string]interface{}{"state": "RESUME"})
}

func TestTier4_WorkspaceDynamicRecyclingHighLoadScenario(t *testing.T) {
	harness := SetupHarness(t)

	ctrIDs := make([]string, 0, 5)
	jwtTokens := make([]string, 0, 5)

	for i := 1; i <= 5; i++ {
		envType := "vscode"
		if i%2 == 0 {
			envType = "jupyter"
		}

		launchResp, launchJson, err := harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{
			"env_type":  envType,
			"cpus":      4,
			"memory_mb": 8192,
		})
		if err != nil || launchResp.StatusCode != http.StatusOK {
			t.Fatalf("Failed to launch container instance %d: %v", i, err)
		}

		id := launchJson["container_id"].(string)
		token := launchJson["token"].(string)
		webURL := launchJson["web_url"].(string)

		if id == "" || token == "" || webURL == "" {
			t.Fatalf("Container launch payload incomplete for instance %d: %v", i, launchJson)
		}

		ctrIDs = append(ctrIDs, id)
		jwtTokens = append(jwtTokens, token)
	}

	listResp, listJson, err := harness.DoGet("/api/v1/slurm/containers/list")
	if err != nil || listResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed container list query: %v", err)
	}
	activeCtrs := listJson["containers"].([]interface{})
	if len(activeCtrs) < 5 {
		t.Fatalf("Expected 5 active containers under load, got %d", len(activeCtrs))
	}

	for i := 0; i < 3; i++ {
		delID := ctrIDs[i]
		delResp, delJson, err := harness.DoDelete(fmt.Sprintf("/api/v1/slurm/containers/%s", delID))
		if err != nil || delResp.StatusCode != http.StatusOK {
			t.Fatalf("Failed to recycle container %s: %v", delID, err)
		}
		if delJson["status"] != "TERMINATED" {
			t.Fatalf("Expected status TERMINATED for recycled container %s, got %v", delID, delJson)
		}
	}

	remResp, remJson, err := harness.DoGet("/api/v1/slurm/containers/list")
	if err != nil || remResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed remaining container query: %v", err)
	}
	remCtrs := remJson["containers"].([]interface{})
	if len(remCtrs) != 2 {
		t.Fatalf("Expected exactly 2 active containers remaining after dynamic recycling, got %d", len(remCtrs))
	}
}

func TestTier4_FullBillingReportExportAuditScenario(t *testing.T) {
	harness := SetupHarness(t)

	for i := 1; i <= 3; i++ {
		_, _, _ = harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{
			"name": fmt.Sprintf("audit-job-%d", i),
			"cpus": i * 4,
		})
	}
	_, _, _ = harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{
		"env_type":  "vscode",
		"cpus":      8,
		"memory_mb": 16384,
	})

	usageResp, usageJson, err := harness.DoGet("/api/v1/slurm/billing/usage?user=hpcuser&project=default")
	if err != nil || usageResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed billing usage audit query: %v", err)
	}

	cpuHours := usageJson["total_cpu_hours"].(float64)
	memHours := usageJson["total_memory_gb_hours"].(float64)
	if cpuHours <= 0 || memHours <= 0 {
		t.Fatalf("Expected positive billing resource hours, got CPU: %f, Mem: %f", cpuHours, memHours)
	}

	jsonExpResp, jsonExp, err := harness.DoGet("/api/v1/slurm/billing/export?format=json&user=hpcuser")
	if err != nil || jsonExpResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed billing JSON report export: %v", err)
	}
	if jsonExp["exported_by"] != "slurm-billing-auditor" {
		t.Fatalf("Expected exported_by slurm-billing-auditor, got %v", jsonExp)
	}

	chartExpResp, chartExp, err := harness.DoGet("/api/v1/slurm/billing/export?format=chart&user=hpcuser")
	if err != nil || chartExpResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed billing Chart report export: %v", err)
	}

	labels := chartExp["labels"].([]interface{})
	series := chartExp["series"].([]interface{})

	if len(labels) == 0 || len(series) == 0 {
		t.Fatalf("Chart export data empty or corrupted: labels=%v, series=%v", labels, series)
	}
}
