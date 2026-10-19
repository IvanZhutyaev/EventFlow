// Package observability provides structured JSON logging.
// Secrets, API keys and sensitive payloads must never be logged (spec §22).
package observability

import (
	"log/slog"
	"os"
)

// NewLogger builds a slog logger emitting structured JSON.
// In development it uses text format for readability.
func NewLogger(appEnv string) *slog.Logger {
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if appEnv == "development" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}
