// Package event defines the event domain model, storage and ingestion service.
package event

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Event is an incoming fact from an external application (spec §6).
type Event struct {
	ID             uuid.UUID       `json:"id"`
	ProjectID      uuid.UUID       `json:"project_id"`
	EventType      string          `json:"type"`
	Source         string          `json:"source"`
	Subject        string          `json:"subject,omitempty"`
	IdempotencyKey string          `json:"-"`
	Data           json.RawMessage `json:"data"`
	CreatedAt      time.Time       `json:"created_at"`
}
