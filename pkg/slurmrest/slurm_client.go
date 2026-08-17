package slurmrest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 集群接入点。apiserver 通常与 slurmctld 容器同机部署，走本地 docker compose exec；
// 若不在同机（如远程开发），退回到 SSH 远程执行。
const (
	composeFile      = "/opt/slurm-cluster/docker-compose.yml"
	slurmctldService = "slurmctld"
	slurmSSHHost     = "root@192.168.20.226"
	defaultSlurmUser = "hpcuser"
)

// shellQuote 对单个命令参数做单引号转义，供 SSH 远程拼接时安全传递。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// RunInSlurmctld 在 slurmctld 容器内执行给定命令：使用 docker exec slurmctld。
func RunInSlurmctld(args ...string) ([]byte, error) {
	execArgs := append([]string{"exec", slurmctldService}, args...)
	out, err := exec.Command("docker", execArgs...).Output()
	if err == nil {
		return out, nil
	}

	// 备选退路：docker compose
	localArgs := append([]string{"compose", "-f", composeFile, "exec", "-T", slurmctldService}, args...)
	out, err = exec.Command("docker", localArgs...).Output()
	if err == nil {
		return out, nil
	}

	return nil, fmt.Errorf("run in slurmctld (%s) failed: %w", strings.Join(args, " "), err)
}

// mintToken 在 slurmctld 容器内为指定用户铸造 slurmrestd JWT（scontrol token username=<u>），
// 返回 SLURM_JWT= 后的 token 值，失败返回空串。docker exec slurmctld 以 root 运行，可为任意
// 用户铸造 token（slurmrestd 的 JWT 身份仅是签名声明，集群共享 jwt_hs256.key）。
func mintToken(username string) string {
	out, err := RunInSlurmctld("scontrol", "token", fmt.Sprintf("username=%s", username), "lifespan=86400")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		cleanLine := strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
		if strings.HasPrefix(cleanLine, "SLURM_JWT=") {
			return strings.TrimPrefix(cleanLine, "SLURM_JWT=")
		}
	}
	return ""
}

// FetchToken 动态获取特权（root）Slurm JWT 身份令牌（24h），供读/控制类调用与 NewClient 使用。
// 保留为包级函数以兼容既有调用方。
func FetchToken() string {
	return mintToken("root")
}

// userToken 返回指定 clusterUser 的 slurmrestd JWT：clusterUser 为空时用 root 特权令牌
// （c.UserToken，惰性获取）；否则用 per-user 缓存（首次或 refresh=true 时铸造）。
// 这是 L1 真·每用户隔离的核心——submit 路径用它让作业以真实 unix 身份运行。
func (c *Client) userToken(clusterUser string, refresh bool) string {
	if clusterUser == "" {
		if refresh || c.UserToken == "" {
			if t := c.mint("root"); t != "" {
				c.UserToken = t
			}
		}
		return c.UserToken
	}
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if !refresh {
		if t, ok := c.tokens[clusterUser]; ok && t != "" {
			return t
		}
	}
	t := c.mint(clusterUser)
	if t != "" {
		c.tokens[clusterUser] = t
	}
	return t
}

