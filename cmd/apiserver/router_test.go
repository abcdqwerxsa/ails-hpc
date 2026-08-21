package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/services/admin"
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

// captureFetcher 记录传给 sacct 的 user 参数并返回固定行（计费 scope 断言用）。
type captureFetcher struct {
	gotUser []string
	rows    []billing.SacctRow
}

func (f *captureFetcher) Query(ctx context.Context, user string, start, end time.Time) ([]billing.SacctRow, error) {
	f.gotUser = append(f.gotUser, user)
	if user == "" {
		return f.rows, nil // 空 user = 全量（同 sacct 不带 --user）
	}
	// 忠实效仿 sacct --user=：服务端按用户过滤
	out := make([]billing.SacctRow, 0, len(f.rows))
	for _, r := range f.rows {
		if r.User == user {
			out = append(out, r)
		}
	}
	return out, nil
}

// setupTestRouter 构造一个接入真实 NewRouter 的测试路由：
//   - 内存四角色用户库（admin/member/tenant_admin/ops，明文 *123）
//   - common.MockSlurmServer 承载 slurmrestd v0.0.37 调用
//
// 该测试驱动的是生产路由表本身，而非 test/e2e 的内存平行实现。
func setupTestRouter(t *testing.T) (*gin.Engine, *common.MockSlurmServer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auth.SetSecret([]byte("integration-test-secret"))

	mock := common.NewMockSlurmServer()
	t.Cleanup(mock.Close)

	slurmClient := slurmrest.NewClient(mock.URL, "hpcuser", "test-token")
	fetcher := &captureFetcher{rows: []billing.SacctRow{
		{JobID: "1", User: "ailsmember", Account: "ailsmember", ElapsedRaw: 3600, AllocCPUS: 2},
		{JobID: "2", User: "ailsother", Account: "ailsother", ElapsedRaw: 3600, AllocCPUS: 4},
	}}
	billingService := billing.NewBillingServiceWithFetcher(fetcher)

	store := auth.NewUserStoreFromList([]auth.User{
		{Username: "admin", PasswordHash: hashPw("admin123"), Role: auth.RoleSystemAdmin, OrgSlug: "hpc-lab", TenantNS: "default", ClusterUser: "ailsadmin", Account: "ailsadmin"},
		{Username: "tenantadmin", PasswordHash: hashPw("tenantadmin123"), Role: auth.RoleTenantAdmin, OrgSlug: "hpc-lab", TenantNS: "default", ClusterUser: "ailstadmin", Account: "ailstadmin"},
		{Username: "member", PasswordHash: hashPw("member123"), Role: auth.RoleMember, OrgSlug: "hpc-lab", TenantNS: "default", ClusterUser: "ailsmember", Account: "ailsmember"},
		{Username: "ops", PasswordHash: hashPw("ops123"), Role: auth.RoleOpsAdmin, OrgSlug: "hpc-lab", TenantNS: "default", ClusterUser: "ailsops", Account: "ailsops"},
		{Username: "member2", PasswordHash: hashPw("member2123"), Role: auth.RoleMember, OrgSlug: "hpc-lab", TenantNS: "default", ClusterUser: "member2", Account: "member2"},
		{Username: "biomember", PasswordHash: hashPw("biomember1"), Role: auth.RoleMember, OrgSlug: "bio-lab", TenantNS: "default", ClusterUser: "ailsmember2", Account: "ailsmember2"},
	})

	tenantMembers := func(st auth.UserStore) auth.TenantResolver {
		return func(tenant string) ([]string, error) {
			var members []string
			for _, u := range st.ListUsers() {
				if u.OrgSlug == tenant {
					members = append(members, u.ClusterUser)
				}
			}
			return members, nil
		}
	}

	h := Handlers{
		Auth:       auth.NewAuthHandler(store),
		Cluster:    cluster.NewClusterHandler(cluster.NewClusterService(slurmClient)),
		Nodes:      nodes.NewNodeHandler(nodes.NewNodeServiceWithApplier(slurmClient, func(string, string, string) error { return nil })),
		Jobs:       jobs.NewJobHandlerScoped(jobs.NewJobService(slurmClient), tenantMembers(store)),
		Containers: containers.NewContainerHandlerScoped(containers.NewContainerService(slurmClient), tenantMembers(store)),
		Billing: billing.NewBillingHandlerWithScope(billingService, func(tenant string) ([]string, error) {
			var members []string
			for _, u := range store.ListUsers() {
				if u.OrgSlug == tenant {
					members = append(members, u.ClusterUser)
				}
			}
			return members, nil
		}),
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
	// WithStore 活体校验要求用户在 store 中；clusterUser 一并对齐 store 的 ails* 命名，
	// 使租户成员解析（按 orgSlug 派生 clusterUser 清单）能命中提交者身份。
	userOf := map[string][2]string{
		auth.RoleSystemAdmin: {"admin", "ailsadmin"},
		auth.RoleTenantAdmin: {"tenantadmin", "ailstadmin"},
		auth.RoleMember:      {"member", "ailsmember"},
		auth.RoleOpsAdmin:    {"ops", "ailsops"},
	}
	pair, ok := userOf[role]
	if !ok {
		pair = [2]string{role, role}
	}
	tok, err := auth.GenerateToken(pair[0], role, "hpc-lab", "default", pair[1], pair[1])
	if err != nil {
		t.Fatalf("mint token for %s: %v", role, err)
	}
	return tok
}

func doRequest(r *gin.Engine, method, path, body, token string) int {
	code, _ := doAuth(r, method, path, body, token)
	return code
}

// doAuth 发带 token 的请求，回传 (code, body)。
func doAuth(r *gin.Engine, method, path, body, token string) (int, string) {
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
	return w.Code, w.Body.String()
}

// TestRouter_JobOwnership 归属隔离：member 只能取消自己的作业；他人 403；tenant_admin 越权。
func TestRouter_JobOwnership(t *testing.T) {
	r, _ := setupTestRouter(t)
	memberTok := tokenFor(t, auth.RoleMember) // username=member
	member2Tok, _ := auth.GenerateToken("member2", auth.RoleMember, "hpc-lab", "default", "member2", "member2")
	tenantTok := tokenFor(t, auth.RoleTenantAdmin)

	submit := func() int {
		code, body := doAuth(r, http.MethodPost, "/api/v1/slurm/jobs/submit", `{"name":"own","script":"echo hi"}`, memberTok)
		if code != http.StatusOK {
			t.Fatalf("submit: want 200 got %d body=%s", code, body)
		}
		var resp struct {
			JobID int `json:"job_id"`
		}
		_ = json.Unmarshal([]byte(body), &resp)
		return resp.JobID
	}
	cancel := func(jobID int, token string) int {
		c, _ := doAuth(r, http.MethodPost, fmt.Sprintf("/api/v1/slurm/jobs/%d/cancel", jobID), "", token)
		return c
	}

	jobID := submit()
	// 他人（member2）取消 member 的作业 → 403
	if c := cancel(jobID, member2Tok); c != http.StatusForbidden {
		t.Errorf("member2 cancel member's job: want 403, got %d", c)
	}
	// owner（member）取消 → 200
	if c := cancel(jobID, memberTok); c != http.StatusOK {
		t.Errorf("owner cancel: want 200, got %d", c)
	}
	// tenant_admin 越权取消 → 200
	jobID2 := submit()
	if c := cancel(jobID2, tenantTok); c != http.StatusOK {
		t.Errorf("tenant_admin override cancel: want 200, got %d", c)
	}
}

// TestRouter_PerUserSubmitIdentity 真·每用户身份：member 提交的作业回读 owner 必须是其
// clusterUser（"member"），不再是无差别的 root。这是 L1 隔离的端到端证据。
func TestRouter_PerUserSubmitIdentity(t *testing.T) {
	r, _ := setupTestRouter(t)
	memberTok := tokenFor(t, auth.RoleMember) // clusterUser="member"

	code, body := doAuth(r, http.MethodPost, "/api/v1/slurm/jobs/submit", `{"name":"idtest","script":"echo hi"}`, memberTok)
	if code != http.StatusOK {
		t.Fatalf("submit: want 200 got %d body=%s", code, body)
	}

	code, body = doAuth(r, http.MethodGet, "/api/v1/slurm/jobs", "", memberTok)
	if code != http.StatusOK {
		t.Fatalf("list: want 200 got %d body=%s", code, body)
	}
	var list jobs.JobListResponse
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("unmarshal jobs: %v body=%s", err, body)
	}
	var found bool
	for _, j := range list.Jobs {
		if j.Name == "idtest" {
			found = true
			if j.Owner != "ailsmember" {
				t.Errorf("job owner=%q want \"ailsmember\" (per-user clusterUser; was root before L1 isolation)", j.Owner)
			}
		}
	}
	if !found {
		t.Fatalf("submitted job idtest not found in list: %+v", list.Jobs)
	}
}

