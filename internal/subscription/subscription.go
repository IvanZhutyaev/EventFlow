// Package subscription defines subscriptions and event pattern matching.
package subscription

import (
	"time"

	"github.com/google/uuid"
)

// Channel identifiers supported by the platform.
const (
	ChannelWebhook  = "webhook"
	ChannelEmail    = "email"
	ChannelTelegram = "telegram"
)

// Subscription binds an event pattern to a delivery destination (spec §7).
type Subscription struct {
	ID           uuid.UUID `json:"id"`
	ProjectID    uuid.UUID `json:"project_id"`
	Name         string    `json:"name"`
	EventPattern string    `json:"event_pattern"`
	Channel      string    `json:"channel"`
	Destination  string    `json:"destination"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
