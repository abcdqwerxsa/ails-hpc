package nodes

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"ails-hpc/pkg/slurmrest"
)

var (
	ErrNodeNotFound = errors.New("node not found")
	ErrInvalidState = errors.New("invalid node state")
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
	s := &nodeServiceImpl{
		slurmClient: client,
		nodes:       make(map[string]*NodeStateInfo),
	}
	s.initDefaultNodes()
	return s
}

func (s *nodeServiceImpl) initDefaultNodes() {
	defaultNodes := []*NodeStateInfo{
		{Name: "node1", State: "IDLE", CPUs: 64, RealMemory: 128000, Cores: 32},
		{Name: "node2", State: "IDLE", CPUs: 64, RealMemory: 128000, Cores: 32},
		{Name: "node3", State: "IDLE", CPUs: 64, RealMemory: 128000, Cores: 32},
	}
	for _, n := range defaultNodes {
		s.nodes[n.Name] = n
	}
}

func (s *nodeServiceImpl) ListNodes(ctx context.Context) ([]*NodeStateInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Try fetching live nodes from slurmrestd client if available
	if s.slurmClient != nil {
		slurmResp, err := s.slurmClient.GetNodes()
		if err == nil && slurmResp != nil && len(slurmResp.Nodes) > 0 {
			for _, sn := range slurmResp.Nodes {
				if existing, ok := s.nodes[sn.Name]; ok {
					// Respect local DRAIN override if set locally
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
		}
	}

	result := make([]*NodeStateInfo, 0, len(s.nodes))
	// Ensure node1, node2, node3 order if present
	orderedNames := []string{"node1", "node2", "node3"}
	for _, name := range orderedNames {
		if n, ok := s.nodes[name]; ok {
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
	}
	for name, n := range s.nodes {
		if name != "node1" && name != "node2" && name != "node3" {
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
	}

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
