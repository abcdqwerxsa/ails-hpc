package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/gorilla/websocket"
)

var defaultAPIBase = "http://192.168.20.226:8090"

type Config struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	OrgSlug  string `json:"orgSlug"`
}

func getHomeConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".hpc.json")
}

func saveConfig(cfg *Config) {
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(getHomeConfigPath(), data, 0600)
}

func loadConfig() *Config {
	data, err := os.ReadFile(getHomeConfigPath())
	if err != nil {
		return &Config{}
	}
	var cfg Config
	json.Unmarshal(data, &cfg)
	return &cfg
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subCmd := os.Args[1]
	switch subCmd {
	case "login":
		handleLogin(os.Args[2:])
	case "submit":
		handleSubmit(os.Args[2:])
	case "status":
		handleStatus(os.Args[2:])
	case "logs":
		handleLogs(os.Args[2:])
	default:
		fmt.Printf("Unknown subcommand: %s\n", subCmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("AILS Cloud-Native HPC CLI (hpc)")
	fmt.Println("\nUsage:")
	fmt.Println("  hpc login --username <user> --password <pass> [--org <org-slug>]")
	fmt.Println("  hpc submit --name <name> [--type mpi|batch] --image <image> --slots <n> --storage <size> --command \"<cmd>\"")
	fmt.Println("  hpc status [--namespace <ns>]")
	fmt.Println("  hpc logs --name <job-name>")
}

func handleLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	username := fs.String("username", "admin", "Username or SSO ID")
	password := fs.String("password", "admin123", "Password")
	org := fs.String("org", "hpc-lab", "Tenant organization slug")
	fs.Parse(args)

	payload := map[string]string{
		"username": *username,
		"password": *password,
		"orgSlug":  *org,
	}

	body, _ := json.Marshal(payload)
	resp, err := http.Post(defaultAPIBase+"/api/v1/auth/login", "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Printf("Failed to connect to SSO Auth Server: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK {
		var res struct {
			Token string `json:"token"`
			User  struct {
				Username string `json:"username"`
				Role     string `json:"role"`
				TenantNs string `json:"tenantNs"`
			} `json:"user"`
		}
		json.Unmarshal(respBody, &res)

		saveConfig(&Config{
			Token:    res.Token,
			Username: res.User.Username,
			OrgSlug:  *org,
		})

		fmt.Printf("🔐 SSO Authentication Successful!\n")
		fmt.Printf("User: %s | Role: %s | Tenant Namespace: %s\n", res.User.Username, res.User.Role, res.User.TenantNs)
		fmt.Printf("JWT Token saved to %s\n", getHomeConfigPath())
	} else {
		fmt.Printf("❌ Authentication Failed: %s\n", string(respBody))
	}
}

func handleSubmit(args []string) {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	name := fs.String("name", "", "Job name")
	jobType := fs.String("type", "mpi", "Job compute type: mpi or batch")
	image := fs.String("image", "quay.io/nilpo1/mpich-ubuntu:v0.8.2", "Container image")
	slots := fs.Int("slots", 4, "Number of parallel slots")
	storage := fs.String("storage", "", "Local-Path PVC Storage Size e.g. 5Gi")
	cmdStr := fs.String("command", "mpirun -np 4 /opt/mpich-3.3.2/examples/cpi", "Command to run")
	namespace := fs.String("namespace", "default", "Target K8s namespace")
	fs.Parse(args)

	if *name == "" {
		fmt.Println("Error: --name is required")
		os.Exit(1)
	}

	cfg := loadConfig()

	payload := map[string]interface{}{
		"name":        *name,
		"namespace":   *namespace,
		"jobType":     *jobType,
		"image":       *image,
		"slots":       *slots,
		"storageSize": *storage,
		"command":     strings.Split(*cmdStr, " "),
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", defaultAPIBase+"/api/v1/hpcjobs", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("Failed to connect to HPC API Server: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusCreated {
		fmt.Printf("🚀 HpcJob '%s' (%s) successfully submitted!\n", *name, *jobType)
		if *storage != "" {
			fmt.Printf("📦 Bound PVC Storage: %s (/workspace)\n", *storage)
		}
	} else {
		fmt.Printf("❌ Failed to submit job: %s\n", string(respBody))
	}
}

func handleStatus(args []string) {
	cfg := loadConfig()
	req, _ := http.NewRequest("GET", defaultAPIBase+"/api/v1/hpcjobs", nil)
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("Failed to connect to HPC API Server: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var data struct {
		Jobs []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				JobType     string `json:"jobType"`
				Image       string `json:"image"`
				Slots       int    `json:"slots"`
				Queue       string `json:"queue"`
				StorageSize string `json:"storageSize"`
			} `json:"spec"`
			Status struct {
				Phase             string  `json:"phase"`
				CoreHours         float64 `json:"coreHours"`
				ExecutionDuration string  `json:"executionDuration"`
			} `json:"status"`
		} `json:"jobs"`
	}

	json.Unmarshal(body, &data)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "JOB NAME\tTYPE\tNAMESPACE\tSLOTS\tSTORAGE\tPHASE\tCORE-HOURS\tDURATION")
	for _, j := range data.Jobs {
		phase := j.Status.Phase
		if phase == "" {
			phase = "Pending"
		}
		jobType := j.Spec.JobType
		if jobType == "" {
			jobType = "mpi"
		}
		storage := j.Spec.StorageSize
		if storage == "" {
			storage = "None"
		}
		duration := j.Status.ExecutionDuration
		if duration == "" {
			duration = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%.4f\t%s\n", j.Metadata.Name, jobType, j.Metadata.Namespace, j.Spec.Slots, storage, phase, j.Status.CoreHours, duration)
	}
	w.Flush()
}

func handleLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	name := fs.String("name", "", "Job name")
	namespace := fs.String("namespace", "default", "Namespace")
	fs.Parse(args)

	if *name == "" {
		fmt.Println("Error: --name is required")
		os.Exit(1)
	}

	cfg := loadConfig()
	podName := fmt.Sprintf("%s-launcher", *name)
	wsURL := fmt.Sprintf("ws://192.168.20.226:8090/ws/logs?podName=%s&namespace=%s&token=%s", podName, *namespace, cfg.Token)

	fmt.Printf("📡 Streaming logs for Pod '%s'...\n\n", podName)
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		fmt.Printf("Failed to connect to log stream: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()

	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			break
		}
		fmt.Println(string(message))
	}
}
