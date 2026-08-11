package jobs

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type JobHandler struct {
	service JobService
}

func NewJobHandler(service JobService) *JobHandler {
	return &JobHandler{service: service}
}

func (h *JobHandler) SubmitJob(c *gin.Context) {
	var req SubmitJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  400,
			"error": "Invalid job submission request: " + err.Error(),
		})
		return
	}

	if req.Script == "" && req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  400,
			"error": "Job script or name is required",
		})
		return
	}

	resp, err := h.service.SubmitJob(c.Request.Context(), &req)
	if err != nil {
		if errors.Is(err, ErrInvalidResourceLimit) || errors.Is(err, ErrNegativeResources) {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":  400,
				"error": err.Error(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  500,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *JobHandler) ListJobs(c *gin.Context) {
	jobsList, err := h.service.ListJobs(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  500,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, JobListResponse{
		Code: 200,
		Jobs: jobsList,
	})
}

func (h *JobHandler) CancelJob(c *gin.Context) {
	idStr := c.Param("id")
	jobID, err := strconv.Atoi(idStr)
	if err != nil || jobID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  400,
			"error": "Invalid Job ID",
		})
		return
	}

	resp, err := h.service.CancelJob(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  500,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *JobHandler) HoldJob(c *gin.Context) {
	idStr := c.Param("id")
	jobID, err := strconv.Atoi(idStr)
	if err != nil || jobID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  400,
			"error": "Invalid Job ID",
		})
		return
	}

	resp, err := h.service.HoldJob(c.Request.Context(), jobID)
	if err != nil {
		if errors.Is(err, ErrCannotHoldCancelled) {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":  400,
				"error": err.Error(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  500,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *JobHandler) RequeueJob(c *gin.Context) {
	idStr := c.Param("id")
	jobID, err := strconv.Atoi(idStr)
	if err != nil || jobID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  400,
			"error": "Invalid Job ID",
		})
		return
	}

	resp, err := h.service.RequeueJob(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  500,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}
