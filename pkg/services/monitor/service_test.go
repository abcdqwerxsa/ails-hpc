package monitor

import (
	"context"
	"sync"
	"testing"
	"time"

	"ails-hpc/pkg/services/nodes"
)

type fakeNodes struct {
	ns  []*nodes.NodeStateInfo
	err error
}

func (f fakeNodes) ListNodes(ctx context.Context) ([]*nodes.NodeStateInfo, error) {
	return f.ns, f.err
}

func TestSnapshot_AggregatesAndDisk(t *testing.T) {
	fn := fakeNodes{ns: []*nodes.NodeStateInfo{
		{Name: "node1", CPUs: 8, AllocCPUs: 4, RealMemory: 3000, AllocMemory: 1000, Gpus: 1, AllocGpus: 0},
		{Name: "node2", CPUs: 8, AllocCPUs: 0, RealMemory: 3000, AllocMemory: 0, Gpus: 0, AllocGpus: 0},
	}}
	disk := func() (int, int, int) { return 80960288, 959218776, 9 }
	svc := NewMonitorServiceWithDeps(fn, disk)

	snap, err := svc.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.CPU.Total != 16 || snap.CPU.Alloc != 4 {
		t.Errorf("cpu = %+v, want alloc=4 total=16", snap.CPU)
	}
	if snap.Mem.Total != 6000 || snap.Mem.Alloc != 1000 {
		t.Errorf("mem = %+v, want alloc=1000 total=6000", snap.Mem)
	}
	if snap.GPU.Total != 1 || snap.GPU.Alloc != 0 {
		t.Errorf("gpu = %+v, want alloc=0 total=1", snap.GPU)
	}
	if got := snap.CPU.Pct(); got != 25 {
		t.Errorf("cpu pct = %d, want 25", got)
	}
	if snap.Disk.Percent != 9 || snap.Disk.TotalKB != 959218776 {
		t.Errorf("disk = %+v", snap.Disk)
	}
}

func TestSnapshot_NodeErrorBubbles(t *testing.T) {
	fn := fakeNodes{err: ErrSlurmUnavailable}
	svc := NewMonitorServiceWithDeps(fn, func() (int, int, int) { return 0, 0, 0 })
	if _, err := svc.Snapshot(context.Background()); err == nil {
		t.Fatal("want error when node source fails (fail-closed)")
	}
}

func TestResource_Pct_ZeroSafe(t *testing.T) {
	if (Resource{}).Pct() != 0 {
		t.Error("zero total should yield 0 pct")
	}
	if (Resource{Alloc: 5, Total: 0}).Pct() != 0 {
		t.Error("zero total should yield 0 pct")
	}
	if (Resource{Alloc: 3, Total: 2}).Pct() != 100 {
		t.Error("over-100 should clamp to 100")
	}
}

// scriptedNodes 按调用次序返回不同数据：第 1 次失败（验证跳过失败采样），
// 之后 CPU 分配百分比逐次递增（1 拍 = 10，2 拍 = 20，…）。
type scriptedNodes struct {
	mu    sync.Mutex
	calls int
}

