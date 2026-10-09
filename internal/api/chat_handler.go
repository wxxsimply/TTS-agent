package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/tts-agent/internal/agent"
	"github.com/tts-agent/internal/config"
	"github.com/tts-agent/internal/middleware"
	"github.com/tts-agent/internal/store"
)

type ChatHandler struct {
	db       *store.Postgres
	pipeline *agent.Pipeline
	cfg      *config.Config
}

func NewChatHandler(db *store.Postgres, pipeline *agent.Pipeline, cfg *config.Config) *ChatHandler {
	return &ChatHandler{db: db, pipeline: pipeline, cfg: cfg}
}

type conversationCreate struct {
	Title     string `json:"title"`
	Domain    string `json:"domain"`
	DatasetID string `json:"dataset_id"`
}

type queryRequest struct {
	Query          string `json:"query" binding:"required"`
	ConversationID string `json:"conversation_id"`
	Domain         string `json:"domain"`
	DatasetID      string `json:"dataset_id"`
}

type queryResponse struct {
	RunID          string                   `json:"run_id"`
	ConversationID string                   `json:"conversation_id"`
	MessageID      string                   `json:"message_id"`
	Summary        string                   `json:"summary"`
	SQL            string                   `json:"sql,omitempty"`
	ChartConfig    map[string]interface{}   `json:"chart_config,omitempty"`
	ResultPreview  []map[string]interface{} `json:"result_preview,omitempty"`
	ResultCount    int                      `json:"result_count"`
	Error          string                   `json:"error,omitempty"`
	Report         agent.Report             `json:"report"`
	RequestID      string                   `json:"request_id"`
	Attempts       int                      `json:"attempts"`
	DurationMS     int64                    `json:"duration_ms"`
	ErrorType      string                   `json:"error_type,omitempty"`
}

type messageResponse struct {
	ID            string                   `json:"id"`
	AgentRunID    string                   `json:"agent_run_id,omitempty"`
	Role          string                   `json:"role"`
	Content       string                   `json:"content"`
	SQL           *string                  `json:"sql,omitempty"`
	ChartConfig   map[string]interface{}   `json:"chart_config,omitempty"`
	ResultPreview []map[string]interface{} `json:"result_preview,omitempty"`
	CreatedAt     interface{}              `json:"created_at"`
	ResultCount   int                      `json:"result_count"`
	Report        *agent.Report            `json:"report,omitempty"`
}

func (h *ChatHandler) ListConversations(c *gin.Context) {
	convs, err := h.db.ListConversations(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "会话列表加载失败"})
		return
	}
	c.JSON(http.StatusOK, convs)
}

func (h *ChatHandler) CreateConversation(c *gin.Context) {
	var req conversationCreate
	_ = c.ShouldBindJSON(&req)
	if req.Title == "" {
		req.Title = "新对话"
	}
	if req.Domain == "" {
		req.Domain = h.cfg.DefaultDomain
	}
	userID := middleware.GetUserID(c)
	datasetID, err := h.authorizedDatasetID(c.Request.Context(), userID, req.DatasetID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	conv, err := h.db.CreateConversationWithDataset(c.Request.Context(), userID, req.Title, req.Domain, datasetID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "新建会话失败"})
		return
	}
	c.JSON(http.StatusOK, conv)
}

func (h *ChatHandler) ListMessages(c *gin.Context) {
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "会话编号无效"})
		return
	}
	conv, _ := h.db.GetConversation(c.Request.Context(), convID, middleware.GetUserID(c))
	if conv == nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "会话不存在或无权访问"})
		return
	}
	msgs, err := h.db.ListMessages(c.Request.Context(), convID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "历史消息加载失败"})
		return
	}
	out := make([]messageResponse, 0, len(msgs))
	for _, m := range msgs {
		item := messageResponse{ID: m.ID.String(), Role: m.Role, Content: m.Content, SQL: m.SQL, ResultCount: m.ResultCount, CreatedAt: m.CreatedAt}
		if m.AgentRunID != nil {
			item.AgentRunID = m.AgentRunID.String()
		}
		decodeJSONRaw(m.ChartConfig, &item.ChartConfig)
		decodeJSONRaw(m.ResultPreview, &item.ResultPreview)
		if len(m.Report) > 0 {
			item.Report = &agent.Report{}
			decodeJSONRaw(m.Report, item.Report)
		}
		out = append(out, item)
	}
	c.JSON(http.StatusOK, out)
}