// TestRouter_L4ControlAuthz 控制操作的下发身份（L4）：member 取消自己的作业时，
// 到 slurmrestd 的请求必须以其 clusterUser 执行（"member"）；tenant_admin 越权取消
// 走 root（"root"）。mock 按 Slurm 语义执法（非属主非 root → 403）。
func TestRouter_L4ControlAuthz(t *testing.T) {
	r, mock := setupTestRouter(t)
	memberTok := tokenFor(t, auth.RoleMember) // clusterUser="member"
	tenantTok := tokenFor(t, auth.RoleTenantAdmin)

	sub := func() int {
		code, body := doAuth(r, http.MethodPost, "/api/v1/slurm/jobs/submit", `{"name":"l4","script":"echo hi"}`, memberTok)
		if code != http.StatusOK {
			t.Fatalf("submit: want 200 got %d body=%s", code, body)
		}
		var resp struct {
			JobID int `json:"job_id"`
		}
		_ = json.Unmarshal([]byte(body), &resp)
		return resp.JobID
	}
	cancel := func(id int, tok string) int {
		c, _ := doAuth(r, http.MethodPost, fmt.Sprintf("/api/v1/slurm/jobs/%d/cancel", id), "", tok)
		return c
	}

	// member 取消自己的作业 → 线上身份必须是 "member"（per-user 令牌）
	id := sub()
	if c := cancel(id, memberTok); c != http.StatusOK {
		t.Fatalf("member cancel own: want 200 got %d", c)
	}
	if got := mock.LastControlUser(); got != "ailsmember" {
		t.Errorf("control acting user = %q, want \"ailsmember\" (per-user token)", got)
	}

	// tenant_admin 越权取消 → 走 root
	id2 := sub()
	if c := cancel(id2, tenantTok); c != http.StatusOK {
		t.Fatalf("tenant_admin override cancel: want 200 got %d", c)
	}
	if got := mock.LastControlUser(); got != "root" {
		t.Errorf("override control acting user = %q, want \"root\"", got)
	}
}

