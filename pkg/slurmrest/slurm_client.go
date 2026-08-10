package slurmrest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client 封装了与原生 slurmrestd REST API 交互的客户端
type Client struct {
	BaseURL    string
	UserName   string
	UserToken  string
	HTTPClient *http.Client
}

// NewClient 创建并初始化一个新的 Slurm REST API Client
func NewClient(baseURL, userName, userToken string) *Client {
	return &Client{
		BaseURL:   baseURL,
		UserName:  userName,
		UserToken: userToken,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// executeRequest 执行 HTTP 请求并附带标准的 Slurm JWT 身份凭证 Header
func (c *Client) executeRequest(method, path string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s%s", c.BaseURL, path)
	req, err := http.NewRequest(method, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-SLURM-USER-NAME", c.UserName)
	req.Header.Set("X-SLURM-USER-TOKEN", c.UserToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, fmt.Errorf("slurmrestd returned status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
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
		Name       string `json:"name"`
		State      string `json:"state"`
		CPUs       int    `json:"cpus"`
		RealMemory int    `json:"real_memory"`
		Cores      int    `json:"cores"`
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
		Name  string `json:"name"`
		Nodes string `json:"nodes"`
		TotalCPUs int `json:"total_cpus"`
		TotalNodes int `json:"total_nodes"`
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
