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
	SubmitJob(ctx context.Context, req *SubmitJobRequest, clusterUser, account string) (*SubmitJobResponse, error)
	ListJobs(ctx context.Context) ([]JobSummary, error)
	// JobDetail 返回单个作业的生命期详情（sacct）+ 输出尾部（/shared/jobs/<id>.out）。
	JobDetail(ctx context.Context, jobID int) (*JobDetail, error)
	// JobOwner 返回作业的归属者（submit 时写入的 slurm account，即 clusterUser）。
	// 用于归属隔离：member 只能控制自己的作业。空 owner 视为遗留作业（放行）。
	JobOwner(ctx context.Context, jobID int) (string, error)
	// 控制类操作带 actAs（L4）：actAs=作业属主 clusterUser 时以该用户令牌下发，Slurm 层
	// 强制校验属主；actAs 为空则 root（tenant_admin 越权 / 管理操作）。
	CancelJob(ctx context.Context, jobID int, actAs string) (*JobControlResponse, error)
	HoldJob(ctx context.Context, jobID int, actAs string) (*JobControlResponse, error)
	RequeueJob(ctx context.Context, jobID int, actAs string) (*JobControlResponse, error)
}

// slurmJobAPI 隔离 slurmrestd 作业相关调用，便于测试注入假实现（镜像 containers 包的同名 seam）。
// *slurmrest.Client 天然满足该接口。
type slurmJobAPI interface {
	SubmitJobAs(req *slurmrest.SlurmJobSubmitReq, actAs string) (*slurmrest.SlurmJobSubmitResp, error)
	GetJobs() (*slurmrest.JobsResponse, error)
	CancelJobAs(jobID int, actAs string) error
	HoldJobAs(jobID int, actAs string) error
	RequeueJobAs(jobID int, actAs string) error
}

type jobServiceImpl struct {
	jobs      slurmJobAPI
	mu        sync.RWMutex
	localJobs map[int]*JobSummary
	// cliSubmit 是 GPU 作业的 CLI 提交路径（slurm 21.08 REST 无 gres 字段）：
	// 经 docker exec slurmctld `sudo -u <clusterUser> sbatch` 以真实身份提交。
	// 测试注入假实现。
	cliSubmit func(opts CliSubmitOpts) (int, error)
	// sacctRun / tailOut 供 JobDetail 注入（默认走 slurmctld CLI）。
	sacctRun func(args ...string) ([]byte, error)
	tailOut  func(jobID int) (string, error)
}

// CliSubmitOpts 是 GPU 作业 CLI 提交参数（sbatch）；导出供测试断言。
type CliSubmitOpts struct {
	ClusterUser string
	Name        string
	Partition   string
	Script      string
	MemoryMB    int
	Gpus        int
	Nodes       int
	Tasks       int
	TimeLimit   int // 秒
}

