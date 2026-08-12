package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/services/billing"
	"ails-hpc/pkg/services/cluster"
	"ails-hpc/pkg/services/common"
	"ails-hpc/pkg/services/containers"
	"ails-hpc/pkg/services/jobs"
	"ails-hpc/pkg/services/nodes"
	"ails-hpc/pkg/slurmrest"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// zeroSacctFetcher 返回空作业历史，让计费路由在无集群的矩阵测试里仍能 200。
type zeroSacctFetcher struct{}

func (zeroSacctFetcher) Query(ctx context.Context, user string, start, end time.Time) ([]billing.SacctRow, error) {
	return nil, nil
}

// setupTestRouter 构造一个接入真实 NewRouter 的测试路由：
//   - 内存四角色用户库（admin/member/tenant_admin/ops，明文 *123）
//   - common.MockSlurmServer 承载 slurmrestd v0.0.37 调用
// 该测试驱动的是生产路由表本身，而非 test/e2e 的内存平行实现。
func setupTestRouter(t *testing.T) (*gin.Engine, *common.MockSlurmServer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auth.SetSecret([]byte("integration-test-secret"))

	mock := common.NewMockSlurmServer()
	t.Cleanup(mock.Close)

	slurmClient := slurmrest.NewClient(mock.URL, "hpcuser", "test-token")
	billingService := billing.NewBillingServiceWithFetcher(zeroSacctFetcher{})

	store := auth.NewUserStoreFromList([]auth.User{
		{Username: "admin", PasswordHash: hashPw("admin123"), Role: auth.RoleSystemAdmin, OrgSlug: "hpc-lab", TenantNS: "default"},
		{Username: "tenantadmin", PasswordHash: hashPw("tenantadmin123"), Role: auth.RoleTenantAdmin, OrgSlug: "hpc-lab", TenantNS: "default"},
		{Username: "member", PasswordHash: hashPw("member123"), Role: auth.RoleMember, OrgSlug: "hpc-lab", TenantNS: "default"},
		{Username: "ops", PasswordHash: hashPw("ops123"), Role: auth.RoleOpsAdmin, OrgSlug: "hpc-lab", TenantNS: "default"},
	})

	h := Handlers{
		Auth:       auth.NewAuthHandler(store),
		Cluster:    cluster.NewClusterHandler(cluster.NewClusterService(slurmClient)),
		Nodes:      nodes.NewNodeHandler(nodes.NewNodeService(slurmClient)),
		Jobs:       jobs.NewJobHandler(jobs.NewJobService(slurmClient)),
		Containers: containers.NewContainerHandler(containers.NewContainerService()),
		Billing:    billing.NewBillingHandler(billingService),
	}
	return NewRouter(h), mock
}

func hashPw(pw string) string {
	h, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	return string(h)
}

func tokenFor(t *testing.T, role string) string {
	t.Helper()
	if role == "" {
		return ""
	}
	tok, err := auth.GenerateToken(role, role, "hpc-lab", "default")
	if err != nil {
		t.Fatalf("mint token for %s: %v", role, err)
	}
	return tok
}

