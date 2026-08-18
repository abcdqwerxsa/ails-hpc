// Package cluster 暴露集群级别的只读状态：slurmrestd 控制面 ping 与分区拓扑。
//
// 它取代了已删除的 pkg/apis.SlurmHandler 承载的 GET /ping 与 GET /partitions
// 两条路由，复用全局共享的 *slurmrest.Client（懒加载 token、401/403 自动续期），
// 与 jobs/nodes 等服务保持一致的 service+handler 骨架。
package cluster

import (
	"context"
	"errors"
	"strings"

	"ails-hpc/pkg/slurmrest"
)

// ErrSlurmUnavailable 表示 slurmrestd 客户端未注入或不可达
var ErrSlurmUnavailable = errors.New("slurm rest api client unavailable")

// ClusterService 集群只读状态服务接口
type ClusterService interface {
	// Ping 测试 slurmrestd 可达性与 Slurm 控制节点状态
	Ping(ctx context.Context) (*slurmrest.PingResponse, error)
	// ListPartitions 获取分区拓扑（含富化：总内存来自分区 TRES；GPU/已占 GPU 按
	// 成员节点的 GRES 聚合——GPU 不在分区记录里，挂在节点上）。
	ListPartitions(ctx context.Context) (*PartitionsResponse, error)
}

// Partition 是面向前端的分区视图（slurmrest 分区记录 + 富化字段）。
type Partition struct {
	Name          string `json:"name"`
	Nodes         string `json:"nodes"`
	TotalCPUs     int    `json:"total_cpus"`
	TotalNodes    int    `json:"total_nodes"`
	TotalMemoryMB int    `json:"total_memory_mb"` // 分区 TRES mem=（成员节点 RealMemory 合计）
	Gpus          int    `json:"gpus"`            // 成员节点 GRES gpu 合计
	AllocGpus     int    `json:"alloc_gpus"`      // 已占 GPU 合计
}

// PartitionsResponse GET /api/v1/slurm/partitions 响应。
type PartitionsResponse struct {
	Errors     []interface{} `json:"errors"`
	Partitions []Partition   `json:"partitions"`
}

type clusterServiceImpl struct {
	slurmClient *slurmrest.Client
}

// NewClusterService 基于 SlurmREST 客户端构造集群状态服务
func NewClusterService(client *slurmrest.Client) ClusterService {
	return &clusterServiceImpl{slurmClient: client}
}

func (s *clusterServiceImpl) Ping(ctx context.Context) (*slurmrest.PingResponse, error) {
	if s.slurmClient == nil {
		return nil, ErrSlurmUnavailable
	}
	return s.slurmClient.Ping()
}

func (s *clusterServiceImpl) ListPartitions(ctx context.Context) (*PartitionsResponse, error) {
	if s.slurmClient == nil {
		return nil, ErrSlurmUnavailable
	}
	pr, err := s.slurmClient.GetPartitions()
	if err != nil {
		return nil, err
	}
	// 节点视图失败不阻塞分区列表（GPU 列退化为 0，与 nodes 服务兜底哲学一致）
	nodeGpu := map[string][2]int{}
	if nr, nerr := s.slurmClient.GetNodes(); nerr == nil && nr != nil {
		for _, n := range nr.Nodes {
			nodeGpu[n.Name] = [2]int{parseGpuGres(n.Gres), parseGpuGres(n.GresUsed)}
		}
	}

	out := &PartitionsResponse{Errors: pr.Errors, Partitions: make([]Partition, 0, len(pr.Partitions))}
	for _, p := range pr.Partitions {
		q := Partition{
			Name: p.Name, Nodes: p.Nodes,
			TotalCPUs: p.TotalCPUs, TotalNodes: p.TotalNodes,
			TotalMemoryMB: slurmrest.ParseTresMemMB(p.Tres),
		}
		for _, name := range slurmrest.ExpandHostlist(p.Nodes) {
			if g, ok := nodeGpu[name]; ok {
				q.Gpus += g[0]
				q.AllocGpus += g[1]
			}
		}
		out.Partitions = append(out.Partitions, q)
	}
	return out, nil
}

// parseGpuGres 从 GRES 串取 gpu 数（"gpu:1"→1；复用 nodes 服务同义逻辑，包内独立避免反向依赖）。
func parseGpuGres(gres string) int {
	i := strings.Index(gres, "gpu:")
	if i < 0 {
		return 0
	}
	n := 0
	for _, c := range gres[i+4:] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
