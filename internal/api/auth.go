package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/IvanZhutyaev/EventFlow/internal/auth"
	"github.com/IvanZhutyaev/EventFlow/internal/project"
	"github.com/IvanZhutyaev/EventFlow/internal/ratelimit"
	"github.com/google/uuid"
)

type projectCtxKey int

const projectIDKey projectCtxKey = iota

// AuthMiddleware authenticates requests via "Authorization: Bearer <api_key>",
// resolves the owning project and puts its id into the context (spec §17).
// Keys are looked up by SHA-256 hash; the raw key never touches the database.
func AuthMiddleware(store *project.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(header, prefix) || len(header) == len(prefix) {
				WriteError(w, http.StatusUnauthorized, CodeUnauthorized,
					"missing or malformed Authorization header", RequestIDFromContext(r.Context()))
				return
			}
			rawKey := strings.TrimSpace(header[len(prefix):])

			proj, _, err := store.FindProjectByAPIKeyRaw(r.Context(), auth.HashKey(rawKey))
			if err != nil {
				WriteError(w, http.StatusUnauthorized, CodeUnauthorized,
					"invalid API key", RequestIDFromContext(r.Context()))
				return
			}

			ctx := context.WithValue(r.Context(), projectIDKey, proj.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ProjectIDFromContext returns the authenticated project id.
func ProjectIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	v, ok := ctx.Value(projectIDKey).(uuid.UUID)
	return v, ok
}

// RateLimitMiddleware enforces the per-project request rate (spec §16).
// Requests over the limit receive 429 RATE_LIMITED.
func RateLimitMiddleware(limiter *ratelimit.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			projID, ok := ProjectIDFromContext(r.Context())
			if !ok {
				// Unauthenticated endpoints (health) skip limiting.
				next.ServeHTTP(w, r)
				return
			}
			if !limiter.Allow(projID.String()) {
				WriteError(w, http.StatusTooManyRequests, CodeRateLimited,
					"rate limit exceeded", RequestIDFromContext(r.Context()))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
