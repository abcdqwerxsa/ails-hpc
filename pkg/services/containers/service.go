// Package containers 把 "开 Web-IDE" 实现为提交一个 Slurm 交互作业（Open OnDemand 范式）：
// 作业脚本在计算节点上拉起 Jupyter Lab / code-server，并把 {node_ip,port} 写回共享存储；
// apiserver 据此反向代理浏览器到计算节点。回收 = 取消作业。会话即作业 → 天然进 SACCT 计费。
//
// 注意：包名 "Container" 为历史命名，实际承载的是 Slurm 交互会话，并非 OS 容器。
package containers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"ails-hpc/pkg/slurmrest"
)

var (
	ErrUnsupportedEnvType = errors.New("unsupported env_type. Expected 'jupyter' or 'vscode'")
	ErrInvalidResources   = errors.New("resource amounts cannot be negative")
	ErrQuotaExceeded      = errors.New("requested resources exceed workspace quota")
	ErrContainerNotFound  = errors.New("interactive session not found or already recycled")
)

const (
	idePartition     = "standard" // E 核默认分区（performance=P 核留给计算作业；见 slurm.conf 双分区）
	ideTimeLimit     = 7200 // 2h 默认会话时长（秒）
	ideBaseURLPath   = "/api/v1/ide" // 反代前缀；与应用 base_url 对齐
	idePortBase      = 8800
	idePortRange     = 1000 // [8800, 9800)
	ideMemoryDefault = 4096
	ideCPUsDefault   = 2
	ideJobNamePrefix = "-ide-"
)

// ContainerService 是 Slurm 支撑的交互式开发会话服务。
type ContainerService interface {
	LaunchContainer(ctx context.Context, req *ContainerLaunchRequest, clusterUser, account string) (*ContainerLaunchResponse, error)
	ListActiveContainers(ctx context.Context) ([]*ContainerInstance, error)
	// SessionOwner 返回会话归属者（launch 时写入 meta）。归属隔离用：member 只能回收自己的会话。
	SessionOwner(ctx context.Context, id string) (string, error)
	RecycleContainer(ctx context.Context, id, actAs string) (*ContainerRecycleResponse, error)
	// ProxyTarget 返回会话的反代目标 (node_ip:port)、状态、env_type，供 /ide/<session>/ 反代 handler 使用。
	// env_type 决定反代是否剥前缀：jupyter 有 base_url 对齐（不剥），vscode 根路径启动（剥 /api/v1/ide/<sid>）。
	ProxyTarget(ctx context.Context, sessionID string) (nodeIP string, port int, status string, envType string, err error)
}

// slurmJobAPI 隔离 slurmrestd 作业三件套，便于测试注入假实现。
type slurmJobAPI interface {
	SubmitJobAs(req *slurmrest.SlurmJobSubmitReq, actAs string) (*slurmrest.SlurmJobSubmitResp, error)
	GetJobs() (*slurmrest.JobsResponse, error)
	CancelJobAs(jobID int, actAs string) error // actAs=会话属主（L4：Slurm 校验属主）；空=root
}

// sessionMetaStore 读写 /shared/sessions 下的会话连接信息。
type sessionMetaStore interface {
	ReadAll() (map[string]SessionMeta, error) // sessionID -> meta
	Delete(sessionID string) error
}

type containerServiceImpl struct {
	jobs    slurmJobAPI
	meta    sessionMetaStore
	mu      sync.RWMutex
	targets map[string]cachedTarget // RUNNING 会话反代目标缓存（热路径，避免每请求 2 次 docker exec）
}

type cachedTarget struct {
	nodeIP    string
	port      int
	envType   string
	expiresAt time.Time
}

const proxyCacheTTL = 30 * time.Second

// NewContainerService 用真实 slurmrest 客户端构造（meta 走 docker exec 读 /shared/sessions）。
func NewContainerService(client *slurmrest.Client) ContainerService {
	return &containerServiceImpl{jobs: client, meta: dockerSessionMetaStore{}, targets: make(map[string]cachedTarget)}
}

// NewContainerServiceWithDeps 注入依赖，供测试。
func NewContainerServiceWithDeps(jobs slurmJobAPI, meta sessionMetaStore) ContainerService {
	return &containerServiceImpl{jobs: jobs, meta: meta, targets: make(map[string]cachedTarget)}
}

