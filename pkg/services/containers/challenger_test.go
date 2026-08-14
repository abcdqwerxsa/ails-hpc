package containers_test

import (
	"context"
	"errors"
	"testing"

	"ails-hpc/pkg/services/containers"
	"ails-hpc/pkg/slurmrest"
)

// TestChallenger_LaunchContainer_QuotaExceeded 资源超上限拒绝（不提交作业）。
func TestChallenger_LaunchContainer_QuotaExceeded(t *testing.T) {
	jobs := &fakeJobsAPI{submitResp: &slurmrest.SlurmJobSubmitResp{}}
	svc := newSvc(jobs, &fakeMeta{m: map[string]containers.SessionMeta{}})
	_, err := svc.LaunchContainer(context.Background(), &containers.ContainerLaunchRequest{EnvType: "jupyter", CPUs: 9999}, "ailsmember", "ailsmember")
	if !errors.Is(err, containers.ErrQuotaExceeded) {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}
	if jobs.lastSubmit != nil {
		t.Errorf("SubmitJob must not be called on quota exceed")
	}
}

// TestChallenger_ListActiveContainers_ToleratesMetaReadError meta 读失败不应阻塞列表。
func TestChallenger_ListActiveContainers_ToleratesMetaReadError(t *testing.T) {
	jobs := &fakeJobsAPI{jobs: jobsResp(jrow{id: 1001, name: "jupyter-ide-aaa", state: "RUNNING", nodes: "node1", submit: 1})}
	meta := &fakeMeta{m: nil, readErr: errors.New("docker exec failed")}
	svc := newSvc(jobs, meta)

	list, err := svc.ListActiveContainers(context.Background())
	if err != nil {
		t.Fatalf("meta read failure should not fail list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "aaa" {
		t.Errorf("expected aaa from jobs despite meta error, got %+v", list)
	}
}

// TestChallenger_RecycleContainer_FallsBackToJobScan meta 缺 jobid 时回退扫作业名匹配。
func TestChallenger_RecycleContainer_FallsBackToJobScan(t *testing.T) {
	jobs := &fakeJobsAPI{jobs: jobsResp(jrow{id: 2002, name: "vscode-ide-xyz", state: "RUNNING", nodes: "node2", submit: 1})}
	meta := &fakeMeta{m: map[string]containers.SessionMeta{"xyz": {SessionID: "xyz", JobID: 0}}} // 无 jobid
	svc := newSvc(jobs, meta)

	if _, err := svc.RecycleContainer(context.Background(), "xyz", ""); err != nil {
		t.Fatalf("recycle via job scan: %v", err)
	}
	if len(jobs.cancelled) != 1 || jobs.cancelled[0] != 2002 {
		t.Errorf("want CancelJob(2002) via scan fallback, got %v", jobs.cancelled)
	}
}

// TestChallenger_RecycleContainer_CancelError 冲突冒泡。
func TestChallenger_RecycleContainer_CancelError(t *testing.T) {
	jobs := &fakeJobsAPI{jobs: jobsResp(jrow{id: 1001, name: "jupyter-ide-aaa", state: "RUNNING", submit: 1}), cancelErr: errors.New("rpc")}
	meta := &fakeMeta{m: map[string]containers.SessionMeta{"aaa": {SessionID: "aaa", JobID: 1001}}}
	svc := newSvc(jobs, meta)
	if _, err := svc.RecycleContainer(context.Background(), "aaa", ""); err == nil {
		t.Fatal("want error when CancelJob fails")
	}
}

// TestChallenger_LaunchContainer_DefaultsResources 缺省资源补默认（不报错）。
func TestChallenger_LaunchContainer_DefaultsResources(t *testing.T) {
	jobs := &fakeJobsAPI{submitResp: &slurmrest.SlurmJobSubmitResp{JobID: 7}}
	svc := newSvc(jobs, &fakeMeta{m: map[string]containers.SessionMeta{}})
	resp, err := svc.LaunchContainer(context.Background(), &containers.ContainerLaunchRequest{EnvType: "vscode"}, "ailsmember", "ailsmember")
	if err != nil {
		t.Fatal(err)
	}
	if jobs.lastSubmit.Job.CpusPerTask != 2 { // ideCPUsDefault
		t.Errorf("default cpus=%d want 2", jobs.lastSubmit.Job.CpusPerTask)
	}
	if resp.Allocated == nil || resp.Allocated.MemoryMB != 4096 {
		t.Errorf("default mem mismatch: %+v", resp.Allocated)
	}
}
