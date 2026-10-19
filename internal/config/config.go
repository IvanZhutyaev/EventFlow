// Package config loads application settings from environment variables.
// All runtime configuration is provided via env (see .env.example);
// nothing sensitive is stored in the repository.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all settings required by the API and worker binaries.
type Config struct {
	APPEnv   string
	HTTPPort string

	DatabaseURL string
	NATSURL     string

	WorkerConcurrency   int
	MaxDeliveryAttempts int
	RequestTimeout      time.Duration
	ShutdownTimeout     time.Duration

	// Retry policy (exponential backoff, see spec §9).
	RetryInitialDelay time.Duration
	RetryMaxDelay     time.Duration
	RetryMultiplier   float64

	// Rate limiting (in-memory per project for MVP; Redis comes in v2).
	RateLimitRPS float64

	// Webhook HMAC-SHA256 signing secret.
	WebhookSecret string

	// Admin token protects the bootstrap admin API (project + api key creation).
	AdminToken string

	// Validation limits (spec §26).
	MaxSubscriptionsPerProject int
	MaxEventBodyBytes          int64
}

// Load reads configuration from environment variables and applies defaults
// suitable for local development.
func Load() (*Config, error) {
	cfg := &Config{
		APPEnv:   getEnv("APP_ENV", "development"),
		HTTPPort: getEnv("HTTP_PORT", "8080"),

		DatabaseURL: os.Getenv("DATABASE_URL"),
		NATSURL:     getEnv("NATS_URL", "nats://localhost:4222"),

		WorkerConcurrency:   getInt("WORKER_CONCURRENCY", 20),
		MaxDeliveryAttempts: getInt("MAX_DELIVERY_ATTEMPTS", 5),
		RequestTimeout:      getDuration("REQUEST_TIMEOUT", 10*time.Second),
		ShutdownTimeout:     getDuration("SHUTDOWN_TIMEOUT", 15*time.Second),

		RetryInitialDelay: getDuration("RETRY_INITIAL_DELAY", 5*time.Second),
		RetryMaxDelay:     getDuration("RETRY_MAX_DELAY", 30*time.Minute),
		RetryMultiplier:   getFloat("RETRY_MULTIPLIER", 2.0),

		RateLimitRPS: getFloat("RATE_LIMIT_RPS", 100),

		WebhookSecret: os.Getenv("WEBHOOK_SECRET"),
		AdminToken:    os.Getenv("ADMIN_TOKEN"),

		MaxSubscriptionsPerProject: getInt("MAX_SUBSCRIPTIONS_PER_PROJECT", 100),
		MaxEventBodyBytes:          int64(getInt("MAX_EVENT_BODY_BYTES", 1<<20)), // 1 MB
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("config: DATABASE_URL is required")
	}
	if cfg.MaxDeliveryAttempts < 1 {
		return nil, fmt.Errorf("config: MAX_DELIVERY_ATTEMPTS must be >= 1")
	}
	if cfg.RetryMultiplier < 1 {
		return nil, fmt.Errorf("config: RETRY_MULTIPLIER must be >= 1")
	}
	if cfg.WorkerConcurrency < 1 {
		return nil, fmt.Errorf("config: WORKER_CONCURRENCY must be >= 1")
	}
	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func getDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
