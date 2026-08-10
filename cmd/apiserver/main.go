package main

import (
	"flag"
	"log"
	"net/http"

	"ails-hpc/pkg/apis"
	"ails-hpc/pkg/auth"

	"github.com/gin-gonic/gin"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	kubeconfig := flag.String("kubeconfig", "/etc/kubernetes/admin.conf", "Path to kubeconfig file")
	port := flag.String("port", "8090", "Port for API server")
	flag.Parse()

	var dynamicClient dynamic.Interface
	var kubeClient kubernetes.Interface

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err == nil {
		dynamicClient, _ = dynamic.NewForConfig(config)
		kubeClient, _ = kubernetes.NewForConfig(config)
	} else {
		log.Printf("Warning: Could not load kubeconfig (%v). Running Slurm REST Portal standalone mode.", err)
	}

	r := gin.Default()

	// Enable CORS for Web Portal
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(24)
			return
		}
		c.Next()
	})

	jobHandler := &apis.JobHandler{DynamicClient: dynamicClient}
	queueHandler := &apis.QueueHandler{DynamicClient: dynamicClient}
	logHandler := &apis.LogHandler{KubeClient: kubeClient}
	authHandler := &apis.AuthHandler{}
	slurmHandler := apis.NewSlurmHandler("http://192.168.20.226:6820", "hpcuser")

	// Public Auth & Slurm Monitoring Endpoints
	r.POST("/api/v1/auth/login", authHandler.Login)
	r.POST("/api/v1/auth/sso", authHandler.Login)

	// Direct Slurm Portal REST APIs
	slurmGroup := r.Group("/api/v1/slurm")
	{
		slurmGroup.GET("/ping", slurmHandler.GetStatus)
		slurmGroup.GET("/nodes", slurmHandler.GetNodes)
		slurmGroup.GET("/jobs", slurmHandler.GetJobs)
		slurmGroup.GET("/partitions", slurmHandler.GetPartitions)
		slurmGroup.POST("/launch", slurmHandler.LaunchDevEnvironment)
	}

	// Serve Neumorphic Web Dashboard Static Portal
	r.Static("/portal", "./apps/web")
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/portal/")
	})

	// Protected API Routes with JWT & RBAC Middleware
	apiGroup := r.Group("/api/v1")
	apiGroup.Use(auth.JWTAuthMiddleware())
	{
		apiGroup.GET("/hpcjobs", jobHandler.ListJobs)
		apiGroup.POST("/hpcjobs", auth.RBACRequireRole("admin", "member"), jobHandler.CreateJob)
		apiGroup.DELETE("/hpcjobs/:name", auth.RBACRequireRole("admin", "member"), jobHandler.DeleteJob)
		apiGroup.GET("/queues", queueHandler.GetQueueStatus)
	}

	r.GET("/ws/logs", logHandler.StreamPodLogs)

	r.Run(":" + *port)
}
