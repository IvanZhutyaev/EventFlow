package delivery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a delivery does not exist in the project scope.
var ErrNotFound = errors.New("delivery: not found")

// Store persists deliveries, attempts and dead letters in PostgreSQL.
type Store struct {
	db *sql.DB
}

// NewStore builds a Store on top of the shared *sql.DB.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create inserts a pending delivery. When tx is non-nil the insert joins the
// surrounding transaction (event + deliveries commit atomically).
func (s *Store) Create(ctx context.Context, tx *sql.Tx, d *Delivery) error {
	var err error
	if tx != nil {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO deliveries (id, event_id, subscription_id, status, attempts, next_attempt_at)
			VALUES ($1, $2, $3, $4, 0, now())`,
			d.ID, d.EventID, d.SubscriptionID, d.Status,
		)
	} else {
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO deliveries (id, event_id, subscription_id, status, attempts, next_attempt_at)
			VALUES ($1, $2, $3, $4, 0, now())`,
			d.ID, d.EventID, d.SubscriptionID, d.Status,
		)
	}
	if err != nil {
		return fmt.Errorf("delivery: insert: %w", err)
	}
	return nil
}

// GetByID returns a delivery joined with its event's project for tenancy checks (spec §17).
func (s *Store) GetByID(ctx context.Context, projectID, id uuid.UUID) (*Delivery, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT d.id, d.event_id, d.subscription_id, d.status, d.attempts, d.next_attempt_at, d.created_at, d.completed_at
		FROM deliveries d
		JOIN events e ON e.id = d.event_id
		WHERE e.project_id = $1 AND d.id = $2`,
		projectID, id,
	)
	return scanDelivery(row)
}

// Filter narrows the delivery list (spec §8).
type Filter struct {
	ProjectID      uuid.UUID
	Status         string
	EventID        *uuid.UUID
	SubscriptionID *uuid.UUID
	Page           int
	Limit          int
}

// List returns a filtered page of deliveries for the project.
func (s *Store) List(ctx context.Context, f Filter) ([]Delivery, int, error) {
	conds := []string{"e.project_id = $1"}
	args := []any{f.ProjectID}
	argIdx := 2

	if f.Status != "" {
		conds = append(conds, fmt.Sprintf("d.status = $%d", argIdx))
		args = append(args, f.Status)
		argIdx++
	}
	if f.EventID != nil {
		conds = append(conds, fmt.Sprintf("d.event_id = $%d", argIdx))
		args = append(args, *f.EventID)
		argIdx++
	}
	if f.SubscriptionID != nil {
		conds = append(conds, fmt.Sprintf("d.subscription_id = $%d", argIdx))
		args = append(args, *f.SubscriptionID)
		argIdx++
	}
	where := strings.Join(conds, " AND ")

	var total int
	countQ := fmt.Sprintf(`SELECT count(*) FROM deliveries d JOIN events e ON e.id = d.event_id WHERE %s`, where)
	if err := s.db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("delivery: count: %w", err)
	}

	offset := (f.Page - 1) * f.Limit
	listQ := fmt.Sprintf(`
		SELECT d.id, d.event_id, d.subscription_id, d.status, d.attempts, d.next_attempt_at, d.created_at, d.completed_at
		FROM deliveries d
		JOIN events e ON e.id = d.event_id
		WHERE %s
		ORDER BY d.created_at DESC
		LIMIT $%d OFFSET $%d`, where, argIdx, argIdx+1)
	args = append(args, f.Limit, offset)

	rows, err := s.db.QueryContext(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("delivery: list: %w", err)
	}
	defer rows.Close()

	var out []Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("delivery: rows: %w", err)
	}
	return out, total, nil
}

// RecordAttempt stores one delivery attempt (spec §5).
func (s *Store) RecordAttempt(ctx context.Context, a *Attempt) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO delivery_attempts (id, delivery_id, attempt_number, status, http_status, error_code, error_message, started_at, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		a.ID, a.DeliveryID, a.AttemptNumber, a.Status, a.HTTPStatus, a.ErrorCode, a.ErrorMessage, a.StartedAt, a.FinishedAt,
	)
	if err != nil {
		return fmt.Errorf("delivery: record attempt: %w", err)
	}
	return nil
}

// ListAttempts returns all attempts of a delivery ordered by attempt number.
func (s *Store) ListAttempts(ctx context.Context, deliveryID uuid.UUID) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, delivery_id, attempt_number, status, http_status, error_code, error_message, started_at, finished_at
		FROM delivery_attempts
		WHERE delivery_id = $1
		ORDER BY attempt_number`, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("delivery: list attempts: %w", err)
	}
	defer rows.Close()

	var out []Attempt
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.ID, &a.DeliveryID, &a.AttemptNumber, &a.Status, &a.HTTPStatus,
			&a.ErrorCode, &a.ErrorMessage, &a.StartedAt, &a.FinishedAt); err != nil {
			return nil, fmt.Errorf("delivery: scan attempt: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("delivery: attempts rows: %w", err)
	}
	return out, nil
}

