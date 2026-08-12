package billing

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ails-hpc/pkg/slurmrest"
)

// 费率模型（定价策略，非集群数据），单位 CNY：CPU 每核时、内存每 GB·时、GPU 每卡时。
// 后续可移至 config；此处保留为常量并显式标注，避免与"集群真实数据"混淆。
const (
	rateCPU = 0.50 // CNY / CPU-hour
	rateMEM = 0.10 // CNY / GB-hour
	rateGPU = 2.50 // CNY / GPU-hour
)

// BillingService 基于真实 SACCT（slurmdbd）作业历史计算资源用量与账单。
// slurmdb 为唯一真源 —— 不再维护内存捏造记录。
type BillingService interface {
	GetUsage(ctx context.Context, param UsageQueryParam) (*UsageResponse, error)
	ExportReport(ctx context.Context, param ExportQueryParam) (interface{}, error)
}

// SacctFetcher 抽象 sacct 作业历史查询：默认走 slurmrest（docker exec slurmctld sacct），
// 测试可注入假实现以脱离集群。
type SacctFetcher interface {
	Query(ctx context.Context, user string, start, end time.Time) ([]SacctRow, error)
}

type billingService struct {
	fetcher SacctFetcher
}

// NewBillingService 用真实 slurmrest 客户端构造计费服务。
func NewBillingService(client *slurmrest.Client) BillingService {
	return &billingService{fetcher: sacctViaSlurmrest{client: client}}
}

// NewBillingServiceWithFetcher 用注入的 sacct 抓取器构造计费服务（测试用）。
func NewBillingServiceWithFetcher(f SacctFetcher) BillingService {
	return &billingService{fetcher: f}
}

// sacctFormat 字段顺序与 SacctRow / ParseSacct 一致；
// --parsable2 用 '|' 分隔，--noheader 去表头，--allocations（-X）每作业一行。
const sacctFormat = "JobID,User,Account,Partition,JobName,State,ElapsedRaw,AllocCPUS,AllocTRES,ReqMem,Start,End"

// --- 默认 sacct 抓取器（slurmrest / docker exec）---

type sacctViaSlurmrest struct{ client *slurmrest.Client }

func (f sacctViaSlurmrest) Query(ctx context.Context, user string, start, end time.Time) ([]SacctRow, error) {
	args := []string{"--parsable2", "--noheader", "--allocations", "--format=" + sacctFormat}
	if !start.IsZero() {
		args = append(args, "--starttime="+start.Format("2006-01-02T15:04:05"))
	}
	if !end.IsZero() {
		args = append(args, "--endtime="+end.Format("2006-01-02T15:04:05"))
	}
	if user != "" {
		args = append(args, "--user="+user)
	}
	out, err := f.client.SacctQuery(args...)
	if err != nil {
		return nil, err
	}
	return ParseSacct(string(out))
}

// --- 业务方法 ---

