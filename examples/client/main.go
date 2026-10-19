// Command client is a small runnable example of the EventFlow API (spec §38).
//
// It creates a project (admin), a subscription pointing at a destination of
// your choice, sends one event and polls the delivery until it settles.
//
// Usage:
//
//	go run ./examples/client \
//	  -api http://localhost:8080 \
//	  -admin-token dev-admin-token \
//	  -destination http://localhost:9091/hook
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
)

func main() {
	api := flag.String("api", "http://localhost:8080", "EventFlow API base URL")
	adminToken := flag.String("admin-token", "dev-admin-token", "admin token")
	destination := flag.String("destination", "http://localhost:9091/hook", "webhook destination")
	email := flag.String("email", fmt.Sprintf("example-%s@example.com", uuid.NewString()), "project owner email")
	flag.Parse()

	// 1. Create a project + API key.
	var created struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
		APIKey string `json:"api_key"`
	}
	body, _ := json.Marshal(map[string]string{"email": *email, "name": "example-project"})
	req, _ := http.NewRequest(http.MethodPost, *api+"/api/v1/projects", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", *adminToken)
	if err := doJSON(req, &created); err != nil {
		fatal("create project", err)
	}
	fmt.Println("project:", created.Project.ID)
	fmt.Println("api_key:", created.APIKey)

	client := &http.Client{Timeout: 10 * time.Second}
	authed := func(method, path string, payload any, headers map[string]string) (int, map[string]any) {
		var rdr io.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			rdr = bytes.NewReader(b)
		}
		r, _ := http.NewRequest(method, *api+path, rdr)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+created.APIKey)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		resp, err := client.Do(r)
		if err != nil {
			fatal("request "+path, err)
		}
		body, err := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if err != nil {
			fatal("request "+path+" read body", err)
		}
		if closeErr != nil {
			fatal("request "+path+" close body", closeErr)
		}
		var out map[string]any
		if len(body) > 0 {
			if err := json.Unmarshal(body, &out); err != nil {
				fatal("request "+path+" decode body", err)
			}
		}
		return resp.StatusCode, out
	}

	// 2. Create a subscription.
	status, bodyResp := authed(http.MethodPost, "/api/v1/subscriptions", map[string]any{
		"name":          "example",
		"event_pattern": "example.*",
		"channel":       "webhook",
		"destination":   *destination,
		"enabled":       true,
	}, nil)
	if status != http.StatusCreated {
		fatalf("create subscription: status %d body %v", status, bodyResp)
	}
	fmt.Println("subscription created")

	// 3. Send an event.
	status, bodyResp = authed(http.MethodPost, "/api/v1/events", map[string]any{
		"type":   "example.hello",
		"source": "example-client",
		"data":   map[string]any{"message": "hello from EventFlow"},
	}, map[string]string{"Idempotency-Key": uuid.NewString()})
	if status != http.StatusAccepted {
		fatalf("send event: status %d body %v", status, bodyResp)
	}
	eventID, _ := bodyResp["id"].(string)
	fmt.Println("event accepted:", eventID)

	// 4. Poll the delivery until it settles.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, list := authed(http.MethodGet, "/api/v1/deliveries?event_id="+eventID, nil, nil)
		if items, ok := list["data"].([]any); ok && len(items) > 0 {
			d, _ := items[0].(map[string]any)
			fmt.Printf("delivery %v → %v (attempts %v)\n", d["id"], d["status"], d["attempts"])
			if s, _ := d["status"].(string); s == "delivered" || s == "dead_letter" {
				return
			}
		}
		time.Sleep(time.Second)
	}
	fmt.Println("timed out waiting for delivery (is the worker running?)")
}

func doJSON(req *http.Request, out any) error {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, b)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func fatal(msg string, err error)    { fmt.Fprintln(os.Stderr, msg+":", err); os.Exit(1) }
func fatalf(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...); os.Exit(1) }