// TestRouter_BillingScope 多租户 Phase 0：计费读按登录者收口。
//   - member 带 ?user=他人 → 无视 query，强制本人（修复"可读任意用户账单"漏洞）
//   - tenant_admin ?user= 跨租户 → 403
//   - tenant_admin 缺省 → 全量拉取后按本租户成员过滤（响应只含本租户用户）
func TestRouter_BillingScope(t *testing.T) {
	r, _ := setupTestRouter(t)
	// clusterUser 与测试用户库对齐（ailsmember ∈ hpc-lab），使 scope 过滤可断言
	memberTok, _ := auth.GenerateToken("member", auth.RoleMember, "hpc-lab", "default", "ailsmember", "ailsmember")
	tenantTok := tokenFor(t, auth.RoleTenantAdmin)
	opsTok := tokenFor(t, auth.RoleOpsAdmin)

	get := func(path, tok string) (int, string) { return doAuth(r, http.MethodGet, path, "", tok) }

	// 1) member 试图读他人：query 被无视，数据只含本维度（fetcher 收到 user="member"）
	code, body := get("/api/v1/slurm/billing/usage?user=ailsother", memberTok)
	if code != http.StatusOK {
		t.Fatalf("member usage: want 200 got %d body=%s", code, body)
	}
	if !jsonContains(body, `"user":"ailsmember"`) || jsonContains(body, `"user":"ailsother"`) {
		t.Errorf("member must be forced to own data only; body=%s", body)
	}

	// 2) tenant_admin 跨租户 ?user= → 403
	if code, _ := get("/api/v1/slurm/billing/usage?user=not-in-tenant", tenantTok); code != http.StatusForbidden {
		t.Errorf("tenant_admin cross-tenant ?user=: want 403 got %d", code)
	}

	// 3) tenant_admin 缺省：只看到本租户成员（测试库 4 用户全在 hpc-lab → 含 ailsmember 不含外部用户）
	code, body = get("/api/v1/slurm/billing/usage", tenantTok)
	if code != http.StatusOK {
		t.Fatalf("tenant_admin usage: want 200 got %d", code)
	}
	if !jsonContains(body, `"user":"ailsmember"`) || jsonContains(body, `"user":"ailsother"`) {
		t.Errorf("tenant_admin default view must be tenant-scoped; body=%s", body)
	}

	// 4) ops_admin 不受限
	if code, _ := get("/api/v1/slurm/billing/usage?user=ailsother", opsTok); code != http.StatusOK {
		t.Errorf("ops_admin unrestricted: want 200 got %d", code)
	}
}