// LaunchContainer 提交一个交互式 Slurm 作业拉起 Jupyter/code-server，返回会话入口 URL。
// 作业以 clusterUser 真实身份运行（per-user JWT 提交，L1 隔离），account 写入 Slurm。
func (s *containerServiceImpl) LaunchContainer(ctx context.Context, req *ContainerLaunchRequest, clusterUser, account string) (*ContainerLaunchResponse, error) {
	if req == nil {
		return nil, ErrUnsupportedEnvType
	}
	envType := strings.ToLower(strings.TrimSpace(req.EnvType))
	if envType != "jupyter" && envType != "vscode" {
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
		cpus = ideCPUsDefault
	}
	memoryMB := req.MemoryMB
	if memoryMB <= 0 {
		memoryMB = ideMemoryDefault
	}

	sessionID := newSessionID()
	port := portFor(sessionID)
	script := buildIDEScript(envType, sessionID, port, cpus, memoryMB, nodes, clusterUser)

	subReq := &slurmrest.SlurmJobSubmitReq{Script: script}
	subReq.Job.Name = envType + ideJobNamePrefix + sessionID
	subReq.Job.Partition = idePartition
	subReq.Job.Tasks = 1
	subReq.Job.MinimumNodes = nodes
	subReq.Job.CpusPerTask = cpus
	subReq.Job.TimeLimit = ideTimeLimit
	subReq.Job.CurrentWorkingDirectory = "/shared"
	// Slurm 21.08 slurmrestd 要求 environment 为非空 dict，否则拒绝提交
	subReq.Job.Environment = map[string]string{
		"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME": "/shared",
	}
	subReq.Job.Account = account // AccountingStorageEnforce=associations：IDE 作业也需带有效 account

	// 以 clusterUser 真实身份提交（per-user JWT）→ 作业以该 unix 身份运行（L1 隔离核心）
	resp, err := s.jobs.SubmitJobAs(subReq, clusterUser)
	if err != nil {
		return nil, fmt.Errorf("submit ide job: %w", err)
	}
	if resp == nil || resp.JobID == 0 {
		// slurmrestd 用 200+errors[] 返回逻辑失败；此处兜底，避免把失败误报为成功
		return nil, fmt.Errorf("slurmrestd rejected ide job submission (no job_id returned)")
	}

	inst := &ContainerInstance{
		ID:        sessionID,
		EnvType:   envType,
		Status:    "STARTING",
		WebURL:    webURLFor(sessionID),
		JobID:     resp.JobID,
		Nodes:     nodes,
		CPUs:      cpus,
		MemoryMB:  memoryMB,
		CreatedAt: time.Now(),
	}
	return &ContainerLaunchResponse{
		ContainerID: sessionID,
		EnvType:     envType,
		Status:      "STARTING",
		WebURL:      inst.WebURL,
		Allocated:   inst,
	}, nil
}

// ListActiveContainers 列出当前非终止的 IDE 会话（从真实 Slurm 作业派生，无内存状态）。
func (s *containerServiceImpl) ListActiveContainers(ctx context.Context) ([]*ContainerInstance, error) {
	jobsResp, err := s.jobs.GetJobs()
	if err != nil {
		return nil, fmt.Errorf("list slurm jobs: %w", err)
	}
	// meta 读失败不阻塞列表（仅缺少连接细节）
	metaMap, _ := s.meta.ReadAll()

	out := make([]*ContainerInstance, 0)
	for _, j := range jobsResp.Jobs {
		sid, envType, ok := parseIDEJobName(j.Name)
		if !ok {
			continue
		}
		status := jobStateToStatus(j.JobState)
		if status == "STOPPED" {
			continue // 仅列活跃会话
		}
		m := metaMap[sid]
		ins := &ContainerInstance{
			ID:        sid,
			EnvType:   envType,
			Status:    status,
			WebURL:    webURLFor(sid),
			JobID:     j.JobID,
			Node:      firstNonEmpty(m.Node, j.Nodes),
			Nodes:     m.Nodes,
			CPUs:      m.CPUs,
			MemoryMB:  m.MemoryMB,
			CreatedAt: time.Unix(j.SubmitTime, 0),
		}
		out = append(out, ins)
	}
	return out, nil
}

// RecycleContainer 取消会话对应的 Slurm 作业并清理 meta（即结束 IDE 会话）。
// SessionOwner 返回会话归属者（meta.owner）。会话不存在返回 ErrContainerNotFound。
func (s *containerServiceImpl) SessionOwner(ctx context.Context, id string) (string, error) {
	metaMap, err := s.meta.ReadAll()
	if err != nil || metaMap == nil {
		return "", ErrContainerNotFound
	}
	m, ok := metaMap[id]
	if !ok {
		return "", ErrContainerNotFound
	}
	return m.Owner, nil
}