// softErrors 探测 slurmrestd 的"软失败"：HTTP 2xx 但 errors[] 非空。
// v0.0.37 对无效令牌等鉴权问题也返回 200 + errors[]（如 error_code 5005
// "Zero Bytes were transmitted or received"），而非 401——典型触发场景是
// slurmctld 容器重建重签 JWT key 后，客户端缓存的旧令牌全部失效。
// 返回非空列表即视为软失败，调用方应刷新令牌重试一次。
func softErrors(body []byte) []string {
	var probe struct {
		Errors []struct {
			Error string `json:"error"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil // 非 JSON 响应体（如裸文本）不走软失败路径
	}
	msgs := make([]string, 0, len(probe.Errors))
	for _, e := range probe.Errors {
		if e.Error != "" {
			msgs = append(msgs, e.Error)
		}
	}
	return msgs
}

// RunInSlurmctldWithStdin 同 RunInSlurmctld，但向容器进程 stdin 注入数据
// （经 `docker exec -i` —— 用于把作业脚本写进 /shared 的文件再 sbatch）。
func RunInSlurmctldWithStdin(stdin string, args ...string) ([]byte, error) {
	execArgs := append([]string{"exec", "-i", slurmctldService}, args...)
	cmd := exec.Command("docker", execArgs...)
	cmd.Stdin = strings.NewReader(stdin)
	if out, err := cmd.Output(); err == nil {
		return out, nil
	}
	localArgs := append([]string{"compose", "-f", composeFile, "exec", "-T", slurmctldService}, args...)
	cmd2 := exec.Command("docker", localArgs...)
	cmd2.Stdin = strings.NewReader(stdin)
	out, err := cmd2.Output()
	if err != nil {
		return nil, fmt.Errorf("run in slurmctld (stdin, %s) failed: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// SacctQuery 在 slurmctld 容器内执行 sacct 并返回原始 stdout，供调用方按
// --parsable2 等格式解析（用于真实 SACCT 计费）。
func (c *Client) SacctQuery(args ...string) ([]byte, error) {
	return RunInSlurmctld(append([]string{"sacct"}, args...)...)
}

// UpdateNodeStateCLI 通过 scontrol 在 slurmctld 容器内下发节点状态变更
// （DRAIN / RESUME 等）。
//
// 为什么走 CLI 而非 REST：slurm 21.08 的 slurmrestd v0.0.37 **没有节点更新 POST 端点**
// （/slurm/v0.0.37/node/{name} 仅支持 GET，已在 prod 上实测确认）；节点写 REST 要
// slurm 22.05+ / v0.0.38。故节点写操作与 sacct 一样经 RunInSlurmctld（docker exec）
// 走 scontrol，这是当前 slurm 版本下唯一能让 DRAIN/RESUME 真正生效的路径。
func UpdateNodeStateCLI(name, state, reason string) error {
	args := []string{"scontrol", "update", fmt.Sprintf("NodeName=%s", name), fmt.Sprintf("State=%s", state)}
	if reason != "" {
		args = append(args, fmt.Sprintf("Reason=%s", reason))
	}
	_, err := RunInSlurmctld(args...)
	return err
}


// Client 封装了与原生 slurmrestd REST API 交互的客户端。
// UserToken 为 root 特权令牌（读/控制类调用）；tokens 为 per-user 令牌缓存（submit 类调用，
// 使作业以各用户真实 unix 身份运行——L1 隔离）。mint 为令牌铸造函数（默认走 scontrol token，
// 测试可注入）。
type Client struct {
	BaseURL    string
	UserName   string
	UserToken  string
	HTTPClient *http.Client
	tokens     map[string]string
	tokenMu    sync.Mutex
	mint       func(username string) string
}

// NewClient 创建并初始化一个新的 Slurm REST API Client
func NewClient(baseURL, userName, userToken string) *Client {
	if userToken == "" && baseURL != "" {
		userToken = FetchToken()
	}
	return &Client{
		BaseURL:   baseURL,
		UserName:  userName,
		UserToken: userToken,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		tokens: make(map[string]string),
		mint:   mintToken,
	}
}

// executeRequestWithBody 通用 HTTP 请求方法（支持 Request Body）
func (c *Client) executeRequestWithBody(method, path string, bodyData interface{}) ([]byte, error) {
	if c.UserToken == "" {
		c.UserToken = FetchToken()
	}

	reqURL := fmt.Sprintf("%s%s", c.BaseURL, path)

	makeReq := func(token string) (*http.Response, []byte, error) {
		var bodyReader io.Reader
		if bodyData != nil {
			jsonBytes, err := json.Marshal(bodyData)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal request body: %w", err)
			}
			bodyReader = bytes.NewBuffer(jsonBytes)
		}

		req, err := http.NewRequest(method, reqURL, bodyReader)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("X-SLURM-USER-NAME", "root")
		req.Header.Set("X-SLURM-USER-TOKEN", token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("http request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp, nil, fmt.Errorf("failed to read response body: %w", err)
		}
		return resp, body, nil
	}

	used := c.UserToken
	resp, body, err := makeReq(used)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		newToken := c.userToken("", true)
		if newToken != "" && newToken != used {
			used = newToken
			resp, body, err = makeReq(used)
			if err != nil {
				return nil, err
			}
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, fmt.Errorf("slurmrestd returned status %d: %s", resp.StatusCode, string(body))
	}

	// 软失败（200 + errors[] 非空，如 slurmctld 重建后旧令牌失效）：刷新令牌重试一次。
	// 注意与 used（本次实际使用的令牌）比较——userToken 刷新会同步改写 c.UserToken。
	if msgs := softErrors(body); len(msgs) > 0 {
		if newToken := c.userToken("", true); newToken != "" && newToken != used {
			if resp2, body2, err2 := makeReq(newToken); err2 == nil && resp2.StatusCode >= 200 && resp2.StatusCode < 300 && len(softErrors(body2)) == 0 {
				return body2, nil
			}
		}
		return body, fmt.Errorf("slurmrestd errors: %s", strings.Join(msgs, "; "))
	}

	return body, nil
}

// executeRequestAs 以指定 clusterUser（actAs）身份发起请求——X-SLURM-USER-NAME=actAs、
// X-SLURM-USER-TOKEN=该用户的 per-user JWT。用于 submit 类调用（作业以 actAs 真实身份运行）。
// 遇 401/403 时刷新该用户令牌并重试一次；actAs 为空则退化为 root 特权令牌。
func (c *Client) executeRequestAs(method, path string, bodyData interface{}, actAs string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s%s", c.BaseURL, path)

	makeReq := func(token string) (*http.Response, []byte, error) {
		var bodyReader io.Reader
		if bodyData != nil {
			jsonBytes, err := json.Marshal(bodyData)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal request body: %w", err)
			}
			bodyReader = bytes.NewBuffer(jsonBytes)
		}

		req, err := http.NewRequest(method, reqURL, bodyReader)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create request: %w", err)
		}

		userName := actAs
		if userName == "" {
			userName = "root"
		}
		req.Header.Set("X-SLURM-USER-NAME", userName)
		req.Header.Set("X-SLURM-USER-TOKEN", token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("http request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp, nil, fmt.Errorf("failed to read response body: %w", err)
		}
		return resp, body, nil
	}

	token := c.userToken(actAs, false)
	resp, body, err := makeReq(token)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		newToken := c.userToken(actAs, true) // 刷新该用户令牌
		if newToken != "" && newToken != token {
			resp, body, err = makeReq(newToken)
			if err != nil {
				return nil, err
			}
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, fmt.Errorf("slurmrestd returned status %d: %s", resp.StatusCode, string(body))
	}

	// 软失败（200 + errors[] 非空，如 slurmctld 重建后旧令牌失效）：刷新该用户令牌重试一次
	if msgs := softErrors(body); len(msgs) > 0 {
		if newToken := c.userToken(actAs, true); newToken != "" && newToken != token {
			if resp2, body2, err2 := makeReq(newToken); err2 == nil && resp2.StatusCode >= 200 && resp2.StatusCode < 300 && len(softErrors(body2)) == 0 {
				return body2, nil
			}
		}
		return body, fmt.Errorf("slurmrestd errors: %s", strings.Join(msgs, "; "))
	}

	return body, nil
}

// executeRequest 执行 HTTP 请求并附带标准的 Slurm JWT 身份凭证 Header
func (c *Client) executeRequest(method, path string) ([]byte, error) {
	return c.executeRequestWithBody(method, path, nil)
}

// PingResponse 定义了 /slurm/v0.0.37/ping 的响应体解析结构
type PingResponse struct {
	Meta struct {
		Slurm struct {
			Release string `json:"release"`
		} `json:"Slurm"`
	} `json:"meta"`
	Errors []interface{} `json:"errors"`
	Pings  []struct {
		Hostname string `json:"hostname"`
		Ping     string `json:"ping"`
		Status   int    `json:"status"`
		Mode     string `json:"mode"`
	} `json:"pings"`
}

// Ping 测试 slurmrestd API 的可达性与 Slurm 控制节点状态
func (c *Client) Ping() (*PingResponse, error) {
	body, err := c.executeRequest("GET", "/slurm/v0.0.37/ping")
	if err != nil {
		return nil, err
	}

	var res PingResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("failed to decode ping response: %w", err)
	}

	return &res, nil
}

// NodesResponse 定义了 /slurm/v0.0.37/nodes 的响应体解析结构
type NodesResponse struct {
	Errors []interface{} `json:"errors"`
	Nodes  []struct {
		Name        string   `json:"name"`
		State       string   `json:"state"`
		StateFlags  []string `json:"state_flags"`
		CPUs        int      `json:"cpus"`
		AllocCPUs   int      `json:"alloc_cpus"`
		RealMemory  int      `json:"real_memory"`
		AllocMemory int      `json:"alloc_memory"`
		Cores       int      `json:"cores"`
		Gres        string   `json:"gres"`     // 配置的 GRES，如 "gpu:1"（无 GPU 为空）
		GresUsed    string   `json:"gres_used"` // 已占用的 GRES，如 "gpu:0"
	} `json:"nodes"`
}

// GetNodes 获取集群所有 Compute Nodes 节点的运行状态与资源统计
func (c *Client) GetNodes() (*NodesResponse, error) {
	body, err := c.executeRequest("GET", "/slurm/v0.0.37/nodes")
	if err != nil {
		return nil, err
	}

	var res NodesResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("failed to decode nodes response: %w", err)
	}

	return &res, nil
}

// JobsResponse 定义了 /slurm/v0.0.37/jobs 的响应体解析结构
type JobsResponse struct {
	Errors []interface{} `json:"errors"`
	Jobs   []struct {
		JobID      int    `json:"job_id"`
		Name       string `json:"name"`
		Partition  string `json:"partition"`
		JobState   string `json:"job_state"`
		Nodes      string `json:"nodes"`
		TimeLimit  int    `json:"time_limit"`
		SubmitTime int64  `json:"submit_time"`
		Account    string `json:"account"` // 归属隔离：提交者用户名（submit 时写入）
	} `json:"jobs"`
}

// GetJobs 获取集群当前排队与执行的所有作业队列
func (c *Client) GetJobs() (*JobsResponse, error) {
	body, err := c.executeRequest("GET", "/slurm/v0.0.37/jobs")
	if err != nil {
		return nil, err
	}

	var res JobsResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("failed to decode jobs response: %w", err)
	}

	return &res, nil
}

// PartitionsResponse 定义了 /slurm/v0.0.37/partitions 的响应体解析结构
// PartitionInfo 是 slurmrestd 分区记录的解析结构。Tres 形如 "cpu=8,mem=3000M,node=2,billing=8"
//（内存在这里；GPU 不在分区记录里——GRES 挂在节点上，需按成员节点聚合）。
type PartitionInfo struct {
	Name       string `json:"name"`
	Nodes      string `json:"nodes"`
	TotalCPUs  int    `json:"total_cpus"`
	TotalNodes int    `json:"total_nodes"`
	Tres       string `json:"tres"`
}

type PartitionsResponse struct {
	Errors     []interface{}   `json:"errors"`
	Partitions []PartitionInfo `json:"partitions"`
}

// ParseTresMemMB 从 TRES 串提取内存（MB）。形如 "cpu=8,mem=3000M,node=2"；
// 后缀 K/M/G/T 分别折算；裸数字按 MB；缺失返回 0。
func ParseTresMemMB(tres string) int {
	for _, kv := range strings.Split(tres, ",") {
		kv = strings.TrimSpace(kv)
		if !strings.HasPrefix(kv, "mem=") {
			continue
		}
		v := strings.TrimPrefix(kv, "mem=")
		if v == "" {
			return 0
		}
		mul := 1.0
		switch c := v[len(v)-1]; c {
		case 'K', 'k':
			mul, v = 1.0/1024, v[:len(v)-1]
		case 'M', 'm':
			mul, v = 1, v[:len(v)-1]
		case 'G', 'g':
			mul, v = 1024, v[:len(v)-1]
		case 'T', 't':
			mul, v = 1024*1024, v[:len(v)-1]
		}
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}
		return int(n * mul)
	}
	return 0
}

// ExpandHostlist 展开 Slurm hostlist 表达式为节点名列表。
// 支持常见形态：name / name1,name2 / pfx[1-3] / pfx[1-3,5]（含零填充 pfx[01-03]）；
// 不做全量 hostlist 语义（无 step/多重前缀），本集群命名足够。
func ExpandHostlist(expr string) []string {
	var out []string
	for _, tok := range splitTopComma(expr) {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		i := strings.IndexByte(tok, '[')
		if i < 0 || !strings.HasSuffix(tok, "]") {
			out = append(out, tok)
			continue
		}
		prefix, body := tok[:i], tok[i+1:len(tok)-1]
		for _, part := range strings.Split(body, ",") {
			loHi := strings.SplitN(part, "-", 2)
			lo, err := strconv.Atoi(loHi[0])
			if err != nil {
				out = append(out, prefix+part)
				continue
			}
			hi := lo
			if len(loHi) == 2 {
				if hi, err = strconv.Atoi(loHi[1]); err != nil {
					out = append(out, prefix+part)
					continue
				}
			}
			width := len(loHi[0])
			for n := lo; n <= hi; n++ {
				out = append(out, prefix+fmt.Sprintf("%0*d", width, n))
			}
		}
	}
	return out
}

// splitTopComma 按顶层逗号切分（括号内逗号不切）。
func splitTopComma(s string) []string {
	var parts []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

// GetPartitions 获取集群分区定义与分配信息
func (c *Client) GetPartitions() (*PartitionsResponse, error) {
	body, err := c.executeRequest("GET", "/slurm/v0.0.37/partitions")
	if err != nil {
		return nil, err
	}

	var res PartitionsResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("failed to decode partitions response: %w", err)
	}

	return &res, nil
}

// SlurmJobSubmitReq 定义发送给 SlurmREST v0.0.37 的作业提交体
type SlurmJobSubmitReq struct {
	Script string `json:"script"`
	Job    struct {
		Name                    string            `json:"name,omitempty"`
		Partition               string            `json:"partition,omitempty"`
		MinimumNodes            int               `json:"minimum_nodes,omitempty"`
		Tasks                   int               `json:"tasks,omitempty"`
		CpusPerTask             int               `json:"cpus_per_task,omitempty"`
		CurrentWorkingDirectory string            `json:"current_working_directory,omitempty"`
		TimeLimit               int               `json:"time_limit,omitempty"`
		Environment             map[string]string `json:"environment,omitempty"`
		// MemoryPerNode 显式内存申请（MB；v0.0.37 实测可用。0=缺省 DefMemPerCPU=350/核）。
		MemoryPerNode int `json:"memory_per_node,omitempty"`
		// StandardOutput 输出重定向（%j 由 Slurm 展开为 jobid；门户用 /shared/jobs/%j.out，
		// stdout/stderr 合流——1.2 作业输出管理，实测 v0.0.37 可用）。
		StandardOutput string `json:"standard_output,omitempty"`
		// Account 携带提交者用户名，作为 apiserver 层归属隔离的 owner 载体
		// （集群 AccountingStorageEnforce=none，不校验 account 存在性，可安全复用）。
		Account string `json:"account,omitempty"`
	} `json:"job"`
}

// SlurmJobSubmitResp 定义 SlurmREST v0.0.37 提交作业返回体
type SlurmJobSubmitResp struct {
	Errors []interface{} `json:"errors"`
	JobID  int           `json:"job_id"`
	StepID string        `json:"step_id"`
}

// SlurmGenericResp 通用 SlurmREST 响应
type SlurmGenericResp struct {
	Errors []interface{} `json:"errors"`
}

// SlurmJobControlReq 通用 SlurmREST 作业修改请求体
type SlurmJobControlReq struct {
	Hold     bool   `json:"hold,omitempty"`
	Requeue  bool   `json:"requeue,omitempty"`
	JobState string `json:"job_state,omitempty"`
}

// SubmitJobAs 以指定 clusterUser（actAs）身份提交作业——作业将以 actAs 的真实 unix 身份运行
// （L1 隔离核心）。actAs 为空时退化为 root（兼容旧调用方与测试）。
func (c *Client) SubmitJobAs(req *SlurmJobSubmitReq, actAs string) (*SlurmJobSubmitResp, error) {
	body, err := c.executeRequestAs("POST", "/slurm/v0.0.37/job/submit", req, actAs)
	if err != nil {
		return nil, err
	}

	var res SlurmJobSubmitResp
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("failed to decode job submit response: %w", err)
	}

	return &res, nil
}

// SubmitJob 提交作业至 SlurmREST v0.0.37/job/submit API（root 身份，兼容旧调用方）。
func (c *Client) SubmitJob(req *SlurmJobSubmitReq) (*SlurmJobSubmitResp, error) {
	return c.SubmitJobAs(req, "")
}

// CancelJobAs 以指定 clusterUser（actAs）身份取消作业——Slurm 层强制校验令牌身份与
// 作业属主一致（L4 控制鉴权）。actAs 为空则退化为 root（管理性越权/系统操作）。
func (c *Client) CancelJobAs(jobID int, actAs string) error {
	path := fmt.Sprintf("/slurm/v0.0.37/job/%d", jobID)
	_, err := c.executeRequestAs("DELETE", path, nil, actAs)
	return err
}

// CancelJob 以 root 身份取消作业（兼容旧调用方）。
func (c *Client) CancelJob(jobID int) error {
	return c.CancelJobAs(jobID, "")
}

// HoldJobAs 以指定 clusterUser 身份挂起作业（L4：Slurm 校验属主）。actAs 空=root。
func (c *Client) HoldJobAs(jobID int, actAs string) error {
	path := fmt.Sprintf("/slurm/v0.0.37/job/%d", jobID)
	payload := SlurmJobControlReq{
		Hold:     true,
		JobState: "HELD",
	}
	_, err := c.executeRequestAs("POST", path, payload, actAs)
	return err
}

// HoldJob 以 root 身份挂起作业（兼容旧调用方）。
func (c *Client) HoldJob(jobID int) error {
	return c.HoldJobAs(jobID, "")
}

// RequeueJobAs 以指定 clusterUser 身份重排作业（L4）。actAs 空=root。
func (c *Client) RequeueJobAs(jobID int, actAs string) error {
	path := fmt.Sprintf("/slurm/v0.0.37/job/%d", jobID)
	payload := SlurmJobControlReq{
		Requeue:  true,
		JobState: "PENDING",
	}
	_, err := c.executeRequestAs("POST", path, payload, actAs)
	return err
}

// RequeueJob 以 root 身份重排作业（兼容旧调用方）。
func (c *Client) RequeueJob(jobID int) error {
	return c.RequeueJobAs(jobID, "")
}
