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
	// tenants 租户成员解析（Phase 4 租户隔离）；nil = 不收紧（仅测试/旧装配）。
	tenants auth.TenantResolver
}

func NewJobHandler(service JobService) *JobHandler {
	return &JobHandler{service: service}
}

// NewJobHandlerScoped 注入租户成员解析（Phase 4：member 只见/只控自己的作业，
// tenant_admin 限本租户，ops/admin 全量）。
func NewJobHandlerScoped(service JobService, tenants auth.TenantResolver) *JobHandler {
	return &JobHandler{service: service, tenants: tenants}
}

// callerFromCtx 从 JWT claims 取 (username, role, clusterUser, account)。
// clusterUser/account 为真·每用户 Slurm 隔离所需：作业以 clusterUser 真实身份提交、account 写入 Slurm。
func callerFromCtx(c *gin.Context) (username, role, clusterUser, account string) {
	if v, ok := c.Get("claims"); ok {
		if cl, ok := v.(*auth.Claims); ok {
			return cl.Username, cl.Role, cl.ClusterUser, cl.Account
		}
	}
	return "", "", "", ""
}

// forbidIfNotOwner 归属隔离：member 只能控制自己的作业（owner==clusterUser，或遗留空 owner 放行）；
// tenant_admin 越权放行。已写入响应（403/404）时返回 true，调用方应 return。
func (h *JobHandler) forbidIfNotOwner(c *gin.Context, jobID int) bool {
	_, role, clusterUser, _ := callerFromCtx(c)
	owner, err := h.service.JobOwner(c.Request.Context(), jobID)
	if err != nil {
		// 作业不存在 → 404；其余（slurmrestd 不可达等）如实 500，不把后端故障伪装成"没有此作业"
		if errors.Is(err, ErrJobNotFound) {
			httpx.NotFound(c, "job not found")
		} else {
			httpx.Internal(c, "JobOwner", err)
		}
		return true
	}
	// Phase 4：member 只控自己的；tenant_admin 限本租户（此前为全局通配，按设计 §6 收紧）；
	// 空属主=遗留作业，全员放行。ops/admin 不经本路由（角色矩阵无控制权）。
	allow, err := auth.ScopeFromClaims(auth.ClaimsFromCtx(c)).RowFilter(h.tenants)
	if err != nil {
		httpx.Internal(c, "forbidIfNotOwner.scope", err)
		return true
	}
	if !allow(owner) {
		httpx.Error(c, http.StatusForbidden, "forbidden: not the job owner")
		return true
	}
	_ = role
	_ = clusterUser
	return false
}

