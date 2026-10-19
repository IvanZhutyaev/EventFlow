// Package api hosts HTTP handlers, middleware and the router.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/IvanZhutyaev/EventFlow/internal/event"
)

// Error codes (spec §25).
const (
	CodeInvalidRequest = "INVALID_REQUEST"
	CodeUnauthorized   = "UNAUTHORIZED"
	CodeForbidden      = "FORBIDDEN"
	CodeNotFound       = "NOT_FOUND"
	CodeRateLimited    = "RATE_LIMITED"
	CodeInternalError  = "INTERNAL_ERROR"
)

// ErrorBody is the unified error envelope (spec §25).
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries code, human-readable message and the request id.
type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteError emits a unified error response.
func WriteError(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorBody{Error: ErrorDetail{
		Code:      code,
		Message:   message,
		RequestID: requestID,
	}})
}

// WriteJSON emits a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Pagination holds page/limit parameters (spec §27).
type Pagination struct {
	Page  int
	Limit int
}

// Default list endpoint bounds.
const (
	DefaultPage  = 1
	DefaultLimit = 20
	MaxLimit     = 100
)

// ParsePagination extracts page/limit with sane defaults and caps.
func ParsePagination(r *http.Request) Pagination {
	p := Pagination{Page: DefaultPage, Limit: DefaultLimit}
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.Page = n
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= MaxLimit {
			p.Limit = n
		}
	}
	return p
}

// PageResult is the list envelope with pagination metadata.
type PageResult struct {
	Data  any `json:"data"`
	Page  int `json:"page"`
	Limit int `json:"limit"`
	Total int `json:"total"`
}

// isValidationError reports whether err is one of the documented validation
// errors from the event package (spec §26).
func isValidationError(err error) bool {
	return errors.Is(err, event.ErrTypeRequired) ||
		errors.Is(err, event.ErrTypeTooLong) ||
		errors.Is(err, event.ErrSourceRequired) ||
		errors.Is(err, event.ErrSourceTooLong) ||
		errors.Is(err, event.ErrSubjectTooLong) ||
		errors.Is(err, event.ErrDataNotJSON)
}
