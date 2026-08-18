// Package monitor 提供集群资源监控快照：CPU/内存/GPU 分配量（复用 nodes 服务的真实节点
// 数据，含 sinfo 兜底与 GPU 解析）+ 共享文件系统 /shared 用量（df via docker exec）。
// 供监控页前端按时间窗累积成趋势图。全部真实数据，无任何硬编码。
package monitor

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"ails-hpc/pkg/services/nodes"
	"ails-hpc/pkg/slurmrest"
)

// ErrSlurmUnavailable 节点数据源不可用。
var ErrSlurmUnavailable = errors.New("monitor: slurm unavailable")

// 采样与历史默认参数：5s 一采，保留 360 点（约 30 分钟趋势）。
const (
	defaultSampleInterval = 5 * time.Second
	defaultHistoryCap     = 360
)

// nodeProvider 隔离节点列表来源，便于测试注入。
type nodeProvider interface {
	ListNodes(ctx context.Context) ([]*nodes.NodeStateInfo, error)
}

// diskProvider 返回 /shared 用量 (usedKB, totalKB, percent)。
type diskProvider func() (usedKB, totalKB, percent int)

// Service 是监控快照服务接口。
type Service interface {
	Snapshot(ctx context.Context) (*Snapshot, error)
	// History 返回进程内滚动趋势（oldest→newest，最多 360 点，拷贝）。
	History() MonitorHistory
}

// sample 是一个历史采样点：unix 时间戳 + 四类资源百分比 + 队列深度。
type sample struct {
	ts         int64
	cpu, mem   int
	gpu, diskP int
	queue      int // PENDING 作业数（计数）
}

type serviceImpl struct {
	nodes   nodeProvider
	disk    diskProvider
	pending func() int  // 队列深度来源（nil=恒 0；生产=REST jobs 计数）
	persist persistence // 采样持久化（nil=纯内存；生产=sqlite monitor.db）

	// mu 保护 samples（采样 goroutine 写、History 读）。
	mu         sync.Mutex
	samples    []sample // oldest→newest，上限 historyCap
	historyCap int

	// 采样 goroutine 生命周期（仅 StartSampler 后非 nil）。
	stopCh  chan struct{}
	doneCh  chan struct{}
	stopped bool
}

// NewMonitorService 用真实 slurmrestd 客户端构造：节点走 nodes 服务，磁盘走 df /shared，
// 队列深度走 REST jobs 计数。自动启动后台采样（5s 间隔，360 点滚动历史）。
func NewMonitorService(client *slurmrest.Client) Service {
	return newMonitorService(client, "")
}

// NewMonitorServicePersistent 同上，但采样落 sqlite（monitorPath，如 var/lib/ails/monitor.db），
// 重启后装回最近窗口（3.2）。库打开失败降级为纯内存（监控不因持久化故障不可用）。
func NewMonitorServicePersistent(client *slurmrest.Client, monitorPath string) Service {
	return newMonitorService(client, monitorPath)
}

func newMonitorService(client *slurmrest.Client, monitorPath string) Service {
	s := &serviceImpl{
		nodes:      nodes.NewNodeService(client),
		disk:       querySharedDisk,
		pending:    realPendingCount(client),
		historyCap: defaultHistoryCap,
	}
	if monitorPath != "" {
		if p, err := openPersistence(monitorPath); err == nil {
			s.persist = p
			if loaded := p.Load(); len(loaded) > 0 {
				if len(loaded) > s.historyCap {
					loaded = loaded[len(loaded)-s.historyCap:]
				}
				s.samples = loaded
			}
		}
	}
	s.StartSampler(defaultSampleInterval)
	return s
}

// realPendingCount 经 slurmrestd 统计 PENDING 作业数（失败返回 0，不阻塞采样）。
func realPendingCount(client *slurmrest.Client) func() int {
	return func() int {
		if client == nil {
			return 0
		}
		resp, err := client.GetJobs()
		if err != nil || resp == nil {
			return 0
		}
		n := 0
		for _, j := range resp.Jobs {
			if j.JobState == "PENDING" {
				n++
			}
		}
		return n
	}
}

// NewMonitorServiceWithDeps 注入节点来源与磁盘查询（测试用）。不自动启动采样，
// 由测试自行调用 StartSampler（可用短间隔）以获得确定性。
func NewMonitorServiceWithDeps(n nodeProvider, disk diskProvider) Service {
	return &serviceImpl{nodes: n, disk: disk, historyCap: defaultHistoryCap}
}

