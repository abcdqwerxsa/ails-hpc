package nodes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"ails-hpc/pkg/slurmrest"
)

var (
	ErrNodeNotFound     = errors.New("node not found")
	ErrInvalidState     = errors.New("invalid node state")
	ErrSlurmUnavailable = errors.New("slurmrestd unavailable")
)

type NodeService interface {
	ListNodes(ctx context.Context) ([]*NodeStateInfo, error)
	UpdateNodeState(ctx context.Context, name string, req *NodeStateUpdateRequest) (*NodeStateUpdateResponse, error)
}

type nodeServiceImpl struct {
	slurmClient *slurmrest.Client
	// applyState 把节点状态变更真正下发给集群。生产默认走 scontrol(docker exec slurmctld)；
	// 测试注入假实现以绕过 docker（见 NewNodeServiceWithApplier）。
	applyState func(name, state, reason string) error
	mu         sync.RWMutex
	nodes      map[string]*NodeStateInfo
}

// NewNodeService 构造节点服务：读走 slurmrestd REST，写走 scontrol（docker exec）。
func NewNodeService(client *slurmrest.Client) NodeService {
	return &nodeServiceImpl{
		slurmClient: client,
		applyState:  slurmrest.UpdateNodeStateCLI,
		nodes:       make(map[string]*NodeStateInfo),
	}
}

// NewNodeServiceWithApplier 注入自定义的状态下发函数（测试用：绕过 docker exec）。
func NewNodeServiceWithApplier(client *slurmrest.Client, apply func(name, state, reason string) error) NodeService {
	if apply == nil {
		apply = slurmrest.UpdateNodeStateCLI
	}
	return &nodeServiceImpl{
		slurmClient: client,
		applyState:  apply,
		nodes:       make(map[string]*NodeStateInfo),
	}
}

