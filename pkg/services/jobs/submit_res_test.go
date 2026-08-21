package jobs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/services/jobs"
	"ails-hpc/pkg/slurmrest"

	"github.com/gin-gonic/gin"
)

// fakeAPI 捕获 REST 提交（内存字段断言用）。
type fakeAPI struct {
	last   *slurmrest.SlurmJobSubmitReq
	lastAs string
	calls  int
}

func (f *fakeAPI) SubmitJobAs(req *slurmrest.SlurmJobSubmitReq, actAs string) (*slurmrest.SlurmJobSubmitResp, error) {
	f.calls++
	f.last, f.lastAs = req, actAs
	return &slurmrest.SlurmJobSubmitResp{JobID: 777}, nil
}
func (f *fakeAPI) GetJobs() (*slurmrest.JobsResponse, error) { return &slurmrest.JobsResponse{}, nil }
func (f *fakeAPI) CancelJobAs(int, string) error             { return nil }
func (f *fakeAPI) HoldJobAs(int, string) error               { return nil }
func (f *fakeAPI) RequeueJobAs(int, string) error            { return nil }

// fakeCli 捕获 GPU CLI 提交参数。
type fakeCli struct {
	opts  jobs.CliSubmitOpts
	calls int
}

func (f *fakeCli) submit(o jobs.CliSubmitOpts) (int, error) {
	f.calls++
	f.opts = o
	return 888, nil
}

// TestSubmit_MemoryViaREST：memory_mb 透传 REST memory_per_node；actAs=clusterUser。
func TestSubmit_MemoryViaREST(t *testing.T) {
	api := &fakeAPI{}
	cli := &fakeCli{}
	svc := jobs.NewJobServiceWithDeps(api, cli.submit)

	resp, err := svc.SubmitJob(context.Background(), &jobs.SubmitJobRequest{
		Name: "m1", Script: "echo hi", MemoryMB: 2048,
	}, "ailsmember", "ailsmember")
	if err != nil {
		t.Fatal(err)
	}
	if resp.JobID != 777 {
		t.Fatalf("jobID=%d want 777(REST)", resp.JobID)
	}
	if api.last.Job.MemoryPerNode != 2048 {
		t.Errorf("memory_per_node=%d want 2048", api.last.Job.MemoryPerNode)
	}
	if api.lastAs != "ailsmember" || api.calls != 1 || cli.calls != 0 {
		t.Errorf("REST path: as=%q calls=%d cli=%d", api.lastAs, api.calls, cli.calls)
	}
}

// TestSubmit_GPUViaCLI：gpus>0 走 CLI（REST 不被调用），参数齐全。
func TestSubmit_GPUViaCLI(t *testing.T) {
	api := &fakeAPI{}
	cli := &fakeCli{}
	svc := jobs.NewJobServiceWithDeps(api, cli.submit)

	resp, err := svc.SubmitJob(context.Background(), &jobs.SubmitJobRequest{
		Name: "g1", Script: "nvidia-smi", Partition: "performance", Gpus: 1, MemoryMB: 2048, Tasks: 2,
	}, "ailsmember", "ailsmember")
	if err != nil {
		t.Fatal(err)
	}
	if resp.JobID != 888 {
		t.Fatalf("jobID=%d want 888(CLI)", resp.JobID)
	}
	if api.calls != 0 {
		t.Error("GPU job must not go through REST")
	}
	o := cli.opts
	if o.ClusterUser != "ailsmember" || o.Gpus != 1 || o.MemoryMB != 2048 || o.Partition != "performance" || o.Tasks != 2 {
		t.Errorf("cli opts = %+v", o)
	}
}

// TestSubmit_GPUPartitionGuard：gpus>0 且非 performance → ErrGPUPartition（handler 400）。
func TestSubmit_GPUPartitionGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := jobs.NewJobServiceWithDeps(&fakeAPI{}, (&fakeCli{}).submit)
	h := jobs.NewJobHandler(svc)
	r := gin.New()
	r.POST("/submit", h.SubmitJob)

	w := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{"name": "g2", "script": "x", "partition": "standard", "gpus": 1})
	req, _ := http.NewRequest("POST", "/submit", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("gpu on standard: want 400 got %d body=%s", w.Code, w.Body.String())
	}
}

// stubHistorySvc：仅实现 History，其余 panic（handler 隔离测试只需要这条路径）。
type stubHistorySvc struct {
	rows []jobs.HistoryEntry
	gotQ jobs.HistoryQuery
}

func (s *stubHistorySvc) SubmitJob(ctx context.Context, r *jobs.SubmitJobRequest, cu, acc string) (*jobs.SubmitJobResponse, error) {
	return nil, nil
}
func (s *stubHistorySvc) ListJobs(ctx context.Context) ([]jobs.JobSummary, error) { return nil, nil }
func (s *stubHistorySvc) JobDetail(ctx context.Context, id int) (*jobs.JobDetail, error) {
	return nil, nil
}
func (s *stubHistorySvc) JobOwner(ctx context.Context, id int) (string, error) { return "", nil }
func (s *stubHistorySvc) CancelJob(ctx context.Context, id int, a string) (*jobs.JobControlResponse, error) {
	return nil, nil
}
func (s *stubHistorySvc) HoldJob(ctx context.Context, id int, a string) (*jobs.JobControlResponse, error) {
	return nil, nil
}
func (s *stubHistorySvc) RequeueJob(ctx context.Context, id int, a string) (*jobs.JobControlResponse, error) {
	return nil, nil
}
func (s *stubHistorySvc) History(ctx context.Context, q jobs.HistoryQuery) ([]jobs.HistoryEntry, error) {
	s.gotQ = q
	return s.rows, nil
}

