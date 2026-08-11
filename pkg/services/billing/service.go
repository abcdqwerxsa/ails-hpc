package billing

import (
	"context"
	"sync"
	"time"
)

type BillingService interface {
	// GetUsage computes resource usage statistics based on query parameters
	GetUsage(ctx context.Context, param UsageQueryParam) (*UsageResponse, error)

	// ExportReport exports billing report in JSON or Chart format
	ExportReport(ctx context.Context, param ExportQueryParam) (interface{}, error)

	// RecordJobUsage registers a completed or running job into SACCT audit memory
	RecordJobUsage(record AccountAuditRecord)

	// RecordContainerUsage registers container resource consumption
	RecordContainerUsage(record AccountAuditRecord)
}

type billingService struct {
	mu      sync.RWMutex
	records []AccountAuditRecord
}

func NewBillingService() BillingService {
	s := &billingService{
		records: make([]AccountAuditRecord, 0),
	}
	s.seedDefaultRecords()
	return s
}

func (s *billingService) seedDefaultRecords() {
	now := time.Now()
	// Seed initial baseline audit records for default user 'hpcuser' and 'default' project
	for i := 1; i <= 5; i++ {
		s.records = append(s.records, AccountAuditRecord{
			ID:           "job-" + time.Now().Format("20060102150405") + "-" + string(rune('0'+i)),
			Type:         "job",
			User:         "hpcuser",
			Project:      "default",
			CPUs:         2,
			MemoryMB:     4096,
			GPUs:         0,
			DurationSecs: 3600, // 1 hour
			CreatedAt:    now,
		})
	}
	for i := 1; i <= 2; i++ {
		s.records = append(s.records, AccountAuditRecord{
			ID:           "ctr-" + time.Now().Format("20060102150405") + "-" + string(rune('0'+i)),
			Type:         "container",
			User:         "hpcuser",
			Project:      "default",
			CPUs:         2,
			MemoryMB:     4096,
			GPUs:         1,
			DurationSecs: 3600, // 1 hour
			CreatedAt:    now,
		})
	}
}

func (s *billingService) RecordJobUsage(record AccountAuditRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record.Type == "" {
		record.Type = "job"
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	s.records = append(s.records, record)
}

func (s *billingService) RecordContainerUsage(record AccountAuditRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record.Type == "" {
		record.Type = "container"
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	s.records = append(s.records, record)
}

func (s *billingService) GetUsage(ctx context.Context, param UsageQueryParam) (*UsageResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user := param.User
	if user == "" {
		user = "hpcuser"
	}
	project := param.Project
	if project == "" {
		project = "default"
	}

	var startTime, endTime time.Time
	if param.StartTime != "" {
		startTime, _ = parseFlexibleTime(param.StartTime)
	}
	if param.EndTime != "" {
		endTime, _ = parseFlexibleTime(param.EndTime)
	}

	var filtered []AccountAuditRecord
	for _, r := range s.records {
		if param.User != "" && r.User != param.User {
			continue
		}
		if param.Project != "" && r.Project != param.Project {
			continue
		}
		if !startTime.IsZero() && r.CreatedAt.Before(startTime) {
			continue
		}
		if !endTime.IsZero() && r.CreatedAt.After(endTime) {
			continue
		}
		filtered = append(filtered, r)
	}

	if param.Limit > 0 && len(filtered) > param.Limit {
		filtered = filtered[:param.Limit]
	}

	var totalCPU, totalMemGB, totalGPU float64
	var jobCount, ctrCount int

	for _, r := range filtered {
		hours := r.DurationSecs / 3600.0
		if hours <= 0 {
			hours = 1.0 // Default 1 hour if unspecified
		}
		totalCPU += float64(r.CPUs) * hours
		totalMemGB += (float64(r.MemoryMB) / 1024.0) * hours
		totalGPU += float64(r.GPUs) * hours

		if r.Type == "job" {
			jobCount++
		} else if r.Type == "container" {
			ctrCount++
		}
	}

	return &UsageResponse{
		User:               user,
		Project:            project,
		TotalCPUHours:      totalCPU,
		TotalMemoryGBHours: totalMemGB,
		TotalGPUHours:      totalGPU,
		JobCount:           jobCount,
		ContainerCount:     ctrCount,
	}, nil
}

func (s *billingService) ExportReport(ctx context.Context, param ExportQueryParam) (interface{}, error) {
	usage, err := s.GetUsage(ctx, UsageQueryParam{
		User:    param.User,
		Project: param.Project,
	})
	if err != nil {
		return nil, err
	}

	if param.Format == "chart" {
		return ExportChartResponse{
			Format: "chart",
			Labels: []string{"Jobs", "Containers", "GPU Workloads"},
			Series: []float64{float64(usage.JobCount), float64(usage.ContainerCount), usage.TotalGPUHours},
		}, nil
	}

	// Default JSON Export format
	totalCost := (usage.TotalCPUHours * 0.50) + (usage.TotalMemoryGBHours * 0.10) + (usage.TotalGPUHours * 2.50)
	user := param.User
	if user == "" {
		user = "hpcuser"
	}

	return ExportJSONResponse{
		Format:     "json",
		User:       user,
		Timestamp:  time.Now().Format(time.RFC3339),
		TotalCost:  totalCost,
		Currency:   "CNY",
		JobCount:   usage.JobCount,
		CtrCount:   usage.ContainerCount,
		ExportedBy: "slurm-billing-auditor",
	}, nil
}

func parseFlexibleTime(str string) (time.Time, error) {
	layouts := []string{
		"2006-01-02",
		"2006-01-02T15:04:05Z07:00",
		time.RFC3339,
		"2006-01-02 15:04:05",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, str); err == nil {
			return t, nil
		}
	}
	return time.Time{}, nil
}
