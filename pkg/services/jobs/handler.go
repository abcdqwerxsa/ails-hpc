package jobs

import (
	"errors"
	"net/http"
	"strconv"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/httpx"

	"github.com/gin-gonic/gin"
)

type JobHandler struct {
	service JobService
}

func NewJobHandler(service JobService) *JobHandler {
	return &JobHandler{service: service}
}

// callerFromCtx 从 JWT claims 取 (username, role)。
func callerFromCtx(c *gin.Context) (string, string) {
	if v, ok := c.Get("claims"); ok {
		if cl, ok := v.(*auth.Claims); ok {
			return cl.Username, cl.Role
		}
	}
	return "", ""
}

// forbidIfNotOwner 归属隔离：member 只能控制自己的作业（owner==user，或遗留空 owner 放行）；
// tenant_admin 越权放行。已写入响应（403/404）时返回 true，调用方应 return。
func (h *JobHandler) forbidIfNotOwner(c *gin.Context, jobID int) bool {
	user, role := callerFromCtx(c)
	if role != auth.RoleMember {
		return false // 非 member（tenant_admin 等）越权放行
	}
	owner, err := h.service.JobOwner(c.Request.Context(), jobID)
	if err != nil {
		httpx.NotFound(c, "job not found")
		return true
	}
	if owner != "" && owner != user {
		httpx.Error(c, http.StatusForbidden, "forbidden: not the job owner")
		return true
	}
	return false
}

func (h *JobHandler) SubmitJob(c *gin.Context) {
	var req SubmitJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "Invalid job submission request: "+err.Error())
		return
	}

	if req.Script == "" && req.Name == "" {
		httpx.BadRequest(c, "Job script or name is required")
		return
	}

	user, _ := callerFromCtx(c)
	resp, err := h.service.SubmitJob(c.Request.Context(), &req, user)
	if err != nil {
		if errors.Is(err, ErrInvalidResourceLimit) || errors.Is(err, ErrNegativeResources) {
			httpx.BadRequest(c, err.Error())
			return
		}
		httpx.Internal(c, "SubmitJob", err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *JobHandler) ListJobs(c *gin.Context) {
	jobsList, err := h.service.ListJobs(c.Request.Context())
	if err != nil {
		httpx.Internal(c, "ListJobs", err)
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
		httpx.BadRequest(c, "Invalid Job ID")
		return
	}

	if h.forbidIfNotOwner(c, jobID) {
		return
	}

	resp, err := h.service.CancelJob(c.Request.Context(), jobID)
	if err != nil {
		httpx.Internal(c, "CancelJob", err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *JobHandler) HoldJob(c *gin.Context) {
	idStr := c.Param("id")
	jobID, err := strconv.Atoi(idStr)
	if err != nil || jobID <= 0 {
		httpx.BadRequest(c, "Invalid Job ID")
		return
	}

	if h.forbidIfNotOwner(c, jobID) {
		return
	}

	resp, err := h.service.HoldJob(c.Request.Context(), jobID)
	if err != nil {
		if errors.Is(err, ErrCannotHoldCancelled) {
			httpx.BadRequest(c, err.Error())
			return
		}
		httpx.Internal(c, "HoldJob", err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *JobHandler) RequeueJob(c *gin.Context) {
	idStr := c.Param("id")
	jobID, err := strconv.Atoi(idStr)
	if err != nil || jobID <= 0 {
		httpx.BadRequest(c, "Invalid Job ID")
		return
	}

	if h.forbidIfNotOwner(c, jobID) {
		return
	}

	resp, err := h.service.RequeueJob(c.Request.Context(), jobID)
	if err != nil {
		httpx.Internal(c, "RequeueJob", err)
		return
	}

	c.JSON(http.StatusOK, resp)
}
