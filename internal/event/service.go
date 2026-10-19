package event

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/IvanZhutyaev/EventFlow/internal/broker"
	"github.com/IvanZhutyaev/EventFlow/internal/dbutil"
	"github.com/IvanZhutyaev/EventFlow/internal/delivery"
	"github.com/IvanZhutyaev/EventFlow/internal/subscription"
	"github.com/google/uuid"
)

// JobPublisher abstracts the message broker for testability.
type JobPublisher interface {
	PublishJob(ctx context.Context, job broker.DeliveryJob) error
}

// Service orchestrates event ingestion: validation, idempotent storage,
// subscription matching, delivery creation and job publishing (spec §6).
type Service struct {
	db        *sql.DB
	events    *Store
	subs      *subscription.Store
	delivery  *delivery.Store
	publisher JobPublisher
	log       *slog.Logger
}

// NewService wires the ingestion service.
func NewService(db *sql.DB, events *Store, subs *subscription.Store, del *delivery.Store, pub JobPublisher, log *slog.Logger) *Service {
	return &Service{db: db, events: events, subs: subs, delivery: del, publisher: pub, log: log}
}

// IngestResult reports what happened during ingestion.
type IngestResult struct {
	Event      *Event
	Created    bool // false when the idempotency key already existed
	Deliveries int  // number of matching subscriptions
}

// Ingest validates and stores the event, creates deliveries for every matching
// subscription and publishes delivery jobs to NATS. The (event, deliveries)
// pair is committed atomically; jobs are published after the commit — a crash
// in between leaves "pending" deliveries visible via the Delivery API.
func (s *Service) Ingest(ctx context.Context, projectID uuid.UUID, req *CreateRequest, idempotencyKey string) (*IngestResult, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	e := &Event{
		ID:             uuid.New(),
		ProjectID:      projectID,
		EventType:      req.EventType,
		Source:         req.Source,
		Subject:        req.Subject,
		IdempotencyKey: idempotencyKey,
		Data:           req.Data,
	}
	if len(e.Data) == 0 {
		e.Data = []byte("{}")
	}

	subs, err := s.subs.ListEnabledByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var matched []subscription.Subscription
	for _, sub := range subs {
		if subscription.Match(sub.EventPattern, req.EventType) {
			matched = append(matched, sub)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("event: begin tx: %w", err)
	}
	defer tx.Rollback()

	err = txQueryRow(ctx, tx, `
		INSERT INTO events (id, project_id, event_type, source, subject, idempotency_key, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at`,
		e.ID, e.ProjectID, e.EventType, e.Source, e.Subject, e.IdempotencyKey, e.Data,
	).Scan(&e.CreatedAt)
	if err != nil {
		if dbutil.IsUniqueViolation(err) {
			// Idempotent replay: return the original event (spec §11).
			existing, gerr := s.events.GetByIdempotencyKey(ctx, projectID, idempotencyKey)
			if gerr != nil {
				return nil, gerr
			}
			return &IngestResult{Event: existing, Created: false}, nil
		}
		return nil, fmt.Errorf("event: insert: %w", err)
	}

	jobs := make([]broker.DeliveryJob, 0, len(matched))
	for _, sub := range matched {
		d := &delivery.Delivery{
			ID:             uuid.New(),
			EventID:        e.ID,
			SubscriptionID: sub.ID,
			Status:         delivery.StatusPending,
		}
		if err := s.delivery.Create(ctx, tx, d); err != nil {
			return nil, err
		}
		jobs = append(jobs, broker.DeliveryJob{DeliveryID: d.ID})
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("event: commit: %w", err)
	}

	// Publish jobs after commit; failures leave deliveries pending for inspection.
	for _, job := range jobs {
		if err := s.publisher.PublishJob(ctx, job); err != nil {
			s.log.Error("failed to publish delivery job",
				"delivery_id", job.DeliveryID, "error", err)
		}
	}

	return &IngestResult{Event: e, Created: true, Deliveries: len(matched)}, nil
}

// txQueryRow mirrors *sql.Tx.QueryRowContext; kept local to avoid exposing tx plumbing.
func txQueryRow(ctx context.Context, tx *sql.Tx, query string, args ...any) *sql.Row {
	return tx.QueryRowContext(ctx, query, args...)
}