func (s *containerServiceImpl) RecycleContainer(ctx context.Context, id, actAs string) (*ContainerRecycleResponse, error) {
	if id == "" {
		return nil, ErrContainerNotFound
	}
	jobID := 0
	if metaMap, _ := s.meta.ReadAll(); metaMap != nil {
		if m, ok := metaMap[id]; ok {
			jobID = m.JobID
		}
	}
	if jobID == 0 {
		// 兜底：扫作业按 name 匹配 sessionID
		if jobs, err := s.jobs.GetJobs(); err == nil {
			for _, j := range jobs.Jobs {
				if sid, _, ok := parseIDEJobName(j.Name); ok && sid == id {
					jobID = j.JobID
					break
				}
			}
		}
	}
	if jobID == 0 {
		return nil, ErrContainerNotFound
	}
	if err := s.jobs.CancelJobAs(jobID, actAs); err != nil {
		return nil, fmt.Errorf("cancel job %d: %w", jobID, err)
	}
	_ = s.meta.Delete(id) // best-effort 清理
	return &ContainerRecycleResponse{
		ContainerID: id,
		Status:      "STOPPED",
		Message:     fmt.Sprintf("Session %s recycled (job %d cancelled)", id, jobID),
	}, nil
}

// ProxyTarget 解析会话的反代目标。RUNNING 会话的目标在 TTL 内缓存（热路径）；
// 非 RUNNING 状态不缓存，以便前端从 STARTING 及时切到 RUNNING。
func (s *containerServiceImpl) ProxyTarget(ctx context.Context, sessionID string) (string, int, string, string, error) {
	s.mu.RLock()
	ct, hit := s.targets[sessionID]
	s.mu.RUnlock()
	if hit && time.Now().Before(ct.expiresAt) {
		return ct.nodeIP, ct.port, "RUNNING", ct.envType, nil
	}

	status := "UNKNOWN"
	nodeIP, port, envType := "", 0, ""
	found := false
	if metaMap, _ := s.meta.ReadAll(); metaMap != nil {
		if m, ok := metaMap[sessionID]; ok {
			nodeIP, port, envType, found = m.NodeIP, m.Port, m.EnvType, true
		}
	}
	if jobs, jErr := s.jobs.GetJobs(); jErr == nil {
		for _, j := range jobs.Jobs {
			if sid, et, isIDE := parseIDEJobName(j.Name); isIDE && sid == sessionID {
				status = jobStateToStatus(j.JobState)
				if envType == "" {
					envType = et // meta 缺失时用作业名里的 env 兜底
				}
				break
			}
		}
	}
	if !found {
		return "", 0, status, envType, ErrContainerNotFound
	}
	if status == "RUNNING" {
		s.mu.Lock()
		s.targets[sessionID] = cachedTarget{nodeIP, port, envType, time.Now().Add(proxyCacheTTL)}
		s.mu.Unlock()
	}
	return nodeIP, port, status, envType, nil
}

// --- 作业脚本生成 ---

