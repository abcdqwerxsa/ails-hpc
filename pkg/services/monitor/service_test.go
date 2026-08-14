package monitor

import (
	"context"
	"testing"

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