func jsonContains(body, sub string) bool {
	return len(sub) == 0 || indexOf(body, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestRouter_PasswordChange 自助改密端到端：登录→改密→旧 token 即刻 401（ver 吊销）→
// 新密码可登录。中间件为 WithStore 形态（活体校验）。
func TestRouter_PasswordChange(t *testing.T) {
	r, _ := setupTestRouter(t)

	// 登录 member
	_, body := doAuth(r, http.MethodPost, "/api/v1/auth/login", `{"username":"member","password":"member123"}`, "")
	var login auth.LoginResponse
	_ = json.Unmarshal([]byte(body), &login)
	if login.Token == "" {
		t.Fatalf("login failed: %s", body)
	}

	// 无鉴权调改密 → 401
	if c := doRequest(r, http.MethodPost, "/api/v1/auth/password", `{"oldPassword":"member123","newPassword":"Changed#99"}`, ""); c != http.StatusUnauthorized {
		t.Errorf("no-token change: want 401 got %d", c)
	}
	// 带令牌改密成功
	if c, b := doAuth(r, http.MethodPost, "/api/v1/auth/password", `{"oldPassword":"member123","newPassword":"Changed#99"}`, login.Token); c != http.StatusOK {
		t.Fatalf("change with token: want 200 got %d body=%s", c, b)
	}
	// 旧 token 已被吊销（对任意受保护路由 401）
	if c := doRequest(r, http.MethodGet, "/api/v1/slurm/nodes", "", login.Token); c != http.StatusUnauthorized {
		t.Errorf("revoked token must 401, got %d", c)
	}
	// 旧密码失效、新密码可登录
	if c, _ := doAuth(r, http.MethodPost, "/api/v1/auth/login", `{"username":"member","password":"member123"}`, ""); c != http.StatusUnauthorized {
		t.Errorf("old password must be rejected, got %d", c)
	}
	if c, b := doAuth(r, http.MethodPost, "/api/v1/auth/login", `{"username":"member","password":"Changed#99"}`, ""); c != http.StatusOK {
		t.Fatalf("new password login: want 200 got %d body=%s", c, b)
	}
}

// TestRouter_TenantScoping 多租户 Phase 4：作业/会话列表与控制按租户收口。
//   - 跨租户 member 的作业：本租户 member 不可控（403）、tenant_admin 不可控（403）
//   - tenant_admin 可控本租户 member 的作业（200）
//   - 列表可见性：member 只见自己；tenant_admin 见本租户；ops 全量
func TestRouter_TenantScoping(t *testing.T) {
	r, _ := setupTestRouter(t)
	// 跨租户 member：bio-lab 租户（store 无此租户成员清单 → 解析为空）
	bioTok, _ := auth.GenerateToken("biomember", auth.RoleMember, "bio-lab", "default", "ailsmember2", "ailsmember2")
	hpTok := tokenFor(t, auth.RoleMember) // clusterUser=ailsmember @ hpc-lab
	taTok := tokenFor(t, auth.RoleTenantAdmin)
	opsTok := tokenFor(t, auth.RoleOpsAdmin)

	subNamed := func(tok, name string) int {
		code, body := doAuth(r, http.MethodPost, "/api/v1/slurm/jobs/submit",
			fmt.Sprintf(`{"name":%q,"script":"echo hi"}`, name), tok)
		if code != http.StatusOK {
			t.Fatalf("submit: %d %s", code, body)
		}
		var resp struct {
			JobID int `json:"job_id"`
		}
		_ = json.Unmarshal([]byte(body), &resp)
		return resp.JobID
	}
	sub := func(tok string) int { return subNamed(tok, "sc") }
	cancel := func(id int, tok string) int {
		c, _ := doAuth(r, http.MethodPost, fmt.Sprintf("/api/v1/slurm/jobs/%d/cancel", id), "", tok)
		return c
	}
	names := func(tok string) map[string]bool {
		_, body := doAuth(r, http.MethodGet, "/api/v1/slurm/jobs", "", tok)
		var l jobs.JobListResponse
		_ = json.Unmarshal([]byte(body), &l)
		out := map[string]bool{}
		for _, j := range l.Jobs {
			out[j.Name] = true
		}
		return out
	}

	bioJob := subNamed(bioTok, "sc-bio") // bio-lab 的作业（owner=ailsmember2）
	hpJob := subNamed(hpTok, "sc-hp")    // hpc-lab member 的作业（owner=ailsmember）

	// 1) hpc-lab member 不能控 bio 的作业
	if c := cancel(bioJob, hpTok); c != http.StatusForbidden {
		t.Errorf("cross-tenant member cancel: want 403 got %d", c)
	}
	// 2) hpc-lab tenant_admin 不能控 bio 的作业（此前全局通配！）
	if c := cancel(bioJob, taTok); c != http.StatusForbidden {
		t.Errorf("cross-tenant tenant_admin cancel: want 403 got %d", c)
	}
	// 3) tenant_admin 可控本租户 member 的作业
	if c := cancel(hpJob, taTok); c != http.StatusOK {
		t.Errorf("own-tenant tenant_admin cancel: want 200 got %d", c)
	}

	// 4) 列表可见性（作业名可区分：sc-bio=bio 租户，sc-hp/sc=hpc-lab）
	sub(hpTok)
	hpView := names(hpTok)
	if !hpView["sc"] || !hpView["sc-hp"] || hpView["sc-bio"] {
		t.Errorf("member must see exactly own jobs, saw %v", hpView)
	}
	bioView := names(bioTok)
	if !bioView["sc-bio"] || bioView["sc"] || bioView["sc-hp"] {
		t.Errorf("cross-tenant member must see only own job, saw %v", bioView)
	}
	opsView := names(opsTok)
	if !opsView["sc"] || !opsView["sc-hp"] || !opsView["sc-bio"] {
		t.Errorf("ops must see all jobs, saw %v", opsView)
	}
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
	expired, _ := auth.GenerateTokenWithTTL("admin", auth.RoleSystemAdmin, "hpc-lab", "default", "admin", "admin", -1*time.Hour)
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

// TestRouter_ErrorEnvelope 锁定统一错误信封契约（防未来漂移）：
//   - error 字段恒在且为 string
//   - request_id 透传客户端 X-Request-ID；未带时由中间件自动生成非空值
//   - 语义 extra（403 的 required）端到端存活
func TestRouter_ErrorEnvelope(t *testing.T) {
	r, _ := setupTestRouter(t)

	// 透传：客户端带的 X-Request-ID 应原样出现在错误体里
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/slurm/nodes", nil)
	req.Header.Set("X-Request-ID", "rid-from-header")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401 got %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error body: %v body=%s", err, w.Body.String())
	}
	if _, ok := body["error"].(string); !ok {
		t.Errorf("error body missing string \"error\": %v", body)
	}
	if body["request_id"] != "rid-from-header" {
		t.Errorf("request_id = %v, want rid-from-header (X-Request-ID not echoed)", body["request_id"])
	}

	// 自动生成：未带 X-Request-ID 时，中间件应生成非空 request_id
	req2, _ := http.NewRequest(http.MethodGet, "/api/v1/slurm/nodes", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	var body2 map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("unmarshal error body: %v body=%s", err, w2.Body.String())
	}
	if rid, _ := body2["request_id"].(string); rid == "" {
		t.Errorf("auto-generated request_id missing/empty: %v", body2)
	}

	// 语义 extra：member 调 admin 独占路由 → 403 体应含 required:["nodes:manage"]
	// （R1 起门面为权限点；四角色鉴权结果与 RequireRole 时代一致）
	drainReq, _ := http.NewRequest(http.MethodPost, "/api/v1/slurm/nodes/node1/state", bytes.NewBufferString(`{"state":"DRAIN"}`))
	drainReq.Header.Set("Content-Type", "application/json")
	drainReq.Header.Set("Authorization", "Bearer "+tokenFor(t, auth.RoleMember))
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, drainReq)
	if w3.Code != http.StatusForbidden {
		t.Fatalf("member drain: want 403 got %d", w3.Code)
	}
	var body3 map[string]any
	if err := json.Unmarshal(w3.Body.Bytes(), &body3); err != nil {
		t.Fatalf("unmarshal 403 body: %v body=%s", err, w3.Body.String())
	}
	required, ok := body3["required"].([]any)
	if !ok || len(required) == 0 || required[0] != auth.PermNodesManage {
		t.Errorf("403 required = %v, want [%q] (semantic extra lost)", body3["required"], auth.PermNodesManage)
	}
}

// TestRouter_Healthz 锁定 liveness 端点：GET /healthz 免鉴权返 200 {"status":"ok"}。
func TestRouter_Healthz(t *testing.T) {
	r, _ := setupTestRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("healthz: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want \"ok\"", body["status"])
	}
}

