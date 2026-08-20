package containers_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ails-hpc/pkg/services/containers"
	"ails-hpc/pkg/slurmrest"
)

// --- fakes ---

type fakeJobsAPI struct {
	lastSubmit  *slurmrest.SlurmJobSubmitReq
	lastActAs   string
	submitResp  *slurmrest.SlurmJobSubmitResp
	submitErr   error
	jobs        *slurmrest.JobsResponse
	cancelled   []int
	cancelActAs string
	cancelErr   error
}

func (f *fakeJobsAPI) SubmitJobAs(req *slurmrest.SlurmJobSubmitReq, actAs string) (*slurmrest.SlurmJobSubmitResp, error) {
	f.lastSubmit = req
	f.lastActAs = actAs
	if f.submitErr != nil {
		return nil, f.submitErr
	}
	return f.submitResp, nil
}
func (f *fakeJobsAPI) GetJobs() (*slurmrest.JobsResponse, error) {
	if f.jobs == nil {
		return &slurmrest.JobsResponse{}, nil
	}
	return f.jobs, nil
}
func (f *fakeJobsAPI) CancelJobAs(jobID int, actAs string) error {
	f.cancelled = append(f.cancelled, jobID)
	f.cancelActAs = actAs
	if f.jobs != nil {
		for i := range f.jobs.Jobs {
			if f.jobs.Jobs[i].JobID == jobID {
				f.jobs.Jobs[i].JobState = "CANCELLED"
			}
		}
	}
	return f.cancelErr
}

type fakeMeta struct {
	m       map[string]containers.SessionMeta
	deleted []string
	readErr error
}

func (f *fakeMeta) ReadAll() (map[string]containers.SessionMeta, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.m, nil
}
func (f *fakeMeta) Delete(sid string) error {
	f.deleted = append(f.deleted, sid)
	delete(f.m, sid)
	return nil
}

// jrow + jobsResp 构造 *slurmrest.JobsResponse（元素匿名结构须与 slurmrest 一致）
type jrow struct {
	id, cpus           int
	name, state, nodes string
	submit             int64
	account            string // P1-4：归属锚定字段（空=旧用例，SessionOwner 视为遗留空属主）
}

func jobsResp(rows ...jrow) *slurmrest.JobsResponse {
	r := &slurmrest.JobsResponse{}
	for _, x := range rows {
		r.Jobs = append(r.Jobs, struct {
			JobID      int    `json:"job_id"`
			Name       string `json:"name"`
			Partition  string `json:"partition"`
			JobState   string `json:"job_state"`
			Nodes      string `json:"nodes"`
			TimeLimit  int    `json:"time_limit"`
			SubmitTime int64  `json:"submit_time"`
			Account    string `json:"account"`
		}{JobID: x.id, Name: x.name, JobState: x.state, Nodes: x.nodes, SubmitTime: x.submit, Account: x.account})
	}
	return r
}

