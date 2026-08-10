package apis

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type QueueHandler struct {
	DynamicClient dynamic.Interface
}

var clusterQueueGVR = schema.GroupVersionResource{
	Group:    "kueue.x-k8s.io",
	Version:  "v1beta1",
	Resource: "clusterqueues",
}

var localQueueGVR = schema.GroupVersionResource{
	Group:    "kueue.x-k8s.io",
	Version:  "v1beta1",
	Resource: "localqueues",
}

func (h *QueueHandler) GetQueueStatus(c *gin.Context) {
	clusterQueues, err := h.DynamicClient.Resource(clusterQueueGVR).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list cluster queues: " + err.Error()})
		return
	}

	localQueues, err := h.DynamicClient.Resource(localQueueGVR).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list local queues: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"clusterQueues": clusterQueues.Items,
		"localQueues":   localQueues.Items,
	})
}