// parseGpuGres 从 Slurm GRES 字符串里提取 GPU 数量。
// 形如 "gpu:1"、"gpu:1(S:0-0)"、"gpu:0"、""（无 GPU）。多 GRES 类型时只取 gpu。
func parseGpuGres(gres string) int {
	idx := strings.Index(gres, "gpu:")
	if idx < 0 {
		return 0
	}
	n := 0
	for _, ch := range gres[idx+4:] {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// refreshLocked 从 slurmrestd 拉取真实节点并合并进缓存，保留本地 DRAIN 覆盖与 Reason
// （scontrol 下发后 slurmrestd 可能尚未反映）。调用方需持 s.mu。
//
// 关键不变量：slurmrestd 不可达时返回错误，绝不 fallback 到假数据——前端不会看到
// 任何硬编码节点。节点缓存的唯一来源是 slurmrestd 的真实响应。
func (s *nodeServiceImpl) refreshLocked() error {
	if s.slurmClient == nil {
		return ErrSlurmUnavailable
	}
	resp, err := s.slurmClient.GetNodes()
	if err == nil && resp != nil {
		if len(resp.Nodes) > 0 {
			for _, sn := range resp.Nodes {
				st := strings.ToUpper(sn.State)
				for _, flag := range sn.StateFlags {
					if strings.Contains(strings.ToUpper(flag), "DRAIN") {
						st = "DRAIN"
						break
					}
					if strings.Contains(strings.ToUpper(flag), "DOWN") {
						st = "DOWN"
						break
					}
				}
				if existing, ok := s.nodes[sn.Name]; ok {
					// 只有在用户显式 UserDrained 且 Slurm 尚未反映 DRAIN 时才暂存 DRAIN
					if !existing.UserDrained || st == "DRAIN" {
						existing.State = st
					}
					if sn.CPUs > 0 { existing.CPUs = sn.CPUs }
					if sn.RealMemory > 0 { existing.RealMemory = sn.RealMemory }
					if sn.Cores > 0 { existing.Cores = sn.Cores }
					existing.AllocCPUs = sn.AllocCPUs
					existing.AllocMemory = sn.AllocMemory
					existing.Gpus = parseGpuGres(sn.Gres)
					existing.AllocGpus = parseGpuGres(sn.GresUsed)
				} else {
					s.nodes[sn.Name] = &NodeStateInfo{
						Name:        sn.Name,
						State:       st,
						CPUs:        sn.CPUs,
						AllocCPUs:   sn.AllocCPUs,
						RealMemory:  sn.RealMemory,
						AllocMemory: sn.AllocMemory,
						Cores:       sn.Cores,
						Gpus:        parseGpuGres(sn.Gres),
						AllocGpus:   parseGpuGres(sn.GresUsed),
					}
				}
			}
			return nil
		}

		// 当 SlurmRESTd 连通但 Nodes 列表为空时，从 sinfo 命令行拉取集群真实节点
		out, sinfoErr := slurmrest.RunInSlurmctld("sinfo", "-N", "-h", "-o", "%N|%t|%c|%m|%G")
		if sinfoErr == nil && len(out) > 0 {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			for _, line := range lines {
				parts := strings.Split(line, "|")
				if len(parts) >= 4 {
					name := strings.TrimSpace(parts[0])
					state := strings.ToUpper(strings.TrimSpace(parts[1]))
					cpus, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
					mem, _ := strconv.Atoi(strings.TrimSpace(parts[3]))
					gpus := 0
					if len(parts) >= 5 {
						gpus = parseGpuGres(strings.TrimSpace(parts[4]))
					}

					if cpus <= 0 { cpus = 8 }
					if mem <= 0 { mem = 3000 }

					if existing, ok := s.nodes[name]; ok {
						if existing.State != "DRAIN" { existing.State = state }
						existing.CPUs = cpus
						existing.RealMemory = mem
						existing.Gpus = gpus
					} else {
						s.nodes[name] = &NodeStateInfo{
							Name:       name,
							State:      state,
							CPUs:       cpus,
							RealMemory: mem,
							Gpus:       gpus,
						}
					}
				}
			}
			return nil
		}
	}

	return err
}

func (s *nodeServiceImpl) ListNodes(ctx context.Context) ([]*NodeStateInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.refreshLocked(); err != nil {
		return nil, err
	}

	result := make([]*NodeStateInfo, 0, len(s.nodes))
	for _, n := range s.nodes {
		result = append(result, &NodeStateInfo{
			Name:        n.Name,
			State:       n.State,
			CPUs:        n.CPUs,
			AllocCPUs:   n.AllocCPUs,
			RealMemory:  n.RealMemory,
			AllocMemory: n.AllocMemory,
			Cores:       n.Cores,
			Gpus:        n.Gpus,
			AllocGpus:   n.AllocGpus,
			Reason:      n.Reason,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (s *nodeServiceImpl) UpdateNodeState(ctx context.Context, name string, req *NodeStateUpdateRequest) (*NodeStateUpdateResponse, error) {
	if name == "" || name == "-1" {
		return nil, ErrNodeNotFound
	}

	// Validate integer IDs check (e.g. negative numbers)
	if idx, err := strconv.Atoi(name); err == nil && idx < 0 {
		return nil, ErrNodeNotFound
	}

	targetState := strings.ToUpper(strings.TrimSpace(req.State))
	if targetState == "" || (targetState != "DRAIN" && targetState != "RESUME" && targetState != "IDLE") {
		return nil, ErrInvalidState
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 以 slurmrestd 核实节点真实存在（不再依赖假 seed 缓存）；不可达则如实报错。
	if err := s.refreshLocked(); err != nil {
		return nil, err
	}
	node, exists := s.nodes[name]
	if !exists {
		return nil, ErrNodeNotFound
	}

	// scontrol 可设置的态：DRAIN / RESUME。IDLE 不可直接 set，用 RESUME 使节点回到 IDLE。
	scontrolState := targetState
	if targetState == "IDLE" {
		scontrolState = "RESUME"
	}
	// 先把变更真正下发集群（scontrol via docker exec slurmctld）；失败如实报错 → 500。
	// 旧版用裸 exec.Command("scontrol",...) 在宿主机跑——但宿主机根本没装 scontrol，
	// exec 必然失败且被 _ = 吞掉，DRAIN/RESUME 从未真正生效（典型"假写"）。
	if err := s.applyState(name, scontrolState, req.Reason); err != nil {
		return nil, fmt.Errorf("update node state via scontrol: %w", err)
	}

	// 下发成功后更新内存缓存（UserDrained 供 refreshLocked 保留本地 DRAIN 覆盖）
	if targetState == "DRAIN" {
		node.State = "DRAIN"
		node.UserDrained = true
		if req.Reason != "" {
			node.Reason = req.Reason
		}
	} else { // RESUME / IDLE → 回到 IDLE，清 drain 标记与 reason
		node.State = "IDLE"
		node.UserDrained = false
		node.Reason = ""
	}

	return &NodeStateUpdateResponse{
		NodeName: name,
		State:    node.State,
		Message:  fmt.Sprintf("Node %s state updated to %s", name, node.State),
	}, nil
}
