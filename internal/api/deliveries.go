package api

import (
	"errors"
	"net/http"

	"github.com/IvanZhutyaev/EventFlow/internal/delivery"
	"github.com/google/uuid"
)

// DeliveryHandler handles the Delivery API (spec §8).
type DeliveryHandler struct {
	Deliveries *delivery.Store
}

// List handles GET /api/v1/deliveries with filters status, event_id,
// subscription_id and pagination page/limit (spec §8).
func (h *DeliveryHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := RequestIDFromContext(r.Context())
	projID, ok := ProjectIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", reqID)
		return
	}

	q := r.URL.Query()
	p := ParsePagination(r)
	f := delivery.Filter{
		ProjectID: projID,
		Status:    q.Get("status"),
		Page:      p.Page,
		Limit:     p.Limit,
	}
	if v := q.Get("event_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid event_id filter", reqID)
			return
		}
		f.EventID = &id
	}
	if v := q.Get("subscription_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid subscription_id filter", reqID)
			return
		}
		f.SubscriptionID = &id
	}

	items, total, err := h.Deliveries.List(r.Context(), f)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to list deliveries", reqID)
		return
	}
	WriteJSON(w, http.StatusOK, PageResult{Data: items, Page: f.Page, Limit: f.Limit, Total: total})
}

// deliveryDetail is the enriched single-delivery view including attempts.
type deliveryDetail struct {
	delivery.Delivery
	Attempts []delivery.Attempt `json:"attempts"`
}

// Get handles GET /api/v1/deliveries/{id}.
func (h *DeliveryHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := RequestIDFromContext(r.Context())
	projID, ok := ProjectIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", reqID)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid delivery id", reqID)
		return
	}

	d, err := h.Deliveries.GetByID(r.Context(), projID, id)
	if err != nil {
		if errors.Is(err, delivery.ErrNotFound) {
			WriteError(w, http.StatusNotFound, CodeNotFound, "delivery not found", reqID)
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to load delivery", reqID)
		return
	}

	attempts, err := h.Deliveries.ListAttempts(r.Context(), d.ID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to load attempts", reqID)
		return
	}
	if attempts == nil {
		attempts = []delivery.Attempt{}
	}

	WriteJSON(w, http.StatusOK, deliveryDetail{Delivery: *d, Attempts: attempts})
}