func newSvc(jobs *fakeJobsAPI, meta *fakeMeta) containers.ContainerService {
	return containers.NewContainerServiceWithDeps(jobs, meta)
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// --- LaunchContainer ---

func TestLaunchContainer_SubmitsJobWithScript(t *testing.T) {
	jobs := &fakeJobsAPI{submitResp: &slurmrest.SlurmJobSubmitResp{JobID: 42}}
	svc := newSvc(jobs, &fakeMeta{m: map[string]containers.SessionMeta{}})

	resp, err := svc.LaunchContainer(context.Background(), &containers.ContainerLaunchRequest{EnvType: "jupyter", CPUs: 4, MemoryMB: 8192, Nodes: 1}, "ailsmember", "ailsmember")
	if err != nil {
		t.Fatal(err)
	}
	if resp.ContainerID == "" || resp.Status != "STARTING" {
		t.Fatalf("bad response: %+v", resp)
	}
	if want := "/api/v1/ide/" + resp.ContainerID + "/"; resp.WebURL != want {
		t.Errorf("web_url=%q want %q", resp.WebURL, want)
	}
	if jobs.lastSubmit == nil {
		t.Fatal("SubmitJob not called")
	}
	if jobs.lastSubmit.Job.Name != "jupyter-ide-"+resp.ContainerID {
		t.Errorf("job name=%q", jobs.lastSubmit.Job.Name)
	}
	if jobs.lastSubmit.Job.CpusPerTask != 4 || jobs.lastSubmit.Job.Partition != "standard" {
		t.Errorf("job spec mismatch: %+v", jobs.lastSubmit.Job)
	}
	// 真·每用户身份：IDE 作业以 clusterUser 提交并带有效 account（L1+L3）
	if jobs.lastActAs != "ailsmember" {
		t.Errorf("actAs=%q want ailsmember", jobs.lastActAs)
	}
	if jobs.lastSubmit.Job.Account != "ailsmember" {
		t.Errorf("account=%q want ailsmember", jobs.lastSubmit.Job.Account)
	}
	s := jobs.lastSubmit.Script
	for _, want := range []string{"--ServerApp.base_url=", "/shared/sessions/", "/shared/home/", "node_ip", "jupyter lab"} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
}

func TestLaunchContainer_RejectsBadEnvType(t *testing.T) {
	svc := newSvc(&fakeJobsAPI{}, &fakeMeta{m: map[string]containers.SessionMeta{}})
	_, err := svc.LaunchContainer(context.Background(), &containers.ContainerLaunchRequest{EnvType: "matlab"}, "ailsmember", "ailsmember")
	if !errors.Is(err, containers.ErrUnsupportedEnvType) {
		t.Fatalf("want ErrUnsupportedEnvType, got %v", err)
	}
}

func TestLaunchContainer_SubmitError(t *testing.T) {
	jobs := &fakeJobsAPI{submitErr: errors.New("slurmrestd down")}
	svc := newSvc(jobs, &fakeMeta{m: map[string]containers.SessionMeta{}})
	if _, err := svc.LaunchContainer(context.Background(), &containers.ContainerLaunchRequest{EnvType: "jupyter"}, "ailsmember", "ailsmember"); err == nil {
		t.Fatal("want error when SubmitJob fails")
	}
}

// --- ListActiveContainers ---

func TestListActiveContainers_JobStateFilter(t *testing.T) {
	jobs := &fakeJobsAPI{jobs: jobsResp(
		jrow{id: 1001, name: "jupyter-ide-aaa", state: "RUNNING", nodes: "node1", submit: 1},
		jrow{id: 1002, name: "vscode-ide-bbb", state: "PENDING", submit: 2},
		jrow{id: 1003, name: "jupyter-ide-ccc", state: "COMPLETED", nodes: "node3", submit: 3}, // STOPPED → 不列
		jrow{id: 1004, name: "unrelated-job", state: "RUNNING", nodes: "node2", submit: 4},     // 非 IDE → 不列
	)}
	meta := &fakeMeta{m: map[string]containers.SessionMeta{
		"aaa": {SessionID: "aaa", NodeIP: "10.0.0.1", Port: 8900, CPUs: 2, MemoryMB: 4096, Nodes: 1},
		"bbb": {SessionID: "bbb", NodeIP: "10.0.0.2", Port: 8901},
	}}
	svc := newSvc(jobs, meta)

	list, err := svc.ListActiveContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 active sessions, got %d: %+v", len(list), list)
	}
	byID := map[string]*containers.ContainerInstance{}
	for _, c := range list {
		byID[c.ID] = c
	}
	if byID["aaa"] == nil || byID["aaa"].Status != "RUNNING" || byID["aaa"].Node != "node1" || byID["aaa"].CPUs != 2 {
		t.Errorf("aaa mismatch: %+v", byID["aaa"])
	}
	if byID["bbb"] == nil || byID["bbb"].Status != "STARTING" {
		t.Errorf("bbb mismatch: %+v", byID["bbb"])
	}
}

// --- RecycleContainer ---

