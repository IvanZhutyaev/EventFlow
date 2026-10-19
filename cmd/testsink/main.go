// Command testsink is a tiny HTTP server used by the e2e tests.
//
// It exercises the documented scenarios (spec §28, §12):
//   - /hook        200, but only if the HMAC signature is valid
//   - /fail-once   500 on the first call, 200 afterwards
//   - /always-fail always 500
//   - /status/:code returns the given status code
//
// When WEBHOOK_SECRET is set, /hook and the success path of /fail-once verify
// X-EventFlow-Signature (HMAC-SHA256 over the raw body) and return 401 on
// mismatch — proving signing works end to end.
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
)

func main() {
	addr := ":8081"
	if v := os.Getenv("SINK_ADDR"); v != "" {
		addr = v
	}
	secret := os.Getenv("WEBHOOK_SECRET")

	// failOnceSeen tracks which deliveries already consumed their single
	// failure on /fail-once. Keyed by X-EventFlow-Delivery-ID, so every
	// test run is deterministic regardless of how many times the endpoint
	// was hit before (each delivery fails exactly once, then succeeds).
	var mu sync.Mutex
	failOnceSeen := map[string]bool{}

	// verify checks the EventFlow HMAC signature when a secret is configured.
	verify := func(w http.ResponseWriter, r *http.Request, body []byte) bool {
		if secret == "" {
			return true
		}
		got := r.Header.Get("X-EventFlow-Signature")
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(want), []byte(got)) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":"invalid signature","want":%q,"got":%q}`, want, got)
			return false
		}
		return true
	}

	mux := http.NewServeMux()

	// /hook always succeeds — but only with a valid signature.
	mux.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !verify(w, r, body) {
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"ok":true}`)
	})

	// /fail-once returns 500 on the first request *per delivery*, 200 (signed) afterwards.
	mux.HandleFunc("/fail-once", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		deliveryID := r.Header.Get("X-EventFlow-Delivery-ID")
		mu.Lock()
		seen := failOnceSeen[deliveryID]
		failOnceSeen[deliveryID] = true
		mu.Unlock()
		if !seen {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintln(w, `{"error":"temporary failure"}`)
			return
		}
		if !verify(w, r, body) {
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"ok":true}`)
	})

	// /always-fail always returns 500 — drives deliveries to dead letter.
	mux.HandleFunc("/always-fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, `{"error":"permanent failure"}`)
	})

	// /status/:code returns an arbitrary status code.
	mux.HandleFunc("/status/", func(w http.ResponseWriter, r *http.Request) {
		code, err := strconv.Atoi(r.URL.Path[len("/status/"):])
		if err != nil || code < 100 || code > 599 {
			code = http.StatusOK
		}
		w.WriteHeader(code)
		fmt.Fprintf(w, `{"status":%d}`, code)
	})

	log.Printf("testsink listening on %s (signature verification: %v)", addr, secret != "")
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