// defaultCliSubmit 生产实现：脚本写入 /shared/portal-jobs/<name>-<rand>.job，
// sudo -u <clusterUser> sbatch（身份/记账与 REST 提交同口径），随后保留脚本（复现/排查）。
func defaultCliSubmit(o CliSubmitOpts) (int, error) {
	scriptPath := fmt.Sprintf("/shared/portal-jobs/%s-%d.job", o.Name, time.Now().UnixNano())
	if _, err := slurmrest.RunInSlurmctldWithStdin(o.Script, "sh", "-c",
		"mkdir -p /shared/portal-jobs && cat > "+scriptPath+" && chmod 644 "+scriptPath); err != nil {
		return 0, fmt.Errorf("stage script: %w", err)
	}
	args := []string{"sudo", "-u", o.ClusterUser, "sbatch", "--parsable",
		"-J", o.Name, "-p", o.Partition,
		fmt.Sprintf("--mem=%d", max(o.MemoryMB, 1)),
		fmt.Sprintf("--gres=gpu:%d", max(o.Gpus, 1)),
		fmt.Sprintf("--time=%d", max(o.TimeLimit, 60)),
		"--chdir=/shared", "--output=/shared/jobs/%j.out",
	}
	if o.Nodes > 1 {
		args = append(args, fmt.Sprintf("--nodes=%d", o.Nodes))
	}
	if o.Tasks > 1 {
		args = append(args, fmt.Sprintf("--ntasks=%d", o.Tasks))
	}
	args = append(args, scriptPath)
	out, err := slurmrest.RunInSlurmctld(args...)
	if err != nil {
		return 0, fmt.Errorf("sbatch: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// --parsable 输出 "jobid" 或 "jobid:cluster"
	idStr := strings.SplitN(strings.TrimSpace(string(out)), ":", 2)[0]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return 0, fmt.Errorf("sbatch job id parse: %w (out=%q)", err, string(out))
	}
	return id, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// NewJobService 以真实 slurmrestd 客户端构造作业服务。
func NewJobService(client *slurmrest.Client) JobService {
	var api slurmJobAPI
	if client != nil {
		api = client
	}
	return &jobServiceImpl{
		jobs:      api,
		localJobs: make(map[int]*JobSummary),
		cliSubmit: defaultCliSubmit,
		sacctRun:  defaultSacctRun,
		tailOut:   defaultTailOut,
	}
}

// NewJobServiceWithDeps 注入自定义作业 API（测试用：绕过真实 slurmrestd；CLI 路径同注）。
func NewJobServiceWithDeps(jobs slurmJobAPI, cliSubmit func(opts CliSubmitOpts) (int, error)) JobService {
	if cliSubmit == nil {
		cliSubmit = defaultCliSubmit
	}
	return &jobServiceImpl{
		jobs:      jobs,
		localJobs: make(map[int]*JobSummary),
		cliSubmit: cliSubmit,
		sacctRun:  defaultSacctRun,
		tailOut:   defaultTailOut,
	}
}

func (s *jobServiceImpl) SubmitJob(ctx context.Context, req *SubmitJobRequest, clusterUser, account string) (*SubmitJobResponse, error) {
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

	// 内存/GPU 申请（1.1）：内存 0<mb≤6000（节点 RealMemory 上限）；GPU 仅 performance
	// 分区（唯一 GPU 节点 node1）——不满足则明确报错而非静默排队。
	if req.MemoryMB < 0 || req.MemoryMB > 6000 {
		return nil, ErrInvalidResourceLimit
	}
	if req.Gpus < 0 || req.Gpus > 8 {
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

	if req.Gpus > 0 && partition != "performance" {
		return nil, ErrGPUPartition
	}

	var jobID int
	// GPU 作业：slurm 21.08 REST 无 gres 提交字段（实测 tres_per_node 未知键、gres 被静默
	// 丢弃、#SBATCH 指令不解析）→ CLI 路径 sudo -u <clusterUser> sbatch（身份/记账同口径）。
	if req.Gpus > 0 && s.cliSubmit != nil {
		id, err := s.cliSubmit(CliSubmitOpts{
			ClusterUser: clusterUser, Name: name, Partition: partition,
			Script: req.Script, MemoryMB: req.MemoryMB, Gpus: req.Gpus,
			Nodes: nodesCount, Tasks: req.Tasks, TimeLimit: timeLimit,
		})
		if err != nil {
			return nil, fmt.Errorf("gpu job submit: %w", err)
		}
		jobID = id
	} else if s.jobs != nil {
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
		slurmReq.Job.Account = account // 真实 Slurm account（== clusterUser），AccountingStorageEnforce=associations 校验其关联存在
		slurmReq.Job.MemoryPerNode = req.MemoryMB // 0=缺省（DefMemPerCPU 350/核）
		// 1.2 输出管理：统一落 /shared/jobs/%j.out（stdout/stderr 合流；%j 由 Slurm 展开，
		// 实测可用），cwd=/shared（容器家目录是临时的）。旧作业输出在临时 home 即丢。
		slurmReq.Job.CurrentWorkingDirectory = "/shared"
		slurmReq.Job.StandardOutput = "/shared/jobs/%j.out"

		// 以 clusterUser 真实身份提交（per-user JWT）→ 作业以该 unix 身份运行（L1 隔离核心）
		resp, err := s.jobs.SubmitJobAs(slurmReq, clusterUser)
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
		Owner:      clusterUser,
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
	if s.jobs != nil {
		resp, err := s.jobs.GetJobs()
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
					Owner:      j.Account, // 归属隔离：从 slurm account 回填
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

// defaultSacctRun 经 slurmctld 执行 sacct。
func defaultSacctRun(args ...string) ([]byte, error) {
	return slurmrest.RunInSlurmctld(append([]string{"sacct"}, args...)...)
}

// defaultTailOut 读作业输出文件尾部（/shared/jobs/<id>.out；共享卷挂载于所有容器）。
func defaultTailOut(jobID int) (string, error) {
	out, err := slurmrest.RunInSlurmctld("sh", "-c",
		fmt.Sprintf("tail -n 200 /shared/jobs/%d.out 2>/dev/null", jobID))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// JobDetail sacct 生命期数据 + 输出尾部。作业不存在 → ErrJobNotFound。
// 取 -P 首条主记录（.batch/.step 行忽略）。
func (s *jobServiceImpl) JobDetail(ctx context.Context, jobID int) (*JobDetail, error) {
	if s.sacctRun == nil || jobID <= 0 {
		return nil, ErrJobNotFound
	}
	out, err := s.sacctRun("-n", "-P", "-j", strconv.Itoa(jobID),
		"-o", "JobID,JobName,User,Account,Partition,State,ElapsedRaw,ExitCode,Start,End,Submit")
	if err != nil {
		return nil, fmt.Errorf("sacct: %w", err)
	}
	for _, ln := range strings.Split(string(out), "\n") {
		f := strings.Split(ln, "|")
		if len(f) < 11 || strings.Contains(f[0], ".") {
			continue // 步骤行/短行
		}
		d := &JobDetail{
			Name: f[1], Owner: f[2], Account: f[3], Partition: f[4], State: f[5],
			ExitCode: f[7], Start: f[8], End: f[9], Submit: f[10],
		}
		d.JobID = jobID
		d.ElapsedSec, _ = strconv.Atoi(f[6])
		if s.tailOut != nil {
			if tail, terr := s.tailOut(jobID); terr == nil {
				d.StdoutTail = tail
			}
		}
		return d, nil
	}
	return nil, ErrJobNotFound
}

// JobOwner 返回作业归属者（submit 时写入的 slurm account）。复用 ListJobs 的合并视图
// （local + slurm）。作业不存在返回 ErrJobNotFound。
func (s *jobServiceImpl) JobOwner(ctx context.Context, jobID int) (string, error) {
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		return "", err
	}
	for _, j := range jobs {
		if j.JobID == jobID {
			return j.Owner, nil
		}
	}
	return "", ErrJobNotFound
}

func (s *jobServiceImpl) CancelJob(ctx context.Context, jobID int, actAs string) (*JobControlResponse, error) {
	if jobID <= 0 {
		return nil, ErrJobNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.jobs != nil {
		if err := s.jobs.CancelJobAs(jobID, actAs); err != nil {
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

func (s *jobServiceImpl) HoldJob(ctx context.Context, jobID int, actAs string) (*JobControlResponse, error) {
	if jobID <= 0 {
		return nil, ErrJobNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	summary, exists := s.localJobs[jobID]
	if exists && summary.JobState == "CANCELLED" {
		return nil, ErrCannotHoldCancelled
	}

	if s.jobs != nil {
		if err := s.jobs.HoldJobAs(jobID, actAs); err != nil {
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

func (s *jobServiceImpl) RequeueJob(ctx context.Context, jobID int, actAs string) (*JobControlResponse, error) {
	if jobID <= 0 {
		return nil, ErrJobNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.jobs != nil {
		if err := s.jobs.RequeueJobAs(jobID, actAs); err != nil {
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
