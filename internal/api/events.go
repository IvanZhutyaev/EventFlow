package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/IvanZhutyaev/EventFlow/internal/event"
	"github.com/google/uuid"
)

// EventHandler handles the Event API (spec §6).
type EventHandler struct {
	Events       *event.Store
	Service      *event.Service
	MaxBodyBytes int64
}

// Create handles POST /api/v1/events.
// Requires Authorization (project scope) and an Idempotency-Key header (spec §6, §11).
func (h *EventHandler) Create(w http.ResponseWriter, r *http.Request) {
	reqID := RequestIDFromContext(r.Context())
	projID, ok := ProjectIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", reqID)
		return
	}

	idemKey := r.Header.Get("Idempotency-Key")
	if idemKey == "" {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "Idempotency-Key header is required", reqID)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.MaxBodyBytes)
	var req event.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid JSON body", reqID)
		return
	}

	res, err := h.Service.Ingest(r.Context(), projID, &req, idemKey)
	if err != nil {
		if isValidationError(err) {
			WriteError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error(), reqID)
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to process event", reqID)
		return
	}

	// 202 Accepted both for new events and idempotent replays (spec §6, §11).
	WriteJSON(w, http.StatusAccepted, map[string]any{
		"id":      res.Event.ID,
		"status":  "accepted",
		"created": res.Created,
	})
}

// Get handles GET /api/v1/events/{eventID}.
func (h *EventHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := RequestIDFromContext(r.Context())
	projID, ok := ProjectIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", reqID)
		return
	}

	id, err := uuid.Parse(r.PathValue("eventID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid event id", reqID)
		return
	}

	e, err := h.Events.GetByID(r.Context(), projID, id)
	if err != nil {
		if errors.Is(err, event.ErrNotFound) {
			WriteError(w, http.StatusNotFound, CodeNotFound, "event not found", reqID)
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to load event", reqID)
		return
	}
	WriteJSON(w, http.StatusOK, e)
}

// List handles GET /api/v1/events with page/limit pagination.
func (h *EventHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := RequestIDFromContext(r.Context())
	projID, ok := ProjectIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", reqID)
		return
	}

	p := ParsePagination(r)
	items, total, err := h.Events.List(r.Context(), projID, p.Page, p.Limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to list events", reqID)
		return
	}
	WriteJSON(w, http.StatusOK, PageResult{Data: items, Page: p.Page, Limit: p.Limit, Total: total})
}
