package containers_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ails-hpc/pkg/services/containers"
	"ails-hpc/pkg/slurmrest"
)

// --- fakes ---

type fakeJobsAPI struct {
	lastSubmit *slurmrest.SlurmJobSubmitReq
	submitResp *slurmrest.SlurmJobSubmitResp
	submitErr  error
	jobs       *slurmrest.JobsResponse
	cancelled  []int
	cancelErr  error
}

func (f *fakeJobsAPI) SubmitJob(req *slurmrest.SlurmJobSubmitReq) (*slurmrest.SlurmJobSubmitResp, error) {
	f.lastSubmit = req
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
func (f *fakeJobsAPI) CancelJob(jobID int) error {
	f.cancelled = append(f.cancelled, jobID)
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
	id, cpus int
	name, state, nodes string
	submit             int64
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
		}{JobID: x.id, Name: x.name, JobState: x.state, Nodes: x.nodes, SubmitTime: x.submit})
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

	resp, err := svc.LaunchContainer(context.Background(), &containers.ContainerLaunchRequest{EnvType: "jupyter", CPUs: 4, MemoryMB: 8192, Nodes: 1}, "owner-test")
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
	if jobs.lastSubmit.Job.CpusPerTask != 4 || jobs.lastSubmit.Job.Partition != "debug" {
		t.Errorf("job spec mismatch: %+v", jobs.lastSubmit.Job)
	}
	s := jobs.lastSubmit.Script
	for _, want := range []string{"--ServerApp.base_url=", "/shared/sessions/", "node_ip", "jupyter lab"} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
}

func TestLaunchContainer_RejectsBadEnvType(t *testing.T) {
	svc := newSvc(&fakeJobsAPI{}, &fakeMeta{m: map[string]containers.SessionMeta{}})
	_, err := svc.LaunchContainer(context.Background(), &containers.ContainerLaunchRequest{EnvType: "matlab"}, "owner-test")
	if !errors.Is(err, containers.ErrUnsupportedEnvType) {
		t.Fatalf("want ErrUnsupportedEnvType, got %v", err)
	}
}

func TestLaunchContainer_SubmitError(t *testing.T) {
	jobs := &fakeJobsAPI{submitErr: errors.New("slurmrestd down")}
	svc := newSvc(jobs, &fakeMeta{m: map[string]containers.SessionMeta{}})
	if _, err := svc.LaunchContainer(context.Background(), &containers.ContainerLaunchRequest{EnvType: "jupyter"}, "owner-test"); err == nil {
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

	if _, err := svc.RecycleContainer(context.Background(), "aaa"); err != nil {
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
	_, err := svc.RecycleContainer(context.Background(), "ghost")
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