func doRequest(r *gin.Engine, method, path, body, token string) int {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}
	req, _ := http.NewRequest(method, path, bodyReader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// TestRouter_Login 校验登录契约与失败路径
func TestRouter_Login(t *testing.T) {
	r, _ := setupTestRouter(t)

	// 成功 → 200 + React 契约 {token, user:{username,role,...}}
	code, body := doRequestWithBody(r, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123","orgSlug":"hpc-lab"}`)
	if code != http.StatusOK {
		t.Fatalf("login success: want 200 got %d body=%s", code, body)
	}
	var resp auth.LoginResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if resp.Token == "" || resp.User.Role != auth.RoleSystemAdmin || resp.User.Username != "admin" {
		t.Fatalf("unexpected login response: %+v", resp)
	}

	// 错误密码 → 401
	if c, _ := doRequestWithBody(r, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"x"}`); c != http.StatusUnauthorized {
		t.Fatalf("wrong password: want 401 got %d", c)
	}

	// 未知用户 → 同样 401（避免枚举）
	if c, _ := doRequestWithBody(r, http.MethodPost, "/api/v1/auth/login", `{"username":"ghost","password":"x"}`); c != http.StatusUnauthorized {
		t.Fatalf("unknown user: want 401 got %d", c)
	}

	// 缺字段 → 400
	if c, _ := doRequestWithBody(r, http.MethodPost, "/api/v1/auth/login", `{"username":"admin"}`); c != http.StatusBadRequest {
		t.Fatalf("missing fields: want 400 got %d", c)
	}
}

// TestRouter_JWTEnforcement fail-closed：无/坏/过期令牌一律 401
func TestRouter_JWTEnforcement(t *testing.T) {
	r, _ := setupTestRouter(t)

	if c := doRequest(r, http.MethodGet, "/api/v1/slurm/nodes", "", ""); c != http.StatusUnauthorized {
		t.Fatalf("no token: want 401 got %d", c)
	}
	if c := doRequest(r, http.MethodGet, "/api/v1/slurm/nodes", "", "garbage.token"); c != http.StatusUnauthorized {
		t.Fatalf("bad token: want 401 got %d", c)
	}
	expired, _ := auth.GenerateTokenWithTTL("admin", auth.RoleSystemAdmin, "hpc-lab", "default", -1*time.Hour)
	if c := doRequest(r, http.MethodGet, "/api/v1/slurm/nodes", "", expired); c != http.StatusUnauthorized {
		t.Fatalf("expired token: want 401 got %d", c)
	}
}

// TestRouter_RouteMatrix 表驱动覆盖四角色 × 关键路由的鉴权矩阵。
// 这是本轮安全重构的核心回归门禁 —— 任何矩阵偏移都会被这里捕获。
func TestRouter_RouteMatrix(t *testing.T) {
	r, _ := setupTestRouter(t)

	jobSubmit := `{"name":"x","script":"echo hi"}`
	launch := `{"env_type":"vscode"}`
	drain := `{"state":"DRAIN"}`

	tests := []struct {
		name   string
		role   string // "" = 不带 token
		method string
		path   string
		body   string
		want   int
	}{
		// fail-closed
		{"no token -> 401", "", http.MethodGet, "/api/v1/slurm/nodes", "", http.StatusUnauthorized},

		// 读：所有已认证角色
		{"admin read nodes", auth.RoleSystemAdmin, http.MethodGet, "/api/v1/slurm/nodes", "", http.StatusOK},
		{"member read nodes", auth.RoleMember, http.MethodGet, "/api/v1/slurm/nodes", "", http.StatusOK},
		{"ops read nodes", auth.RoleOpsAdmin, http.MethodGet, "/api/v1/slurm/nodes", "", http.StatusOK},

		// 节点 DRAIN/RESUME：admin 独占
		{"admin drain ok", auth.RoleSystemAdmin, http.MethodPost, "/api/v1/slurm/nodes/node1/state", drain, http.StatusOK},
		{"member drain denied", auth.RoleMember, http.MethodPost, "/api/v1/slurm/nodes/node1/state", drain, http.StatusForbidden},
		{"tenant_admin drain denied", auth.RoleTenantAdmin, http.MethodPost, "/api/v1/slurm/nodes/node1/state", drain, http.StatusForbidden},
		{"ops drain denied", auth.RoleOpsAdmin, http.MethodPost, "/api/v1/slurm/nodes/node1/state", drain, http.StatusForbidden},

		// 作业提交：member + tenant_admin（admin 纯监控不可提交）
		{"admin submit denied", auth.RoleSystemAdmin, http.MethodPost, "/api/v1/slurm/jobs/submit", jobSubmit, http.StatusForbidden},
		{"member submit ok", auth.RoleMember, http.MethodPost, "/api/v1/slurm/jobs/submit", jobSubmit, http.StatusOK},
		{"tenant_admin submit ok", auth.RoleTenantAdmin, http.MethodPost, "/api/v1/slurm/jobs/submit", jobSubmit, http.StatusOK},
		{"ops submit denied", auth.RoleOpsAdmin, http.MethodPost, "/api/v1/slurm/jobs/submit", jobSubmit, http.StatusForbidden},

		// 容器 IDE：member + tenant_admin
		{"member launch ok", auth.RoleMember, http.MethodPost, "/api/v1/slurm/containers/launch", launch, http.StatusOK},
		{"admin launch denied", auth.RoleSystemAdmin, http.MethodPost, "/api/v1/slurm/containers/launch", launch, http.StatusForbidden},
		{"ops launch denied", auth.RoleOpsAdmin, http.MethodPost, "/api/v1/slurm/containers/launch", launch, http.StatusForbidden},

		// 计费读取：member/tenant_admin/ops（admin 不可）
		{"admin billing denied", auth.RoleSystemAdmin, http.MethodGet, "/api/v1/slurm/billing/usage", "", http.StatusForbidden},
		{"member billing ok", auth.RoleMember, http.MethodGet, "/api/v1/slurm/billing/usage", "", http.StatusOK},
		{"tenant_admin billing ok", auth.RoleTenantAdmin, http.MethodGet, "/api/v1/slurm/billing/usage", "", http.StatusOK},
		{"ops billing ok", auth.RoleOpsAdmin, http.MethodGet, "/api/v1/slurm/billing/usage", "", http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := doRequest(r, tc.method, tc.path, tc.body, tokenFor(t, tc.role))
			if got != tc.want {
				t.Fatalf("%s %s as %q: want %d got %d", tc.method, tc.path, tc.role, tc.want, got)
			}
		})
	}
}

// doRequestWithBody 与 doRequest 类似，但回传响应体（登录测试需要解析）。
func doRequestWithBody(r *gin.Engine, method, path, body string) (int, string) {
	req, _ := http.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}
