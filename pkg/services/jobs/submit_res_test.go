package jobs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ails-hpc/pkg/services/jobs"
	"ails-hpc/pkg/slurmrest"

	"github.com/gin-gonic/gin"
)

// fakeAPI 捕获 REST 提交（内存字段断言用）。
type fakeAPI struct {
	last    *slurmrest.SlurmJobSubmitReq
	lastAs  string
	calls   int
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
	opts jobs.CliSubmitOpts
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
