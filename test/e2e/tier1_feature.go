package e2e

import (
	"fmt"
	"net/http"
	"testing"
)

// Tier 1: Feature Coverage (>=5 test cases per feature group)

// R1 Feature Group: Jobs Control (sbatch, scancel, shold, srequeue)
func TestTier1_Jobs_SbatchSubmission(t *testing.T) {
	harness := SetupHarness(t)

	payload := map[string]interface{}{
		"name":      "e2e-job-sbatch-01",
		"script":    "#!/bin/bash\necho 'Running Sbatch Test'",
		"nodes":     1,
		"cpus":      2,
		"partition": "standard",
	}

	resp, jsonResult, err := harness.DoPost("/api/v1/slurm/jobs/submit", payload)
	if err != nil {
		t.Fatalf("Failed to execute HTTP request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200 OK, got %d", resp.StatusCode)
	}

	jobIDVal, ok := jsonResult["job_id"]
	if !ok || jobIDVal == nil {
		t.Fatalf("Response missing 'job_id': %v", jsonResult)
	}
	status, _ := jsonResult["status"].(string)
	if status != "SUBMITTED" {
		t.Fatalf("Expected status SUBMITTED, got %s", status)
	}
}

func TestTier1_Jobs_ScancelTermination(t *testing.T) {
	harness := SetupHarness(t)

	subResp, subJson, err := harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{
		"name":   "job-to-cancel",
		"script": "#!/bin/bash\nsleep 100",
	})
	if err != nil || subResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed job submission setup: %v", err)
	}
	jobID := int64(subJson["job_id"].(float64))

	resp, jsonResult, err := harness.DoPost(fmt.Sprintf("/api/v1/slurm/jobs/%d/cancel", jobID), nil)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	status, _ := jsonResult["status"].(string)
	if status != "CANCELLED" {
		t.Fatalf("Expected status CANCELLED, got %s", status)
	}
}

func TestTier1_Jobs_SholdPause(t *testing.T) {
	harness := SetupHarness(t)

	subResp, subJson, err := harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{
		"name":   "job-to-hold",
		"script": "#!/bin/bash\nsleep 50",
	})
	if err != nil || subResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed job submission setup: %v", err)
	}
	jobID := int64(subJson["job_id"].(float64))

	resp, jsonResult, err := harness.DoPost(fmt.Sprintf("/api/v1/slurm/jobs/%d/hold", jobID), nil)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	status, _ := jsonResult["status"].(string)
	if status != "HELD" {
		t.Fatalf("Expected status HELD, got %s", status)
	}
}

func TestTier1_Jobs_SrequeueRestart(t *testing.T) {
	harness := SetupHarness(t)

	subResp, subJson, err := harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{
		"name":   "job-to-requeue",
		"script": "#!/bin/bash\nsleep 30",
	})
	if err != nil || subResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed job submission setup: %v", err)
	}
	jobID := int64(subJson["job_id"].(float64))

	resp, jsonResult, err := harness.DoPost(fmt.Sprintf("/api/v1/slurm/jobs/%d/requeue", jobID), nil)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	status, _ := jsonResult["status"].(string)
	if status != "PENDING" {
		t.Fatalf("Expected status PENDING, got %s", status)
	}
}

func TestTier1_Jobs_ListJobsQueue(t *testing.T) {
	harness := SetupHarness(t)

	_, _, _ = harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{"name": "job-q1", "script": "echo 1"})
	_, _, _ = harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{"name": "job-q2", "script": "echo 2"})

	resp, jsonResult, err := harness.DoGet("/api/v1/slurm/jobs")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	jobsList, ok := jsonResult["jobs"].([]interface{})
	if !ok || len(jobsList) < 2 {
		t.Fatalf("Expected at least 2 jobs in queue listing, got %v", jsonResult)
	}
}

