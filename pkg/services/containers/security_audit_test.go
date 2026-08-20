package containers_test

// 安全审计 2026-08-19 回归（containers 层）：
//   - P0-3 ProxyIDE 会话归属校验——非属主（member 视角）反代他人会话 → 403
//   - P1-4 SessionOwner 锚定 Slurm 作业 Account（meta.Owner 不再可信）

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/services/containers"

	"github.com/gin-gonic/gin"
)

// memberAttackerFixture：作业 Account=victim（Slurm 强制身份），伪造 meta 把 owner
// 写成 attacker——旧实现以 meta.Owner 判归属会被骗过；新实现两者都必须拒。
func TestProxyIDE_OwnerEnforced(t *testing.T) {
	gin.SetMode(gin.TestMode)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // 若属主校验失效，攻击者会拿到 200
	}))
	defer backend.Close()
	host, port := backendHostPort(t, backend.URL)

	jobs := &fakeJobsAPI{jobs: jobsResp(jrow{id: 3001, name: "jupyter-ide-vic", state: "RUNNING", nodes: "node1", submit: 1, account: "victim"})}
	meta := &fakeMeta{m: map[string]containers.SessionMeta{
		"vic": {SessionID: "vic", NodeIP: host, Port: port, EnvType: "jupyter", Owner: "attacker"},
	}}
	svc := newSvc(jobs, meta)

	// SessionOwner：以作业 Account 为准（P1-4）
	owner, err := svc.SessionOwner(context.Background(), "vic")
	if err != nil || owner != "victim" {
		t.Fatalf("SessionOwner = (%q, %v), want victim（作业 Account 权威）", owner, err)
	}

	tenants := auth.TenantResolver(func(string) ([]string, error) { return []string{"victim"}, nil })
	handler := containers.NewContainerHandlerScoped(svc, tenants)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("claims", &auth.Claims{Username: "attacker", Role: auth.RoleMember, ClusterUser: "attacker"})
		c.Next()
	})
	router.Any("/api/v1/ide/:session/*any", handler.ProxyIDE)

	req, _ := http.NewRequest("GET", "/api/v1/ide/vic/api", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("attacker proxying victim session: want 403, got %d（P0-3/P1-4 失效）", w.Code)
	}

	// 属主本人放行（ReverseProxy 的 CloseNotify 需要真连接——与既有 ProxyIDE 测试同教义）
	router2 := gin.New()
	router2.Use(func(c *gin.Context) {
		c.Set("claims", &auth.Claims{Username: "victim", Role: auth.RoleMember, ClusterUser: "victim"})
		c.Next()
	})
	router2.Any("/api/v1/ide/:session/*any", handler.ProxyIDE)
	front2 := httptest.NewServer(router2)
	defer front2.Close()

	resp2, err := http.Get(front2.URL + "/api/v1/ide/vic/api")
	if err != nil {
		t.Fatalf("owner get: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("owner proxying own session: want 200, got %d", resp2.StatusCode)
	}
}
