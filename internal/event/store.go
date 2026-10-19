package event

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/IvanZhutyaev/EventFlow/internal/dbutil"
	"github.com/google/uuid"
)

// ErrNotFound is returned when an event does not exist (or belongs to another project).
var ErrNotFound = errors.New("event: not found")

// Store persists events in PostgreSQL.
type Store struct {
	db *sql.DB
}

// NewStore builds a Store on top of the shared *sql.DB.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create inserts a new event. Returns ErrIdempotencyConflict when the
// (project_id, idempotency_key) pair already exists.
var ErrIdempotencyConflict = errors.New("event: idempotency conflict")

// Create inserts the event, relying on the UNIQUE (project_id, idempotency_key)
// constraint for idempotency (spec §11).
func (s *Store) Create(ctx context.Context, e *Event) error {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO events (id, project_id, event_type, source, subject, idempotency_key, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at`,
		e.ID, e.ProjectID, e.EventType, e.Source, e.Subject, e.IdempotencyKey, e.Data,
	).Scan(&e.CreatedAt)
	if err != nil {
		// Unique violation on (project_id, idempotency_key).
		if dbutil.IsUniqueViolation(err) {
			return ErrIdempotencyConflict
		}
		return fmt.Errorf("event: insert: %w", err)
	}
	return nil
}

// GetByIdempotencyKey returns the previously stored event for the same key (spec §11).
func (s *Store) GetByIdempotencyKey(ctx context.Context, projectID uuid.UUID, key string) (*Event, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, event_type, source, subject, idempotency_key, payload, created_at
		FROM events
		WHERE project_id = $1 AND idempotency_key = $2`,
		projectID, key,
	)
	return scanEvent(row)
}

// GetByID returns an event scoped to its project (multi-tenancy, spec §17).
func (s *Store) GetByID(ctx context.Context, projectID, id uuid.UUID) (*Event, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, event_type, source, subject, idempotency_key, payload, created_at
		FROM events
		WHERE project_id = $1 AND id = $2`,
		projectID, id,
	)
	return scanEvent(row)
}

// List returns a page of events for the project, newest first.
func (s *Store) List(ctx context.Context, projectID uuid.UUID, page, limit int) ([]Event, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM events WHERE project_id = $1`, projectID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("event: count: %w", err)
	}

	offset := (page - 1) * limit
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, event_type, source, subject, idempotency_key, payload, created_at
		FROM events
		WHERE project_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`,
		projectID, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("event: list: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		var payload []byte
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.EventType, &e.Source, &e.Subject,
			&e.IdempotencyKey, &payload, &e.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("event: scan: %w", err)
		}
		e.Data = json.RawMessage(payload)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("event: rows: %w", err)
	}
	return out, total, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvent(row rowScanner) (*Event, error) {
	var e Event
	var payload []byte
	err := row.Scan(&e.ID, &e.ProjectID, &e.EventType, &e.Source, &e.Subject,
		&e.IdempotencyKey, &payload, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("event: scan: %w", err)
	}
	e.Data = json.RawMessage(payload)
	return &e, nil
}
