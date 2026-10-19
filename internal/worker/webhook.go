// Package worker implements the asynchronous delivery workers consuming
// delivery jobs from NATS JetStream (spec §14–15).
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/IvanZhutyaev/EventFlow/internal/broker"
	"github.com/IvanZhutyaev/EventFlow/internal/delivery"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

// webhookPayload is the JSON body delivered to webhook subscribers (spec §12).
type webhookPayload struct {
	Event webhookEvent    `json:"event"`
	Data  json.RawMessage `json:"data"`
}

type webhookEvent struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Source string `json:"source"`
}

// WebhookWorker consumes delivery jobs and performs HTTP webhook deliveries
// with HMAC signing, timeouts and retry classification (spec §9–12).
type WebhookWorker struct {
	deliveries  *delivery.Store
	consumer    jetstream.Consumer
	client      *http.Client
	policy      delivery.RetryPolicy
	secret      string
	log         *slog.Logger
	concurrency int
}

// NewWebhookWorker wires the worker dependencies.
func NewWebhookWorker(
	deliveries *delivery.Store,
	consumer jetstream.Consumer,
	policy delivery.RetryPolicy,
	secret string,
	timeout time.Duration,
	concurrency int,
	log *slog.Logger,
) *WebhookWorker {
	return &WebhookWorker{
		deliveries:  deliveries,
		consumer:    consumer,
		policy:      policy,
		secret:      secret,
		client:      &http.Client{Timeout: timeout},
		concurrency: concurrency,
		log:         log,
	}
}

// Run starts `concurrency` fetch loops and blocks until ctx is cancelled,
// letting in-flight jobs drain (graceful shutdown, spec §30).
func (w *WebhookWorker) Run(ctx context.Context) error {
	jobs := make(chan jetstream.Msg, w.concurrency)
	var wg sync.WaitGroup

	for i := 0; i < w.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.consumeLoop(ctx, jobs)
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		w.fetchLoop(ctx, jobs)
	}()

	<-ctx.Done()
	// Drain: close jobs after the fetch loop returns so workers exit gracefully.
	wg.Wait()
	return nil
}

