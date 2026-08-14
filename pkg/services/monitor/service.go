// Package monitor 提供集群资源监控快照：CPU/内存/GPU 分配量（复用 nodes 服务的真实节点
// 数据，含 sinfo 兜底与 GPU 解析）+ 共享文件系统 /shared 用量（df via docker exec）。
// 供监控页前端按时间窗累积成趋势图。全部真实数据，无任何硬编码。
package monitor

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"ails-hpc/pkg/services/nodes"
	"ails-hpc/pkg/slurmrest"
)

// ErrSlurmUnavailable 节点数据源不可用。
var ErrSlurmUnavailable = errors.New("monitor: slurm unavailable")

// nodeProvider 隔离节点列表来源，便于测试注入。
type nodeProvider interface {
	ListNodes(ctx context.Context) ([]*nodes.NodeStateInfo, error)
}

// diskProvider 返回 /shared 用量 (usedKB, totalKB, percent)。
type diskProvider func() (usedKB, totalKB, percent int)

// Service 是监控快照服务接口。
type Service interface {
	Snapshot(ctx context.Context) (*Snapshot, error)
}

type serviceImpl struct {
	nodes nodeProvider
	disk  diskProvider
}

// NewMonitorService 用真实 slurmrestd 客户端构造：节点走 nodes 服务，磁盘走 df /shared。
func NewMonitorService(client *slurmrest.Client) Service {
	return &serviceImpl{
		nodes: nodes.NewNodeService(client),
		disk:  querySharedDisk,
	}
}

// NewMonitorServiceWithDeps 注入节点来源与磁盘查询（测试用）。
func NewMonitorServiceWithDeps(n nodeProvider, disk diskProvider) Service {
	return &serviceImpl{nodes: n, disk: disk}
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
//   1K-blocks     Used Use%
//   959218776 80960288   9%
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