func (h *ChatHandler) Query(c *gin.Context) {
	var req queryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请输入要分析的业务问题"})
		return
	}
	userID := middleware.GetUserID(c)
	ctx := c.Request.Context()
	requestID := middleware.GetRequestID(c)

	var conv *store.Conversation
	if req.ConversationID != "" {
		convID, err := uuid.Parse(req.ConversationID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "会话编号无效"})
			return
		}
		conv, _ = h.db.GetConversation(ctx, convID, userID)
		if conv == nil {
			c.JSON(http.StatusNotFound, gin.H{"detail": "会话不存在或无权访问"})
			return
		}
		if conv.DataSourceUnavailable {
			c.JSON(http.StatusConflict, gin.H{"detail": "该会话绑定的数据源已被删除，请选择其他数据源并新建分析"})
			return
		}
	} else {
		title := truncateText(req.Query, 50)
		domainName := req.Domain
		if domainName == "" {
			domainName = h.cfg.DefaultDomain
		}
		datasetID, datasetErr := h.authorizedDatasetID(ctx, userID, req.DatasetID)
		if datasetErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"detail": datasetErr.Error()})
			return
		}
		var err error
		conv, err = h.db.CreateConversationWithDataset(ctx, userID, title, domainName, datasetID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": "创建分析会话失败"})
			return
		}
	}

	analysisQuery := h.resolveAnalysisQuery(ctx, conv.ID, req.Query)
	userMsg := &store.Message{ConversationID: conv.ID, Role: "user", Content: req.Query}
	_ = h.db.CreateMessage(ctx, userMsg)

	datasetID := uuid.Nil
	if conv.DatasetID != nil {
		datasetID = *conv.DatasetID
	}
	result, err := h.pipeline.Run(ctx, agent.Input{
		Query: analysisQuery, Domain: conv.Domain, UserID: userID,
		ConversationID: conv.ID, DatasetID: datasetID,
	})
	if err != nil {
		logAgentRun(requestID, userID, datasetID, result, err)
		response := gin.H{"detail": "智能分析服务暂时不可用，请稍后重试", "request_id": requestID}
		if result != nil && result.RunID != uuid.Nil {
			response["run_id"] = result.RunID.String()
		}
		c.JSON(http.StatusInternalServerError, response)
		return
	}
	logAgentRun(requestID, userID, datasetID, result, nil)

	if result.Status == agent.RunFailed {
		sql := result.SQL
		assistant := &store.Message{ConversationID: conv.ID, Role: "assistant", Content: result.Error, SQL: &sql}
		_ = h.db.CreateMessage(ctx, assistant)
		c.JSON(http.StatusOK, queryResponse{
			RunID:          result.RunID.String(),
			ConversationID: conv.ID.String(), MessageID: assistant.ID.String(),
			Summary: result.Error, SQL: result.SQL, Error: result.Error,
			RequestID: requestID, Attempts: result.Attempts, DurationMS: result.DurationMS, ErrorType: result.ErrorType,
		})
		return
	}

	preview := result.ResultRows
	if len(preview) > 20 {
		preview = preview[:20]
	}
	chartJSON, _ := json.Marshal(result.ChartConfig)
	previewJSON, _ := json.Marshal(preview)
	resultJSON, _ := json.Marshal(result.ResultRows)
	reportJSON, _ := json.Marshal(result.Report)
	sql := result.SQL
	assistant := &store.Message{
		ConversationID: conv.ID, Role: "assistant", Content: result.Summary,
		SQL: &sql, ChartConfig: chartJSON, ResultPreview: previewJSON,
		ResultData: resultJSON, Report: reportJSON, ResultCount: result.ResultCount,
	}
	_ = h.db.CreateMessage(ctx, assistant)
	_ = h.db.TouchConversation(ctx, conv.ID)

	c.JSON(http.StatusOK, queryResponse{
		RunID:          result.RunID.String(),
		ConversationID: conv.ID.String(), MessageID: assistant.ID.String(),
		Summary: result.Summary, SQL: result.SQL, ChartConfig: result.ChartConfig,
		ResultPreview: preview, ResultCount: result.ResultCount, Report: result.Report,
		RequestID: requestID, Attempts: result.Attempts, DurationMS: result.DurationMS,
	})
}

// resolveAnalysisQuery lets a short display follow-up reuse the last business
// question. Without it, a request such as “做成折线图” has no metric or
// grouping dimension, so an LLM tends to fall back to unsafe SELECT * queries.
func (h *ChatHandler) resolveAnalysisQuery(ctx context.Context, conversationID uuid.UUID, query string) string {
	if !isChartOnlyRequest(query) {
		return query
	}
	messages, err := h.db.ListMessages(ctx, conversationID)
	if err != nil {
		return query
	}
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != "user" || isChartOnlyRequest(messages[index].Content) {
			continue
		}
		return fmt.Sprintf("%s。补充要求：%s。请只返回适合图表展示的汇总结果；如按日期展示，必须按日期聚合，每个日期仅保留一行。", messages[index].Content, query)
	}
	return query
}