// StartSampler 启动后台采样 goroutine（幂等；重复调用无效果）。
// interval <= 0 时用默认 5s。每拍调用 Snapshot，失败则跳过该拍（保持运行）。
// 用 StopSampler 停止；历史在停止后仍可读。
func (s *serviceImpl) StartSampler(interval time.Duration) {
	if interval <= 0 {
		interval = defaultSampleInterval
	}
	s.mu.Lock()
	if s.stopCh != nil { // 已在运行
		s.mu.Unlock()
		return
	}
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.mu.Unlock()
	go s.sampleLoop(interval)
}

// StopSampler 停止采样 goroutine 并等待其退出（幂等）。
func (s *serviceImpl) StopSampler() {
	s.mu.Lock()
	if s.stopCh == nil {
		s.mu.Unlock()
		return
	}
	if !s.stopped {
		s.stopped = true
		close(s.stopCh)
	}
	done := s.doneCh
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (s *serviceImpl) sampleLoop(interval time.Duration) {
	defer close(s.doneCh)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			s.recordSample()
		}
	}
}

// recordSample 采一拍：Snapshot 失败直接跳过（下一拍继续），不中断采样循环。
func (s *serviceImpl) recordSample() {
	snap, err := s.Snapshot(context.Background())
	if err != nil {
		return
	}
	if s.pending != nil {
		snap.Queue = s.pending()
	}
	sm := sample{
		ts:    time.Now().Unix(),
		cpu:   snap.CPU.Pct(),
		mem:   snap.Mem.Pct(),
		gpu:   snap.GPU.Pct(),
		diskP: snap.Disk.Percent,
		queue: snap.Queue,
	}
	if s.persist != nil {
		s.persist.Append(sm)
		s.persist.Prune(s.historyCap * 2) // 库留 2 倍冗余
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.historyCap <= 0 {
		return
	}
	if len(s.samples) >= s.historyCap { // 滚动窗口：挤掉最老的一拍
		copy(s.samples, s.samples[1:])
		s.samples = s.samples[:s.historyCap-1]
	}
	s.samples = append(s.samples, sm)
}

// History 返回趋势历史的拷贝（oldest→newest）。空历史返回非 nil 空切片。
func (s *serviceImpl) History() MonitorHistory {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.samples)
	h := MonitorHistory{
		Timestamps: make([]int64, 0, n),
		CPU:        make([]int, 0, n),
		Mem:        make([]int, 0, n),
		GPU:        make([]int, 0, n),
		Disk:       make([]int, 0, n),
		Queue:      make([]int, 0, n),
	}
	for _, sm := range s.samples {
		h.Timestamps = append(h.Timestamps, sm.ts)
		h.CPU = append(h.CPU, sm.cpu)
		h.Mem = append(h.Mem, sm.mem)
		h.GPU = append(h.GPU, sm.gpu)
		h.Disk = append(h.Disk, sm.diskP)
		h.Queue = append(h.Queue, sm.queue)
	}
	return h
}

// Snapshot 聚合集群资源分配 + 共享盘用量。节点不可达时如实返回错误（fail-closed）。
func (s *serviceImpl) Snapshot(ctx context.Context) (*Snapshot, error) {
	if s.nodes == nil {
		return nil, ErrSlurmUnavailable
	}
	ns, err := s.nodes.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{}
	for _, n := range ns {
		if n == nil {
			continue
		}
		snap.CPU.Alloc += n.AllocCPUs
		snap.CPU.Total += n.CPUs
		snap.Mem.Alloc += n.AllocMemory
		snap.Mem.Total += n.RealMemory
		snap.GPU.Alloc += n.AllocGpus
		snap.GPU.Total += n.Gpus
	}
	if s.disk != nil {
		u, t, p := s.disk()
		snap.Disk = Disk{UsedKB: u, TotalKB: t, Percent: p}
	}
	return snap, nil
}

// querySharedDisk 通过 docker exec slurmctld df 读取共享卷 /shared 的用量。
// 输出形如：
//
//	1K-blocks     Used Use%
//	959218776 80960288   9%
//
// 失败时 percent 返回 -1 作为哨兵（区别于"空盘 0%"），供前端显示"磁盘不可用"。
func querySharedDisk() (usedKB, totalKB, percent int) {
	out, err := slurmrest.RunInSlurmctld("df", "-k", "--output=size,used,pcent", "/shared")
	if err != nil {
		return 0, 0, -1
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n")
	for _, ln := range lines[1:] { // 跳过表头
		f := strings.Fields(ln)
		if len(f) < 3 {
			continue
		}
		total, _ := strconv.Atoi(f[0])
		used, _ := strconv.Atoi(f[1])
		p, _ := strconv.Atoi(strings.TrimSuffix(f[2], "%"))
		if total > 0 {
			return used, total, p
		}
	}
	return 0, 0, -1
}
