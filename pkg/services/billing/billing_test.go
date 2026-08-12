package billing

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// fakeFetcher 用 canned sacct 行测试聚合与过滤，无需集群。
type fakeFetcher struct {
	rows []SacctRow
	err  error
}

func (f fakeFetcher) Query(ctx context.Context, user string, start, end time.Time) ([]SacctRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func approxEq(a, b, eps float64) bool { return math.Abs(a-b) < eps }

// --- 解析器 ---

func TestParseSacct_RealShape(t *testing.T) {
	out := strings.Join([]string{
		"4|root|root|debug|probe-A|COMPLETED|6|1|billing=1,cpu=1,node=1|3000M|2026-08-12T01:02:54|2026-08-12T01:03:00",
		"5|root|root|debug|probe-B|COMPLETED|8|4|billing=4,cpu=4,gres/gpu=2,node=1|4096M|2026-08-12T01:02:54|2026-08-12T01:03:02",
	}, "\n")
	rows, err := ParseSacct(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].JobID != "4" || rows[0].ElapsedRaw != 6 || rows[0].AllocCPUS != 1 || rows[0].ReqMem != "3000M" {
		t.Errorf("row0 parse mismatch: %+v", rows[0])
	}
	if rows[1].AllocCPUS != 4 || rows[1].AllocTRES != "billing=4,cpu=4,gres/gpu=2,node=1" {
		t.Errorf("row1 parse mismatch: %+v", rows[1])
	}
}

func TestParseSacct_EmptyAndMalformed(t *testing.T) {
	if rows, _ := ParseSacct(""); len(rows) != 0 {
		t.Fatalf("empty input → want 0 rows, got %d", len(rows))
	}
	// 第一行残缺（<12 列）→ 跳过；第二行合法 → 保留
	rows, _ := ParseSacct("a|b|c\n4|root|root|debug|probe-A|COMPLETED|6|1|cpu=1|3000M|s|e")
	if len(rows) != 1 || rows[0].JobID != "4" {
		t.Fatalf("want 1 valid row, got %+v", rows)
	}
}

// --- 聚合数学 ---

func TestAggregate_Math(t *testing.T) {
	rows := []SacctRow{
		{ElapsedRaw: 3600, AllocCPUS: 2, ReqMem: "4096M", AllocTRES: "cpu=2,gres/gpu=1"}, // 2 CPU·h, 4 GB·h, 1 GPU·h
		{ElapsedRaw: 1800, AllocCPUS: 4, ReqMem: "8G", AllocTRES: "cpu=4"},                // 2 CPU·h, 4 GB·h, 0 GPU·h
	}
	cpu, mem, gpu, jobs := aggregate(rows)
	if !approxEq(cpu, 4.0, 1e-9) {
		t.Errorf("cpu-hrs=%v want 4", cpu)
	}
	if !approxEq(mem, 8.0, 1e-9) {
		t.Errorf("mem-gb-hrs=%v want 8", mem)
	}
	if !approxEq(gpu, 1.0, 1e-9) {
		t.Errorf("gpu-hrs=%v want 1", gpu)
	}
	if jobs != 2 {
		t.Errorf("jobs=%d want 2", jobs)
	}
}

func TestParseReqMemMB_Units(t *testing.T) {
	cases := map[string]float64{
		"3000M": 3000, "4G": 4096, "1024K": 1, "2T": 2097152,
		"0": 0, "": 0, "100Mn": 100, "5Gc": 5120,
	}
	for in, want := range cases {
		if got := parseReqMemMB(in); !approxEq(got, want, 1e-3) {
			t.Errorf("parseReqMemMB(%q)=%v want %v", in, got, want)
		}
	}
}

func TestParseTRESGPU(t *testing.T) {
	if n := parseTRESGPU("cpu=4,gres/gpu=2,node=1"); n != 2 {
		t.Errorf("gpu=%d want 2", n)
	}
	if n := parseTRESGPU("cpu=4,node=1"); n != 0 {
		t.Errorf("gpu=%d want 0", n)
	}
	if n := parseTRESGPU(""); n != 0 {
		t.Errorf("gpu=%d want 0", n)
	}
}

// --- GetUsage / ExportReport ---

func TestGetUsage_FiltersAndAggregation(t *testing.T) {
	rows := []SacctRow{
		{JobID: "1", User: "alice", Account: "lab-a", ElapsedRaw: 3600, AllocCPUS: 2, ReqMem: "4096M"},
		{JobID: "2", User: "bob", Account: "lab-b", ElapsedRaw: 3600, AllocCPUS: 4, ReqMem: "4096M"},
		{JobID: "3", User: "alice", Account: "lab-a", ElapsedRaw: 1800, AllocCPUS: 1, ReqMem: "4096M"},
	}
	svc := NewBillingServiceWithFetcher(fakeFetcher{rows: rows})

	// 无过滤：3 作业，CPU = 2 + 4 + 0.5 = 6.5
	u, err := svc.GetUsage(context.Background(), UsageQueryParam{})
	if err != nil {
		t.Fatal(err)
	}
	if u.JobCount != 3 {
		t.Errorf("JobCount=%d want 3", u.JobCount)
	}
	if !approxEq(u.TotalCPUHours, 6.5, 1e-9) {
		t.Errorf("CPU=%v want 6.5", u.TotalCPUHours)
	}
	if u.ContainerCount != 0 {
		t.Errorf("ContainerCount=%d want 0（SACCT 无容器）", u.ContainerCount)
	}

	// 按 Account=lab-a 过滤 → alice 的 2 个作业
	u2, _ := svc.GetUsage(context.Background(), UsageQueryParam{Project: "lab-a"})
	if u2.JobCount != 2 {
		t.Errorf("filtered JobCount=%d want 2", u2.JobCount)
	}

	// limit 截断记录数
	u3, _ := svc.GetUsage(context.Background(), UsageQueryParam{Limit: 1})
	if u3.JobCount != 1 {
		t.Errorf("limit JobCount=%d want 1", u3.JobCount)
	}
}

func TestGetUsage_FetcherError(t *testing.T) {
	svc := NewBillingServiceWithFetcher(fakeFetcher{err: errors.New("sacct down")})
	if _, err := svc.GetUsage(context.Background(), UsageQueryParam{}); err == nil {
		t.Fatal("want error when sacct fetcher fails")
	}
}

func TestExportReport_JSONAndChart(t *testing.T) {
	// 1h × 2 CPU；mem 4096M=4GB·h；gpu=1 → cost = 2·0.5 + 4·0.1 + 1·2.5 = 3.9
	rows := []SacctRow{{ElapsedRaw: 3600, AllocCPUS: 2, ReqMem: "4096M", AllocTRES: "gres/gpu=1"}}
	svc := NewBillingServiceWithFetcher(fakeFetcher{rows: rows})

	r, err := svc.ExportReport(context.Background(), ExportQueryParam{Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	jr, ok := r.(ExportJSONResponse)
	if !ok {
		t.Fatalf("want ExportJSONResponse, got %T", r)
	}
	if jr.Currency != "CNY" || jr.ExportedBy != "slurm-billing-auditor" {
		t.Errorf("bad meta: %+v", jr)
	}
	if !approxEq(jr.TotalCost, 3.9, 1e-6) {
		t.Errorf("cost=%v want 3.9", jr.TotalCost)
	}

	rc, _ := svc.ExportReport(context.Background(), ExportQueryParam{Format: "chart"})
	cr, ok := rc.(ExportChartResponse)
	if !ok || cr.Format != "chart" {
		t.Fatalf("bad chart: %+v", rc)
	}
}
