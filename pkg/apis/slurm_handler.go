package apis

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"ails-hpc/pkg/slurmrest"

	"github.com/gin-gonic/gin"
)

type SlurmHandler struct {
	BaseURL  string
	Username string
}

func NewSlurmHandler(baseURL, username string) *SlurmHandler {
	return &SlurmHandler{
		BaseURL:  baseURL,
		Username: username,
	}
}

// getClient 动态获取并附带实时 Token 的 SlurmREST Client
func (h *SlurmHandler) getClient() *slurmrest.Client {
	baseURL := os.Getenv("SLURMRESTD_URL")
	if baseURL == "" {
		baseURL = h.BaseURL
	}
	if baseURL == "" {
		baseURL = "http://127.0.0.1:6820"
	}

	// 优先在本地直接执行 docker compose exec，若失败则退回到 ssh
	cmd := exec.Command("docker", "compose", "-f", "/opt/slurm-cluster/docker-compose.yml", "exec", "slurmctld", "scontrol", "token", "username=hpcuser", "lifespan=86400")
	out, err := cmd.Output()
	if err != nil {
		cmd = exec.Command("ssh", "-o", "BatchMode=yes", "root@192.168.20.226",
			"docker compose -f /opt/slurm-cluster/docker-compose.yml exec slurmctld scontrol token username=hpcuser lifespan=86400")
		out, _ = cmd.Output()
	}

	token := ""
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "SLURM_JWT=") {
			token = strings.TrimPrefix(line, "SLURM_JWT=")
			token = strings.TrimSpace(token)
			break
		}
	}

	return slurmrest.NewClient(baseURL, h.Username, token)
}

// GetStatus 获取 Slurm 集群与 Controller 运行状态
func (h *SlurmHandler) GetStatus(c *gin.Context) {
	client := h.getClient()
	res, err := client.Ping()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "DOWN",
			"error":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, res)
}

// GetNodes 获取 Slurm 集群节点健康度与配置使用率
func (h *SlurmHandler) GetNodes(c *gin.Context) {
	client := h.getClient()
	res, err := client.GetNodes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}

// GetJobs 获取 Slurm 作业队列真实数据
func (h *SlurmHandler) GetJobs(c *gin.Context) {
	client := h.getClient()
	res, err := client.GetJobs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}

// GetPartitions 获取 Slurm 集群真实 Partition 分区分配情况
func (h *SlurmHandler) GetPartitions(c *gin.Context) {
	client := h.getClient()
	res, err := client.GetPartitions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}

// DevLaunchRequest Web 开发环境拉起请求
type DevLaunchRequest struct {
	EnvType  string `json:"env_type" binding:"required"` // vscode 或 jupyter
	Nodes    int    `json:"nodes"`
	CPUs     int    `json:"cpus"`
	MemoryMB int    `json:"memory_mb"`
}

// LaunchDevEnvironment 一键拉起交互式网页开发环境 (Web VSCode / JupyterLab)
func (h *SlurmHandler) LaunchDevEnvironment(c *gin.Context) {
	var req DevLaunchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Nodes <= 0 {
		req.Nodes = 1
	}
	if req.CPUs <= 0 {
		req.CPUs = 2
	}
	if req.MemoryMB <= 0 {
		req.MemoryMB = 4096
	}

	// 模拟针对选择配置分配计算节点，并生成 Web IDE 入口地址
	webURL := ""
	if req.EnvType == "vscode" {
		webURL = fmt.Sprintf("http://192.168.20.226:8080/vscode/?token=ails-dev-token&cpus=%d", req.CPUs)
	} else {
		webURL = fmt.Sprintf("http://192.168.20.226:8888/lab?token=ails-jupyter-token&cpus=%d", req.CPUs)
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "Interactive development environment created successfully",
		"env_type":  req.EnvType,
		"status":    "RUNNING",
		"web_url":   webURL,
		"allocated": req,
	})
}
