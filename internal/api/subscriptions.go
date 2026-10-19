package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/IvanZhutyaev/EventFlow/internal/subscription"
	"github.com/google/uuid"
)

// SubscriptionHandler handles the Subscriptions API (spec §7).
type SubscriptionHandler struct {
	Subs          *subscription.Store
	MaxPerProject int
}

// Create handles POST /api/v1/subscriptions.
func (h *SubscriptionHandler) Create(w http.ResponseWriter, r *http.Request) {
	reqID := RequestIDFromContext(r.Context())
	projID, ok := ProjectIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", reqID)
		return
	}

	var req subscription.Subscription
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid JSON body", reqID)
		return
	}

	if msg := validateSubscription(&req); msg != "" {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, msg, reqID)
		return
	}

	// Per-project subscription limit (spec §26).
	count, err := h.Subs.CountByProject(r.Context(), projID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to count subscriptions", reqID)
		return
	}
	if count >= h.MaxPerProject {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest,
			"subscription limit reached for this project", reqID)
		return
	}

	sub := &subscription.Subscription{
		ID:           uuid.New(),
		ProjectID:    projID,
		Name:         strings.TrimSpace(req.Name),
		EventPattern: strings.TrimSpace(req.EventPattern),
		Channel:      req.Channel,
		Destination:  strings.TrimSpace(req.Destination),
		Enabled:      req.Enabled,
	}
	if err := h.Subs.Create(r.Context(), sub); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to create subscription", reqID)
		return
	}
	WriteJSON(w, http.StatusCreated, sub)
}

// List handles GET /api/v1/subscriptions.
func (h *SubscriptionHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := RequestIDFromContext(r.Context())
	projID, ok := ProjectIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", reqID)
		return
	}

	p := ParsePagination(r)
	items, total, err := h.Subs.List(r.Context(), projID, p.Page, p.Limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to list subscriptions", reqID)
		return
	}
	WriteJSON(w, http.StatusOK, PageResult{Data: items, Page: p.Page, Limit: p.Limit, Total: total})
}

type subscriptionValidationError struct{ msg string }

func (e *subscriptionValidationError) Error() string { return e.msg }

// Get handles GET /api/v1/subscriptions/{id}.
func (h *SubscriptionHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := RequestIDFromContext(r.Context())
	projID, ok := ProjectIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", reqID)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid subscription id", reqID)
		return
	}
	sub, err := h.Subs.GetByID(r.Context(), projID, id)
	if err != nil {
		writeSubError(w, err, reqID)
		return
	}
	WriteJSON(w, http.StatusOK, sub)
}

// Patch handles PATCH /api/v1/subscriptions/{id} (partial update).
func (h *SubscriptionHandler) Patch(w http.ResponseWriter, r *http.Request) {
	reqID := RequestIDFromContext(r.Context())
	projID, ok := ProjectIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", reqID)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid subscription id", reqID)
		return
	}

	var patch subscription.Subscription
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid JSON body", reqID)
		return
	}

	sub, err := h.Subs.Update(r.Context(), projID, id, func(s *subscription.Subscription) error {
		if patch.Name != "" {
			s.Name = strings.TrimSpace(patch.Name)
		}
		if patch.EventPattern != "" {
			s.EventPattern = strings.TrimSpace(patch.EventPattern)
		}
		if patch.Channel != "" {
			s.Channel = patch.Channel
		}
		if patch.Destination != "" {
			s.Destination = strings.TrimSpace(patch.Destination)
		}
		s.Enabled = patch.Enabled
		if msg := validateSubscription(s); msg != "" {
			return &subscriptionValidationError{msg: msg}
		}
		return nil
	})
	if err != nil {
		var verr *subscriptionValidationError
		if errors.As(err, &verr) {
			WriteError(w, http.StatusBadRequest, CodeInvalidRequest, verr.Error(), reqID)
			return
		}
		writeSubError(w, err, reqID)
		return
	}
	WriteJSON(w, http.StatusOK, sub)
}

// Delete handles DELETE /api/v1/subscriptions/{id}.
func (h *SubscriptionHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := RequestIDFromContext(r.Context())
	projID, ok := ProjectIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", reqID)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid subscription id", reqID)
		return
	}
	if err := h.Subs.Delete(r.Context(), projID, id); err != nil {
		writeSubError(w, err, reqID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validateSubscription enforces spec §7 semantics; returns "" when valid.
func validateSubscription(s *subscription.Subscription) string {
	if strings.TrimSpace(s.Name) == "" {
		return "name is required"
	}
	if strings.TrimSpace(s.EventPattern) == "" {
		return "event_pattern is required"
	}
	switch s.Channel {
	case subscription.ChannelWebhook, subscription.ChannelEmail, subscription.ChannelTelegram:
	default:
		return "channel must be one of: webhook, email, telegram"
	}
	if strings.TrimSpace(s.Destination) == "" {
		return "destination is required"
	}
	if s.Channel == subscription.ChannelWebhook {
		u, err := url.Parse(s.Destination)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "destination must be a valid http(s) URL for webhook channel"
		}
	}
	return ""
}

func writeSubError(w http.ResponseWriter, err error, reqID string) {
	switch {
	case errors.Is(err, subscription.ErrNotFound):
		WriteError(w, http.StatusNotFound, CodeNotFound, "subscription not found", reqID)
	default:
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "subscription operation failed", reqID)
	}
}
