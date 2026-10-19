// Command migrate applies SQL migrations from the embedded migrations directory.
// Usage: migrate [-dry-run]
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/IvanZhutyaev/EventFlow/internal/config"
	"github.com/IvanZhutyaev/EventFlow/internal/observability"
	"github.com/IvanZhutyaev/EventFlow/migrations"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "print pending migrations without applying")
	flag.Parse()

	logger := observability.NewLogger("development")
	if err := run(logger, *dryRun); err != nil {
		logger.Error("migration failed", "error", err)
		panic(err)
	}
}

func run(logger *slog.Logger, dryRun bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, name).Scan(&exists); err != nil {
			return fmt.Errorf("check version %s: %w", name, err)
		}
		if exists {
			continue
		}

		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		if dryRun {
			logger.Info("pending migration", "version", name)
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin tx %s: %w", name, err)
		}

		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}

		logger.Info("applied migration", "version", name, "at", time.Now().UTC().Format(time.RFC3339))
	}

	return nil
}
