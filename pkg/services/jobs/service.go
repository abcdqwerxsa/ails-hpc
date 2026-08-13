package jobs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ails-hpc/pkg/slurmrest"
)

var globalJobIDCounter int64 = 1000

type JobService interface {
	SubmitJob(ctx context.Context, req *SubmitJobRequest) (*SubmitJobResponse, error)
	ListJobs(ctx context.Context) ([]JobSummary, error)
	CancelJob(ctx context.Context, jobID int) (*JobControlResponse, error)
	HoldJob(ctx context.Context, jobID int) (*JobControlResponse, error)
	RequeueJob(ctx context.Context, jobID int) (*JobControlResponse, error)
}

type jobServiceImpl struct {
	client    *slurmrest.Client
	mu        sync.RWMutex
	localJobs map[int]*JobSummary
}

func NewJobService(client *slurmrest.Client) JobService {
	return &jobServiceImpl{
		client:    client,
		localJobs: make(map[int]*JobSummary),
	}
}

func (s *jobServiceImpl) SubmitJob(ctx context.Context, req *SubmitJobRequest) (*SubmitJobResponse, error) {
	if req == nil {
		return nil, ErrNegativeResources
	}

	cpus := req.CPUs
	if cpus <= 0 && req.Tasks > 0 && req.CpusPerTask > 0 {
		cpus = req.Tasks * req.CpusPerTask
	}

	if req.Nodes < 0 || cpus < 0 || req.Tasks < 0 || req.CpusPerTask < 0 {
		return nil, ErrNegativeResources
	}

	if cpus > 1000 || req.Nodes > 100 {
		return nil, ErrInvalidResourceLimit
	}

	timeLimit, _ := strconv.Atoi(req.TimeLimit.String())
	if timeLimit <= 0 {
		timeLimit = 3600
	}

	nodesCount := req.Nodes
	if nodesCount <= 0 {
		nodesCount = 1
	}

	if cpus <= 0 {
		cpus = 1
	}

	partition := req.Partition
	if partition == "" {
		partition = "standard"
	}

	name := req.Name
	if name == "" {
		name = "unnamed_job"
	}

	var jobID int
	if s.client != nil {
		slurmReq := &slurmrest.SlurmJobSubmitReq{
			Script: req.Script,
		}
		slurmReq.Job.Name = name
		if partition != "standard" {
			slurmReq.Job.Partition = partition
		}
		slurmReq.Job.MinimumNodes = nodesCount
		slurmReq.Job.Tasks = req.Tasks
		slurmReq.Job.CpusPerTask = req.CpusPerTask
		slurmReq.Job.CurrentWorkingDirectory = req.CurrentWorkingDirectory
		slurmReq.Job.TimeLimit = timeLimit
		slurmReq.Job.Environment = map[string]string{
			"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		}

		resp, err := s.client.SubmitJob(slurmReq)
		if err != nil {
			return nil, fmt.Errorf("failed to submit job to slurmrestd: %w", err)
		}
		if resp != nil {
			jobID = resp.JobID
		}
	}

	if jobID <= 0 {
		jobID = int(atomic.AddInt64(&globalJobIDCounter, 1))
	}

	summary := &JobSummary{
		JobID:      jobID,
		Name:       name,
		Partition:  partition,
		JobState:   "SUBMITTED",
		Nodes:      fmt.Sprintf("%d", nodesCount),
		TimeLimit:  timeLimit,
		SubmitTime: time.Now().Unix(),
	}

	s.mu.Lock()
	s.localJobs[jobID] = summary
	s.mu.Unlock()

	return &SubmitJobResponse{
		Code:      200,
		Message:   "Job submitted successfully",
		JobID:     jobID,
		Name:      name,
		Status:    "SUBMITTED",
		Nodes:     nodesCount,
		CPUs:      cpus,
		Partition: partition,
	}, nil
}

func (s *jobServiceImpl) ListJobs(ctx context.Context) ([]JobSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	jobMap := make(map[int]JobSummary)

	// Fetch from SlurmRESTd if client available
	if s.client != nil {
		resp, err := s.client.GetJobs()
		if err != nil {
			return nil, fmt.Errorf("failed to list jobs from slurmrestd: %w", err)
		}
		if resp != nil && len(resp.Jobs) > 0 {
			for _, j := range resp.Jobs {
				jobMap[j.JobID] = JobSummary{
					JobID:      j.JobID,
					Name:       j.Name,
					Partition:  j.Partition,
					JobState:   j.JobState,
					Nodes:      j.Nodes,
					TimeLimit:  j.TimeLimit,
					SubmitTime: j.SubmitTime,
				}
			}
		} else {
			// 通过 squeue 命令行解析实时作业列表
			out, sqErr := slurmrest.RunInSlurmctld("squeue", "-h", "-o", "%i|%j|%P|%T|%D")
			if sqErr == nil && len(out) > 0 {
				lines := strings.Split(strings.TrimSpace(string(out)), "\n")
				for _, line := range lines {
					parts := strings.Split(line, "|")
					if len(parts) >= 5 {
						id, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
						if id > 0 {
							jobMap[id] = JobSummary{
								JobID:     id,
								Name:      strings.TrimSpace(parts[1]),
								Partition: strings.TrimSpace(parts[2]),
								JobState:  strings.ToUpper(strings.TrimSpace(parts[3])),
								Nodes:     strings.TrimSpace(parts[4]),
							}
						}
					}
				}
			}
		}
	}

	// Merge local jobs map (local overrides taking precedence for status changes)
	for id, j := range s.localJobs {
		jobMap[id] = *j
	}

	summaries := make([]JobSummary, 0, len(jobMap))
	for _, j := range jobMap {
		summaries = append(summaries, j)
	}

	return summaries, nil
}

func (s *jobServiceImpl) CancelJob(ctx context.Context, jobID int) (*JobControlResponse, error) {
	if jobID <= 0 {
		return nil, ErrJobNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		if err := s.client.CancelJob(jobID); err != nil {
			return nil, fmt.Errorf("failed to cancel job %d: %w", jobID, err)
		}
	}

	summary, exists := s.localJobs[jobID]
	if exists {
		summary.JobState = "CANCELLED"
	} else {
		s.localJobs[jobID] = &JobSummary{
			JobID:    jobID,
			JobState: "CANCELLED",
		}
	}

	return &JobControlResponse{
		Code:    200,
		Message: fmt.Sprintf("Job %d cancelled successfully", jobID),
		JobID:   jobID,
		Action:  "cancel",
		Status:  "CANCELLED",
	}, nil
}

func (s *jobServiceImpl) HoldJob(ctx context.Context, jobID int) (*JobControlResponse, error) {
	if jobID <= 0 {
		return nil, ErrJobNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	summary, exists := s.localJobs[jobID]
	if exists && summary.JobState == "CANCELLED" {
		return nil, ErrCannotHoldCancelled
	}

	if s.client != nil {
		if err := s.client.HoldJob(jobID); err != nil {
			return nil, fmt.Errorf("failed to hold job %d: %w", jobID, err)
		}
	}

	if exists {
		summary.JobState = "HELD"
	} else {
		s.localJobs[jobID] = &JobSummary{
			JobID:    jobID,
			JobState: "HELD",
		}
	}

	return &JobControlResponse{
		Code:    200,
		Message: fmt.Sprintf("Job %d held successfully", jobID),
		JobID:   jobID,
		Action:  "hold",
		Status:  "HELD",
	}, nil
}

func (s *jobServiceImpl) RequeueJob(ctx context.Context, jobID int) (*JobControlResponse, error) {
	if jobID <= 0 {
		return nil, ErrJobNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		if err := s.client.RequeueJob(jobID); err != nil {
			return nil, fmt.Errorf("failed to requeue job %d: %w", jobID, err)
		}
	}

	summary, exists := s.localJobs[jobID]
	if exists {
		summary.JobState = "PENDING"
	} else {
		s.localJobs[jobID] = &JobSummary{
			JobID:    jobID,
			JobState: "PENDING",
		}
	}

	return &JobControlResponse{
		Code:    200,
		Message: fmt.Sprintf("Job %d requeued successfully", jobID),
		JobID:   jobID,
		Action:  "requeue",
		Status:  "PENDING",
	}, nil
}
