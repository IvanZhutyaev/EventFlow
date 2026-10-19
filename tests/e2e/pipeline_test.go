//go:build e2e

// Package e2e exercises the full EventFlow pipeline over HTTP:
// project → API key → subscription → event → delivery → webhook → delivered.
//
// Run with the full stack up:
//
//	docker compose up -d --build
//	E2E=1 go test -tags=e2e ./tests/e2e/...
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	apiBase = "http://localhost:8080"
	// testsinkBase must be reachable from inside the worker container,
	// so it uses the compose service name, not localhost.
	testsinkBase  = "http://testsink:8081"
	adminTokenEnv = "dev-admin-token"
)

type apiClient struct {
	t      *testing.T
	apiKey string
}

func (c *apiClient) do(method, path string, body any, headers map[string]string) (*http.Response, map[string]any) {
	c.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, apiBase+path, rdr)
	if err != nil {
		c.t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", msg)
}

// TestFullPipeline covers the documented E2E flow (spec §28):
// create project → create API key → create subscription → POST event →
// event stored → delivery created → worker consumes → webhook called → delivered.
func TestFullPipeline(t *testing.T) {
	if os.Getenv("E2E") == "" {
		t.Skip("set E2E=1 to run against a live stack")
	}

	// Readiness.
	resp, _ := (&apiClient{t: t}).do(http.MethodGet, "/health/ready", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stack not ready: status %d", resp.StatusCode)
	}

	// 1. Create project + API key via admin API.
	admin := &apiClient{t: t}
	resp, body := admin.do(http.MethodPost, "/api/v1/projects", map[string]string{
		"email":       fmt.Sprintf("e2e-%s@example.com", uuid.NewString()),
		"name":        "e2e-project",
		"description": "created by e2e test",
	}, map[string]string{"X-Admin-Token": adminTokenEnv})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status %d body %v", resp.StatusCode, body)
	}
	apiKey, _ := body["api_key"].(string)
	if apiKey == "" {
		t.Fatal("api_key missing in response")
	}
	c := &apiClient{t: t, apiKey: apiKey}

	// 2. Create a subscription pointing to the local webhook sink.
	resp, body = c.do(http.MethodPost, "/api/v1/subscriptions", map[string]any{
		"name":          "e2e-hook",
		"event_pattern": "e2e.*",
		"channel":       "webhook",
		"destination":   testsinkBase + "/hook",
		"enabled":       true,
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create subscription: status %d body %v", resp.StatusCode, body)
	}

	// 3. POST an event with an Idempotency-Key.
	idemKey := uuid.NewString()
	resp, body = c.do(http.MethodPost, "/api/v1/events", map[string]any{
		"type":   "e2e.test",
		"source": "e2e",
		"data":   map[string]any{"hello": "world"},
	}, map[string]string{"Idempotency-Key": idemKey})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("post event: status %d body %v", resp.StatusCode, body)
	}
	eventID, _ := body["id"].(string)
	if eventID == "" {
		t.Fatalf("event id missing: %v", body)
	}

	// 4. The event is retrievable.
	resp, _ = c.do(http.MethodGet, "/api/v1/events/"+eventID, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get event: status %d", resp.StatusCode)
	}

	// 5. An idempotent replay returns the same event id.
	resp, body = c.do(http.MethodPost, "/api/v1/events", map[string]any{
		"type":   "e2e.test",
		"source": "e2e",
		"data":   map[string]any{"hello": "world"},
	}, map[string]string{"Idempotency-Key": idemKey})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("replay event: status %d", resp.StatusCode)
	}
	if replayID, _ := body["id"].(string); replayID != eventID {
		t.Errorf("replay returned %s, want %s", replayID, eventID)
	}

	// 6. Wait until the worker delivers the webhook (poll the deliveries API).
	var deliveryID string
	waitFor(t, 30*time.Second, func() bool {
		_, list := c.do(http.MethodGet, "/api/v1/deliveries?event_id="+eventID, nil, nil)
		items, _ := list["data"].([]any)
		if len(items) == 0 {
			return false
		}
		d, _ := items[0].(map[string]any)
		deliveryID, _ = d["id"].(string)
		return d["status"] == "delivered"
	}, "delivery to become delivered")

	// 7. The delivery detail contains a successful attempt.
	_, detail := c.do(http.MethodGet, "/api/v1/deliveries/"+deliveryID, nil, nil)
	attempts, _ := detail["attempts"].([]any)
	if len(attempts) == 0 {
		t.Fatalf("no attempts recorded: %v", detail)
	}
	first, _ := attempts[0].(map[string]any)
	if first["status"] != "success" {
		t.Errorf("first attempt status = %v, want success", first["status"])
	}
}
