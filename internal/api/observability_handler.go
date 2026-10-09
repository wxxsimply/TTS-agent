package api

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/tts-agent/internal/config"
	"github.com/tts-agent/internal/middleware"
	"github.com/tts-agent/internal/store"
)

type ObservabilityHandler struct {
	db  *store.Postgres
	cfg *config.Config
}

func NewObservabilityHandler(db *store.Postgres, cfg *config.Config) *ObservabilityHandler {
	return &ObservabilityHandler{db: db, cfg: cfg}
}

func (h *ObservabilityHandler) requireAdmin(c *gin.Context) bool {
	user, _ := c.Get("user")
	account, _ := user.(*store.User)
	if account == nil || !h.cfg.IsAdminEmail(account.Email) {
		c.JSON(http.StatusForbidden, gin.H{"detail": "当前账号没有管理权限"})
		return false
	}
	return true
}

func (h *ObservabilityHandler) Overview(c *gin.Context) {
	if !h.requireAdmin(c) {
		return
	}
	hours, _ := strconv.Atoi(c.DefaultQuery("hours", "24"))
	overview, err := h.db.GetOperationsOverview(c.Request.Context(), hours)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "指标查询失败"})
		return
	}
	failures, err := h.db.ListRecentRunFailures(c.Request.Context(), 20)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "失败记录查询失败"})
		return
	}
	feedback, err := h.db.ListAgentFeedback(c.Request.Context(), 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "反馈查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"overview": overview, "recent_failures": failures, "feedback": feedback})
}

type feedbackRequest struct {
	MessageID    string `json:"message_id"`
	FeedbackType string `json:"feedback_type" binding:"required"`
	Comment      string `json:"comment"`
}

func (h *ObservabilityHandler) Feedback(c *gin.Context) {
	runID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "运行编号无效"})
		return
	}
	var request feedbackRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "反馈内容无效"})
		return
	}
	allowed := map[string]bool{"helpful": true, "not_helpful": true, "data_error": true, "sql_error": true, "chart_error": true}
	if !allowed[request.FeedbackType] || len(strings.TrimSpace(request.Comment)) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "反馈类型或备注无效"})
		return
	}
	var messageID *uuid.UUID
	if strings.TrimSpace(request.MessageID) != "" {
		parsed, parseErr := uuid.Parse(request.MessageID)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "消息编号无效"})
			return
		}
		messageID = &parsed
	}
	feedback := &store.AgentFeedback{RunID: runID, UserID: middleware.GetUserID(c), MessageID: messageID, FeedbackType: request.FeedbackType, Comment: strings.TrimSpace(request.Comment)}
	if err := h.db.UpsertAgentFeedback(c.Request.Context(), feedback); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "运行不存在或无权访问"})
		return
	}
	c.JSON(http.StatusOK, feedback)
}

func (h *ObservabilityHandler) PromoteFeedback(c *gin.Context) {
	if !h.requireAdmin(c) {
		return
	}
	feedbackID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "反馈编号无效"})
		return
	}
	evalID, err := h.db.PromoteFeedbackToEvaluation(c.Request.Context(), feedbackID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "反馈不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"evaluation_case_id": evalID, "status": "candidate"})
}

func (h *ObservabilityHandler) Prometheus(c *gin.Context) {
	overview, err := h.db.GetOperationsOverview(c.Request.Context(), 24)
	if err != nil {
		log.Printf("prometheus metrics query failed: %v", err)
		c.String(http.StatusServiceUnavailable, "# metrics unavailable\n")
		return
	}
	lines := []string{
		"# HELP tts_agent_runs_total Terminal Agent runs in the last 24 hours.",
		"# TYPE tts_agent_runs_total gauge",
		fmt.Sprintf("tts_agent_runs_total %d", overview.TotalRuns),
		fmt.Sprintf("tts_agent_runs_completed %d", overview.CompletedRuns),
		fmt.Sprintf("tts_agent_runs_failed %d", overview.FailedRuns),
		fmt.Sprintf("tts_agent_success_rate %g", overview.SuccessRate),
		fmt.Sprintf("tts_agent_run_duration_p95_milliseconds %g", overview.P95DurationMS),
		fmt.Sprintf("tts_agent_queue_wait_average_milliseconds %g", overview.AverageQueueMS),
		fmt.Sprintf("tts_agent_llm_input_tokens_total %d", overview.InputTokens),
		fmt.Sprintf("tts_agent_llm_output_tokens_total %d", overview.OutputTokens),
		fmt.Sprintf("tts_agent_llm_calls_total %d", overview.LLMCalls),
		fmt.Sprintf("tts_agent_llm_estimated_cost_yuan %g", overview.EstimatedCostYuan),
		fmt.Sprintf("tts_agent_jobs{status=\"queued\"} %d", overview.QueuedJobs),
		fmt.Sprintf("tts_agent_jobs{status=\"running\"} %d", overview.RunningJobs),
		fmt.Sprintf("tts_agent_jobs{status=\"retry_wait\"} %d", overview.RetryWaitJobs),
	}
	c.Data(http.StatusOK, "text/plain; version=0.0.4; charset=utf-8", []byte(strings.Join(lines, "\n")+"\n"))
}
