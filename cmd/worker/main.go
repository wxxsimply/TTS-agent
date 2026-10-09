package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/tts-agent/internal/agent"
	"github.com/tts-agent/internal/bootstrap"
	"github.com/tts-agent/internal/config"
	"github.com/tts-agent/internal/domain/community"
	"github.com/tts-agent/internal/domain/ecommerce"
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := store.NewPostgres(ctx, cfg.DatabaseURL, cfg.DatabaseReadonlyURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := bootstrap.RunMigrations(db); err != nil {
		log.Fatal(err)
	}

	llmClient := llm.NewClient(cfg.DeepSeekAPIKey, cfg.DeepSeekBaseURL, cfg.DeepSeekModel, cfg.LLMTimeoutSec).
		WithPricing(cfg.LLMInputPricePerMillion, cfg.LLMOutputPricePerMillion)
	schemaRouter := schema.NewRouter(db, cfg.EmbeddingDimension, cfg.DomainsPath)
	pipeline := agent.NewPipeline(db, llmClient, schemaRouter, cfg.MaxAgentRetries, cfg.StatementTimeoutMS)
	jobs := agent.NewPostgresJobStore(db)
	worker := agent.NewWorker(jobs, pipeline, agent.WorkerConfig{
		Concurrency:       cfg.WorkerConcurrency,
		PollInterval:      time.Duration(cfg.WorkerPollMS) * time.Millisecond,
		LeaseDuration:     time.Duration(cfg.WorkerLeaseSec) * time.Second,
		HeartbeatInterval: time.Duration(cfg.WorkerHeartbeatSec) * time.Second,
		BackoffBase:       time.Duration(cfg.JobBackoffBaseSec) * time.Second,
		UserConcurrency:   cfg.WorkerUserConcurrency,
	})
	log.Printf("Agent worker started with concurrency=%d", cfg.WorkerConcurrency)
	worker.Run(ctx)
	log.Print("Agent worker stopped")
}
