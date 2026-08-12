package billing

import (
	"net/http"
	"strconv"

	"ails-hpc/pkg/httpx"

	"github.com/gin-gonic/gin"
)

type BillingHandler struct {
	service BillingService
}

func NewBillingHandler(service BillingService) *BillingHandler {
	return &BillingHandler{service: service}
}

func (h *BillingHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/billing/usage", h.GetUsage)
	rg.GET("/billing/export", h.ExportReport)
}

func (h *BillingHandler) GetUsage(c *gin.Context) {
	format := c.Query("format")
	if format != "" && format != "json" && format != "chart" {
		httpx.BadRequest(c, "Invalid format query parameter")
		return
	}

	startTime := c.Query("start_time")
	endTime := c.Query("end_time")
	if startTime != "" && endTime != "" && startTime > endTime {
		httpx.BadRequest(c, "start_time cannot be greater than end_time")
		return
	}

	limitStr := c.Query("limit")
	limit := 0
	if limitStr != "" {
		var err error
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit < 0 {
			httpx.BadRequest(c, "limit must be non-negative")
			return
		}
	}

	param := UsageQueryParam{
		User:      c.Query("user"),
		Project:   c.Query("project"),
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     limit,
		Format:    format,
	}

	resp, err := h.service.GetUsage(c.Request.Context(), param)
	if err != nil {
		httpx.Internal(c, "GetUsage", err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *BillingHandler) ExportReport(c *gin.Context) {
	format := c.DefaultQuery("format", "json")
	if format != "json" && format != "chart" {
		httpx.BadRequest(c, "Invalid export format. Supported: json, chart")
		return
	}

	param := ExportQueryParam{
		Format:  format,
		User:    c.Query("user"),
		Project: c.Query("project"),
	}

	report, err := h.service.ExportReport(c.Request.Context(), param)
	if err != nil {
		httpx.Internal(c, "ExportReport", err)
		return
	}

	c.JSON(http.StatusOK, report)
}
