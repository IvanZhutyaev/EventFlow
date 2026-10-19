//go:build integration

// Package integration verifies EventFlow stores against a real PostgreSQL.
// Run with: docker compose up -d postgres nats && make test-integration
package integration

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	"github.com/IvanZhutyaev/EventFlow/internal/auth"
	"github.com/IvanZhutyaev/EventFlow/internal/broker"
	"github.com/IvanZhutyaev/EventFlow/internal/project"
	"github.com/IvanZhutyaev/EventFlow/migrations"
	"github.com/google/uuid"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://eventflow:eventflow@localhost:5432/eventflow?sslmode=disable"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db (is postgres running?): %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func applyMigrations(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	if err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	for _, e := range entries {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)`, e.Name()).Scan(&exists); err != nil {
			t.Fatalf("check migration: %v", err)
		}
		if exists {
			continue
		}
		body, _ := migrations.FS.ReadFile(e.Name())
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", e.Name(), err)
		}
		db.Exec(`INSERT INTO schema_migrations (version) VALUES ($1)`, e.Name())
	}
}

// seedProject creates an isolated user+project per test and returns the project id.
func seedProject(t *testing.T, db *sql.DB) (projectID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	store := project.NewStore(db)
	userID, err := store.CreateUser(ctx, "it-"+uuid.NewString()+"@example.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	p, err := store.CreateProject(ctx, userID, "itest", "")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p.ID
}

// noopPublisher swallows published jobs in tests that only exercise storage.
type noopPublisher struct{}

func (noopPublisher) PublishJob(ctx context.Context, job broker.DeliveryJob) error { return nil }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func generateKey() (string, string, error) {
	return auth.GenerateKey()
}