// TestRouter_Readyz：readiness 探针。mock slurmrestd 可达 → 200 {"status":"ready"}。
func TestRouter_Readyz(t *testing.T) {
	r, _ := setupTestRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/readyz", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("readyz: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if body["status"] != "ready" {
		t.Errorf("status = %v, want \"ready\"", body["status"])
	}
}

// TestRouter_ReadyzDegraded：slurmrestd 不可达时 readiness 应返 503（degraded）。
func TestRouter_ReadyzDegraded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deadClient := slurmrest.NewClient("http://127.0.0.1:9", "hpcuser", "test-token")
	h := cluster.NewClusterHandler(cluster.NewClusterService(deadClient))
	r := gin.New()
	r.GET("/readyz", h.Readyz)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/readyz", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz degraded: want 503, got %d body=%s", w.Code, w.Body.String())
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

// TestRouter_AuthMe R4 权限自描述：/auth/me 返回基角色+权限点清单+集群身份；
// 无 token → 401。内存库用户 → 权限回退内置映射（与登录响应一致）。
func TestRouter_AuthMe(t *testing.T) {
	r, _ := setupTestRouter(t)

	// 无 token
	if c := doRequest(r, http.MethodGet, "/api/v1/auth/me", "", ""); c != http.StatusUnauthorized {
		t.Fatalf("no token: want 401 got %d", c)
	}
	// member：基角色 member + 内置权限集
	code, body := doAuth(r, http.MethodGet, "/api/v1/auth/me", "", tokenFor(t, auth.RoleMember))
	if code != http.StatusOK {
		t.Fatalf("member me: want 200 got %d body=%s", code, body)
	}
	var resp auth.LoginResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if resp.User.Username != "member" || resp.User.Role != auth.RoleMember {
		t.Errorf("user = %+v", resp.User)
	}
	permSet := map[string]bool{}
	for _, p := range resp.User.Permissions {
		permSet[p] = true
	}
	for _, want := range []string{auth.PermClusterRead, auth.PermJobsSubmit, auth.PermIdeList, auth.PermBillingRead} {
		if !permSet[want] {
			t.Errorf("member /auth/me missing %q (has %v)", want, resp.User.Permissions)
		}
	}
	if permSet[auth.PermNodesManage] {
		t.Errorf("member /auth/me must not hold nodes:manage (has %v)", resp.User.Permissions)
	}
	// admin：平台权限集（无 jobs/ide/billing——纯监控）
	_, body = doAuth(r, http.MethodGet, "/api/v1/auth/me", "", tokenFor(t, auth.RoleSystemAdmin))
	_ = json.Unmarshal([]byte(body), &resp)
	permSet = map[string]bool{}
	for _, p := range resp.User.Permissions {
		permSet[p] = true
	}
	if !permSet[auth.PermRolesManage] || permSet[auth.PermJobsSubmit] {
		t.Errorf("admin /auth/me perms wrong: %v", resp.User.Permissions)
	}
}

