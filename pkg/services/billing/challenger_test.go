package billing

import (
	"context"
	"testing"
)

// TestChallenger_Parse_MalformedNumerics 坏数值字段必须降级为 0，而非 panic/报错。
func TestChallenger_Parse_MalformedNumerics(t *testing.T) {
	out := "x|u|a|p|n|COMPLETED|notanumber|alsonot|cpu=1|0M|s|e\n"
	rows, err := ParseSacct(out)
	if err != nil {
		t.Fatalf("malformed numerics should not error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].ElapsedRaw != 0 || rows[0].AllocCPUS != 0 {
		t.Errorf("bad numeric should default to 0: %+v", rows[0])
	}
}

// TestChallenger_Parse_HugeElapsed 超大 ElapsedRaw 不应溢出/panic，聚合仍成立。
func TestChallenger_Parse_HugeElapsed(t *testing.T) {
	out := "1|u|a|p|n|COMPLETED|9999999999|64|cpu=64,gres/gpu=8|128000M|s|e\n"
	rows, _ := ParseSacct(out)
	if len(rows) != 1 || rows[0].ElapsedRaw != 9999999999 {
		t.Fatalf("huge elapsed mishandled: %+v", rows)
	}
	cpu, _, gpu, _ := aggregate(rows)
	if cpu <= 0 || gpu <= 0 {
		t.Errorf("aggregate of huge row should be positive: cpu=%v gpu=%v", cpu, gpu)
	}
}

// TestChallenger_GetUsage_EmptyCluster 空集群（sacct 无记录）应返回零用量、不报错。
func TestChallenger_GetUsage_EmptyCluster(t *testing.T) {
	svc := NewBillingServiceWithFetcher(fakeFetcher{rows: nil})
	u, err := svc.GetUsage(context.Background(), UsageQueryParam{})
	if err != nil {
		t.Fatalf("empty cluster should not error: %v", err)
	}
	if u.JobCount != 0 || u.TotalCPUHours != 0 || u.TotalMemoryGBHours != 0 || u.TotalGPUHours != 0 {
		t.Errorf("empty cluster → zero usage, got %+v", u)
	}
}

// TestChallenger_GetUsage_LimitZeroMeansAll limit=0 表示不截断（与旧行为一致）。
func TestChallenger_GetUsage_LimitZeroMeansAll(t *testing.T) {
	rows := []SacctRow{
		{ElapsedRaw: 1, AllocCPUS: 1, ReqMem: "0"},
		{ElapsedRaw: 1, AllocCPUS: 1, ReqMem: "0"},
		{ElapsedRaw: 1, AllocCPUS: 1, ReqMem: "0"},
	}
	svc := NewBillingServiceWithFetcher(fakeFetcher{rows: rows})
	u, _ := svc.GetUsage(context.Background(), UsageQueryParam{Limit: 0})
	if u.JobCount != 3 {
		t.Errorf("Limit=0 → all 3 jobs, got %d", u.JobCount)
	}
}

// TestChallenger_GetUsage_FetcherError sacct 不可用时向上冒泡错误（运维可见，不静默）。
func TestChallenger_GetUsage_FetcherError(t *testing.T) {
	svc := NewBillingServiceWithFetcher(fakeFetcher{err: context.DeadlineExceeded})
	if _, err := svc.GetUsage(context.Background(), UsageQueryParam{}); err == nil {
		t.Fatal("want error surfaced when sacct fetcher errors")
	}
}
