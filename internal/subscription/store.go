package subscription

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a subscription does not exist in the project.
var ErrNotFound = errors.New("subscription: not found")

// ErrLimitExceeded is returned when the project already has the maximum number of subscriptions.
var ErrLimitExceeded = errors.New("subscription: limit exceeded")

// Store persists subscriptions in PostgreSQL.
type Store struct {
	db *sql.DB
}

// NewStore builds a Store on top of the shared *sql.DB.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create inserts a new subscription.
func (s *Store) Create(ctx context.Context, sub *Subscription) error {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO subscriptions (id, project_id, name, event_pattern, channel, destination, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at`,
		sub.ID, sub.ProjectID, sub.Name, sub.EventPattern, sub.Channel, sub.Destination, sub.Enabled,
	).Scan(&sub.CreatedAt, &sub.UpdatedAt)
	if err != nil {
		return fmt.Errorf("subscription: insert: %w", err)
	}
	return nil
}

// GetByID returns a subscription scoped to its project.
func (s *Store) GetByID(ctx context.Context, projectID, id uuid.UUID) (*Subscription, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, name, event_pattern, channel, destination, enabled, created_at, updated_at
		FROM subscriptions
		WHERE project_id = $1 AND id = $2`,
		projectID, id,
	)
	return scan(row)
}

// List returns a page of subscriptions for the project.
func (s *Store) List(ctx context.Context, projectID uuid.UUID, page, limit int) ([]Subscription, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM subscriptions WHERE project_id = $1`, projectID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("subscription: count: %w", err)
	}

	offset := (page - 1) * limit
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, name, event_pattern, channel, destination, enabled, created_at, updated_at
		FROM subscriptions
		WHERE project_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`,
		projectID, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("subscription: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Subscription
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.ID, &sub.ProjectID, &sub.Name, &sub.EventPattern, &sub.Channel,
			&sub.Destination, &sub.Enabled, &sub.CreatedAt, &sub.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("subscription: scan: %w", err)
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("subscription: rows: %w", err)
	}
	return out, total, nil
}

// ListEnabledByProject returns all enabled subscriptions for matching.
func (s *Store) ListEnabledByProject(ctx context.Context, projectID uuid.UUID) ([]Subscription, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, name, event_pattern, channel, destination, enabled, created_at, updated_at
		FROM subscriptions
		WHERE project_id = $1 AND enabled`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("subscription: list enabled: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Subscription
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.ID, &sub.ProjectID, &sub.Name, &sub.EventPattern, &sub.Channel,
			&sub.Destination, &sub.Enabled, &sub.CreatedAt, &sub.UpdatedAt); err != nil {
			return nil, fmt.Errorf("subscription: scan: %w", err)
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// CountByProject returns the number of subscriptions in a project (for the per-project limit).
func (s *Store) CountByProject(ctx context.Context, projectID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM subscriptions WHERE project_id = $1`, projectID).Scan(&n)
	return n, err
}

// Update applies a partial update via the provided mutation function.
func (s *Store) Update(ctx context.Context, projectID, id uuid.UUID, mutate func(*Subscription) error) (*Subscription, error) {
	sub, err := s.GetByID(ctx, projectID, id)
	if err != nil {
		return nil, err
	}
	if err := mutate(sub); err != nil {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx, `
		UPDATE subscriptions
		SET name = $3, event_pattern = $4, channel = $5, destination = $6, enabled = $7, updated_at = now()
		WHERE project_id = $1 AND id = $2
		RETURNING updated_at`,
		projectID, id, sub.Name, sub.EventPattern, sub.Channel, sub.Destination, sub.Enabled,
	).Scan(&sub.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("subscription: update: %w", err)
	}
	return sub, nil
}

// Delete removes a subscription (hard delete; spec §7 DELETE).
func (s *Store) Delete(ctx context.Context, projectID, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM subscriptions WHERE project_id = $1 AND id = $2`, projectID, id)
	if err != nil {
		return fmt.Errorf("subscription: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scan(row rowScanner) (*Subscription, error) {
	var sub Subscription
	err := row.Scan(&sub.ID, &sub.ProjectID, &sub.Name, &sub.EventPattern, &sub.Channel,
		&sub.Destination, &sub.Enabled, &sub.CreatedAt, &sub.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("subscription: scan: %w", err)
	}
	return &sub, nil
}
