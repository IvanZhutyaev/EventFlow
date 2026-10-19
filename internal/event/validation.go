package event

import (
	"encoding/json"
	"errors"
	"strings"
)

// Validation errors map to INVALID_REQUEST (spec §25–26).
var (
	ErrTypeRequired   = errors.New("event_type is required")
	ErrTypeTooLong    = errors.New("event_type must be at most 128 characters")
	ErrSourceRequired = errors.New("source is required")
	ErrSourceTooLong  = errors.New("source must be at most 128 characters")
	ErrSubjectTooLong = errors.New("subject must be at most 256 characters")
	ErrDataNotJSON    = errors.New("data must be valid JSON")
)

// CreateRequest is the payload of POST /api/v1/events (spec §6).
type CreateRequest struct {
	EventType string          `json:"type"`
	Source    string          `json:"source"`
	Subject   string          `json:"subject"`
	Data      json.RawMessage `json:"data"`
}

// Validate checks the request against the documented limits (spec §26).
func (r *CreateRequest) Validate() error {
	if strings.TrimSpace(r.EventType) == "" {
		return ErrTypeRequired
	}
	if len(r.EventType) > 128 {
		return ErrTypeTooLong
	}
	if strings.TrimSpace(r.Source) == "" {
		return ErrSourceRequired
	}
	if len(r.Source) > 128 {
		return ErrSourceTooLong
	}
	if len(r.Subject) > 256 {
		return ErrSubjectTooLong
	}
	if len(r.Data) > 0 && !json.Valid(r.Data) {
		return ErrDataNotJSON
	}
	return nil
}