func (s *scriptedNodes) ListNodes(ctx context.Context) ([]*nodes.NodeStateInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == 1 {
		return nil, ErrSlurmUnavailable
	}
	return []*nodes.NodeStateInfo{
		{Name: "n1", CPUs: 100, AllocCPUs: s.calls * 10, RealMemory: 1000, AllocMemory: 500, Gpus: 10, AllocGpus: 5},
	}, nil
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func newTestSampler(t *testing.T, interval time.Duration, cap int) *serviceImpl {
	t.Helper()
	svc := NewMonitorServiceWithDeps(&scriptedNodes{}, func() (int, int, int) {
		return 80, 100, 80
	}).(*serviceImpl)
	svc.historyCap = cap
	svc.StartSampler(interval)
	t.Cleanup(svc.StopSampler)
	return svc
}

func TestSampler_HistorySkipsFailedSamples(t *testing.T) {
	svc := newTestSampler(t, 5*time.Millisecond, defaultHistoryCap)

	// 第 1 拍失败被跳过，之后两拍成功 → 历史 2 点。
	waitFor(t, 2*time.Second, func() bool { return len(svc.History().CPU) >= 2 })
	svc.StopSampler() // 冻结窗口，避免后续拍影响精确断言

	h := svc.History()
	if len(h.CPU) < 2 {
		t.Fatalf("len(cpu) = %d, want >= 2", len(h.CPU))
	}
	for name, arr := range map[string][]int{"cpu": h.CPU, "mem": h.Mem, "gpu": h.GPU, "disk": h.Disk} {
		if len(arr) != len(h.CPU) || len(h.Timestamps) != len(h.CPU) {
			t.Fatalf("%s length mismatch: timestamps=%d cpu=%d %s=%d",
				name, len(h.Timestamps), len(h.CPU), name, len(arr))
		}
	}
	if h.CPU[0] != 20 || h.CPU[1] != 30 { // 调用 1 失败被跳过；调用 2、3 的百分比
		t.Errorf("cpu = %v, want first two [20 30]", h.CPU)
	}
	if h.Mem[0] != 50 || h.GPU[0] != 50 || h.Disk[0] != 80 {
		t.Errorf("mem/gpu/disk = %v/%v/%v, want 50/50/80", h.Mem, h.GPU, h.Disk)
	}
	if h.Timestamps[0] > h.Timestamps[1] {
		t.Errorf("timestamps not oldest→newest: %v", h.Timestamps)
	}
}

func TestSampler_CapEnforced(t *testing.T) {
	const capN = 3
	svc := newTestSampler(t, 2*time.Millisecond, capN)
	sn := svc.nodes.(*scriptedNodes)

	// 1 次失败 + 5 次成功 = 5 个成功采样，窗口只留最新 3 个（30/40/50）。
	waitFor(t, 2*time.Second, func() bool {
		sn.mu.Lock()
		defer sn.mu.Unlock()
		return sn.calls >= 6 && len(svc.History().CPU) == capN
	})
	svc.StopSampler() // 冻结窗口，避免后续拍继续挤掉老样本

	svc.StopSampler() // 冻结窗口后按实际成功采样数推导期望值
	sn.mu.Lock()
	calls := sn.calls
	sn.mu.Unlock()

	h := svc.History()
	if len(h.CPU) != capN {
		t.Fatalf("len(cpu) = %d, want %d", len(h.CPU), capN)
	}
	successes := calls - 1 // 第 1 次调用失败，被跳过
	if successes < capN {
		t.Fatalf("successful samples = %d, want >= %d", successes, capN)
	}
	for i := 0; i < capN; i++ { // 第 k 个成功采样来自第 k+1 次调用（pct=calls*10），窗口留最后 capN 个
		want := (successes - capN + 2 + i) * 10
		if h.CPU[i] != want {
			t.Errorf("cpu[%d] = %d, want %d (full=%v, calls=%d)", i, h.CPU[i], want, h.CPU, calls)
		}
	}
}

func TestHistory_EmptyIsNonNil(t *testing.T) {
	svc := NewMonitorServiceWithDeps(fakeNodes{}, func() (int, int, int) { return 0, 0, 0 })
	h := svc.History()
	if h.Timestamps == nil || h.CPU == nil || h.Mem == nil || h.GPU == nil || h.Disk == nil {
		t.Fatalf("empty history must be non-nil slices: %+v", h)
	}
	if len(h.Timestamps)+len(h.CPU)+len(h.Mem)+len(h.GPU)+len(h.Disk) != 0 {
		t.Fatalf("empty history must be empty: %+v", h)
	}
}

func TestHistory_ReturnsCopy(t *testing.T) {
	svc := newTestSampler(t, 5*time.Millisecond, defaultHistoryCap)
	waitFor(t, 2*time.Second, func() bool { return len(svc.History().CPU) >= 1 })

	h1 := svc.History()
	h1.CPU[0] = 999
	h1.Timestamps[0] = -1

	h2 := svc.History()
	if h2.CPU[0] == 999 || h2.Timestamps[0] == -1 {
		t.Fatalf("History must return a copy; internal state mutated: %+v", h2)
	}
}

// TestSampler_RecordsQueue：注入 pending 提供者 → 采样记录队列深度。
func TestSampler_RecordsQueue(t *testing.T) {
	svc := NewMonitorServiceWithDeps(fakeNodes{}, func() (int, int, int) { return 0, 0, 0 }).(*serviceImpl)
	svc.pending = func() int { return 7 }
	svc.StartSampler(2 * time.Millisecond)
	t.Cleanup(svc.StopSampler)
	waitFor(t, 2*time.Second, func() bool { return len(svc.History().Queue) >= 1 })
	if got := svc.History().Queue[0]; got != 7 {
		t.Errorf("queue sample = %d, want 7", got)
	}
}

// TestPersistence_SqliteRoundtrip：采样落 sqlite → 新实例从库装回窗口（重启不清零）。
func TestPersistence_SqliteRoundtrip(t *testing.T) {
	path := t.TempDir() + "/monitor.db"

	p1, err := openPersistence(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i, ts := range []int64{100, 101, 102} {
		p1.Append(sample{ts: ts, cpu: i * 10, mem: 5, gpu: 0, diskP: 9, queue: i})
	}
	p1.Prune(2) // 只留最近 2 条（101,102）
	_ = p1.Close()

	p2, err := openPersistence(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer p2.Close()
	loaded := p2.Load()
	if len(loaded) != 2 || loaded[0].ts != 101 || loaded[1].ts != 102 {
		t.Fatalf("loaded = %+v, want ts 101,102", loaded)
	}
	if loaded[1].queue != 2 || loaded[1].cpu != 20 { // ts=102 是第 3 条（i=2）
		t.Errorf("fields = %+v", loaded[1])
	}
}
