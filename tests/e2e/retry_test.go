//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestRetryFlow mirrors the documented retry test (spec §28):
// webhook returns 500 → delivery=retrying → then webhook returns 200 →
// delivery=delivered with attempts=2.
func TestRetryFlow(t *testing.T) {
	if os.Getenv("E2E") == "" {
		t.Skip("set E2E=1 to run against a live stack")
	}

	admin := &apiClient{t: t}
	resp, body := admin.do(http.MethodPost, "/api/v1/projects", map[string]string{
		"email": fmt.Sprintf("retry-%s@example.com", uuid.NewString()),
		"name":  "retry-project",
	}, map[string]string{"X-Admin-Token": adminTokenEnv})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status %d", resp.StatusCode)
	}
	apiKey, _ := body["api_key"].(string)
	c := &apiClient{t: t, apiKey: apiKey}

	// /fail-once returns 500 on the first request and 200 afterwards.
	resp, body = c.do(http.MethodPost, "/api/v1/subscriptions", map[string]any{
		"name":          "retry-hook",
		"event_pattern": "retry.*",
		"channel":       "webhook",
		"destination":   testsinkBase + "/fail-once",
		"enabled":       true,
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create subscription: status %d body %v", resp.StatusCode, body)
	}

	resp, body = c.do(http.MethodPost, "/api/v1/events", map[string]any{
		"type":   "retry.test",
		"source": "e2e",
	}, map[string]string{"Idempotency-Key": uuid.NewString()})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("post event: status %d", resp.StatusCode)
	}
	eventID, _ := body["id"].(string)

	// Observe the full lifecycle: pending → retrying → delivered (attempts=2).
	sawRetrying := false
	waitFor(t, 60*time.Second, func() bool {
		_, list := c.do(http.MethodGet, "/api/v1/deliveries?event_id="+eventID, nil, nil)
		items, _ := list["data"].([]any)
		if len(items) == 0 {
			return false
		}
		d, _ := items[0].(map[string]any)
		status, _ := d["status"].(string)
		if status == "retrying" {
			sawRetrying = true
		}
		return status == "delivered"
	}, "delivery to become delivered after retry")

	if !sawRetrying {
		t.Error("delivery never entered retrying state")
	}

	// attempts must equal 2 (spec §28 retry test).
	_, list := c.do(http.MethodGet, "/api/v1/deliveries?event_id="+eventID, nil, nil)
	items, _ := list["data"].([]any)
	d, _ := items[0].(map[string]any)
	if attempts, _ := d["attempts"].(float64); int(attempts) != 2 {
		t.Errorf("attempts = %v, want 2", d["attempts"])
	}
}

// TestDeadLetterFlow verifies that exhausting retries moves a delivery to
// dead_letter (spec §9): /always-fail keeps returning 500.
func TestDeadLetterFlow(t *testing.T) {
	if os.Getenv("E2E") == "" {
		t.Skip("set E2E=1 to run against a live stack")
	}

	admin := &apiClient{t: t}
	resp, body := admin.do(http.MethodPost, "/api/v1/projects", map[string]string{
		"email": fmt.Sprintf("dl-%s@example.com", uuid.NewString()),
		"name":  "deadletter-project",
	}, map[string]string{"X-Admin-Token": adminTokenEnv})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status %d", resp.StatusCode)
	}
	apiKey, _ := body["api_key"].(string)
	c := &apiClient{t: t, apiKey: apiKey}

	resp, body = c.do(http.MethodPost, "/api/v1/subscriptions", map[string]any{
		"name":          "dl-hook",
		"event_pattern": "dl.*",
		"channel":       "webhook",
		"destination":   testsinkBase + "/always-fail",
		"enabled":       true,
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create subscription: status %d body %v", resp.StatusCode, body)
	}

	resp, body = c.do(http.MethodPost, "/api/v1/events", map[string]any{
		"type":   "dl.test",
		"source": "e2e",
	}, map[string]string{"Idempotency-Key": uuid.NewString()})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("post event: status %d", resp.StatusCode)
	}
	eventID, _ := body["id"].(string)

	// With the default backoff (5s,10s,20s,40s) five attempts take ~75s.
	waitFor(t, 150*time.Second, func() bool {
		_, list := c.do(http.MethodGet, "/api/v1/deliveries?event_id="+eventID, nil, nil)
		items, _ := list["data"].([]any)
		if len(items) == 0 {
			return false
		}
		d, _ := items[0].(map[string]any)
		return d["status"] == "dead_letter"
	}, "delivery to reach dead_letter")
}