func TestTier1_Jobs_PartitionAssignment(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoPost("/api/v1/slurm/jobs/submit", map[string]interface{}{
		"name":      "job-gpu-partition",
		"script":    "#!/bin/bash\npython train.py",
		"partition": "gpu-a100",
	})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed job partition submission: %v", err)
	}

	partition, _ := jsonResult["partition"].(string)
	if partition != "gpu-a100" {
		t.Fatalf("Expected partition gpu-a100, got %s", partition)
	}
}

// R1 Feature Group: Node State Control (DRAIN / RESUME)
func TestTier1_Nodes_ListNodes(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoGet("/api/v1/slurm/nodes")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	nodes, ok := jsonResult["nodes"].([]interface{})
	if !ok || len(nodes) < 3 {
		t.Fatalf("Expected 3 cluster nodes (node1~node3), got %v", jsonResult)
	}
}

func TestTier1_Nodes_DrainNode1(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoPost("/api/v1/slurm/nodes/node1/state", map[string]interface{}{
		"state": "DRAIN",
	})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	state, _ := jsonResult["state"].(string)
	if state != "DRAIN" {
		t.Fatalf("Expected state DRAIN, got %s", state)
	}
}

func TestTier1_Nodes_ResumeNode1(t *testing.T) {
	harness := SetupHarness(t)

	_, _, _ = harness.DoPost("/api/v1/slurm/nodes/node1/state", map[string]interface{}{"state": "DRAIN"})

	resp, jsonResult, err := harness.DoPost("/api/v1/slurm/nodes/node1/state", map[string]interface{}{
		"state": "RESUME",
	})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	state, _ := jsonResult["state"].(string)
	if state != "IDLE" && state != "RESUME" {
		t.Fatalf("Expected state IDLE or RESUME after resume action, got %s", state)
	}
}

func TestTier1_Nodes_DrainNode2(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoPost("/api/v1/slurm/nodes/node2/state", map[string]interface{}{
		"state": "DRAIN",
	})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to drain node2: %v", err)
	}

	state, _ := jsonResult["state"].(string)
	if state != "DRAIN" {
		t.Fatalf("Expected node2 state DRAIN, got %s", state)
	}
}

func TestTier1_Nodes_ResumeNode2(t *testing.T) {
	harness := SetupHarness(t)

	_, _, _ = harness.DoPost("/api/v1/slurm/nodes/node2/state", map[string]interface{}{"state": "DRAIN"})
	resp, jsonResult, err := harness.DoPost("/api/v1/slurm/nodes/node2/state", map[string]interface{}{
		"state": "RESUME",
	})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to resume node2: %v", err)
	}

	state, _ := jsonResult["state"].(string)
	if state != "IDLE" && state != "RESUME" {
		t.Fatalf("Expected node2 state IDLE/RESUME, got %s", state)
	}
}

func TestTier1_Nodes_StateGridVerification(t *testing.T) {
	harness := SetupHarness(t)

	_, _, _ = harness.DoPost("/api/v1/slurm/nodes/node3/state", map[string]interface{}{"state": "DRAIN"})

	resp, jsonResult, err := harness.DoGet("/api/v1/slurm/nodes")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to query node grid: %v", err)
	}

	nodesList, _ := jsonResult["nodes"].([]interface{})
	node3FoundAndDrained := false
	for _, n := range nodesList {
		nM, _ := n.(map[string]interface{})
		if nM["name"] == "node3" && nM["state"] == "DRAIN" {
			node3FoundAndDrained = true
			break
		}
	}
	if !node3FoundAndDrained {
		t.Fatalf("Node grid status check failed for node3: %v", jsonResult)
	}
}

// R2 Feature Group: Container Workspaces (launch, JWT proxy, recycle, list)
func TestTier1_Containers_LaunchVSCode(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{
		"env_type":  "vscode",
		"cpus":      4,
		"memory_mb": 8192,
	})
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	ctrID, ok := jsonResult["container_id"].(string)
	if !ok || ctrID == "" {
		t.Fatalf("Expected valid container_id, got %v", jsonResult)
	}
	webURL, _ := jsonResult["web_url"].(string)
	if webURL == "" {
		t.Fatalf("Expected non-empty web_url, got %v", jsonResult)
	}
}