// fetchLoop pulls batches from the durable consumer and fans jobs out to workers.
func (w *WebhookWorker) fetchLoop(ctx context.Context, jobs chan<- jetstream.Msg) {
	defer close(jobs)
	for ctx.Err() == nil {
		batch, err := w.consumer.Fetch(10, jetstream.FetchMaxWait(time.Second))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.log.Error("nats fetch failed", "error", err)
			time.Sleep(time.Second)
			continue
		}
		for msg := range batch.Messages() {
			select {
			case jobs <- msg:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (w *WebhookWorker) consumeLoop(ctx context.Context, jobs <-chan jetstream.Msg) {
	for msg := range jobs {
		w.handle(ctx, msg)
	}
}

// ack acknowledges a message, logging (but not propagating) ack errors:
// by the time we ack, the delivery result is already persisted.
func (w *WebhookWorker) ack(msg jetstream.Msg, deliveryID any) {
	if err := msg.Ack(); err != nil {
		w.log.Error("nats ack failed", "delivery_id", deliveryID, "error", err)
	}
}

// nak negatively acknowledges a message for redelivery, logging nak errors.
func (w *WebhookWorker) nak(msg jetstream.Msg, deliveryID any) {
	if err := msg.Nak(); err != nil {
		w.log.Error("nats nak failed", "delivery_id", deliveryID, "error", err)
	}
}

// nakDelay negatively acknowledges a message with a redelivery delay,
// logging errors.
func (w *WebhookWorker) nakDelay(msg jetstream.Msg, deliveryID any, delay time.Duration) {
	if err := msg.NakWithDelay(delay); err != nil {
		w.log.Error("nats nak with delay failed", "delivery_id", deliveryID, "delay", delay.String(), "error", err)
	}
}

// handle processes a single delivery job end to end.
func (w *WebhookWorker) handle(ctx context.Context, msg jetstream.Msg) {
	var job broker.DeliveryJob
	if err := json.Unmarshal(msg.Data(), &job); err != nil {
		w.log.Error("invalid job payload, discarding", "error", err)
		w.ack(msg, nil)
		return
	}

	rel, err := w.deliveries.GetForWorker(ctx, job.DeliveryID)
	if err != nil {
		if errors.Is(err, delivery.ErrNotFound) {
			// Delivery row gone (e.g. project deleted) — nothing to do.
			w.log.Warn("delivery not found, discarding", "delivery_id", job.DeliveryID)
			w.ack(msg, job.DeliveryID)
			return
		}
		// Transient DB error: nak so JetStream redelivers later.
		w.log.Error("load delivery failed", "delivery_id", job.DeliveryID, "error", err)
		w.nak(msg, job.DeliveryID)
		return
	}

	// Idempotency: a message redelivered after success must not re-send (§11).
	if rel.Delivery.Status == delivery.StatusDelivered || rel.Delivery.Status == delivery.StatusDeadLetter {
		w.ack(msg, rel.Delivery.ID)
		return
	}

	// Respect scheduled retry time: if the next attempt is in the future,
	// nak with delay so JetStream redelivers later.
	if rel.Delivery.Status == delivery.StatusRetrying && rel.Delivery.NextAttemptAt != nil {
		if wait := time.Until(*rel.Delivery.NextAttemptAt); wait > 0 {
			w.nakDelay(msg, rel.Delivery.ID, wait)
			return
		}
	}

	attemptNumber := rel.Delivery.Attempts + 1
	started := time.Now()
	httpStatus, sendErr := w.send(ctx, rel)
	finished := time.Now()

	status := delivery.AttemptSuccess
	var errCode, errMsg *string
	retryable := false

	switch {
	case sendErr != nil:
		// Network timeout, connection refused, DNS issues — all retryable (§10).
		status = delivery.AttemptFailed
		code := "NETWORK_ERROR"
		text := sendErr.Error()
		errCode, errMsg = &code, &text
		retryable = true
	case httpStatus >= 200 && httpStatus < 300:
		// Delivered.
	default:
		status = delivery.AttemptFailed
		code := "HTTP_" + strconv.Itoa(httpStatus)
		text := fmt.Sprintf("webhook returned %d", httpStatus)
		errCode, errMsg = &code, &text
		retryable = delivery.IsRetryableStatus(httpStatus)
	}

	attempt := &delivery.Attempt{
		ID:            uuid.New(),
		DeliveryID:    rel.Delivery.ID,
		AttemptNumber: attemptNumber,
		Status:        status,
		ErrorCode:     errCode,
		ErrorMessage:  errMsg,
		StartedAt:     started,
		FinishedAt:    finished,
	}
	if httpStatus > 0 {
		attempt.HTTPStatus = &httpStatus
	}
	if err := w.deliveries.RecordAttempt(ctx, attempt); err != nil {
		w.log.Error("record attempt failed", "delivery_id", rel.Delivery.ID, "error", err)
		w.nak(msg, rel.Delivery.ID)
		return
	}

	if status == delivery.AttemptSuccess {
		if err := w.deliveries.MarkDelivered(ctx, rel.Delivery.ID, attemptNumber); err != nil {
			w.log.Error("mark delivered failed", "delivery_id", rel.Delivery.ID, "error", err)
			w.nak(msg, rel.Delivery.ID)
			return
		}
		w.ack(msg, rel.Delivery.ID)
		return
	}

	if w.policy.ShouldRetry(attemptNumber, retryable) {
		delay := w.policy.NextDelay(attemptNumber)
		next := time.Now().Add(delay)
		if err := w.deliveries.MarkRetrying(ctx, rel.Delivery.ID, attemptNumber, next); err != nil {
			w.log.Error("mark retrying failed", "delivery_id", rel.Delivery.ID, "error", err)
			w.nak(msg, rel.Delivery.ID)
			return
		}
		w.log.Info("delivery scheduled for retry",
			"delivery_id", rel.Delivery.ID, "attempt", attemptNumber, "delay", delay.String())
		w.nakDelay(msg, rel.Delivery.ID, delay)
		return
	}

	// Non-retryable or attempts exhausted → dead letter (spec §9–10).
	reason := "max_attempts_exhausted"
	if !retryable {
		reason = "non_retryable_error"
	}
	lastError := ""
	if errMsg != nil {
		lastError = *errMsg
	}
	if err := w.deliveries.MarkDeadLetter(ctx, rel.Delivery.ID, attemptNumber, reason, lastError); err != nil {
		w.log.Error("mark dead letter failed", "delivery_id", rel.Delivery.ID, "error", err)
		w.nak(msg, rel.Delivery.ID)
		return
	}
	w.log.Warn("delivery moved to dead letter",
		"delivery_id", rel.Delivery.ID, "reason", reason, "attempts", attemptNumber)
	w.ack(msg, rel.Delivery.ID)
}

// send performs the signed webhook POST and returns the HTTP status
// (0 when the request never reached the server).
func (w *WebhookWorker) send(ctx context.Context, rel *delivery.DeliveryWithRelations) (int, error) {
	payload := webhookPayload{
		Event: webhookEvent{
			ID:     rel.Delivery.EventID.String(),
			Type:   rel.EventType,
			Source: rel.EventSource,
		},
		Data: json.RawMessage(rel.EventData),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rel.Destination, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "EventFlow/1.0")
	req.Header.Set("X-EventFlow-Event-ID", rel.Delivery.EventID.String())
	req.Header.Set("X-EventFlow-Delivery-ID", rel.Delivery.ID.String())
	if w.secret != "" {
		req.Header.Set(delivery.SignatureHeader, delivery.Sign(w.secret, body))
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}