// MarkDelivered transitions the delivery to "delivered".
func (s *Store) MarkDelivered(ctx context.Context, id uuid.UUID, attempts int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries
		SET status = $2, attempts = $3, next_attempt_at = NULL, completed_at = now()
		WHERE id = $1 AND status <> $4`,
		id, StatusDelivered, attempts, StatusDeadLetter,
	)
	if err != nil {
		return fmt.Errorf("delivery: mark delivered: %w", err)
	}
	return nil
}

// MarkRetrying schedules the next attempt (spec §9).
func (s *Store) MarkRetrying(ctx context.Context, id uuid.UUID, attempts int, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries
		SET status = $2, attempts = $3, next_attempt_at = $4
		WHERE id = $1 AND status <> $5`,
		id, StatusRetrying, attempts, next, StatusDeadLetter,
	)
	if err != nil {
		return fmt.Errorf("delivery: mark retrying: %w", err)
	}
	return nil
}

// MarkDeadLetter moves the delivery to "dead_letter" and records the reason (spec §9).
func (s *Store) MarkDeadLetter(ctx context.Context, id uuid.UUID, attempts int, reason, lastError string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delivery: begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		UPDATE deliveries
		SET status = $2, attempts = $3, next_attempt_at = NULL, completed_at = now()
		WHERE id = $1`,
		id, StatusDeadLetter, attempts,
	)
	if err != nil {
		return fmt.Errorf("delivery: mark dead letter: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO dead_letters (delivery_id, reason, last_error)
		VALUES ($1, $2, $3)`,
		id, reason, lastError,
	)
	if err != nil {
		return fmt.Errorf("delivery: insert dead letter: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delivery: commit: %w", err)
	}
	return nil
}

// DeliveryWithRelations bundles a delivery with its event payload and
// subscription destination — everything the worker needs for one attempt,
// even after a restart.
type DeliveryWithRelations struct {
	Delivery     Delivery
	ProjectID    uuid.UUID
	EventType    string
	EventSource  string
	EventSubject string
	EventData    []byte
	Destination  string
	Channel      string
}

// GetForWorker loads a delivery together with event payload and subscription destination.
func (s *Store) GetForWorker(ctx context.Context, deliveryID uuid.UUID) (*DeliveryWithRelations, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT d.id, d.event_id, d.subscription_id, d.status, d.attempts, d.next_attempt_at, d.created_at, d.completed_at,
		       e.project_id, e.event_type, e.source, e.subject, e.payload,
		       sub.destination, sub.channel
		FROM deliveries d
		JOIN events e ON e.id = d.event_id
		JOIN subscriptions sub ON sub.id = d.subscription_id
		WHERE d.id = $1`, deliveryID)

	var out DeliveryWithRelations
	err := row.Scan(&out.Delivery.ID, &out.Delivery.EventID, &out.Delivery.SubscriptionID,
		&out.Delivery.Status, &out.Delivery.Attempts, &out.Delivery.NextAttemptAt,
		&out.Delivery.CreatedAt, &out.Delivery.CompletedAt,
		&out.ProjectID, &out.EventType, &out.EventSource, &out.EventSubject, &out.EventData,
		&out.Destination, &out.Channel)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("delivery: load for worker: %w", err)
	}
	return &out, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDelivery(row rowScanner) (*Delivery, error) {
	var d Delivery
	err := row.Scan(&d.ID, &d.EventID, &d.SubscriptionID, &d.Status, &d.Attempts,
		&d.NextAttemptAt, &d.CreatedAt, &d.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("delivery: scan: %w", err)
	}
	return &d, nil
}
