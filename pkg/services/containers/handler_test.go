package containers_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"ails-hpc/pkg/services/containers"

	"github.com/gin-gonic/gin"
)

// 走真 HTTP 连接（httptest.Server）而非 httptest.ResponseRecorder：
// ReverseProxy 的 FlushInterval=-1 会调 ResponseWriter 的 CloseNotify，
// ResponseRecorder 没实现该接口会 panic；真连接有。
func runProxyIDETest(t *testing.T, session, envType, reqSuffix, wantBackendPath string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	host, port := backendHostPort(t, backend.URL)

	jobName := envType + "-ide-" + session
	jobs := &fakeJobsAPI{jobs: jobsResp(jrow{id: 2001, name: jobName, state: "RUNNING", nodes: "node1", submit: 1})}
	meta := &fakeMeta{m: map[string]containers.SessionMeta{session: {SessionID: session, NodeIP: host, Port: port, EnvType: envType}}}
	handler := containers.NewContainerHandler(newSvc(jobs, meta))

	router := gin.New()
	router.Any("/api/v1/ide/:session/*any", handler.ProxyIDE)
	front := httptest.NewServer(router)
	defer front.Close()

	resp, err := http.Get(front.URL + "/api/v1/ide/" + session + "/" + reqSuffix)
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if gotPath != wantBackendPath {
		t.Errorf("env=%s session=%s：后端收到 path=%q，want %q", envType, session, gotPath, wantBackendPath)
	}
}

// code-server 根路径启动，反代必须剥掉 /api/v1/ide/<session> 前缀。
func TestProxyIDE_VscodeStripsPrefix(t *testing.T) {
	runProxyIDETest(t, "ccc", "vscode", "static/foo.js", "/static/foo.js")
}

// Jupyter 有 base_url 对齐反代前缀，反代不剥（保持原样）。
func TestProxyIDE_JupyterKeepsPrefix(t *testing.T) {
	runProxyIDETest(t, "ddd", "jupyter", "lab", "/api/v1/ide/ddd/lab")
}

func backendHostPort(t *testing.T, rawurl string) (string, int) {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatalf("parse backend url: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("atoi port: %v", err)
	}
	return host, port
}