// controlActAs 决定控制操作的下发身份（L4 控制鉴权）：member 用自己的 clusterUser
// （Slurm 层强制令牌身份==作业属主，apiserver 校验之外的第二道门）；tenant_admin
// 越权与管理性操作走 root（actAs=""）。
func controlActAs(c *gin.Context) string {
	_, role, clusterUser, _ := callerFromCtx(c)
	if role == auth.RoleMember {
		return clusterUser
	}
	return ""
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

	_, _, clusterUser, account := callerFromCtx(c)
	resp, err := h.service.SubmitJob(c.Request.Context(), &req, clusterUser, account)
	if err != nil {
		if errors.Is(err, ErrInvalidResourceLimit) || errors.Is(err, ErrNegativeResources) || errors.Is(err, ErrGPUPartition) || errors.Is(err, ErrInvalidSpec) ||
			errors.Is(err, ErrInvalidJobName) || errors.Is(err, ErrInvalidPartition) || errors.Is(err, ErrInvalidQOS) {
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

	// Phase 4 租户隔离：member 只见自己的作业；tenant_admin 见本租户；ops/admin 全量。
	// owner 为空的行（遗留/squeue 兜底）对所有人可见（迁移期兼容）。
	allow, err := auth.ScopeFromClaims(auth.ClaimsFromCtx(c)).RowFilter(h.tenants)
	if err != nil {
		httpx.Internal(c, "ListJobs.scope", err)
		return
	}
	scoped := make([]JobSummary, 0, len(jobsList))
	for _, j := range jobsList {
		if allow(j.Owner) {
			scoped = append(scoped, j)
		}
	}

	c.JSON(http.StatusOK, JobListResponse{
		Code: 200,
		Jobs: scoped,
	})
}

// GetJobDetail GET /api/v1/slurm/jobs/:id/detail —— sacct 生命期数据 + 输出尾部。
// 租户隔离同列表：member 仅本人，tenant_admin 本租户（owner 取自 sacct User），ops/admin 全量。
func (h *JobHandler) GetJobDetail(c *gin.Context) {
	jobID, err := strconv.Atoi(c.Param("id"))
	if err != nil || jobID <= 0 {
		httpx.BadRequest(c, "Invalid Job ID")
		return
	}
	d, err := h.service.JobDetail(c.Request.Context(), jobID)
	if err != nil {
		if errors.Is(err, ErrJobNotFound) {
			httpx.NotFound(c, "job not found")
		} else {
			httpx.Internal(c, "JobDetail", err)
		}
		return
	}
	allow, err := auth.ScopeFromClaims(auth.ClaimsFromCtx(c)).RowFilter(h.tenants)
	if err != nil {
		httpx.Internal(c, "GetJobDetail.scope", err)
		return
	}
	if !allow(d.Owner) {
		// 他人作业：404（与跨租户用户管理一致，防枚举）
		httpx.NotFound(c, "job not found")
		return
	}
	c.JSON(http.StatusOK, d)
}

// ListHistory GET /api/v1/slurm/jobs/history?user=&state=&limit=（1.3 历史页）。
// 租户隔离与列表同源：member 仅本人（?user= 被强制为本人），tenant_admin 本租户，
// ops/admin 全量（可 ?user= 过滤）。
func (h *JobHandler) ListHistory(c *gin.Context) {
	sc := auth.ScopeFromClaims(auth.ClaimsFromCtx(c))
	q := HistoryQuery{
		User:  c.Query("user"),
		State: c.Query("state"),
	}
	// member：无视 ?user=，强制本人
	if sc.Mode == auth.ScopeSelf {
		q.User = sc.ClusterUser
	} else if sc.Mode == auth.ScopeTenant && h.tenants != nil && q.User != "" {
		// tenant_admin 指定 ?user= 时必须属本租户
		members, err := h.tenants(sc.TenantSlug)
		if err != nil {
			httpx.Internal(c, "ListHistory.tenants", err)
			return
		}
		ok := false
		for _, m := range members {
			if m == q.User {
				ok = true
				break
			}
		}
		if !ok {
			httpx.Error(c, http.StatusForbidden, "forbidden: user is outside your tenant")
			return
		}
	}
	entries, err := h.service.History(c.Request.Context(), q)
	if err != nil {
		httpx.Internal(c, "History", err)
		return
	}
	// 统一后过滤（与列表同谓词）：member=本人；tenant_admin 缺省=本租户成员；
	// ops/admin=全量。service 层的 User 过滤是 sacct 侧优化,信任边界在这里。
	allow, err := sc.RowFilter(h.tenants)
	if err != nil {
		httpx.Internal(c, "ListHistory.scope", err)
		return
	}
	scoped := make([]HistoryEntry, 0, len(entries))
	for _, e := range entries {
		if allow(e.Owner) {
			scoped = append(scoped, e)
		}
	}
	c.JSON(http.StatusOK, gin.H{"history": scoped})
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

	resp, err := h.service.CancelJob(c.Request.Context(), jobID, controlActAs(c))
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

	resp, err := h.service.HoldJob(c.Request.Context(), jobID, controlActAs(c))
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

	resp, err := h.service.RequeueJob(c.Request.Context(), jobID, controlActAs(c))
	if err != nil {
		httpx.Internal(c, "RequeueJob", err)
		return
	}

	c.JSON(http.StatusOK, resp)
}
