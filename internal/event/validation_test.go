package event

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func validRequest() *CreateRequest {
	return &CreateRequest{
		EventType: "order.created",
		Source:    "shop",
		Subject:   "order:9281",
		Data:      json.RawMessage(`{"order_id":9281}`),
	}
}

func TestCreateRequestValidate(t *testing.T) {
	if err := validRequest().Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	// Missing type.
	r := validRequest()
	r.EventType = ""
	if !errors.Is(r.Validate(), ErrTypeRequired) {
		t.Error("empty type must yield ErrTypeRequired")
	}

	// Type too long.
	r = validRequest()
	r.EventType = strings.Repeat("a", 129)
	if !errors.Is(r.Validate(), ErrTypeTooLong) {
		t.Error("129-char type must yield ErrTypeTooLong")
	}

	// Missing source.
	r = validRequest()
	r.Source = ""
	if !errors.Is(r.Validate(), ErrSourceRequired) {
		t.Error("empty source must yield ErrSourceRequired")
	}

	// Source too long.
	r = validRequest()
	r.Source = strings.Repeat("a", 129)
	if !errors.Is(r.Validate(), ErrSourceTooLong) {
		t.Error("129-char source must yield ErrSourceTooLong")
	}

	// Subject too long.
	r = validRequest()
	r.Subject = strings.Repeat("a", 257)
	if !errors.Is(r.Validate(), ErrSubjectTooLong) {
		t.Error("257-char subject must yield ErrSubjectTooLong")
	}

	// Invalid JSON data.
	r = validRequest()
	r.Data = json.RawMessage(`{invalid`)
	if !errors.Is(r.Validate(), ErrDataNotJSON) {
		t.Error("malformed JSON must yield ErrDataNotJSON")
	}

	// Empty data is allowed.
	r = validRequest()
	r.Data = nil
	if err := r.Validate(); err != nil {
		t.Errorf("nil data must be valid, got %v", err)
	}
}