// buildIDEScript 生成在计算节点上拉起 IDE 应用并回写连接信息的 sbatch 脚本。
// 应用 auth 关闭——访问由 apiserver 的 JWT 网关守门；base_url 对齐反代前缀。
// 注意：不使用 set -u，且节点名取自 hostname -s（容器主机名即 Slurm 节点名），
// 避免引用可能未设置的 Slurm 环境变量导致脚本在写 meta 前就失败。
func buildIDEScript(envType, sessionID string, port, cpus, memoryMB, nodes int, clusterUser string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#!/bin/bash\n")
	fmt.Fprintf(&b, "# AILS interactive dev session env=%s session=%s port=%d cpus=%d mem=%d nodes=%d\n",
		envType, sessionID, port, cpus, memoryMB, nodes)
	fmt.Fprintf(&b, "SESSION_ID=%q\n", sessionID)
	fmt.Fprintf(&b, "PORT=%d\n", port)
	fmt.Fprintf(&b, "BASE_URL=%q\n", ideBaseURLPath+"/"+sessionID)
	fmt.Fprintf(&b, "NODE_NAME=$(hostname -s)\n")
	fmt.Fprintf(&b, "NODE_IP=$(hostname -I | awk '{print $1}')\n")
	fmt.Fprintf(&b, "mkdir -p /shared/sessions\n")
	// per-user HOME：隔离 jupyter/code-server 的 runtime/config。作业默认 HOME=/shared，多用户会
	// 争用 /shared/.local，非 root 用户无权覆盖前人留下的 runtime 文件（cookie secret、server-info、
	// browser-open）而崩溃。改为各用户独享 /shared/home/<user>（/shared 是 1777，可自建子目录）。
	fmt.Fprintf(&b, "export HOME=\"/shared/home/$(whoami)\"\n")
	fmt.Fprintf(&b, "mkdir -p \"$HOME\"\n")
	// 应用启动前先回写连接信息，apiserver 据此反代
	fmt.Fprintf(&b, "cat > /shared/sessions/${SESSION_ID}.json <<EOF\n")
	fmt.Fprintf(&b, "{\"session_id\":\"${SESSION_ID}\",\"job_id\":${SLURM_JOB_ID:-0},\"node\":\"${NODE_NAME}\",\"node_ip\":\"${NODE_IP}\",\"port\":${PORT},\"env_type\":\"%s\",\"cpus\":%d,\"memory_mb\":%d,\"nodes\":%d,\"owner\":\"%s\"}\n",
		envType, cpus, memoryMB, nodes, clusterUser)
	fmt.Fprintf(&b, "EOF\n")
	// 应用输出重定向到会话日志，便于排查启动失败
	fmt.Fprintf(&b, "exec > /shared/sessions/${SESSION_ID}.log 2>&1\n")
	switch envType {
	case "jupyter":
		// base_url 对齐反代前缀；token 置空（由 apiserver JWT 网关守门）。
		// 作业以 clusterUser 非 root 身份运行（per-user JWT 提交），无需 allow_root。
		fmt.Fprintf(&b, "exec jupyter lab --no-browser --ip=0.0.0.0 --port=${PORT} --ServerApp.base_url=${BASE_URL}/ --ServerApp.token= --ServerApp.allow_remote_access=True --ServerApp.tornado_settings='{\"headers\":{\"Content-Security-Policy\":\"\"}}'\n")
	case "vscode":
		// code-server 对子路径代理支持有限（已知限制）：先以根路径启动，反代尽力而为
		fmt.Fprintf(&b, "exec code-server --bind-addr 0.0.0.0:${PORT} --auth none --disable-telemetry\n")
	}
	return b.String()
}

// --- 会话 meta 读写（生产实现：docker exec slurmctld）---

type dockerSessionMetaStore struct{}

func (dockerSessionMetaStore) ReadAll() (map[string]SessionMeta, error) {
	out, err := slurmrest.RunInSlurmctld("sh", "-c", "cat /shared/sessions/*.json 2>/dev/null")
	if err != nil {
		return nil, err
	}
	m := map[string]SessionMeta{}
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var sm SessionMeta
		if json.Unmarshal([]byte(ln), &sm) == nil && sm.SessionID != "" {
			m[sm.SessionID] = sm
		}
	}
	return m, nil
}

func (dockerSessionMetaStore) Delete(sessionID string) error {
	_, err := slurmrest.RunInSlurmctld("rm", "-f", "/shared/sessions/"+sessionID+".json")
	return err
}

// --- 辅助 ---

func newSessionID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:]) // 16 hex chars
}

// portFor 从 sessionID 派生一个 [8800,9800) 端口。小集群冲突概率低；冲突则应用 bind 失败、作业失败、用户重试。
func portFor(sessionID string) int {
	hex := sessionID
	if len(hex) > 4 {
		hex = hex[:4]
	}
	n, err := strconv.ParseInt(hex, 16, 64)
	if err != nil {
		n = 0
	}
	return idePortBase + int(n%int64(idePortRange))
}

func webURLFor(sessionID string) string {
	return ideBaseURLPath + "/" + sessionID + "/"
}

// parseIDEJobName 从 "jupyter-ide-<sid>" / "vscode-ide-<sid>" 解析 (sid, envType)。
func parseIDEJobName(name string) (sid, envType string, ok bool) {
	idx := strings.Index(name, ideJobNamePrefix)
	if idx <= 0 {
		return "", "", false
	}
	env := name[:idx]
	sid = name[idx+len(ideJobNamePrefix):]
	if (env != "jupyter" && env != "vscode") || sid == "" {
		return "", "", false
	}
	return sid, env, true
}

func jobStateToStatus(state string) string {
	state = strings.ToUpper(strings.TrimSpace(state))
	switch {
	case strings.HasPrefix(state, "RUNNING"), strings.HasPrefix(state, "COMPLETING"):
		return "RUNNING"
	case strings.HasPrefix(state, "PENDING"), strings.HasPrefix(state, "CONFIGURING"), strings.HasPrefix(state, "REQUEUED"):
		return "STARTING"
	default: // COMPLETED, CANCELLED, FAILED, TIMEOUT, OUT_OF_MEMORY, ...
		return "STOPPED"
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
