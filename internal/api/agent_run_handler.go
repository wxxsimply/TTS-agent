package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/tts-agent/internal/agent"
	"github.com/tts-agent/internal/config"
	"github.com/tts-agent/internal/middleware"
	"github.com/tts-agent/internal/store"
)

type AgentRunHandler struct {
	store    *agent.PostgresRunRecorder
	jobs     *agent.PostgresJobStore
	pipeline *agent.Pipeline
	db       *store.Postgres
	cfg      *config.Config
}

func NewAgentRunHandler(db *store.Postgres, pipeline *agent.Pipeline, cfg *config.Config) *AgentRunHandler {
	return &AgentRunHandler{
		store: agent.NewPostgresRunRecorder(db), jobs: agent.NewPostgresJobStore(db),
		pipeline: pipeline, db: db, cfg: cfg,
	}
}

type asyncRunRequest struct {
	Query          string `json:"query" binding:"required"`
	Domain         string `json:"domain"`
	DatasetID      string `json:"dataset_id"`
	ConversationID string `json:"conversation_id"`
	Priority       int    `json:"priority"`
}

func (h *AgentRunHandler) Submit(c *gin.Context) {
	var request asyncRunRequest
	if err := c.ShouldBindJSON(&request); err != nil || strings.TrimSpace(request.Query) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请输入要分析的业务问题"})
		return
	}
	if len([]rune(request.Query)) > 4000 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "问题内容不能超过 4000 个字符"})
		return
	}
	if request.Priority < -1 {
		request.Priority = -1
	}
	if request.Priority > 1 {
		request.Priority = 1
	}
	userID := middleware.GetUserID(c)
	domainName := strings.TrimSpace(request.Domain)
	if domainName == "" {
		domainName = h.cfg.DefaultDomain
	}
	conversationID, datasetID, ok := h.authorizeRunInput(c, userID, request.ConversationID, request.DatasetID, &domainName)
	if !ok {
		return
	}
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if len(idempotencyKey) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "Idempotency-Key 不能超过 128 个字符"})
		return
	}
	state := &agent.State{
		RunID: uuid.New(), GraphVersion: agent.CurrentGraphVersion,
		UserID: userID, ConversationID: conversationID, DatasetID: datasetID,
		Domain: domainName, Query: strings.TrimSpace(request.Query),
		Errors: []string{}, SeenSQL: map[string]struct{}{},
	}
	queued, err := h.jobs.Enqueue(c.Request.Context(), state, agent.EnqueueOptions{
		IdempotencyKey: idempotencyKey, Priority: request.Priority,
		MaxAttempts: h.cfg.JobMaxAttempts, QueueLimit: h.cfg.JobQueueLimit,
		UserLimit: h.cfg.UserActiveJobLimit,
	})
	if err != nil {
		writeAgentJobError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, queued)
}

func (h *AgentRunHandler) Get(c *gin.Context) {
	runID, ok := parseRunID(c)
	if !ok {
		return
	}
	record, err := h.store.GetRun(c.Request.Context(), runID, middleware.GetUserID(c))
	if err != nil {
		writeAgentRunError(c, err)
		return
	}
	if job, jobErr := h.jobs.GetJob(c.Request.Context(), runID, middleware.GetUserID(c)); jobErr == nil {
		record.Job = job
	}
	c.JSON(http.StatusOK, record)
}

func (h *AgentRunHandler) Result(c *gin.Context) {
	runID, ok := parseRunID(c)
	if !ok {
		return
	}
	result, err := h.store.GetResult(c.Request.Context(), runID, middleware.GetUserID(c))
	if err != nil {
		writeAgentRunError(c, err)
		return
	}
	if len(result.Rows) > 20 {
		result.Rows = result.Rows[:20]
	}
	c.JSON(http.StatusOK, result)
}

func (h *AgentRunHandler) Cancel(c *gin.Context) {
	runID, ok := parseRunID(c)
	if !ok {
		return
	}
	status, err := h.jobs.Cancel(c.Request.Context(), runID, middleware.GetUserID(c))
	if err != nil {
		writeAgentJobError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"run_id": runID.String(), "status": status})
}

func (h *AgentRunHandler) Retry(c *gin.Context) {
	runID, ok := parseRunID(c)
	if !ok {
		return
	}
	status, err := h.jobs.Retry(c.Request.Context(), runID, middleware.GetUserID(c))
	if err != nil {
		writeAgentJobError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"run_id": runID.String(), "status": status})
}

