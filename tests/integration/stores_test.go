//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/IvanZhutyaev/EventFlow/internal/delivery"
	"github.com/IvanZhutyaev/EventFlow/internal/event"
	"github.com/IvanZhutyaev/EventFlow/internal/project"
	"github.com/IvanZhutyaev/EventFlow/internal/subscription"
	"github.com/google/uuid"
)

func TestEventIdempotency(t *testing.T) {
	db := setupDB(t)
	applyMigrations(t, db)
	ctx := context.Background()

	projID := seedProject(t, db)
	eventStore := event.NewStore(db)

	req := &event.CreateRequest{EventType: "order.created", Source: "shop", Data: []byte(`{"a":1}`)}
	key := "idem-" + uuid.NewString()

	svc := event.NewService(db, eventStore, subscription.NewStore(db), delivery.NewStore(db), noopPublisher{}, discardLogger())

	res1, err := svc.Ingest(ctx, projID, req, key)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if !res1.Created {
		t.Error("first ingest must create the event")
	}

	// Replaying the same Idempotency-Key returns the original event (spec §11).
	res2, err := svc.Ingest(ctx, projID, req, key)
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if res2.Created {
		t.Error("replay must not create a new event")
	}
	if res2.Event.ID != res1.Event.ID {
		t.Errorf("replay must return the original event: got %s want %s", res2.Event.ID, res1.Event.ID)
	}
}

func TestSubscriptionMatchingCreatesDeliveries(t *testing.T) {
	db := setupDB(t)
	applyMigrations(t, db)
	ctx := context.Background()

	projID := seedProject(t, db)
	eventStore := event.NewStore(db)
	subStore := subscription.NewStore(db)
	delStore := delivery.NewStore(db)

	matching := &subscription.Subscription{
		ID: uuid.New(), ProjectID: projID, Name: "orders",
		EventPattern: "order.*", Channel: "webhook",
		Destination: "http://localhost:9090/hook", Enabled: true,
	}
	if err := subStore.Create(ctx, matching); err != nil {
		t.Fatalf("create matching sub: %v", err)
	}
	other := &subscription.Subscription{
		ID: uuid.New(), ProjectID: projID, Name: "users",
		EventPattern: "user.*", Channel: "webhook",
		Destination: "http://localhost:9090/hook", Enabled: true,
	}
	if err := subStore.Create(ctx, other); err != nil {
		t.Fatalf("create other sub: %v", err)
	}

	svc := event.NewService(db, eventStore, subStore, delStore, noopPublisher{}, discardLogger())
	res, err := svc.Ingest(ctx, projID, &event.CreateRequest{EventType: "order.created", Source: "shop"}, "k-"+uuid.NewString())
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if res.Deliveries != 1 {
		t.Errorf("deliveries = %d, want 1 (only order.* matches)", res.Deliveries)
	}

	items, total, err := delStore.List(ctx, delivery.Filter{ProjectID: projID, Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Errorf("stored deliveries = %d/%d, want 1", len(items), total)
	}
	if items[0].Status != delivery.StatusPending {
		t.Errorf("status = %q, want pending", items[0].Status)
	}
}

func TestTenancyIsolation(t *testing.T) {
	db := setupDB(t)
	applyMigrations(t, db)
	ctx := context.Background()

	projA := seedProject(t, db)
	projB := seedProject(t, db)

	eventStore := event.NewStore(db)
	e := &event.Event{
		ID: uuid.New(), ProjectID: projA, EventType: "t", Source: "s",
		IdempotencyKey: uuid.NewString(), Data: []byte("{}"),
	}
	if err := eventStore.Create(ctx, e); err != nil {
		t.Fatalf("create event: %v", err)
	}

	// Project B must not see project A's event (spec §17).
	if _, err := eventStore.GetByID(ctx, projB, e.ID); err != event.ErrNotFound {
		t.Errorf("cross-project read must return ErrNotFound, got %v", err)
	}
	if _, err := eventStore.GetByID(ctx, projA, e.ID); err != nil {
		t.Errorf("owner read must succeed, got %v", err)
	}
}

func TestApiKeyLookup(t *testing.T) {
	db := setupDB(t)
	applyMigrations(t, db)
	ctx := context.Background()

	projID := seedProject(t, db)
	store := project.NewStore(db)

	_, hash, err := generateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, projID, "default", hash); err != nil {
		t.Fatalf("create api key: %v", err)
	}

	p, k, err := store.FindProjectByAPIKeyRaw(ctx, hash)
	if err != nil {
		t.Fatalf("find by key: %v", err)
	}
	if p.ID != projID || k.ProjectID != projID {
		t.Error("key must resolve to the owning project")
	}

	if _, _, err := store.FindProjectByAPIKeyRaw(ctx, "deadbeef"); err == nil {
		t.Error("unknown key must fail")
	}
}

func TestDeliveryLifecycle(t *testing.T) {
	db := setupDB(t)
	applyMigrations(t, db)
	ctx := context.Background()

	projID := seedProject(t, db)
	eventStore := event.NewStore(db)
	subStore := subscription.NewStore(db)
	delStore := delivery.NewStore(db)

	sub := &subscription.Subscription{
		ID: uuid.New(), ProjectID: projID, Name: "n", EventPattern: "*",
		Channel: "webhook", Destination: "http://localhost:9090/h", Enabled: true,
	}
	if err := subStore.Create(ctx, sub); err != nil {
		t.Fatalf("create sub: %v", err)
	}

	e := &event.Event{
		ID: uuid.New(), ProjectID: projID, EventType: "x.y", Source: "s",
		IdempotencyKey: uuid.NewString(), Data: []byte(`{}`),
	}
	if err := eventStore.Create(ctx, e); err != nil {
		t.Fatalf("create event: %v", err)
	}

	d := &delivery.Delivery{
		ID: uuid.New(), EventID: e.ID, SubscriptionID: sub.ID, Status: delivery.StatusPending,
	}
	if err := delStore.Create(ctx, nil, d); err != nil {
		t.Fatalf("create delivery: %v", err)
	}

	// Worker view must include event payload + destination.
	rel, err := delStore.GetForWorker(ctx, d.ID)
	if err != nil {
		t.Fatalf("get for worker: %v", err)
	}
	if rel.EventType != "x.y" || rel.Destination == "" {
		t.Errorf("worker relations incomplete: %+v", rel)
	}

	attempt := &delivery.Attempt{
		ID: uuid.New(), DeliveryID: d.ID, AttemptNumber: 1,
		Status: delivery.AttemptFailed, StartedAt: time.Now(), FinishedAt: time.Now(),
	}
	if err := delStore.RecordAttempt(ctx, attempt); err != nil {
		t.Fatalf("record attempt: %v", err)
	}
	if err := delStore.MarkRetrying(ctx, d.ID, 1, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("mark retrying: %v", err)
	}
	if err := delStore.MarkDeadLetter(ctx, d.ID, 5, "max_attempts_exhausted", "timeout"); err != nil {
		t.Fatalf("mark dead letter: %v", err)
	}

	got, err := delStore.GetByID(ctx, projID, d.ID)
	if err != nil {
		t.Fatalf("get delivery: %v", err)
	}
	if got.Status != delivery.StatusDeadLetter {
		t.Errorf("status = %q, want dead_letter", got.Status)
	}

	var dlCount int
	db.QueryRow(`SELECT count(*) FROM dead_letters WHERE delivery_id = $1`, d.ID).Scan(&dlCount)
	if dlCount != 1 {
		t.Errorf("dead_letters rows = %d, want 1", dlCount)
	}
}
