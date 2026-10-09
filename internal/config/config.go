package config

import (
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	DatabaseURL         string `env:"DATABASE_URL" envDefault:"postgres://tts_agent:tts_agent_secret@localhost:5432/tts_agent?sslmode=disable"`
	DatabaseReadonlyURL string `env:"DATABASE_READONLY_URL" envDefault:"postgres://agent_readonly:readonly_secret@localhost:5432/tts_agent?sslmode=disable"`
	RedisURL            string `env:"REDIS_URL" envDefault:"redis://localhost:6379/0"`

	JWTSecretKey          string `env:"JWT_SECRET_KEY" envDefault:"change-me-to-a-random-secret-key-in-production"`
	AccessTokenExpireMin  int    `env:"ACCESS_TOKEN_EXPIRE_MINUTES" envDefault:"15"`
	RefreshTokenExpireDay int    `env:"REFRESH_TOKEN_EXPIRE_DAYS" envDefault:"7"`
	EmailProvider         string `env:"EMAIL_PROVIDER" envDefault:"log"`
	EmailFrom             string `env:"EMAIL_FROM" envDefault:"经营分析助手 <no-reply@example.com>"`
	EmailSMTPHost         string `env:"EMAIL_SMTP_HOST"`
	EmailSMTPPort         int    `env:"EMAIL_SMTP_PORT" envDefault:"587"`
	EmailSMTPTLSMode      string `env:"EMAIL_SMTP_TLS_MODE" envDefault:"starttls"`
	EmailSMTPUsername     string `env:"EMAIL_SMTP_USERNAME"`
	EmailSMTPPassword     string `env:"EMAIL_SMTP_PASSWORD"`
	EmailCodeExpireMin    int    `env:"EMAIL_CODE_EXPIRE_MINUTES" envDefault:"10"`
	EmailCodeResendSec    int    `env:"EMAIL_CODE_RESEND_SECONDS" envDefault:"60"`
	EmailCodeMaxAttempts  int    `env:"EMAIL_CODE_MAX_ATTEMPTS" envDefault:"5"`
	EmailCodeMaxPerHour   int    `env:"EMAIL_CODE_MAX_PER_HOUR" envDefault:"5"`

	DeepSeekAPIKey           string  `env:"DEEPSEEK_API_KEY"`
	DeepSeekBaseURL          string  `env:"DEEPSEEK_BASE_URL" envDefault:"https://api.deepseek.com"`
	DeepSeekModel            string  `env:"DEEPSEEK_MODEL" envDefault:"deepseek-v4-flash"`
	LLMTimeoutSec            int     `env:"LLM_TIMEOUT_SECONDS" envDefault:"30"`
	LLMInputPricePerMillion  float64 `env:"LLM_INPUT_PRICE_PER_MILLION" envDefault:"0"`
	LLMOutputPricePerMillion float64 `env:"LLM_OUTPUT_PRICE_PER_MILLION" envDefault:"0"`
	AdminEmails              string  `env:"ADMIN_EMAILS"`

	EmbeddingDimension    int    `env:"EMBEDDING_DIMENSION" envDefault:"1536"`
	CORSOrigins           string `env:"CORS_ORIGINS" envDefault:"http://localhost:5173,http://localhost:3000"`
	DefaultDomain         string `env:"DEFAULT_DOMAIN" envDefault:"ecommerce"`
	StatementTimeoutMS    int    `env:"STATEMENT_TIMEOUT_MS" envDefault:"10000"`
	MaxAgentRetries       int    `env:"MAX_AGENT_RETRIES" envDefault:"3"`
	RateLimitPerMinute    int    `env:"RATE_LIMIT_PER_MINUTE" envDefault:"20"`
	WorkerConcurrency     int    `env:"WORKER_CONCURRENCY" envDefault:"2"`
	WorkerUserConcurrency int    `env:"WORKER_USER_CONCURRENCY" envDefault:"1"`
	WorkerPollMS          int    `env:"WORKER_POLL_MS" envDefault:"500"`
	WorkerLeaseSec        int    `env:"WORKER_LEASE_SECONDS" envDefault:"60"`
	WorkerHeartbeatSec    int    `env:"WORKER_HEARTBEAT_SECONDS" envDefault:"10"`
	JobMaxAttempts        int    `env:"JOB_MAX_ATTEMPTS" envDefault:"3"`
	JobBackoffBaseSec     int    `env:"JOB_BACKOFF_BASE_SECONDS" envDefault:"2"`
	JobQueueLimit         int    `env:"JOB_QUEUE_LIMIT" envDefault:"1000"`
	UserActiveJobLimit    int    `env:"USER_ACTIVE_JOB_LIMIT" envDefault:"3"`
	DomainsPath           string `env:"DOMAINS_PATH" envDefault:"domains"`
	Port                  string `env:"PORT" envDefault:"8000"`
}

func (c *Config) IsAdminEmail(email string) bool {
	for _, candidate := range strings.Split(c.AdminEmails, ",") {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(email)) {
			return true
		}
	}
	return false
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) CORSOriginList() []string {
	parts := strings.Split(c.CORSOrigins, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (c *Config) AccessTokenDuration() time.Duration {
	return time.Duration(c.AccessTokenExpireMin) * time.Minute
}

func (c *Config) RefreshTokenDuration() time.Duration {
	return time.Duration(c.RefreshTokenExpireDay) * 24 * time.Hour
}

func (c *Config) EmailCodeDuration() time.Duration {
	return time.Duration(c.EmailCodeExpireMin) * time.Minute
}
