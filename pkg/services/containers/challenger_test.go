package containers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ails-hpc/pkg/services/containers"
	"github.com/gin-gonic/gin"
)

// TestChallenger_Containers_ConcurrencyStress tests concurrent launch, list, and recycle with go test -race.
func TestChallenger_Containers_ConcurrencyStress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := containers.NewContainerService()
	handler := containers.NewContainerHandler(svc)

	router := gin.New()
	router.POST("/api/v1/slurm/containers/launch", handler.LaunchContainer)
	router.GET("/api/v1/slurm/containers/list", handler.ListContainers)
	router.DELETE("/api/v1/slurm/containers/:id", handler.RecycleContainer)

	ctx := context.Background()
	var wg sync.WaitGroup

	numWorkers := 40
	opsPerWorker := 20

	launchedIDs := make(chan string, numWorkers*opsPerWorker)

	// 1. Concurrent Launchers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			envTypes := []string{"vscode", "jupyter", "  vscode  ", "JUPYTER"}

			for j := 0; j < opsPerWorker; j++ {
				env := envTypes[(workerID+j)%len(envTypes)]
				req := &containers.ContainerLaunchRequest{
					EnvType:  env,
					CPUs:     2,
					MemoryMB: 4096,
				}

				// HTTP endpoint call
				bodyBytes, _ := json.Marshal(req)
				httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/slurm/containers/launch", bytes.NewBuffer(bodyBytes))
				httpReq.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httpReq)

				if w.Code == http.StatusOK {
					var resp containers.ContainerLaunchResponse
					if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil && resp.ContainerID != "" {
						launchedIDs <- resp.ContainerID
					}
				}
			}
		}(i)
	}

	// 2. Concurrent Readers (ListContainers)
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < opsPerWorker; j++ {
				_, err := svc.ListActiveContainers(ctx)
				if err != nil {
					t.Errorf("ListActiveContainers returned error: %v", err)
				}

				httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/slurm/containers/list", nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httpReq)
				if w.Code != http.StatusOK {
					t.Errorf("HTTP ListContainers expected 200, got %d", w.Code)
				}
			}
		}(i)
	}

	wg.Wait()
	close(launchedIDs)

	// 3. Concurrent Recyclers
	var recycleWg sync.WaitGroup
	var successRecycles int64

	for id := range launchedIDs {
		recycleWg.Add(1)
		go func(ctrID string) {
			defer recycleWg.Done()
			httpReq := httptest.NewRequest(http.MethodDelete, "/api/v1/slurm/containers/"+ctrID, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httpReq)
			if w.Code == http.StatusOK {
				atomic.AddInt64(&successRecycles, 1)
			}
		}(id)
	}

	recycleWg.Wait()

	if successRecycles == 0 {
		t.Errorf("Expected at least one successful container recycling during stress test")
	}
}

// TestChallenger_Containers_ConcurrentRecycleSameID tests race condition when multiple callers recycle the same container ID.
func TestChallenger_Containers_ConcurrentRecycleSameID(t *testing.T) {
	router, svc := setupContainerTestRouter()
	ctx := context.Background()

	// Launch single container
	res, err := svc.LaunchContainer(ctx, &containers.ContainerLaunchRequest{EnvType: "vscode"})
	if err != nil {
		t.Fatalf("Failed to launch container: %v", err)
	}
	ctrID := res.ContainerID

	numCallers := 30
	var wg sync.WaitGroup
	var okCount int64
	var notFoundCount int64

	for i := 0; i < numCallers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/slurm/containers/"+ctrID, nil)
			router.ServeHTTP(w, req)

			if w.Code == http.StatusOK {
				atomic.AddInt64(&okCount, 1)
			} else if w.Code == http.StatusNotFound {
				atomic.AddInt64(&notFoundCount, 1)
			}
		}()
	}

	wg.Wait()

	if okCount != 1 {
		t.Errorf("Expected exactly 1 OK status (200) for concurrent recycling of same container, got %d", okCount)
	}
	if notFoundCount != int64(numCallers-1) {
		t.Errorf("Expected %d 404 Not Found responses, got %d", numCallers-1, notFoundCount)
	}
}

// TestChallenger_Containers_UnsupportedIDEEnvs tests a wide variety of invalid or unsupported environment types.
func TestChallenger_Containers_UnsupportedIDEEnvs(t *testing.T) {
	router, _ := setupContainerTestRouter()

	unsupportedEnvs := []string{
		"eclipse",
		"rstudio",
		"pycharm",
		"intellij",
		"sublime",
		"webstudio",
		"vscode-insiders",
		"12345",
		"null",
		"true",
		"",
	}

	for _, env := range unsupportedEnvs {
		t.Run("Env_"+env, func(t *testing.T) {
			body, _ := json.Marshal(containers.ContainerLaunchRequest{EnvType: env})
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/slurm/containers/launch", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("Expected status 400 Bad Request for unsupported env %q, got %d. Body: %s", env, w.Code, w.Body.String())
			}
		})
	}
}

