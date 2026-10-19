// Command api runs the EventFlow HTTP API server (spec §6–8, §30).
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/IvanZhutyaev/EventFlow/internal/api"
	"github.com/IvanZhutyaev/EventFlow/internal/broker"
	"github.com/IvanZhutyaev/EventFlow/internal/config"
	"github.com/IvanZhutyaev/EventFlow/internal/delivery"
	"github.com/IvanZhutyaev/EventFlow/internal/event"
	"github.com/IvanZhutyaev/EventFlow/internal/observability"
	"github.com/IvanZhutyaev/EventFlow/internal/project"
	"github.com/IvanZhutyaev/EventFlow/internal/ratelimit"
	"github.com/IvanZhutyaev/EventFlow/internal/subscription"

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
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)

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

	// Stores.
	projectStore := project.NewStore(db)
	eventStore := event.NewStore(db)
	subStore := subscription.NewStore(db)
	deliveryStore := delivery.NewStore(db)

	// Ingestion service: event → matching → deliveries → NATS jobs.
	ingest := event.NewService(db, eventStore, subStore, deliveryStore, bus, log)

	// Handlers.
	health := &api.HealthHandler{DB: db, Broker: bus}
	events := &api.EventHandler{Events: eventStore, Service: ingest, MaxBodyBytes: cfg.MaxEventBodyBytes}
	subs := &api.SubscriptionHandler{Subs: subStore, MaxPerProject: cfg.MaxSubscriptionsPerProject}
	deliveries := &api.DeliveryHandler{Deliveries: deliveryStore}
	admin := &api.AdminHandler{Projects: projectStore, AdminToken: cfg.AdminToken}

	handler := api.NewRouter(api.Deps{
		Health:     health,
		Events:     events,
		Subs:       subs,
		Deliveries: deliveries,
		Admin:      admin,
		Projects:   projectStore,
		Limiter:    ratelimit.NewLimiter(cfg.RateLimitRPS),
		Timeout:    cfg.RequestTimeout,
		Logger:     log,
	})

	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Info("api listening", "port", cfg.HTTPPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down api")

	// Graceful shutdown: stop accepting requests, finish active ones (spec §30).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "error", err)
	}
}