func (s *billingService) GetUsage(ctx context.Context, param UsageQueryParam) (*UsageResponse, error) {
	user := param.User
	if user == "" {
		user = "all"
	}
	project := param.Project
	if project == "" {
		project = "all"
	}

	var start, end time.Time
	if param.StartTime != "" {
		start, _ = parseFlexibleTime(param.StartTime)
	}
	if param.EndTime != "" {
		end, _ = parseFlexibleTime(param.EndTime)
	}

	rows, err := s.fetcher.Query(ctx, param.User, start, end)
	if err != nil {
		return nil, fmt.Errorf("sacct query: %w", err)
	}

	// Project 在 slurm 里最贴近 Account，做后过滤
	if param.Project != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if r.Account == param.Project {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	if param.Limit > 0 && len(rows) > param.Limit {
		rows = rows[:param.Limit]
	}

	cpuHrs, memGBHrs, gpuHrs, jobCount := aggregate(rows)

	return &UsageResponse{
		User:               user,
		Project:            project,
		TotalCPUHours:      round(cpuHrs),
		TotalMemoryGBHours: round(memGBHrs),
		TotalGPUHours:      round(gpuHrs),
		JobCount:           jobCount,
		ContainerCount:     0, // SACCT 无容器概念，如实为 0
	}, nil
}

func (s *billingService) ExportReport(ctx context.Context, param ExportQueryParam) (interface{}, error) {
	usage, err := s.GetUsage(ctx, UsageQueryParam{User: param.User, Project: param.Project})
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

	cost := usage.TotalCPUHours*rateCPU + usage.TotalMemoryGBHours*rateMEM + usage.TotalGPUHours*rateGPU
	user := param.User
	if user == "" {
		user = "all"
	}
	return ExportJSONResponse{
		Format:     "json",
		User:       user,
		Timestamp:  time.Now().Format(time.RFC3339),
		TotalCost:  round(cost),
		Currency:   "CNY",
		JobCount:   usage.JobCount,
		CtrCount:   usage.ContainerCount,
		ExportedBy: "slurm-billing-auditor",
	}, nil
}

// --- 聚合与解析 ---

func aggregate(rows []SacctRow) (cpuHrs, memGBHrs, gpuHrs float64, jobCount int) {
	for _, r := range rows {
		hrs := float64(r.ElapsedRaw) / 3600.0
		cpuHrs += hrs * float64(r.AllocCPUS)
		memGBHrs += hrs * (parseReqMemMB(r.ReqMem) / 1024.0)
		gpuHrs += hrs * float64(parseTRESGPU(r.AllocTRES))
		jobCount++
	}
	return
}

// ParseSacct 解析 sacct --parsable2 --noheader 输出，按 sacctFormat 字段顺序取列。
// 残缺行（<12 列）跳过；数值解析失败置零。
func ParseSacct(out string) ([]SacctRow, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	rows := make([]SacctRow, 0, len(lines))
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		f := strings.Split(ln, "|")
		if len(f) < 12 {
			continue
		}
		elapsed, _ := strconv.ParseInt(strings.TrimSpace(f[6]), 10, 64)
		cpus, _ := strconv.Atoi(strings.TrimSpace(f[7]))
		rows = append(rows, SacctRow{
			JobID:      strings.TrimSpace(f[0]),
			User:       strings.TrimSpace(f[1]),
			Account:    strings.TrimSpace(f[2]),
			Partition:  strings.TrimSpace(f[3]),
			JobName:    strings.TrimSpace(f[4]),
			State:      strings.TrimSpace(f[5]),
			ElapsedRaw: elapsed,
			AllocCPUS:  cpus,
			AllocTRES:  strings.TrimSpace(f[8]),
			ReqMem:     strings.TrimSpace(f[9]),
			Start:      strings.TrimSpace(f[10]),
			End:        strings.TrimSpace(f[11]),
		})
	}
	return rows, nil
}

// parseReqMemMB 解析 sacct ReqMem（如 "3000M"、"4G"、"0"、"100Mc"）为 MB。
// 忽略 per-cpu/per-node 类型后缀（c/n）。
func parseReqMemMB(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0
	}
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i == 0 {
		return 0
	}
	num, _ := strconv.ParseFloat(s[:i], 64)
	var unit byte
	for j := i; j < len(s); j++ {
		c := s[j]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			unit = c
			break
		}
	}
	switch unit {
	case 'K', 'k':
		num /= 1024.0
	case 'M', 'm':
		// MB
	case 'G', 'g':
		num *= 1024.0
	case 'T', 't':
		num *= 1024.0 * 1024.0
	}
	return num
}

// parseTRESGPU 从 AllocTRES（"cpu=4,gres/gpu=1,node=1"）提取 GPU 数量。
func parseTRESGPU(tres string) int {
	for _, part := range strings.Split(tres, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "gres/gpu=") {
			n, _ := strconv.Atoi(strings.TrimPrefix(part, "gres/gpu="))
			return n
		}
	}
	return 0
}

func round(f float64) float64 {
	return float64(int64(f*1e6+0.5)) / 1e6 // 保留 6 位小数，避免浮点尾数
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