// TestHistoryHandler_Scoping：member 的 ?user= 被强制为本人；tenant_admin 缺省视图
// 按租户成员后过滤；tenant_admin ?user= 跨租户 403。
func TestHistoryHandler_Scoping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	members := func(string) ([]string, error) { return []string{"ailsmember", "ailstadmin"}, nil }
	stub := &stubHistorySvc{rows: []jobs.HistoryEntry{
		{JobID: 1, Owner: "ailsmember"}, {JobID: 2, Owner: "ailsother"},
	}}
	h := jobs.NewJobHandlerScoped(stub, members)
	r := gin.New()
	with := func(username, role, cu, tid string) []gin.HandlerFunc {
		return []gin.HandlerFunc{func(c *gin.Context) {
			c.Set("claims", &auth.Claims{Username: username, Role: role, ClusterUser: cu, OrgSlug: tid, TID: tid})
			c.Next()
		}, auth.RequireRole(role)}
	}
	r.GET("/api/v1/slurm/jobs/history", with("member", auth.RoleMember, "ailsmember", "hpc-lab")[0], with("member", auth.RoleMember, "ailsmember", "hpc-lab")[1], h.ListHistory)
	r.GET("/as-tenant", with("tenantadmin", auth.RoleTenantAdmin, "ailstadmin", "hpc-lab")[0], with("tenantadmin", auth.RoleTenantAdmin, "ailstadmin", "hpc-lab")[1], h.ListHistory)

	// member：?user= 他人被强制为本人，只回本人行
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/slurm/jobs/history?user=ailsother", nil)
	r.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"owner":"ailsmember"`) || strings.Contains(w.Body.String(), "ailsother") {
		t.Errorf("member: %d %s", w.Code, w.Body.String())
	}
	if stub.gotQ.User != "ailsmember" {
		t.Errorf("forced user = %q", stub.gotQ.User)
	}

	// tenant_admin 缺省：租户后过滤（ailsother 被滤掉）
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/as-tenant", nil)
	r.ServeHTTP(w, req)
	if w.Code != 200 || strings.Contains(w.Body.String(), "ailsother") {
		t.Errorf("tenant default view: %d %s", w.Code, w.Body.String())
	}
}

// TestSubmit_ArrayDependencyViaCLI：数组/依赖走 CLI 且参数透传；非法语法 400。
func TestSubmit_ArrayDependencyViaCLI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var got jobs.CliSubmitOpts
	api := &fakeAPI{}
	svc := jobs.NewJobServiceWithDeps(api, func(o jobs.CliSubmitOpts) (int, error) {
		got = o
		return 999, nil
	})
	h := jobs.NewJobHandler(svc)
	r := gin.New()
	r.POST("/submit", h.SubmitJob)

	// 数组 + 依赖 → CLI,字段透传
	w := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{"name": "arr", "script": "x", "array_spec": "1-4", "dependency": "afterok:123"})
	req, _ := http.NewRequest("POST", "/submit", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("array submit: %d %s", w.Code, w.Body.String())
	}
	if got.ArraySpec != "1-4" || got.Dependency != "afterok:123" || api.calls != 0 {
		t.Errorf("opts = %+v apiCalls=%d", got, api.calls)
	}
	if got.Gpus != 0 || got.MemoryMB != 0 {
		t.Errorf("array job must not force gpu/mem: %+v", got)
	}

	// 非法数组语法（注入尝试）→ 400
	w = httptest.NewRecorder()
	body, _ = json.Marshal(map[string]any{"name": "bad", "script": "x", "array_spec": "1;rm -rf /"})
	req, _ = http.NewRequest("POST", "/submit", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("injection spec: want 400 got %d", w.Code)
	}

	// 非法依赖语法 → 400
	w = httptest.NewRecorder()
	body, _ = json.Marshal(map[string]any{"name": "bad2", "script": "x", "dependency": "afterok:1 && cat /etc/passwd"})
	req, _ = http.NewRequest("POST", "/submit", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("injection dep: want 400 got %d", w.Code)
	}
}

// TestSubmit_QOSViaREST：QOS 透传 REST Job.Qos；actAs=clusterUser。
func TestSubmit_QOSViaREST(t *testing.T) {
	api := &fakeAPI{}
	cli := &fakeCli{}
	svc := jobs.NewJobServiceWithDeps(api, cli.submit)

	resp, err := svc.SubmitJob(context.Background(), &jobs.SubmitJobRequest{
		Name: "qos_job", Script: "echo hi", QOS: "gpu-vip",
	}, "ailsmember", "ailsmember")
	if err != nil {
		t.Fatal(err)
	}
	if resp.JobID != 777 {
		t.Fatalf("jobID=%d want 777(REST)", resp.JobID)
	}
	if api.last.Job.Qos != "gpu-vip" {
		t.Errorf("job.qos=%q want 'gpu-vip'", api.last.Job.Qos)
	}
	if api.lastAs != "ailsmember" || api.calls != 1 || cli.calls != 0 {
		t.Errorf("REST path: as=%q calls=%d cli=%d", api.lastAs, api.calls, cli.calls)
	}
}

// TestSubmit_QOSViaCLI：gpus>0 携带 QOS 透传至 CliSubmitOpts.QOS。
func TestSubmit_QOSViaCLI(t *testing.T) {
	api := &fakeAPI{}
	cli := &fakeCli{}
	svc := jobs.NewJobServiceWithDeps(api, cli.submit)

	resp, err := svc.SubmitJob(context.Background(), &jobs.SubmitJobRequest{
		Name: "gpu_qos", Script: "nvidia-smi", Partition: "performance", Gpus: 1, QOS: "high-priority",
	}, "ailsmember", "ailsmember")
	if err != nil {
		t.Fatal(err)
	}
	if resp.JobID != 888 {
		t.Fatalf("jobID=%d want 888(CLI)", resp.JobID)
	}
	if cli.opts.QOS != "high-priority" {
		t.Errorf("cli.opts.QOS=%q want 'high-priority'", cli.opts.QOS)
	}
	if api.calls != 0 {
		t.Error("GPU job must not go through REST")
	}
}

// TestSubmit_QOSOmittedDefault：未指定 QOS 时不透传 QOS（空值），由 Slurm 应用关联默认 QOS。
func TestSubmit_QOSOmittedDefault(t *testing.T) {
	api := &fakeAPI{}
	cli := &fakeCli{}
	svc := jobs.NewJobServiceWithDeps(api, cli.submit)

	// REST path with omitted QOS
	_, err := svc.SubmitJob(context.Background(), &jobs.SubmitJobRequest{
		Name: "def_job", Script: "echo hi",
	}, "ailsmember", "ailsmember")
	if err != nil {
		t.Fatal(err)
	}
	if api.last.Job.Qos != "" {
		t.Errorf("expected empty Qos in REST submit, got %q", api.last.Job.Qos)
	}

	// CLI path with omitted QOS
	_, err = svc.SubmitJob(context.Background(), &jobs.SubmitJobRequest{
		Name: "def_gpu_job", Script: "nvidia-smi", Partition: "performance", Gpus: 1,
	}, "ailsmember", "ailsmember")
	if err != nil {
		t.Fatal(err)
	}
	if cli.opts.QOS != "" {
		t.Errorf("expected empty QOS in CLI opts, got %q", cli.opts.QOS)
	}
}

// TestSubmit_QOSAntiInjection：非法与恶意 QOS 载荷全部拦截返回 400。
func TestSubmit_QOSAntiInjection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := jobs.NewJobServiceWithDeps(&fakeAPI{}, (&fakeCli{}).submit)
	h := jobs.NewJobHandler(svc)
	r := gin.New()
	r.POST("/submit", h.SubmitJob)

	injectionCases := []struct {
		qos  string
		desc string
	}{
		{"gpu-vip; rm -rf /", "Semicolon command injection"},
		{"gpu-vip; touch /tmp/pwned", "Semicolon file creation"},
		{"vip$(id)", "Subshell command substitution"},
		{"vip`id`", "Backtick command substitution"},
		{"'gpu-vip'", "Single quotes wrapping"},
		{"\"gpu-vip\"", "Double quotes wrapping"},
		{"gpu-vip' OR '1'='1", "SQL injection style"},
		{"gpu-vip|cat /etc/passwd", "Pipe command injection"},
		{"gpu-vip && reboot", "Logical AND injection"},
		{"gpu-vip\n--exclusive", "Newline header injection"},
		{"gpu-vip\r\n--exclusive", "CRLF injection"},
		{"gpu-vip\x00pwn", "Null byte injection"},
		{"1vip", "Leading digit"},
		{"_vip", "Leading underscore"},
		{"-vip", "Leading hyphen"},
		{"--qos=vip", "Double dash option injection"},
		{"gpu vip", "Space separated"},
		{"toolong_qos_name_exceeding_32_characters_limit", "Overflow >32 chars"},
		{"../etc/passwd", "Path traversal"},
		{"vip/normal", "Slash in name"},
	}

	for _, tc := range injectionCases {
		t.Run(tc.desc, func(t *testing.T) {
			w := httptest.NewRecorder()
			body, _ := json.Marshal(map[string]any{
				"name":   "job1",
				"script": "echo hi",
				"qos":    tc.qos,
			})
			req, _ := http.NewRequest("POST", "/submit", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("[%s] qos=%q: want 400 Bad Request, got %d, body=%s", tc.desc, tc.qos, w.Code, w.Body.String())
			}
		})
	}
}
