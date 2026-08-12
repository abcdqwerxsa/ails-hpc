package nodes_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ails-hpc/pkg/services/nodes"
	"github.com/gin-gonic/gin"
)

func setupNodeTestRouter() (*gin.Engine, nodes.NodeService) {
	gin.SetMode(gin.TestMode)
	service := nodes.NewNodeService(nil)
	handler := nodes.NewNodeHandler(service)

	router := gin.New()
	router.Use(gin.Recovery())

	slurmGroup := router.Group("/api/v1/slurm")
	{
		slurmGroup.GET("/nodes", handler.GetNodes)
		slurmGroup.POST("/nodes/:name/state", handler.UpdateNodeState)
	}

	return router, service
}

func TestNodes_ListNodes_Success(t *testing.T) {
	router, _ := setupNodeTestRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/slurm/nodes", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp nodes.NodesListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp.Nodes) < 3 {
		t.Fatalf("expected at least 3 nodes (node1~node3), got %d", len(resp.Nodes))
	}
}

func TestNodes_UpdateState_DrainAndResume(t *testing.T) {
	router, _ := setupNodeTestRouter()

	// 1. Drain node1
	drainPayload, _ := json.Marshal(nodes.NodeStateUpdateRequest{State: "DRAIN"})
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/api/v1/slurm/nodes/node1/state", bytes.NewBuffer(drainPayload))
	req1.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", w1.Code, w1.Body.String())
	}

	var resp1 nodes.NodeStateUpdateResponse
	_ = json.Unmarshal(w1.Body.Bytes(), &resp1)
	if resp1.State != "DRAIN" {
		t.Errorf("expected state DRAIN, got %s", resp1.State)
	}

	// 2. Resume node1
	resumePayload, _ := json.Marshal(nodes.NodeStateUpdateRequest{State: "RESUME"})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/api/v1/slurm/nodes/node1/state", bytes.NewBuffer(resumePayload))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w2.Code)
	}

	var resp2 nodes.NodeStateUpdateResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.State != "IDLE" && resp2.State != "RESUME" {
		t.Errorf("expected state IDLE or RESUME, got %s", resp2.State)
	}
}

func TestNodes_UpdateState_Idempotency(t *testing.T) {
	router, _ := setupNodeTestRouter()

	drainPayload, _ := json.Marshal(nodes.NodeStateUpdateRequest{State: "DRAIN"})

	// First DRAIN call
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/api/v1/slurm/nodes/node2/state", bytes.NewBuffer(drainPayload))
	req1.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w1, req1)

	// Second DRAIN call (repeat)
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/api/v1/slurm/nodes/node2/state", bytes.NewBuffer(drainPayload))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK for repeat DRAIN, got %d", w2.Code)
	}

	var resp2 nodes.NodeStateUpdateResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.State != "DRAIN" {
		t.Errorf("expected state DRAIN on repeat call, got %s", resp2.State)
	}
}

func TestNodes_UpdateState_NotFound(t *testing.T) {
	router, _ := setupNodeTestRouter()

	drainPayload, _ := json.Marshal(nodes.NodeStateUpdateRequest{State: "DRAIN"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/nodes/non_existent_node_xyz/state", bytes.NewBuffer(drainPayload))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 Not Found, got %d", w.Code)
	}
}

func TestNodes_UpdateState_InvalidState(t *testing.T) {
	router, _ := setupNodeTestRouter()

	invalidPayload, _ := json.Marshal(nodes.NodeStateUpdateRequest{State: "UNSUPPORTED_NODE_STATE"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/nodes/node1/state", bytes.NewBuffer(invalidPayload))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d", w.Code)
	}
}

func TestNodes_UpdateState_EmptyPayload(t *testing.T) {
	router, _ := setupNodeTestRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/nodes/node1/state", bytes.NewBuffer([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request for empty payload, got %d", w.Code)
	}
}
