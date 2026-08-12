package nodes_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ails-hpc/pkg/services/common"
	"ails-hpc/pkg/services/nodes"
	"ails-hpc/pkg/slurmrest"

	"github.com/gin-gonic/gin"
)

// setupNodeTestRouter 用真实 mock slurmrestd 支撑 service（不再用 nil client + 假 seed）。
// mock 返回 node1/node2/node3，与 docker-compose 集群拓扑一致。
func setupNodeTestRouter(t *testing.T) (*gin.Engine, nodes.NodeService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mock := common.NewMockSlurmServer()
	t.Cleanup(mock.Close)

	client := slurmrest.NewClient(mock.URL, "hpcuser", "test-token")
	service := nodes.NewNodeService(client)
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
	router, _ := setupNodeTestRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/slurm/nodes", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp nodes.NodesListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp.Nodes) < 3 {
		t.Fatalf("expected ≥3 real nodes from slurmrestd, got %d", len(resp.Nodes))
	}
	// 确认是 mock 返回的真实 node1/2/3，而非任何硬编码假数据
	seen := map[string]bool{}
	for _, n := range resp.Nodes {
		seen[n.Name] = true
	}
	for _, name := range []string{"node1", "node2", "node3"} {
		if !seen[name] {
			t.Errorf("expected real node %q in list", name)
		}
	}
}

// 回归守护：slurmrestd 不可达时绝不返回假数据——必须 500，且响应体不含 node1 等假节点。
// 这是本次"消灭假数据"修复的核心保证。
func TestNodes_ListNodes_NoFakeDataOnOutage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 指向死端口的客户端：连接被拒，模拟 slurmrestd 宕机
	client := slurmrest.NewClient("http://127.0.0.1:9", "hpcuser", "test-token")
	service := nodes.NewNodeService(client)
	handler := nodes.NewNodeHandler(service)

	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/api/v1/slurm/nodes", handler.GetNodes)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/slurm/nodes", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("slurmrestd 不可达应返回 500，got %d body=%s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("node1")) {
		t.Errorf("断网时泄露了假节点数据（不应出现 node1）: %s", w.Body.String())
	}
}

func TestNodes_UpdateState_DrainAndResume(t *testing.T) {
	router, _ := setupNodeTestRouter(t)

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
		t.Fatalf("expected status 200, got %d. Body: %s", w2.Code, w2.Body.String())
	}

	var resp2 nodes.NodeStateUpdateResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.State != "IDLE" && resp2.State != "RESUME" {
		t.Errorf("expected state IDLE or RESUME, got %s", resp2.State)
	}
}

func TestNodes_UpdateState_Idempotency(t *testing.T) {
	router, _ := setupNodeTestRouter(t)

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
		t.Fatalf("expected status 200 OK for repeat DRAIN, got %d. Body: %s", w2.Code, w2.Body.String())
	}

	var resp2 nodes.NodeStateUpdateResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.State != "DRAIN" {
		t.Errorf("expected state DRAIN on repeat call, got %s", resp2.State)
	}
}

func TestNodes_UpdateState_NotFound(t *testing.T) {
	router, _ := setupNodeTestRouter(t)

	drainPayload, _ := json.Marshal(nodes.NodeStateUpdateRequest{State: "DRAIN"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/nodes/non_existent_node_xyz/state", bytes.NewBuffer(drainPayload))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 Not Found, got %d. Body: %s", w.Code, w.Body.String())
	}
}

func TestNodes_UpdateState_InvalidState(t *testing.T) {
	router, _ := setupNodeTestRouter(t)

	invalidPayload, _ := json.Marshal(nodes.NodeStateUpdateRequest{State: "UNSUPPORTED_NODE_STATE"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/nodes/node1/state", bytes.NewBuffer(invalidPayload))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d. Body: %s", w.Code, w.Body.String())
	}
}

func TestNodes_UpdateState_EmptyPayload(t *testing.T) {
	router, _ := setupNodeTestRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/nodes/node1/state", bytes.NewBuffer([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request for empty payload, got %d. Body: %s", w.Code, w.Body.String())
	}
}
