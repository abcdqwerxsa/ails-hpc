package cluster_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ails-hpc/pkg/services/cluster"
	"ails-hpc/pkg/services/common"
	"ails-hpc/pkg/slurmrest"

	"github.com/gin-gonic/gin"
)

func setupClusterTestRouter(client *slurmrest.Client) *gin.Engine {
	gin.SetMode(gin.TestMode)
	service := cluster.NewClusterService(client)
	handler := cluster.NewClusterHandler(service)

	router := gin.New()
	router.Use(gin.Recovery())
	g := router.Group("/api/v1/slurm")
	g.GET("/ping", handler.GetStatus)
	g.GET("/partitions", handler.GetPartitions)
	return router
}

func TestCluster_Ping_Success(t *testing.T) {
	mock := common.NewMockSlurmServer()
	defer mock.Close()
	// 传入非空 token，避免 NewClient 触发 FetchToken 外壳调用
	client := slurmrest.NewClient(mock.URL, "hpcuser", "test-token")
	router := setupClusterTestRouter(client)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/slurm/ping", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Pings []struct {
			Ping string `json:"ping"`
		} `json:"pings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal ping response: %v", err)
	}
	if len(resp.Pings) == 0 || resp.Pings[0].Ping != "UP" {
		t.Fatalf("expected pings[0].ping=UP, got %+v", resp.Pings)
	}
}

func TestCluster_Partitions_Success(t *testing.T) {
	mock := common.NewMockSlurmServer()
	defer mock.Close()
	client := slurmrest.NewClient(mock.URL, "hpcuser", "test-token")
	router := setupClusterTestRouter(client)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/slurm/partitions", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Partitions []struct {
			Name       string `json:"name"`
			TotalNodes int    `json:"total_nodes"`
		} `json:"partitions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal partitions response: %v", err)
	}
	if len(resp.Partitions) == 0 {
		t.Fatalf("expected at least 1 partition, got 0")
	}
}

func TestCluster_NilClient_ReportedDown(t *testing.T) {
	router := setupClusterTestRouter(nil)

	// ping 不可达 → 503 {status:DOWN}
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/slurm/ping", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for nil client on ping, got %d", w.Code)
	}

	// partitions 不可达 → 500
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/api/v1/slurm/partitions", nil)
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for nil client on partitions, got %d", w2.Code)
	}
}