// TestRouter_QOS_Endpoints_And_RBAC 验证 QOS 完整 CRUD 端点及 PermQosManage RBAC 拦截。
func TestRouter_QOS_Endpoints_And_RBAC(t *testing.T) {
	r, _, adminSvc := setupRBACStack(t)

	// 配置 Mock Runner 输出
	adminSvc.SetClusterRunner(func(args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		if strings.Contains(cmd, "show qos") {
			return []byte("normal|0||||||\ngpu-vip|1000|gres/gpu=4,cpu=32|gres/gpu=1,cpu=8|02:00:00|1|5|\n"), nil
		}
		if strings.Contains(cmd, "modify qos nonexistent") || strings.Contains(cmd, "delete qos nonexistent") {
			return []byte("sacctmgr: error: Unknown QOS: nonexistent\n"), nil
		}
		return []byte(""), nil
	})

	// 签发测试令牌
	adminTok := loginViaAPI(t, r, "padmin", "platform123")  // 平台管理员，持 PermQosManage
	memberTok := loginViaAPI(t, r, "alice", "alice12345")   // 普通成员，无 PermQosManage
	tadminTok := loginViaAPI(t, r, "tadmin", "tenant12345") // 租户管理员，无平台 PermQosManage

	// 1. GET /api/v1/admin/qos
	t.Run("GET /admin/qos", func(t *testing.T) {
		// 未认证 -> 401
		code, _ := doAuth(r, http.MethodGet, "/api/v1/admin/qos", "", "")
		if code != http.StatusUnauthorized {
			t.Errorf("unauth: want 401 got %d", code)
		}

		// 普通成员 -> 403
		code, _ = doAuth(r, http.MethodGet, "/api/v1/admin/qos", "", memberTok)
		if code != http.StatusForbidden {
			t.Errorf("member: want 403 got %d", code)
		}

		// 平台管理员 -> 200 OK + QOS 列表
		code, body := doAuth(r, http.MethodGet, "/api/v1/admin/qos", "", adminTok)
		if code != http.StatusOK {
			t.Fatalf("admin: want 200 got %d (body: %s)", code, body)
		}
		var resp struct {
			QOS []admin.QOS `json:"qos"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil || len(resp.QOS) != 2 {
			t.Errorf("invalid list response: %s", body)
		}
	})

	// 2. GET /api/v1/admin/qos/:name
	t.Run("GET /admin/qos/:name", func(t *testing.T) {
		// 未认证 -> 401
		code, _ := doAuth(r, http.MethodGet, "/api/v1/admin/qos/gpu-vip", "", "")
		if code != http.StatusUnauthorized {
			t.Errorf("unauth: want 401 got %d", code)
		}

		// 普通成员 -> 403
		code, _ = doAuth(r, http.MethodGet, "/api/v1/admin/qos/gpu-vip", "", memberTok)
		if code != http.StatusForbidden {
			t.Errorf("member: want 403 got %d", code)
		}

		// 平台管理员查询存在 -> 200 OK
		code, body := doAuth(r, http.MethodGet, "/api/v1/admin/qos/gpu-vip", "", adminTok)
		if code != http.StatusOK {
			t.Fatalf("admin get: want 200 got %d (body: %s)", code, body)
		}
		var resp struct {
			QOS admin.QOS `json:"qos"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.QOS.Name != "gpu-vip" {
			t.Errorf("invalid get response: %s", body)
		}

		// 查询不存在 -> 404
		code, _ = doAuth(r, http.MethodGet, "/api/v1/admin/qos/nonexistent", "", adminTok)
		if code != http.StatusNotFound {
			t.Errorf("admin get nonexistent: want 404 got %d", code)
		}
	})

	// 3. POST /api/v1/admin/qos
	t.Run("POST /admin/qos", func(t *testing.T) {
		payload := `{
			"name": "ai-train",
			"priority": "500",
			"grp_tres": "gres/gpu=8",
			"max_tres_pu": "gres/gpu=2",
			"max_jobs_pu": "2",
			"max_wall": "12:00:00"
		}`

		// 租户管理员 -> 403
		code, _ := doAuth(r, http.MethodPost, "/api/v1/admin/qos", payload, tadminTok)
		if code != http.StatusForbidden {
			t.Errorf("tadmin create: want 403 got %d", code)
		}

		// 平台管理员合法创建 -> 200 OK
		code, body := doAuth(r, http.MethodPost, "/api/v1/admin/qos", payload, adminTok)
		if code != http.StatusOK {
			t.Fatalf("admin create: want 200 got %d (body: %s)", code, body)
		}

		// 平台管理员非法参数创建（名称包含特殊字符）-> 400 Bad Request
		badPayload := `{"name": "bad;name!"}`
		code, _ = doAuth(r, http.MethodPost, "/api/v1/admin/qos", badPayload, adminTok)
		if code != http.StatusBadRequest {
			t.Errorf("admin create invalid: want 400 got %d", code)
		}
	})

	// 4. PATCH /api/v1/admin/qos/:name
	t.Run("PATCH /admin/qos/:name", func(t *testing.T) {
		patchPayload := `{"priority": "800", "max_jobs_pu": "4"}`

		// 普通成员 -> 403
		code, _ := doAuth(r, http.MethodPatch, "/api/v1/admin/qos/ai-train", patchPayload, memberTok)
		if code != http.StatusForbidden {
			t.Errorf("member patch: want 403 got %d", code)
		}

		// 平台管理员合法更新 -> 200 OK
		code, body := doAuth(r, http.MethodPatch, "/api/v1/admin/qos/ai-train", patchPayload, adminTok)
		if code != http.StatusOK {
			t.Fatalf("admin patch: want 200 got %d (body: %s)", code, body)
		}

		// 空修改体 -> 400 Bad Request
		code, _ = doAuth(r, http.MethodPatch, "/api/v1/admin/qos/ai-train", `{}`, adminTok)
		if code != http.StatusBadRequest {
			t.Errorf("admin empty patch: want 400 got %d", code)
		}

		// 修改不存在的 QOS -> 404 Not Found
		code, _ = doAuth(r, http.MethodPatch, "/api/v1/admin/qos/nonexistent", patchPayload, adminTok)
		if code != http.StatusNotFound {
			t.Errorf("admin patch nonexistent: want 404 got %d", code)
		}
	})

	// 5. DELETE /api/v1/admin/qos/:name
	t.Run("DELETE /admin/qos/:name", func(t *testing.T) {
		// 普通成员 -> 403
		code, _ := doAuth(r, http.MethodDelete, "/api/v1/admin/qos/ai-train", "", memberTok)
		if code != http.StatusForbidden {
			t.Errorf("member delete: want 403 got %d", code)
		}

		// 平台管理员删除合法 QOS -> 200 OK
		code, body := doAuth(r, http.MethodDelete, "/api/v1/admin/qos/ai-train", "", adminTok)
		if code != http.StatusOK {
			t.Fatalf("admin delete: want 200 got %d (body: %s)", code, body)
		}

		// 删除不存在的 QOS -> 404 Not Found
		code, _ = doAuth(r, http.MethodDelete, "/api/v1/admin/qos/nonexistent", "", adminTok)
		if code != http.StatusNotFound {
			t.Errorf("admin delete nonexistent: want 404 got %d", code)
		}

		// 非法名称删除 -> 400 Bad Request
		code, _ = doAuth(r, http.MethodDelete, "/api/v1/admin/qos/invalid;name", "", adminTok)
		if code != http.StatusBadRequest {
			t.Errorf("admin delete invalid name: want 400 got %d", code)
		}
	})
}

// TestRouter_UserQOS_Endpoints_And_Scoping 测试用户级 QOS 端点、RBAC 鉴权与多租户作用域隔离
func TestRouter_UserQOS_Endpoints_And_Scoping(t *testing.T) {
	r, _, adminSvc := setupRBACStack(t)

	adminSvc.SetClusterRunner(func(args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		if strings.Contains(cmd, "show assoc") {
			return []byte("alice|hpc-lab|normal,gpu-vip|normal\n"), nil
		}
		if strings.Contains(cmd, "show qos") {
			return []byte("Name|Priority|GrpTRES|MaxTRESPU|MaxWall|MaxJobsPU|MaxSubmitPU|Description\n" +
				"normal|0||||||Standard default\n" +
				"gpu-vip|1000|gres/gpu=4|gres/gpu=1|02:00:00|1|5|VIP GPU\n"), nil
		}
		return []byte(""), nil
	})

	adminTok := loginViaAPI(t, r, "padmin", "platform123")
	tadminTok := loginViaAPI(t, r, "tadmin", "tenant12345")
	memberTok := loginViaAPI(t, r, "alice", "alice12345")

	// 1. GET /api/v1/slurm/qos/available (All authenticated roles)
	t.Run("GET /slurm/qos/available", func(t *testing.T) {
		// Unauthenticated -> 401
		code, _ := doAuth(r, http.MethodGet, "/api/v1/slurm/qos/available", "", "")
		if code != http.StatusUnauthorized {
			t.Errorf("unauth want 401 got %d", code)
		}

		// Member -> 200
		code, body := doAuth(r, http.MethodGet, "/api/v1/slurm/qos/available", "", memberTok)
		if code != http.StatusOK {
			t.Fatalf("member available qos: want 200 got %d (%s)", code, body)
		}
		var resp admin.AvailableQOSResponse
		if err := json.Unmarshal([]byte(body), &resp); err != nil || len(resp.AllowedQOS) == 0 {
			t.Errorf("invalid available qos response: %s", body)
		}

		// Tenant admin -> 200
		code, _ = doAuth(r, http.MethodGet, "/api/v1/slurm/qos/available", "", tadminTok)
		if code != http.StatusOK {
			t.Errorf("tadmin available qos: want 200 got %d", code)
		}
	})

	// 2. GET /api/v1/admin/users/:username/qos (Platform Admin only)
	t.Run("GET /admin/users/:username/qos", func(t *testing.T) {
		// Member -> 403
		code, _ := doAuth(r, http.MethodGet, "/api/v1/admin/users/alice/qos", "", memberTok)
		if code != http.StatusForbidden {
			t.Errorf("member get user qos: want 403 got %d", code)
		}

		// Tenant Admin -> 403
		code, _ = doAuth(r, http.MethodGet, "/api/v1/admin/users/alice/qos", "", tadminTok)
		if code != http.StatusForbidden {
			t.Errorf("tenant admin get platform user qos: want 403 got %d", code)
		}

		// Platform Admin -> 200
		code, body := doAuth(r, http.MethodGet, "/api/v1/admin/users/alice/qos", "", adminTok)
		if code != http.StatusOK {
			t.Fatalf("admin get user qos: want 200 got %d (%s)", code, body)
		}

		// Nonexistent user -> 404
		code, _ = doAuth(r, http.MethodGet, "/api/v1/admin/users/nonexistent/qos", "", adminTok)
		if code != http.StatusNotFound {
			t.Errorf("admin get nonexistent user qos: want 404 got %d", code)
		}
	})

	// 3. PATCH /api/v1/admin/users/:username/qos (Platform Admin only)
	t.Run("PATCH /admin/users/:username/qos", func(t *testing.T) {
		payload := `{"defaultQOS":"gpu-vip","allowedQOS":["normal","gpu-vip"]}`

		// Member -> 403
		code, _ := doAuth(r, http.MethodPatch, "/api/v1/admin/users/alice/qos", payload, memberTok)
		if code != http.StatusForbidden {
			t.Errorf("member patch user qos: want 403 got %d", code)
		}

		// Tenant Admin -> 403
		code, _ = doAuth(r, http.MethodPatch, "/api/v1/admin/users/alice/qos", payload, tadminTok)
		if code != http.StatusForbidden {
			t.Errorf("tenant admin patch platform user qos: want 403 got %d", code)
		}

		// Platform Admin -> 200
		code, body := doAuth(r, http.MethodPatch, "/api/v1/admin/users/alice/qos", payload, adminTok)
		if code != http.StatusOK {
			t.Fatalf("admin patch user qos: want 200 got %d (%s)", code, body)
		}

		// Invalid payload (default not in allowed) -> 400
		badPayload := `{"defaultQOS":"vip","allowedQOS":["normal"]}`
		code, _ = doAuth(r, http.MethodPatch, "/api/v1/admin/users/alice/qos", badPayload, adminTok)
		if code != http.StatusBadRequest {
			t.Errorf("admin patch bad qos: want 400 got %d", code)
		}
	})

	// 4. PATCH /api/v1/tenants/me/users/:username/qos (Tenant Admin Scoping)
	t.Run("TenantAdmin_Scoping", func(t *testing.T) {
		payload := `{"defaultQOS":"gpu-vip","allowedQOS":["normal","gpu-vip"]}`

		// Member -> 403
		code, _ := doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/qos", payload, memberTok)
		if code != http.StatusForbidden {
			t.Errorf("member tenant patch: want 403 got %d", code)
		}

		// Tenant Admin own tenant user (alice in hpc-lab) -> 200
		code, body := doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/qos", payload, tadminTok)
		if code != http.StatusOK {
			t.Fatalf("tadmin update own user: want 200 got %d (%s)", code, body)
		}

		// Tenant Admin cross-tenant user (biomember in bio-lab) -> 404 (Anti-IDOR)
		code, _ = doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/biomember/qos", payload, tadminTok)
		if code != http.StatusNotFound {
			t.Errorf("tadmin update cross tenant user: want 404 got %d", code)
		}

		// Tenant Admin update system admin (padmin) -> 404 (Anti-Privilege Escalation)
		code, _ = doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/padmin/qos", payload, tadminTok)
		if code != http.StatusNotFound {
			t.Errorf("tadmin update padmin: want 404 got %d", code)
		}
	})
}
