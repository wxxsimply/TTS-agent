package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tts-agent/internal/agent"
	"github.com/tts-agent/internal/api"
	"github.com/tts-agent/internal/auth"
	"github.com/tts-agent/internal/bootstrap"
	"github.com/tts-agent/internal/config"
	"github.com/tts-agent/internal/domain/community"
	"github.com/tts-agent/internal/domain/ecommerce"
	mail "github.com/tts-agent/internal/email"
	"github.com/tts-agent/internal/llm"
	"github.com/tts-agent/internal/schema"
	"github.com/tts-agent/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	ecommerce.InitRegistry(cfg.DomainsPath)
	community.InitRegistry(cfg.DomainsPath)

	ctx := context.Background()
	db, err := store.NewPostgres(ctx, cfg.DatabaseURL, cfg.DatabaseReadonlyURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := bootstrap.EnsureSetup(ctx, db, cfg.DatabaseURL, cfg.DomainsPath, cfg.DefaultDomain, cfg.EmbeddingDimension); err != nil {
		log.Fatal(err)
	}
	if count, err := agent.NewPostgresRunRecorder(db).MarkStaleRunsInterrupted(ctx); err != nil {
		log.Fatal(err)
	} else if count > 0 {
		log.Printf("Marked %d stale agent runs as interrupted", count)
	}

	authSvc := auth.NewService(cfg.JWTSecretKey, cfg.AccessTokenDuration(), cfg.RefreshTokenDuration())
	emailSender, err := mail.NewSender(cfg)
	if err != nil {
		log.Fatal(err)
	}
	llmClient := llm.NewClient(cfg.DeepSeekAPIKey, cfg.DeepSeekBaseURL, cfg.DeepSeekModel, cfg.LLMTimeoutSec).
		WithPricing(cfg.LLMInputPricePerMillion, cfg.LLMOutputPricePerMillion)
	schemaRouter := schema.NewRouter(db, cfg.EmbeddingDimension, cfg.DomainsPath)
	pipeline := agent.NewPipeline(db, llmClient, schemaRouter, cfg.MaxAgentRetries, cfg.StatementTimeoutMS)

	router := api.SetupRouter(cfg, db, authSvc, pipeline, emailSender)
	srv := &http.Server{Addr: ":" + cfg.Port, Handler: router}

	go func() {
		log.Printf("Server listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
