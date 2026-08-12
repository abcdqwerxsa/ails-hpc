package nodes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"ails-hpc/pkg/services/common"
	"ails-hpc/pkg/services/nodes"
	"ails-hpc/pkg/slurmrest"

	"github.com/gin-gonic/gin"
)

// TestChallenger_Nodes_ConcurrencyStress tests concurrent read (ListNodes) and write (UpdateNodeState) operations using go test -race.
func TestChallenger_Nodes_ConcurrencyStress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mock := common.NewMockSlurmServer()
	t.Cleanup(mock.Close)
	client := slurmrest.NewClient(mock.URL, "hpcuser", "test-token")
	svc := nodes.NewNodeService(client)
	handler := nodes.NewNodeHandler(svc)

	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/api/v1/slurm/nodes", handler.GetNodes)
	router.POST("/api/v1/slurm/nodes/:name/state", handler.UpdateNodeState)

	ctx := context.Background()
	var wg sync.WaitGroup

	numGoroutines := 40
	numOps := 25

	// 1. Concurrent State Updaters (DRAIN and RESUME on node1, node2, node3)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			nodeNames := []string{"node1", "node2", "node3"}
			states := []string{"DRAIN", "RESUME", "IDLE"}

			for j := 0; j < numOps; j++ {
				nodeName := nodeNames[(workerID+j)%len(nodeNames)]
				state := states[(workerID+j)%len(states)]

				// Direct service call
				req := &nodes.NodeStateUpdateRequest{
					State:  state,
					Reason: fmt.Sprintf("stress-worker-%d-op-%d", workerID, j),
				}
				_, _ = svc.UpdateNodeState(ctx, nodeName, req)

				// HTTP endpoint call
				bodyBytes, _ := json.Marshal(req)
				httpReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/slurm/nodes/%s/state", nodeName), bytes.NewBuffer(bodyBytes))
				httpReq.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httpReq)
			}
		}(i)
	}

	// 2. Concurrent Readers (ListNodes)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				// Direct service call
				list, err := svc.ListNodes(ctx)
				if err != nil {
					t.Errorf("ListNodes returned error: %v", err)
				}
				if len(list) < 3 {
					t.Errorf("Expected at least 3 nodes, got %d", len(list))
				}

				// HTTP endpoint call
				httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/nodes", nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httpReq)
				if w.Code != http.StatusOK {
					t.Errorf("HTTP GetNodes expected 200, got %d", w.Code)
				}
			}
		}(i)
	}

	wg.Wait()
}

// TestChallenger_Nodes_InvalidAndBoundaryNodeNames tests node state updates with invalid node names and paths.
func TestChallenger_Nodes_InvalidAndBoundaryNodeNames(t *testing.T) {
	router, _ := setupNodeTestRouter(t)

	testCases := []struct {
		name           string
		nodeName       string
		state          string
		expectedStatus int
	}{
		{"Non-existent string node name", "non_existent_node_999", "DRAIN", http.StatusNotFound},
		{"Negative integer node ID path -1", "-1", "DRAIN", http.StatusNotFound},
		{"Negative integer node ID path -99", "-99", "DRAIN", http.StatusNotFound},
		{"Whitespace node name", "   ", "DRAIN", http.StatusNotFound},
		{"Special char node name", "node1@#$", "DRAIN", http.StatusNotFound},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			payload, _ := json.Marshal(nodes.NodeStateUpdateRequest{State: tc.state})
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/slurm/nodes/%s/state", tc.nodeName), bytes.NewBuffer(payload))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			if w.Code != tc.expectedStatus {
				t.Errorf("expected status %d for %s, got %d. Body: %s", tc.expectedStatus, tc.nodeName, w.Code, w.Body.String())
			}
		})
	}
}

// TestChallenger_Nodes_InvalidStateValues tests node state updates with invalid/unsupported state strings.
func TestChallenger_Nodes_InvalidStateValues(t *testing.T) {
	router, _ := setupNodeTestRouter(t)

	invalidStates := []string{
		"UNKNOWN",
		"DOWN",
		"DRAINED",
		"FAIL",
		"MAINT",
		"12345",
		"",
	}

	for _, invalidState := range invalidStates {
		t.Run("State_"+invalidState, func(t *testing.T) {
			payload, _ := json.Marshal(nodes.NodeStateUpdateRequest{State: invalidState})
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPost, "/api/v1/slurm/nodes/node1/state", bytes.NewBuffer(payload))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status 400 Bad Request for state %q, got %d. Body: %s", invalidState, w.Code, w.Body.String())
			}
		})
	}
}

// TestChallenger_Nodes_StateTransitionsAndReasoning tests node state transition idempotency and reason persistence.
func TestChallenger_Nodes_StateTransitionsAndReasoning(t *testing.T) {
	router, svc := setupNodeTestRouter(t)
	ctx := context.Background()

	// 1. DRAIN with reason
	drainReason := "Scheduled GPU Maintenance"
	payload1, _ := json.Marshal(nodes.NodeStateUpdateRequest{
		State:  "DRAIN",
		Reason: drainReason,
	})
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest(http.MethodPost, "/api/v1/slurm/nodes/node3/state", bytes.NewBuffer(payload1))
	req1.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for DRAIN with reason, got %d", w1.Code)
	}

	list1, _ := svc.ListNodes(ctx)
	var node3Info *nodes.NodeStateInfo
	for _, n := range list1 {
		if n.Name == "node3" {
			node3Info = n
			break
		}
	}
	if node3Info == nil || node3Info.State != "DRAIN" || node3Info.Reason != drainReason {
		t.Errorf("expected node3 to have State=DRAIN and Reason=%q, got State=%s, Reason=%q", drainReason, node3Info.State, node3Info.Reason)
	}

	// 2. Repeat DRAIN without reason (idempotency check)
	payload2, _ := json.Marshal(nodes.NodeStateUpdateRequest{State: "DRAIN"})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodPost, "/api/v1/slurm/nodes/node3/state", bytes.NewBuffer(payload2))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for repeat DRAIN, got %d", w2.Code)
	}

	// 3. RESUME node3
	payload3, _ := json.Marshal(nodes.NodeStateUpdateRequest{State: "RESUME"})
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest(http.MethodPost, "/api/v1/slurm/nodes/node3/state", bytes.NewBuffer(payload3))
	req3.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for RESUME, got %d", w3.Code)
	}

	list2, _ := svc.ListNodes(ctx)
	for _, n := range list2 {
		if n.Name == "node3" {
			if n.State != "IDLE" {
				t.Errorf("expected node3 State to be IDLE after RESUME, got %s", n.State)
			}
		}
	}
}