func TestRecycleContainer_CancelsJobAndDeletesMeta(t *testing.T) {
	jobs := &fakeJobsAPI{jobs: jobsResp(jrow{id: 1001, name: "jupyter-ide-aaa", state: "RUNNING", nodes: "node1", submit: 1})}
	meta := &fakeMeta{m: map[string]containers.SessionMeta{"aaa": {SessionID: "aaa", JobID: 1001}}}
	svc := newSvc(jobs, meta)

	if _, err := svc.RecycleContainer(context.Background(), "aaa", "ailsmember"); err != nil {
		t.Fatal(err)
	}
	if len(jobs.cancelled) != 1 || jobs.cancelled[0] != 1001 {
		t.Errorf("want CancelJob(1001), got %v", jobs.cancelled)
	}
	if !containsStr(meta.deleted, "aaa") {
		t.Errorf("meta not deleted: %v", meta.deleted)
	}
}

func TestRecycleContainer_NotFound(t *testing.T) {
	svc := newSvc(&fakeJobsAPI{}, &fakeMeta{m: map[string]containers.SessionMeta{}})
	_, err := svc.RecycleContainer(context.Background(), "ghost", "")
	if !errors.Is(err, containers.ErrContainerNotFound) {
		t.Fatalf("want ErrContainerNotFound, got %v", err)
	}
}

// --- ProxyTarget ---

func TestProxyTarget_RunningReady(t *testing.T) {
	jobs := &fakeJobsAPI{jobs: jobsResp(jrow{id: 1001, name: "jupyter-ide-aaa", state: "RUNNING", nodes: "node1", submit: 1})}
	meta := &fakeMeta{m: map[string]containers.SessionMeta{"aaa": {SessionID: "aaa", NodeIP: "10.0.0.1", Port: 8900, EnvType: "jupyter"}}}
	svc := newSvc(jobs, meta)

	ip, port, status, envType, err := svc.ProxyTarget(context.Background(), "aaa")
	if err != nil || status != "RUNNING" || ip != "10.0.0.1" || port != 8900 || envType != "jupyter" {
		t.Fatalf("proxy target: ip=%s port=%d status=%s env=%s err=%v", ip, port, status, envType, err)
	}
}

func TestProxyTarget_StartingStatus(t *testing.T) {
	jobs := &fakeJobsAPI{jobs: jobsResp(jrow{id: 1002, name: "jupyter-ide-bbb", state: "PENDING", submit: 1})}
	meta := &fakeMeta{m: map[string]containers.SessionMeta{"bbb": {SessionID: "bbb", NodeIP: "10.0.0.2", Port: 8901}}}
	svc := newSvc(jobs, meta)
	_, _, status, _, err := svc.ProxyTarget(context.Background(), "bbb")
	if err != nil {
		t.Fatalf("starting session should not error: %v", err)
	}
	if status != "STARTING" {
		t.Errorf("status=%s want STARTING", status)
	}
}

func TestProxyTarget_UnknownSession(t *testing.T) {
	svc := newSvc(&fakeJobsAPI{}, &fakeMeta{m: map[string]containers.SessionMeta{}})
	_, _, _, _, err := svc.ProxyTarget(context.Background(), "ghost")
	if !errors.Is(err, containers.ErrContainerNotFound) {
		t.Fatalf("want ErrContainerNotFound, got %v", err)
	}
}

// TestLaunchContainer_TimeLimitAdjustable：time_limit_min 透传（默认 7200，可调，>720 封顶）。
func TestLaunchContainer_TimeLimitAdjustable(t *testing.T) {
	// want 单位=分钟（v0.0.37 REST time_limit 实测为分钟；内部秒→分钟取整）
	cases := []struct {
		min, want int
	}{
		{0, 120},   // 默认 2h
		{30, 30},   // 30 分钟
		{800, 720}, // 超上限 → 12h 封顶
	}
	for _, c := range cases {
		jobs := &fakeJobsAPI{submitResp: &slurmrest.SlurmJobSubmitResp{JobID: 9}}
		svc := newSvc(jobs, &fakeMeta{m: map[string]containers.SessionMeta{}})
		if _, err := svc.LaunchContainer(context.Background(),
			&containers.ContainerLaunchRequest{EnvType: "jupyter", TimeLimitMin: c.min}, "ailsmember", "ailsmember"); err != nil {
			t.Fatalf("min=%d: %v", c.min, err)
		}
		if got := jobs.lastSubmit.Job.TimeLimit; got != c.want {
			t.Errorf("time_limit_min=%d → TimeLimit=%d want %d", c.min, got, c.want)
		}
	}
}

