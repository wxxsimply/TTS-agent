package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tts-agent/internal/agent"
	"github.com/tts-agent/internal/auth"
	"github.com/tts-agent/internal/config"
	mail "github.com/tts-agent/internal/email"
	"github.com/tts-agent/internal/middleware"
	"github.com/tts-agent/internal/store"
)

func SetupRouter(cfg *config.Config, db *store.Postgres, authSvc *auth.Service, pipeline *agent.Pipeline, sender mail.Sender) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(middleware.RequestID(), gin.Recovery(), gin.Logger())

	r.Use(func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		for _, o := range cfg.CORSOriginList() {
			if o == origin || o == "*" {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Access-Control-Allow-Credentials", "true")
				c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
				c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				c.Header("Access-Control-Expose-Headers", "Content-Disposition, X-Request-ID")
				break
			}
		}
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	authH := NewAuthHandler(authSvc, db, sender, cfg.EmailCodeDuration(), time.Duration(cfg.EmailCodeResendSec)*time.Second, cfg.EmailCodeMaxAttempts, cfg.EmailCodeMaxPerHour)
	chatH := NewChatHandler(db, pipeline, cfg)
	agentRunH := NewAgentRunHandler(db, pipeline, cfg)
	exportH := NewExportHandler(db)
	datasetH := NewDatasetHandler(db)
	observabilityH := NewObservabilityHandler(db, cfg)
	webhookH := NewWebhookHandler(pipeline, cfg)
	limiter := middleware.NewRateLimiter(cfg.RateLimitPerMinute)
	authLimiter := middleware.NewRateLimiter(cfg.RateLimitPerMinute)
	authRateLimit := authLimiter.Middleware(func(c *gin.Context) string {
		return c.FullPath() + ":" + c.ClientIP()
	})

	api := r.Group("/api")
	{
		api.GET("/health/live", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
		api.GET("/health", readinessHandler(db, cfg.DefaultDomain))
		api.GET("/health/ready", readinessHandler(db, cfg.DefaultDomain))

		authGroup := api.Group("/auth")
		{
			authGroup.POST("/register", authRateLimit, authH.Register)
			authGroup.POST("/login", authRateLimit, authH.Login)
			authGroup.POST("/email-code", authRateLimit, authH.SendEmailCode)
			authGroup.POST("/email-login", authRateLimit, authH.EmailLogin)
			authGroup.POST("/refresh", authRateLimit, authH.Refresh)
			authGroup.POST("/logout", authRateLimit, authH.Logout)
			authGroup.GET("/me", middleware.JWT(authSvc, db), authH.Me)
		}

		protected := api.Group("")
		protected.Use(middleware.JWT(authSvc, db))
		{
			protected.POST("/agent-runs", limiter.Middleware(func(c *gin.Context) string {
				return middleware.GetUserID(c).String()
			}), agentRunH.Submit)
			protected.POST("/auth/password", limiter.Middleware(func(c *gin.Context) string {
				return "password:" + middleware.GetUserID(c).String()
			}), authH.SetPassword)
			protected.GET("/agent-jobs/stats", agentRunH.Stats)
			protected.GET("/conversations", chatH.ListConversations)
			protected.POST("/conversations", chatH.CreateConversation)
			protected.GET("/conversations/:id/messages", chatH.ListMessages)
			protected.POST("/chat/query", limiter.Middleware(func(c *gin.Context) string {
				return middleware.GetUserID(c).String()
			}), chatH.Query)
			protected.GET("/agent-runs/:id", agentRunH.Get)
			protected.GET("/agent-runs/:id/steps", agentRunH.Steps)
			protected.GET("/agent-runs/:id/result", agentRunH.Result)
			protected.POST("/agent-runs/:id/cancel", agentRunH.Cancel)
			protected.POST("/agent-runs/:id/retry", agentRunH.Retry)
			protected.POST("/agent-runs/:id/resume", limiter.Middleware(func(c *gin.Context) string {
				return middleware.GetUserID(c).String()
			}), agentRunH.Resume)
			protected.POST("/agent-runs/:id/feedback", observabilityH.Feedback)
			protected.GET("/admin/agent-metrics", observabilityH.Overview)
			protected.POST("/admin/feedback/:id/promote", observabilityH.PromoteFeedback)
			protected.GET("/export/:message_id/excel", exportH.Excel)
			protected.GET("/datasets", datasetH.List)
			protected.POST("/datasets/preview", datasetH.Preview)
			protected.POST("/datasets/import", datasetH.Upload)
			protected.DELETE("/datasets/:id", datasetH.Delete)
		}

		webhook := api.Group("/webhook")
		webhook.Use(limiter.Middleware(func(c *gin.Context) string {
			return c.ClientIP()
		}))
		{
			webhook.POST("/feishu/query", webhookH.FeishuQuery)
		}
	}
	r.GET("/metrics", observabilityH.Prometheus)

	return r
}

func readinessHandler(db *store.Postgres, domainName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := db.Pool.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "dependency": "database"})
			return
		}
		if err := db.ReadonlyPool.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "dependency": "readonly_database"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "domain": domainName})
	}
}
