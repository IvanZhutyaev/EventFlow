// Package broker wraps NATS JetStream: stream setup, publishing delivery jobs
// and consuming them with durable consumers and explicit acknowledgement (spec §14).
package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Subjects used by the platform (spec §14).
const (
	SubjectEventsReceived    = "eventflow.events.received"
	SubjectDeliveriesWebhook = "eventflow.deliveries.webhook"
	SubjectDeliveriesRetry   = "eventflow.deliveries.retry"
	SubjectDeliveriesDead    = "eventflow.deliveries.dead"
	StreamName               = "EVENTFLOW"
	WebhookConsumerDurable   = "webhook-worker"
)

// DeliveryJob is the message published to NATS for each delivery.
type DeliveryJob struct {
	DeliveryID uuid.UUID `json:"delivery_id"`
	// Retry marks jobs re-published after a backoff; informational only.
	Retry bool `json:"retry,omitempty"`
}

// Broker is a JetStream-backed message bus.
type Broker struct {
	nc  *nats.Conn
	js  jetstream.JetStream
	log *slog.Logger
}

// New connects to NATS and initializes JetStream.
func New(url string, log *slog.Logger) (*Broker, error) {
	nc, err := nats.Connect(url,
		nats.Name("eventflow"),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return nil, fmt.Errorf("broker: connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("broker: jetstream: %w", err)
	}
	return &Broker{nc: nc, js: js, log: log}, nil
}

// EnsureStream creates the platform stream if it does not exist yet.
// The stream keeps messages durable across consumer and server restarts.
func (b *Broker) EnsureStream(ctx context.Context) error {
	_, err := b.js.CreateStream(ctx, jetstream.StreamConfig{
		Name:      StreamName,
		Subjects:  []string{"eventflow.>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    24 * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("broker: ensure stream: %w", err)
	}
	return nil
}

// PublishJob publishes a delivery job to the webhook subject.
func (b *Broker) PublishJob(ctx context.Context, job DeliveryJob) error {
	return b.publish(ctx, SubjectDeliveriesWebhook, job)
}

// PublishRetry publishes a delivery job to the retry subject.
func (b *Broker) PublishRetry(ctx context.Context, job DeliveryJob) error {
	job.Retry = true
	return b.publish(ctx, SubjectDeliveriesRetry, job)
}

// PublishDead publishes a dead-letter notification (informational).
func (b *Broker) PublishDead(ctx context.Context, job DeliveryJob) error {
	return b.publish(ctx, SubjectDeliveriesDead, job)
}

func (b *Broker) publish(ctx context.Context, subject string, job DeliveryJob) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("broker: marshal job: %w", err)
	}
	if _, err := b.js.Publish(ctx, subject, data); err != nil {
		return fmt.Errorf("broker: publish %s: %w", subject, err)
	}
	return nil
}

// WebhookConsumer returns the durable consumer used by webhook workers.
// Explicit acknowledgement: a message is acked only after the delivery result
// is persisted; otherwise JetStream redelivers it (at-least-once, spec §14).
func (b *Broker) WebhookConsumer(ctx context.Context) (jetstream.Consumer, error) {
	consumer, err := b.js.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
		Durable:       WebhookConsumerDurable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: SubjectDeliveriesWebhook,
		MaxDeliver:    -1,
		AckWait:       60 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("broker: create consumer: %w", err)
	}
	return consumer, nil
}

// Healthy checks the NATS connection state.
func (b *Broker) Healthy() bool {
	return b.nc != nil && b.nc.IsConnected()
}

// Close drains and closes the NATS connection (graceful shutdown, spec §30).
func (b *Broker) Close() {
	if b.nc != nil {
		// Drain errors are expected during shutdown; nothing to do with them.
		_ = b.nc.Drain()
		b.nc.Close()
	}
}