func TestTier1_Containers_LaunchJupyterLab(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{
		"env_type":  "jupyter",
		"cpus":      2,
		"memory_mb": 4096,
	})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to launch JupyterLab container: %v", err)
	}

	envType, _ := jsonResult["env_type"].(string)
	if envType != "jupyter" {
		t.Fatalf("Expected env_type jupyter, got %s", envType)
	}
}

func TestTier1_Containers_JWTProxyValidation(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{
		"env_type": "vscode",
	})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed container launch: %v", err)
	}

	token, _ := jsonResult["token"].(string)
	if token == "" {
		t.Fatalf("Expected JWT proxy token in launch response: %v", jsonResult)
	}
}

func TestTier1_Containers_ListActiveContainers(t *testing.T) {
	harness := SetupHarness(t)

	_, _, _ = harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{"env_type": "vscode"})
	_, _, _ = harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{"env_type": "jupyter"})

	resp, jsonResult, err := harness.DoGet("/api/v1/slurm/containers/list")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	ctrs, ok := jsonResult["containers"].([]interface{})
	if !ok || len(ctrs) < 2 {
		t.Fatalf("Expected active containers list with at least 2 entries, got %v", jsonResult)
	}
}

func TestTier1_Containers_RecycleContainerInstance(t *testing.T) {
	harness := SetupHarness(t)

	launchResp, launchJson, err := harness.DoPost("/api/v1/slurm/containers/launch", map[string]interface{}{
		"env_type": "vscode",
	})
	if err != nil || launchResp.StatusCode != http.StatusOK {
		t.Fatalf("Container launch failed: %v", err)
	}
	ctrID := launchJson["container_id"].(string)

	resp, jsonResult, err := harness.DoDelete(fmt.Sprintf("/api/v1/slurm/containers/%s", ctrID))
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	status, _ := jsonResult["status"].(string)
	if status != "TERMINATED" {
		t.Fatalf("Expected container status TERMINATED, got %s", status)
	}
}

// R3 Feature Group: Billing & User Quotas (usage stats, export report)
func TestTier1_Billing_QueryUsageStats(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoGet("/api/v1/slurm/billing/usage")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	cpuHoursVal, ok := jsonResult["total_cpu_hours"]
	if !ok || cpuHoursVal == nil {
		t.Fatalf("Expected total_cpu_hours in usage metrics, got %v", jsonResult)
	}
}

func TestTier1_Billing_QueryUserUsageFilter(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoGet("/api/v1/slurm/billing/usage?user=testuser1")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed user billing usage query: %v", err)
	}

	user, _ := jsonResult["user"].(string)
	if user != "testuser1" {
		t.Fatalf("Expected user testuser1 in usage response, got %s", user)
	}
}

func TestTier1_Billing_QueryProjectUsageFilter(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoGet("/api/v1/slurm/billing/usage?project=ai-lab")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed project billing usage query: %v", err)
	}

	project, _ := jsonResult["project"].(string)
	if project != "ai-lab" {
		t.Fatalf("Expected project ai-lab, got %s", project)
	}
}

func TestTier1_Billing_ExportJSONReport(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoGet("/api/v1/slurm/billing/export?format=json")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	fmtVal, _ := jsonResult["format"].(string)
	if fmtVal != "json" {
		t.Fatalf("Expected export format json, got %s", fmtVal)
	}
}

func TestTier1_Billing_ExportChartReport(t *testing.T) {
	harness := SetupHarness(t)

	resp, jsonResult, err := harness.DoGet("/api/v1/slurm/billing/export?format=chart")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	fmtVal, _ := jsonResult["format"].(string)
	if fmtVal != "chart" {
		t.Fatalf("Expected export format chart, got %s", fmtVal)
	}
	labels, ok := jsonResult["labels"].([]interface{})
	if !ok || len(labels) == 0 {
		t.Fatalf("Expected non-empty series labels for chart export, got %v", jsonResult)
	}
}
