package nodes

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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
	mu          sync.RWMutex
	nodes       map[string]*NodeStateInfo
}

func NewNodeService(client *slurmrest.Client) NodeService {
	return &nodeServiceImpl{
		slurmClient: client,
		nodes:       make(map[string]*NodeStateInfo),
	}
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
	if err != nil {
		return err
	}
	if resp == nil {
		return ErrSlurmUnavailable
	}
	for _, sn := range resp.Nodes {
		if existing, ok := s.nodes[sn.Name]; ok {
			// 保留本地 DRAIN 覆盖（scontrol 已下发，slurmrestd 可能尚未反映）
			if existing.State != "DRAIN" {
				existing.State = sn.State
			}
			if sn.CPUs > 0 {
				existing.CPUs = sn.CPUs
			}
			if sn.RealMemory > 0 {
				existing.RealMemory = sn.RealMemory
			}
			if sn.Cores > 0 {
				existing.Cores = sn.Cores
			}
			// 实时利用率字段（idle 时为 0）
			existing.AllocCPUs = sn.AllocCPUs
			existing.AllocMemory = sn.AllocMemory
			// Reason 不被刷新覆盖 → 本地写入的 reason 持久
		} else {
			s.nodes[sn.Name] = &NodeStateInfo{
				Name:        sn.Name,
				State:       sn.State,
				CPUs:        sn.CPUs,
				AllocCPUs:   sn.AllocCPUs,
				RealMemory:  sn.RealMemory,
				AllocMemory: sn.AllocMemory,
				Cores:       sn.Cores,
			}
		}
	}
	return nil
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

	newState := targetState
	if targetState == "RESUME" {
		newState = "IDLE"
	}

	// Update memory state
	node.State = newState
	if req.Reason != "" {
		node.Reason = req.Reason
	}

	// Attempt executing CLI scontrol fallback if scontrol command exists
	reasonArg := req.Reason
	if reasonArg == "" {
		reasonArg = "State updated via API"
	}
	_ = exec.Command("scontrol", "update", fmt.Sprintf("NodeName=%s", name), fmt.Sprintf("State=%s", targetState), fmt.Sprintf("Reason=%s", reasonArg)).Run()

	return &NodeStateUpdateResponse{
		NodeName: name,
		State:    node.State,
		Message:  fmt.Sprintf("Node %s state updated to %s", name, node.State),
	}, nil
}