func TestLaunchContainer_GPU_UsesCliSubmit(t *testing.T) {
	var cliCalled bool
	var capturedOpts containers.CliSubmitOpts
	cliSubmit := func(opts containers.CliSubmitOpts) (int, error) {
		cliCalled = true
		capturedOpts = opts
		return 8888, nil
	}

	jobs := &fakeJobsAPI{}
	meta := &fakeMeta{m: map[string]containers.SessionMeta{}}
	svc := containers.NewContainerServiceWithAllDeps(jobs, meta, cliSubmit)

	req := &containers.ContainerLaunchRequest{
		EnvType:   "jupyter",
		EnvPreset: "pytorch",
		GPUs:      1,
		CPUs:      4,
		MemoryMB:  8192,
	}

	resp, err := svc.LaunchContainer(context.Background(), req, "user1", "user1")
	if err != nil {
		t.Fatalf("LaunchContainer failed: %v", err)
	}

	if !cliCalled {
		t.Fatalf("expected cliSubmit to be called for GPU container, but was not")
	}
	if capturedOpts.Partition != "performance" {
		t.Errorf("expected partition 'performance', got %q", capturedOpts.Partition)
	}
	if capturedOpts.Gpus != 1 {
		t.Errorf("expected Gpus 1, got %d", capturedOpts.Gpus)
	}
	if resp.Allocated.JobID != 8888 {
		t.Errorf("expected JobID 8888, got %d", resp.Allocated.JobID)
	}
	if resp.Allocated.GPUs != 1 {
		t.Errorf("expected Allocated.GPUs 1, got %d", resp.Allocated.GPUs)
	}
	if resp.Allocated.EnvPreset != "pytorch" {
		t.Errorf("expected Allocated.EnvPreset 'pytorch', got %q", resp.Allocated.EnvPreset)
	}
}

func TestIdleReaper_ReclaimsExpiredSessions(t *testing.T) {
	sid := "test-idle-sid"
	now := time.Now()
	// 模拟已启动 20 分钟（超过 10 分钟保护期）的会话
	submitTime := now.Add(-20 * time.Minute).Unix()

	jobs := &fakeJobsAPI{
		jobs: jobsResp(jrow{id: 777, name: "jupyter-ide-" + sid, state: "RUNNING", nodes: "node1", submit: submitTime, account: "user1"}),
	}
	meta := &fakeMeta{
		m: map[string]containers.SessionMeta{
			sid: {SessionID: sid, JobID: 777, EnvType: "jupyter", NodeIP: "1.2.3.4", Port: 8888, Owner: "user1"},
		},
	}
	svc := containers.NewContainerServiceWithDeps(jobs, meta)

	var reclaimedSid string
	var reclaimedJobID int
	callback := func(sessionID string, jobID int, owner string, idleMin int) {
		reclaimedSid = sessionID
		reclaimedJobID = jobID
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动 reaper，设置超时 15 分钟（20 > 15），检查间隔 10ms
	svc.StartIdleReaper(ctx, 10*time.Millisecond, 15, callback)

	// 等待 reaper 触发
	time.Sleep(50 * time.Millisecond)

	if reclaimedSid != sid || reclaimedJobID != 777 {
		t.Errorf("expected session %s (job 777) to be reclaimed, got sid=%q jobID=%d", sid, reclaimedSid, reclaimedJobID)
	}
	if len(jobs.cancelled) != 1 || jobs.cancelled[0] != 777 {
		t.Errorf("expected job 777 to be cancelled in Slurm, got cancelled: %v", jobs.cancelled)
	}
}