// TestChallenger_Containers_QuotaAndResourceBoundaries tests boundary CPU/Memory and negative resource validation.
func TestChallenger_Containers_QuotaAndResourceBoundaries(t *testing.T) {
	router, _ := setupContainerTestRouter()

	tests := []struct {
		name           string
		req            containers.ContainerLaunchRequest
		expectedStatus int
	}{
		{"Exact max CPUs 512", containers.ContainerLaunchRequest{EnvType: "vscode", CPUs: 512, MemoryMB: 8192}, http.StatusOK},
		{"Exceed max CPUs 513", containers.ContainerLaunchRequest{EnvType: "vscode", CPUs: 513, MemoryMB: 8192}, http.StatusBadRequest},
		{"Exact max Memory 1000000 MB", containers.ContainerLaunchRequest{EnvType: "jupyter", CPUs: 4, MemoryMB: 1000000}, http.StatusOK},
		{"Exceed max Memory 1000001 MB", containers.ContainerLaunchRequest{EnvType: "jupyter", CPUs: 4, MemoryMB: 1000001}, http.StatusBadRequest},
		{"Negative CPUs -1", containers.ContainerLaunchRequest{EnvType: "vscode", CPUs: -1, MemoryMB: 4096}, http.StatusBadRequest},
		{"Negative Memory -1024", containers.ContainerLaunchRequest{EnvType: "vscode", CPUs: 2, MemoryMB: -1024}, http.StatusBadRequest},
		{"Negative Nodes -5", containers.ContainerLaunchRequest{EnvType: "vscode", CPUs: 2, MemoryMB: 4096, Nodes: -5}, http.StatusBadRequest},
		{"Zero values default correctly", containers.ContainerLaunchRequest{EnvType: "vscode", CPUs: 0, MemoryMB: 0, Nodes: 0}, http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.req)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/slurm/containers/launch", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			if w.Code != tc.expectedStatus {
				t.Errorf("Expected status %d for %s, got %d. Body: %s", tc.expectedStatus, tc.name, w.Code, w.Body.String())
			}

			if w.Code == http.StatusOK {
				var resp containers.ContainerLaunchResponse
				_ = json.Unmarshal(w.Body.Bytes(), &resp)
				if tc.req.CPUs == 0 && resp.Allocated.CPUs != 2 {
					t.Errorf("Expected default 2 CPUs, got %d", resp.Allocated.CPUs)
				}
				if tc.req.MemoryMB == 0 && resp.Allocated.MemoryMB != 4096 {
					t.Errorf("Expected default 4096 MemoryMB, got %d", resp.Allocated.MemoryMB)
				}
			}
		})
	}
}

// TestChallenger_Containers_JWTProxyAndWebURLValidation checks token construction and web URL proxy formatting.
func TestChallenger_Containers_JWTProxyAndWebURLValidation(t *testing.T) {
	router, _ := setupContainerTestRouter()

	// 1. VSCode URL test
	bodyVS, _ := json.Marshal(containers.ContainerLaunchRequest{EnvType: "vscode", CPUs: 8})
	wVS := httptest.NewRecorder()
	reqVS := httptest.NewRequest(http.MethodPost, "/api/v1/slurm/containers/launch", bytes.NewBuffer(bodyVS))
	reqVS.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wVS, reqVS)

	var respVS containers.ContainerLaunchResponse
	_ = json.Unmarshal(wVS.Body.Bytes(), &respVS)

	if !strings.HasPrefix(respVS.WebURL, "http://192.168.20.226:8080/vscode/?token=") {
		t.Errorf("Unexpected VSCode URL format: %s", respVS.WebURL)
	}
	if !strings.Contains(respVS.WebURL, "&cpus=8") {
		t.Errorf("VSCode WebURL missing cpus parameter: %s", respVS.WebURL)
	}

	// 2. Jupyter URL test
	bodyJup, _ := json.Marshal(containers.ContainerLaunchRequest{EnvType: "jupyter", CPUs: 16})
	wJup := httptest.NewRecorder()
	reqJup := httptest.NewRequest(http.MethodPost, "/api/v1/slurm/containers/launch", bytes.NewBuffer(bodyJup))
	reqJup.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wJup, reqJup)

	var respJup containers.ContainerLaunchResponse
	_ = json.Unmarshal(wJup.Body.Bytes(), &respJup)

	if !strings.HasPrefix(respJup.WebURL, "http://192.168.20.226:8888/lab?token=") {
		t.Errorf("Unexpected JupyterLab URL format: %s", respJup.WebURL)
	}
	if !strings.Contains(respJup.WebURL, "&cpus=16") {
		t.Errorf("JupyterLab WebURL missing cpus parameter: %s", respJup.WebURL)
	}
}

// TestChallenger_Containers_NonExistentAndRepeatRecycle tests deletion of non-existent and recycled container instances.
func TestChallenger_Containers_NonExistentAndRepeatRecycle(t *testing.T) {
	router, svc := setupContainerTestRouter()
	ctx := context.Background()

	// 1. Non-existent deletion
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodDelete, "/api/v1/slurm/containers/c-non-existent-9999", nil)
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusNotFound {
		t.Errorf("Expected 404 for non-existent container deletion, got %d", w1.Code)
	}

	// 2. Double recycle
	res, _ := svc.LaunchContainer(ctx, &containers.ContainerLaunchRequest{EnvType: "jupyter"})
	ctrID := res.ContainerID

	// First recycle -> 200
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodDelete, "/api/v1/slurm/containers/"+ctrID, nil)
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on first recycle, got %d", w2.Code)
	}

	// Second recycle -> 404
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodDelete, "/api/v1/slurm/containers/"+ctrID, nil)
	router.ServeHTTP(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("Expected 404 Not Found on second recycle of same container, got %d", w3.Code)
	}
}
