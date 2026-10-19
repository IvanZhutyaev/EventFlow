// Package delivery defines delivery jobs, attempts, retry policy and
// dead-letter records.
package delivery

import (
	"time"

	"github.com/google/uuid"
)

// Delivery statuses.
const (
	StatusPending    = "pending"
	StatusRetrying   = "retrying"
	StatusDelivered  = "delivered"
	StatusDeadLetter = "dead_letter"
)

// Delivery is a unit of work: deliver one event to one subscription (spec §5).
type Delivery struct {
	ID             uuid.UUID  `json:"id"`
	EventID        uuid.UUID  `json:"event_id"`
	SubscriptionID uuid.UUID  `json:"subscription_id"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// Attempt records one delivery try (spec §5, delivery_attempts).
type Attempt struct {
	ID            uuid.UUID `json:"id"`
	DeliveryID    uuid.UUID `json:"delivery_id"`
	AttemptNumber int       `json:"attempt_number"`
	Status        string    `json:"status"`
	HTTPStatus    *int      `json:"http_status,omitempty"`
	ErrorCode     *string   `json:"error_code,omitempty"`
	ErrorMessage  *string   `json:"error_message,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
}

// Attempt statuses.
const (
	AttemptSuccess = "success"
	AttemptFailed  = "failed"
)

// DeadLetter stores a delivery that exhausted retries or failed permanently (spec §5).
type DeadLetter struct {
	ID         uuid.UUID  `json:"id"`
	DeliveryID uuid.UUID  `json:"delivery_id"`
	Reason     string     `json:"reason"`
	LastError  string     `json:"last_error"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}
