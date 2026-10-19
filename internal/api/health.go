package api

import (
	"database/sql"
	"net/http"

	"github.com/IvanZhutyaev/EventFlow/internal/broker"
)

// HealthHandler exposes liveness and readiness endpoints (spec §19).
type HealthHandler struct {
	DB     *sql.DB
	Broker *broker.Broker
}

// Live is a simple liveness probe: the process is up.
func (h *HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Ready checks PostgreSQL and NATS; any unavailable dependency yields 503 (spec §19).
func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	deps := map[string]string{}

	ctx := r.Context()
	if err := h.DB.PingContext(ctx); err != nil {
		deps["postgres"] = "unavailable"
	} else {
		deps["postgres"] = "ok"
	}
	if h.Broker == nil || !h.Broker.Healthy() {
		deps["nats"] = "unavailable"
	} else {
		deps["nats"] = "ok"
	}

	status := http.StatusOK
	for _, v := range deps {
		if v != "ok" {
			status = http.StatusServiceUnavailable
			break
		}
	}
	WriteJSON(w, status, map[string]any{"status": http.StatusText(status), "dependencies": deps})
}
