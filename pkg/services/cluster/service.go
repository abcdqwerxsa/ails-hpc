// Package cluster 暴露集群级别的只读状态：slurmrestd 控制面 ping 与分区拓扑。
//
// 它取代了已删除的 pkg/apis.SlurmHandler 承载的 GET /ping 与 GET /partitions
// 两条路由，复用全局共享的 *slurmrest.Client（懒加载 token、401/403 自动续期），
// 与 jobs/nodes 等服务保持一致的 service+handler 骨架。
package cluster

import (
	"context"
	"errors"

	"ails-hpc/pkg/slurmrest"
)

// ErrSlurmUnavailable 表示 slurmrestd 客户端未注入或不可达
var ErrSlurmUnavailable = errors.New("slurm rest api client unavailable")

// ClusterService 集群只读状态服务接口
type ClusterService interface {
	// Ping 测试 slurmrestd 可达性与 Slurm 控制节点状态
	Ping(ctx context.Context) (*slurmrest.PingResponse, error)
	// ListPartitions 获取集群分区定义与分配信息
	ListPartitions(ctx context.Context) (*slurmrest.PartitionsResponse, error)
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

func (s *clusterServiceImpl) ListPartitions(ctx context.Context) (*slurmrest.PartitionsResponse, error) {
	if s.slurmClient == nil {
		return nil, ErrSlurmUnavailable
	}
	return s.slurmClient.GetPartitions()
}
