package containers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ails-hpc/pkg/services/containers"
	"github.com/gin-gonic/gin"
)

func setupContainerTestRouter() (*gin.Engine, containers.ContainerService) {
	gin.SetMode(gin.TestMode)
	service := containers.NewContainerService()
	handler := containers.NewContainerHandler(service)

	router := gin.New()
	router.Use(gin.Recovery())

	slurmGroup := router.Group("/api/v1/slurm")
	{
		slurmGroup.POST("/containers/launch", handler.LaunchContainer)
		slurmGroup.GET("/containers/list", handler.ListContainers)
		slurmGroup.DELETE("/containers/:id", handler.RecycleContainer)
	}

	return router, service
}

func TestContainers_Launch_VSCodeAndJupyter(t *testing.T) {
	router, _ := setupContainerTestRouter()

	// 1. Launch VSCode
	bodyVSCode, _ := json.Marshal(containers.ContainerLaunchRequest{
		EnvType:  "vscode",
		CPUs:     4,
		MemoryMB: 8192,
	})
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/api/v1/slurm/containers/launch", bytes.NewBuffer(bodyVSCode))
	req1.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", w1.Code, w1.Body.String())
	}

	var resp1 containers.ContainerLaunchResponse
	if err := json.Unmarshal(w1.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp1.ContainerID == "" || resp1.Token == "" || resp1.WebURL == "" {
		t.Errorf("expected valid container_id, token, and web_url, got %v", resp1)
	}
	if !strings.Contains(resp1.WebURL, "vscode") {
		t.Errorf("expected web_url to contain 'vscode', got %s", resp1.WebURL)
	}

	// 2. Launch JupyterLab
	bodyJupyter, _ := json.Marshal(containers.ContainerLaunchRequest{
		EnvType:  "jupyter",
		CPUs:     2,
		MemoryMB: 4096,
	})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/api/v1/slurm/containers/launch", bytes.NewBuffer(bodyJupyter))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w2.Code)
	}

	var resp2 containers.ContainerLaunchResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.EnvType != "jupyter" || !strings.Contains(resp2.WebURL, "lab") {
		t.Errorf("expected env_type jupyter and lab web_url, got %v", resp2)
	}
}

func TestContainers_Launch_UnsupportedEnvType(t *testing.T) {
	router, _ := setupContainerTestRouter()

	body, _ := json.Marshal(containers.ContainerLaunchRequest{
		EnvType: "eclipse-ide",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/containers/launch", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d", w.Code)
	}
}

func TestContainers_Launch_NegativeResources(t *testing.T) {
	router, _ := setupContainerTestRouter()

	body, _ := json.Marshal(containers.ContainerLaunchRequest{
		EnvType:  "vscode",
		CPUs:     -10,
		MemoryMB: -4096,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/containers/launch", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d", w.Code)
	}
}

func TestContainers_Launch_ExceedingQuotaLimit(t *testing.T) {
	router, _ := setupContainerTestRouter()

	body, _ := json.Marshal(containers.ContainerLaunchRequest{
		EnvType:  "vscode",
		CPUs:     9999,
		MemoryMB: 9999999,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/slurm/containers/launch", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request for exceeding quota limit, got %d", w.Code)
	}
}

func TestContainers_ListActiveContainers(t *testing.T) {
	router, _ := setupContainerTestRouter()

	// Launch two containers
	b1, _ := json.Marshal(containers.ContainerLaunchRequest{EnvType: "vscode"})
	r1, _ := http.NewRequest("POST", "/api/v1/slurm/containers/launch", bytes.NewBuffer(b1))
	r1.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), r1)

	b2, _ := json.Marshal(containers.ContainerLaunchRequest{EnvType: "jupyter"})
	r2, _ := http.NewRequest("POST", "/api/v1/slurm/containers/launch", bytes.NewBuffer(b2))
	r2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), r2)

	// List active containers
	wList := httptest.NewRecorder()
	reqList, _ := http.NewRequest("GET", "/api/v1/slurm/containers/list", nil)
	router.ServeHTTP(wList, reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", wList.Code)
	}

	var listResp containers.ContainerListResponse
	if err := json.Unmarshal(wList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to unmarshal list response: %v", err)
	}

	if len(listResp.Containers) < 2 {
		t.Fatalf("expected at least 2 active containers, got %d", len(listResp.Containers))
	}
}

func TestContainers_RecycleContainerInstance(t *testing.T) {
	router, _ := setupContainerTestRouter()

	// Launch
	body, _ := json.Marshal(containers.ContainerLaunchRequest{EnvType: "vscode"})
	wLaunch := httptest.NewRecorder()
	reqLaunch, _ := http.NewRequest("POST", "/api/v1/slurm/containers/launch", bytes.NewBuffer(body))
	reqLaunch.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wLaunch, reqLaunch)

	var launchResp containers.ContainerLaunchResponse
	_ = json.Unmarshal(wLaunch.Body.Bytes(), &launchResp)
	ctrID := launchResp.ContainerID

	// Recycle
	wRecycle := httptest.NewRecorder()
	reqRecycle, _ := http.NewRequest("DELETE", "/api/v1/slurm/containers/"+ctrID, nil)
	router.ServeHTTP(wRecycle, reqRecycle)

	if wRecycle.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", wRecycle.Code)
	}

	var recycleResp containers.ContainerRecycleResponse
	_ = json.Unmarshal(wRecycle.Body.Bytes(), &recycleResp)
	if recycleResp.Status != "TERMINATED" {
		t.Errorf("expected status TERMINATED, got %s", recycleResp.Status)
	}

	// Recycle again (should return 404)
	wRecycle2 := httptest.NewRecorder()
	reqRecycle2, _ := http.NewRequest("DELETE", "/api/v1/slurm/containers/"+ctrID, nil)
	router.ServeHTTP(wRecycle2, reqRecycle2)

	if wRecycle2.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 Not Found when recycling already recycled container, got %d", wRecycle2.Code)
	}
}

func TestContainers_RecycleNonExistentContainer(t *testing.T) {
	router, _ := setupContainerTestRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/api/v1/slurm/containers/non_existent_ctr_999", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 Not Found for non-existent container, got %d", w.Code)
	}
}