func isChartOnlyRequest(query string) bool {
	trimmed := strings.TrimSpace(query)
	if len([]rune(trimmed)) > 40 {
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "折线图") || strings.Contains(lower, "柱状图") || strings.Contains(lower, "饼图") || strings.Contains(lower, "图表")
}

func logAgentRun(requestID string, userID, datasetID uuid.UUID, result *agent.Result, runErr error) {
	entry := map[string]interface{}{
		"event": "agent_run", "request_id": requestID, "user_id": userID.String(),
		"dataset_id": datasetID.String(),
	}
	if result != nil {
		entry["run_id"] = result.RunID.String()
		entry["graph_version"] = result.GraphVersion
		entry["attempts"] = result.Attempts
		entry["duration_ms"] = result.DurationMS
		entry["error_type"] = result.ErrorType
		entry["step_count"] = len(result.Steps)
	}
	if runErr != nil {
		entry["status"] = "error"
		if result == nil || result.ErrorType == "" {
			entry["error_type"] = "internal_error"
		}
	} else {
		entry["status"] = result.Status
		steps := make([]map[string]interface{}, 0, len(result.Steps))
		for _, step := range result.Steps {
			steps = append(steps, map[string]interface{}{
				"node": step.Node, "next": step.Next, "status": step.Status,
				"attempt": step.Attempt, "duration_ms": step.DurationMS,
			})
		}
		entry["steps"] = steps
		if result.SQL != "" {
			hash := sha256.Sum256([]byte(result.SQL))
			entry["sql_fingerprint"] = fmt.Sprintf("%x", hash[:8])
		}
	}
	encoded, _ := json.Marshal(entry)
	log.Print(string(encoded))
}

func (h *ChatHandler) authorizedDatasetID(ctx context.Context, userID uuid.UUID, raw string) (*uuid.UUID, error) {
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("数据集编号无效")
	}
	dataset, err := h.db.GetDataset(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	if dataset == nil {
		return nil, fmt.Errorf("数据集不存在或无权访问")
	}
	return &id, nil
}

func truncateText(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}

type WebhookHandler struct {
	pipeline *agent.Pipeline
	cfg      *config.Config
}

func NewWebhookHandler(pipeline *agent.Pipeline, cfg *config.Config) *WebhookHandler {
	return &WebhookHandler{pipeline: pipeline, cfg: cfg}
}

type feishuQueryRequest struct {
	Query  string `json:"query" binding:"required"`
	Domain string `json:"domain"`
	UserID string `json:"user_id"`
}

func (h *WebhookHandler) FeishuQuery(c *gin.Context) {
	var req feishuQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请输入要分析的业务问题"})
		return
	}
	domainName := req.Domain
	if domainName == "" {
		domainName = h.cfg.DefaultDomain
	}
	result, err := h.pipeline.Run(c.Request.Context(), agent.Input{Query: req.Query, Domain: domainName})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	if result.Status == agent.RunFailed {
		c.JSON(http.StatusOK, gin.H{"run_id": result.RunID.String(), "text": result.Error, "sql": result.SQL})
		return
	}
	preview := result.ResultRows
	if len(preview) > 10 {
		preview = preview[:10]
	}
	c.JSON(http.StatusOK, gin.H{
		"run_id": result.RunID.String(),
		"text":   result.Summary, "sql": result.SQL, "chart_config": result.ChartConfig,
		"result_count": result.ResultCount, "result_preview": preview, "report": result.Report,
	})
}

type ExportHandler struct {
	db *store.Postgres
}

func NewExportHandler(db *store.Postgres) *ExportHandler {
	return &ExportHandler{db: db}
}

func (h *ExportHandler) Excel(c *gin.Context) {
	msgID, err := uuid.Parse(c.Param("message_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "报告编号无效"})
		return
	}
	msg, err := h.db.GetMessage(c.Request.Context(), msgID, middleware.GetUserID(c))
	if err != nil || msg == nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "报告不存在或无权访问"})
		return
	}
	data := msg.ResultData
	if len(data) == 0 {
		data = msg.ResultPreview
	}
	if len(data) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "没有可导出的数据"})
		return
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(data, &rows); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "导出数据解析失败"})
		return
	}
	report := agent.Report{Title: "数据分析报告", Conclusion: msg.Content, DataScope: agent.DataScope{RowCount: len(rows)}}
	if len(msg.Report) > 0 {
		decodeJSONRaw(msg.Report, &report)
	}
	fileName := exportFileName(report.Title)
	c.Header("Content-Disposition", "attachment; filename=report.xlsx; filename*=UTF-8''"+fileName)
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buildExcel(rows, report))
}
