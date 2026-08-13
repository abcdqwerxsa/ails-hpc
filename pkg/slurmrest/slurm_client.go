package slurmrest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
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

// FetchToken 动态获取 Slurm JWT 身份令牌（特权用户 root，24h 有效）。
func FetchToken() string {
	out, err := RunInSlurmctld("scontrol", "token", "username=root", "lifespan=86400")
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


// Client 封装了与原生 slurmrestd REST API 交互的客户端
type Client struct {
	BaseURL    string
	UserName   string
	UserToken  string
	HTTPClient *http.Client
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

	resp, body, err := makeReq(c.UserToken)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		newToken := FetchToken()
		if newToken != "" && newToken != c.UserToken {
			c.UserToken = newToken
			resp, body, err = makeReq(c.UserToken)
			if err != nil {
				return nil, err
			}
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, fmt.Errorf("slurmrestd returned status %d: %s", resp.StatusCode, string(body))
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
type PartitionsResponse struct {
	Errors []interface{} `json:"errors"`
	Partitions []struct {
		Name       string `json:"name"`
		Nodes      string `json:"nodes"`
		TotalCPUs  int    `json:"total_cpus"`
		TotalNodes int    `json:"total_nodes"`
	} `json:"partitions"`
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

// SubmitJob 提交作业至 SlurmREST v0.0.37/job/submit API
func (c *Client) SubmitJob(req *SlurmJobSubmitReq) (*SlurmJobSubmitResp, error) {
	body, err := c.executeRequestWithBody("POST", "/slurm/v0.0.37/job/submit", req)
	if err != nil {
		return nil, err
	}

	var res SlurmJobSubmitResp
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("failed to decode job submit response: %w", err)
	}

	return &res, nil
}

// CancelJob 通过 DELETE /slurm/v0.0.37/job/{job_id} 取消指定作业
func (c *Client) CancelJob(jobID int) error {
	path := fmt.Sprintf("/slurm/v0.0.37/job/%d", jobID)
	_, err := c.executeRequestWithBody("DELETE", path, nil)
	return err
}

// HoldJob 通过 POST /slurm/v0.0.37/job/{job_id} 暂停指定作业
func (c *Client) HoldJob(jobID int) error {
	path := fmt.Sprintf("/slurm/v0.0.37/job/%d", jobID)
	payload := SlurmJobControlReq{
		Hold:     true,
		JobState: "HELD",
	}
	_, err := c.executeRequestWithBody("POST", path, payload)
	return err
}

// RequeueJob 通过 POST /slurm/v0.0.37/job/{job_id} 重新入队指定作业
func (c *Client) RequeueJob(jobID int) error {
	path := fmt.Sprintf("/slurm/v0.0.37/job/%d", jobID)
	payload := SlurmJobControlReq{
		Requeue:  true,
		JobState: "PENDING",
	}
	_, err := c.executeRequestWithBody("POST", path, payload)
	return err
}
