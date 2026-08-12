package containers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

)

var (
	ErrUnsupportedEnvType = errors.New("unsupported env_type. Expected 'vscode' or 'jupyter'")
	ErrInvalidResources   = errors.New("resource amounts cannot be negative")
	ErrQuotaExceeded     = errors.New("requested resources exceed workspace quota")
	ErrContainerNotFound  = errors.New("container workspace not found or already recycled")
)

var globalContainerCounter int64 = 10000

type ContainerService interface {
	LaunchContainer(ctx context.Context, req *ContainerLaunchRequest) (*ContainerLaunchResponse, error)
	ListActiveContainers(ctx context.Context) ([]*ContainerInstance, error)
	RecycleContainer(ctx context.Context, id string) (*ContainerRecycleResponse, error)
}

type containerServiceImpl struct {
	mu         sync.RWMutex
	containers map[string]*ContainerInstance
}

func NewContainerService() ContainerService {
	return &containerServiceImpl{
		containers: make(map[string]*ContainerInstance),
	}
}

func (s *containerServiceImpl) LaunchContainer(ctx context.Context, req *ContainerLaunchRequest) (*ContainerLaunchResponse, error) {
	if req == nil {
		return nil, ErrUnsupportedEnvType
	}

	envType := strings.ToLower(strings.TrimSpace(req.EnvType))
	if envType != "vscode" && envType != "jupyter" {
		return nil, ErrUnsupportedEnvType
	}

	if req.CPUs < 0 || req.MemoryMB < 0 || req.Nodes < 0 {
		return nil, ErrInvalidResources
	}

	if req.CPUs > 512 || req.MemoryMB > 1000000 {
		return nil, ErrQuotaExceeded
	}

	nodes := req.Nodes
	if nodes <= 0 {
		nodes = 1
	}

	cpus := req.CPUs
	if cpus <= 0 {
		cpus = 2
	}

	memoryMB := req.MemoryMB
	if memoryMB <= 0 {
		memoryMB = 4096
	}

	seqID := atomic.AddInt64(&globalContainerCounter, 1)
	containerID := fmt.Sprintf("c-%d", seqID)

	jwtToken, err := GenerateJWTToken(containerID, envType, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate JWT token: %w", err)
	}

	webURL := BuildWebURL(envType, jwtToken, cpus)

	instance := &ContainerInstance{
		ID:        containerID,
		EnvType:   envType,
		Status:    "RUNNING",
		WebURL:    webURL,
		Token:     jwtToken,
		Nodes:     nodes,
		CPUs:      cpus,
		MemoryMB:  memoryMB,
		CreatedAt: time.Now(),
	}

	s.mu.Lock()
	s.containers[containerID] = instance
	s.mu.Unlock()

	return &ContainerLaunchResponse{
		ContainerID: instance.ID,
		EnvType:     instance.EnvType,
		Status:      instance.Status,
		WebURL:      instance.WebURL,
		Token:       instance.Token,
		Allocated:   instance,
	}, nil
}

func (s *containerServiceImpl) ListActiveContainers(ctx context.Context) ([]*ContainerInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*ContainerInstance, 0)
	for _, ctr := range s.containers {
		if ctr.Status == "RUNNING" {
			list = append(list, ctr)
		}
	}
	return list, nil
}

func (s *containerServiceImpl) RecycleContainer(ctx context.Context, id string) (*ContainerRecycleResponse, error) {
	if id == "" {
		return nil, ErrContainerNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	instance, exists := s.containers[id]
	if !exists || instance.Status == "TERMINATED" {
		return nil, ErrContainerNotFound
	}

	instance.Status = "TERMINATED"

	return &ContainerRecycleResponse{
		ContainerID: id,
		Status:      "TERMINATED",
		Message:     fmt.Sprintf("Container %s recycled successfully", id),
	}, nil
}
