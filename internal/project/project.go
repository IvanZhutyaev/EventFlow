// Package project manages users, projects and API keys (spec §5, §17).
package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a project or API key does not exist.
var ErrNotFound = errors.New("project: not found")

// Project is a tenant owning events, subscriptions and deliveries.
type Project struct {
	ID          uuid.UUID `json:"id"`
	UserID      uuid.UUID `json:"user_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// APIKey is the credential of a project. Only the hash is stored (spec §17, §33).
type APIKey struct {
	ID         uuid.UUID  `json:"id"`
	ProjectID  uuid.UUID  `json:"project_id"`
	Name       string     `json:"name"`
	KeyHash    string     `json:"-"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// Store persists users, projects and API keys.
type Store struct {
	db *sql.DB
}

// NewStore builds a Store on top of the shared *sql.DB.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateUser creates (or finds) a user by email.
func (s *Store) CreateUser(ctx context.Context, email string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO users (email) VALUES ($1)
		ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id`, email).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("project: create user: %w", err)
	}
	return id, nil
}

// CreateProject inserts a project owned by the user.
func (s *Store) CreateProject(ctx context.Context, userID uuid.UUID, name, description string) (*Project, error) {
	p := &Project{UserID: userID, Name: name, Description: description}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO projects (user_id, name, description)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at`,
		userID, name, description,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("project: create: %w", err)
	}
	return p, nil
}

// CreateAPIKey stores the hash of a newly generated key and returns the record.
func (s *Store) CreateAPIKey(ctx context.Context, projectID uuid.UUID, name, keyHash string) (*APIKey, error) {
	k := &APIKey{ProjectID: projectID, Name: name, KeyHash: keyHash}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO api_keys (project_id, name, key_hash)
		VALUES ($1, $2, $3)
		RETURNING id, created_at`,
		projectID, name, keyHash,
	).Scan(&k.ID, &k.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("project: create api key: %w", err)
	}
	return k, nil
}

// GetProjectByID returns a project.
func (s *Store) GetProjectByID(ctx context.Context, id uuid.UUID) (*Project, error) {
	p := &Project{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, description, created_at, updated_at
		FROM projects WHERE id = $1`, id).
		Scan(&p.ID, &p.UserID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("project: get: %w", err)
	}
	return p, nil
}

// FindProjectByAPIKeyRaw looks up an active (non-revoked) API key by the hash of
// the raw key and returns the owning project. It also bumps last_used_at.
func (s *Store) FindProjectByAPIKeyRaw(ctx context.Context, keyHash string) (*Project, *APIKey, error) {
	k := &APIKey{}
	err := s.db.QueryRowContext(ctx, `
		UPDATE api_keys SET last_used_at = now()
		WHERE key_hash = $1 AND revoked_at IS NULL
		RETURNING id, project_id, name, created_at`, keyHash).
		Scan(&k.ID, &k.ProjectID, &k.Name, &k.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("project: find api key: %w", err)
	}

	p, err := s.GetProjectByID(ctx, k.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	return p, k, nil
}