func (h *AgentRunHandler) Stats(c *gin.Context) {
	stats, err := h.jobs.GetStats(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		writeAgentJobError(c, err)
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (h *AgentRunHandler) Steps(c *gin.Context) {
	runID, ok := parseRunID(c)
	if !ok {
		return
	}
	steps, err := h.store.ListSteps(c.Request.Context(), runID, middleware.GetUserID(c))
	if err != nil {
		writeAgentRunError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"run_id": runID.String(), "steps": steps})
}

func (h *AgentRunHandler) Resume(c *gin.Context) {
	runID, ok := parseRunID(c)
	if !ok {
		return
	}
	requestID := middleware.GetRequestID(c)
	userID := middleware.GetUserID(c)
	result, err := h.pipeline.Resume(c.Request.Context(), runID, userID)
	if err != nil {
		if result == nil {
			writeAgentRunError(c, err)
			return
		}
		logAgentRun(requestID, userID, uuid.Nil, result, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "运行恢复失败，请稍后重试", "request_id": requestID,
			"run_id": result.RunID.String(), "error_type": result.ErrorType,
		})
		return
	}
	logAgentRun(requestID, userID, uuid.Nil, result, nil)
	preview := result.ResultRows
	if len(preview) > 20 {
		preview = preview[:20]
	}
	c.JSON(http.StatusOK, gin.H{
		"run_id": result.RunID.String(), "graph_version": result.GraphVersion,
		"status": result.Status, "summary": result.Summary, "sql": result.SQL,
		"chart_config": result.ChartConfig, "result_preview": preview,
		"result_count": result.ResultCount, "report": result.Report,
		"attempts": result.Attempts, "duration_ms": result.DurationMS,
		"error": result.Error, "error_type": result.ErrorType, "request_id": requestID,
	})
}

func parseRunID(c *gin.Context) (uuid.UUID, bool) {
	runID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "运行编号无效"})
		return uuid.Nil, false
	}
	return runID, true
}

func writeAgentRunError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, agent.ErrRunNotFound):
		c.JSON(http.StatusNotFound, gin.H{"detail": "运行不存在或无权访问"})
	case errors.Is(err, agent.ErrRunCompleted):
		c.JSON(http.StatusConflict, gin.H{"detail": "运行已经完成，无需恢复"})
	case errors.Is(err, agent.ErrRunActive):
		c.JSON(http.StatusConflict, gin.H{"detail": "运行仍在执行，暂不能重复恢复"})
	case errors.Is(err, agent.ErrRunNotRecoverable):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "该运行不满足恢复条件"})
	case errors.Is(err, agent.ErrCheckpointNotFound):
		c.JSON(http.StatusConflict, gin.H{"detail": "运行缺少有效恢复点"})
	case errors.Is(err, agent.ErrGraphVersionUnsupported):
		c.JSON(http.StatusConflict, gin.H{"detail": "运行使用的 Graph 版本与当前服务不兼容"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "运行服务暂时不可用"})
	}
}

func writeAgentJobError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, agent.ErrJobNotFound), errors.Is(err, agent.ErrRunNotFound):
		c.JSON(http.StatusNotFound, gin.H{"detail": "任务不存在或无权访问"})
	case errors.Is(err, agent.ErrJobActive):
		c.JSON(http.StatusConflict, gin.H{"detail": "任务仍在排队或执行中"})
	case errors.Is(err, agent.ErrJobTerminal):
		c.JSON(http.StatusConflict, gin.H{"detail": "任务已经结束，无法执行该操作"})
	case errors.Is(err, agent.ErrRunNotRecoverable), errors.Is(err, agent.ErrCheckpointNotFound):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "任务没有可用的恢复点"})
	case errors.Is(err, agent.ErrJobQueueFull):
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "任务队列已满，请稍后重试"})
	case errors.Is(err, agent.ErrUserJobLimit):
		c.JSON(http.StatusTooManyRequests, gin.H{"detail": "当前未完成任务过多，请等待已有任务完成"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "任务服务暂时不可用"})
	}
}

func (h *AgentRunHandler) authorizeRunInput(c *gin.Context, userID uuid.UUID, rawConversationID, rawDatasetID string, domainName *string) (uuid.UUID, uuid.UUID, bool) {
	conversationID := uuid.Nil
	datasetID := uuid.Nil
	if rawConversationID != "" {
		parsed, err := uuid.Parse(rawConversationID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "会话编号无效"})
			return uuid.Nil, uuid.Nil, false
		}
		conversation, err := h.db.GetConversation(c.Request.Context(), parsed, userID)
		if err != nil || conversation == nil {
			c.JSON(http.StatusNotFound, gin.H{"detail": "会话不存在或无权访问"})
			return uuid.Nil, uuid.Nil, false
		}
		conversationID = parsed
		if strings.TrimSpace(*domainName) == "" {
			*domainName = conversation.Domain
		}
		if rawDatasetID == "" && conversation.DatasetID != nil {
			datasetID = *conversation.DatasetID
		}
	}
	if rawDatasetID != "" {
		parsed, err := uuid.Parse(rawDatasetID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "数据集编号无效"})
			return uuid.Nil, uuid.Nil, false
		}
		dataset, err := h.db.GetDataset(c.Request.Context(), parsed, userID)
		if err != nil || dataset == nil {
			c.JSON(http.StatusNotFound, gin.H{"detail": "数据集不存在或无权访问"})
			return uuid.Nil, uuid.Nil, false
		}
		datasetID = parsed
	}
	return conversationID, datasetID, true
}
