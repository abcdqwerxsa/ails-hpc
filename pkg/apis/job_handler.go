package apis

import (
	"context"
	"fmt"
	"net/http"

	"ails-hpc/pkg/auth"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type CreateJobRequest struct {
	Name              string   `json:"name" binding:"required"`
	Namespace         string   `json:"namespace"`
	JobType           string   `json:"jobType"`
	Image             string   `json:"image" binding:"required"`
	Slots             int32    `json:"slots" binding:"required"`
	Command           []string `json:"command"`
	Queue             string   `json:"queue"`
	TenantNamespace   string   `json:"tenantNamespace"`
	StorageSize       string   `json:"storageSize"`
	PriorityClassName string   `json:"priorityClassName"`
}

type JobHandler struct {
	DynamicClient dynamic.Interface
}

var hpcJobGVR = schema.GroupVersionResource{
	Group:    "ails.hpc",
	Version:  "v1alpha1",
	Resource: "hpcjobs",
}

func (h *JobHandler) CreateJob(c *gin.Context) {
	var req CreateJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 1. RBAC & Tenant Namespace Isolation
	var claims *auth.Claims
	if val, exists := c.Get("claims"); exists {
		claims = val.(*auth.Claims)
	}

	namespace := req.Namespace
	if claims != nil && claims.Role != "admin" {
		// Non-admin users are strictly bounded to their assigned tenant namespace
		namespace = claims.TenantNS
		req.TenantNamespace = claims.TenantNS
	}

	if namespace == "" {
		namespace = "default"
	}

	queue := req.Queue
	if queue == "" {
		queue = "user-queue"
	}

	jobType := req.JobType
	if jobType == "" {
		jobType = "mpi"
	}

	fmt.Printf("[APIServer] Creating job '%s' with JobType='%s' in Namespace='%s'\n", req.Name, jobType, namespace)

	specMap := map[string]interface{}{
		"jobType":         jobType,
		"image":           req.Image,
		"slots":           req.Slots,
		"command":         req.Command,
		"queue":           queue,
		"tenantNamespace": req.TenantNamespace,
	}

	if req.StorageSize != "" {
		specMap["storageSize"] = req.StorageSize
	}
	if req.PriorityClassName != "" {
		specMap["priorityClassName"] = req.PriorityClassName
	}

	hpcJob := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "ails.hpc/v1alpha1",
			"kind":       "HpcJob",
			"metadata": map[string]interface{}{
				"name":      req.Name,
				"namespace": namespace,
			},
			"spec": specMap,
		},
	}

	res, err := h.DynamicClient.Resource(hpcJobGVR).Namespace(namespace).Create(context.Background(), hpcJob, metav1.CreateOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "HpcJob created successfully",
		"job":     res.Object,
	})
}

func (h *JobHandler) ListJobs(c *gin.Context) {
	var claims *auth.Claims
	if val, exists := c.Get("claims"); exists {
		claims = val.(*auth.Claims)
	}

	namespace := c.Query("namespace")
	if claims != nil && claims.Role != "admin" {
		// Strictly enforce tenant isolation
		namespace = claims.TenantNS
	}

	var list *unstructured.UnstructuredList
	var err error

	if namespace != "" {
		list, err = h.DynamicClient.Resource(hpcJobGVR).Namespace(namespace).List(context.Background(), metav1.ListOptions{})
	} else {
		list, err = h.DynamicClient.Resource(hpcJobGVR).List(context.Background(), metav1.ListOptions{})
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var jobs []map[string]interface{}
	for _, item := range list.Items {
		jobs = append(jobs, item.Object)
	}

	c.JSON(http.StatusOK, gin.H{
		"jobs": jobs,
	})
}

func (h *JobHandler) DeleteJob(c *gin.Context) {
	name := c.Param("name")
	var claims *auth.Claims
	if val, exists := c.Get("claims"); exists {
		claims = val.(*auth.Claims)
	}

	namespace := c.DefaultQuery("namespace", "default")
	if claims != nil && claims.Role != "admin" {
		namespace = claims.TenantNS
	}

	err := h.DynamicClient.Resource(hpcJobGVR).Namespace(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "HpcJob deleted successfully", "name": name})
}
