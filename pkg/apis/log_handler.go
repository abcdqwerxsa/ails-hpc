package apis

import (
	"bufio"
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

type LogHandler struct {
	KubeClient kubernetes.Interface
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (h *LogHandler) StreamPodLogs(c *gin.Context) {
	podName := c.Query("podName")
	namespace := c.DefaultQuery("namespace", "default")
	container := c.Query("container")

	if podName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "podName parameter is required"})
		return
	}

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	req := h.KubeClient.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: container,
		Follow:    true,
	})

	stream, err := req.Stream(context.Background())
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("Failed to open log stream: "+err.Error()))
		return
	}
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		text := scanner.Text()
		if err := ws.WriteMessage(websocket.TextMessage, []byte(text)); err != nil {
			break
		}
	}
}
