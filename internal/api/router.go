package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/IvanZhutyaev/EventFlow/internal/project"
	"github.com/IvanZhutyaev/EventFlow/internal/ratelimit"
)

// Deps bundles everything the router needs.
type Deps struct {
	Health     *HealthHandler
	Events     *EventHandler
	Subs       *SubscriptionHandler
	Deliveries *DeliveryHandler
	Admin      *AdminHandler
	Projects   *project.Store // for auth middleware
	Limiter    *ratelimit.Limiter
	Timeout    time.Duration
	Logger     *slog.Logger
}

// NewRouter builds the full middleware chain and route table (spec §6–8, §18–19).
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	// Health — no auth (spec §19).
	mux.HandleFunc("GET /health/live", d.Health.Live)
	mux.HandleFunc("GET /health/ready", d.Health.Ready)

	// Admin bootstrap (project + API key creation); guarded by its own token.
	mux.HandleFunc("POST /api/v1/projects", d.Admin.CreateProject)

	// Authenticated API surface.
	authed := http.NewServeMux()
	authed.HandleFunc("POST /api/v1/events", d.Events.Create)
	authed.HandleFunc("GET /api/v1/events", d.Events.List)
	authed.HandleFunc("GET /api/v1/events/{eventID}", d.Events.Get)

	authed.HandleFunc("POST /api/v1/subscriptions", d.Subs.Create)
	authed.HandleFunc("GET /api/v1/subscriptions", d.Subs.List)
	authed.HandleFunc("GET /api/v1/subscriptions/{id}", d.Subs.Get)
	authed.HandleFunc("PATCH /api/v1/subscriptions/{id}", d.Subs.Patch)
	authed.HandleFunc("DELETE /api/v1/subscriptions/{id}", d.Subs.Delete)

	authed.HandleFunc("GET /api/v1/deliveries", d.Deliveries.List)
	authed.HandleFunc("GET /api/v1/deliveries/{id}", d.Deliveries.Get)

	// Chain: recovery → request id → CORS → timeout → logging → auth → rate limit.
	var handler http.Handler = authed
	handler = AuthMiddleware(d.Projects)(handler)
	handler = RateLimitMiddleware(d.Limiter)(handler)
	handler = LoggingMiddleware(d.Logger)(handler)
	handler = TimeoutMiddleware(d.Timeout)(handler)
	handler = RequestIDMiddleware(handler)
	handler = RecoveryMiddleware(handler)
	handler = CORSMiddleware(handler)

	mux.Handle("/api/v1/events", handler)
	mux.Handle("/api/v1/events/", handler)
	mux.Handle("/api/v1/subscriptions", handler)
	mux.Handle("/api/v1/subscriptions/", handler)
	mux.Handle("/api/v1/deliveries", handler)
	mux.Handle("/api/v1/deliveries/", handler)

	return mux
}
