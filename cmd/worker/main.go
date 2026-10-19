// Command worker runs the EventFlow delivery workers consuming NATS JetStream
// jobs (spec §14–15, §30).
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/IvanZhutyaev/EventFlow/internal/broker"
	"github.com/IvanZhutyaev/EventFlow/internal/config"
	"github.com/IvanZhutyaev/EventFlow/internal/delivery"
	"github.com/IvanZhutyaev/EventFlow/internal/observability"
	"github.com/IvanZhutyaev/EventFlow/internal/worker"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config error", "error", err)
		os.Exit(1)
	}
	log := observability.NewLogger(cfg.APPEnv)

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		log.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(cfg.WorkerConcurrency + 5)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bus, err := broker.New(cfg.NATSURL, log)
	if err != nil {
		log.Error("connect nats", "error", err)
		os.Exit(1)
	}
	defer bus.Close()
	if err := bus.EnsureStream(ctx); err != nil {
		log.Error("ensure stream", "error", err)
		os.Exit(1)
	}

	consumer, err := bus.WebhookConsumer(ctx)
	if err != nil {
		log.Error("create consumer", "error", err)
		os.Exit(1)
	}

	deliveryStore := delivery.NewStore(db)
	policy := delivery.NewRetryPolicy(cfg.MaxDeliveryAttempts, cfg.RetryInitialDelay, cfg.RetryMaxDelay, cfg.RetryMultiplier)

	wh := worker.NewWebhookWorker(
		deliveryStore, consumer, policy,
		cfg.WebhookSecret, cfg.RequestTimeout, cfg.WorkerConcurrency, log,
	)

	log.Info("webhook worker started", "concurrency", cfg.WorkerConcurrency)
	if err := wh.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("worker failed", "error", err)
		os.Exit(1)
	}
	log.Info("worker stopped")
}
