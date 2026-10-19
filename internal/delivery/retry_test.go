package delivery

import (
	"testing"
	"time"
)

func TestRetryPolicyNextDelay(t *testing.T) {
	p := NewRetryPolicy(5, 5*time.Second, 30*time.Minute, 2)

	// Growth: initial delay doubles per attempt.
	if d := p.NextDelay(1); d != 5*time.Second {
		t.Errorf("attempt 1 delay = %v, want 5s", d)
	}
	if d := p.NextDelay(2); d != 10*time.Second {
		t.Errorf("attempt 2 delay = %v, want 10s", d)
	}
	if d := p.NextDelay(3); d != 20*time.Second {
		t.Errorf("attempt 3 delay = %v, want 20s", d)
	}

	// Cap at MaxDelay.
	if d := p.NextDelay(20); d != 30*time.Minute {
		t.Errorf("delay must be capped at max, got %v", d)
	}

	// attempts < 1 behaves like the first attempt.
	if d := p.NextDelay(0); d != 5*time.Second {
		t.Errorf("attempt 0 delay = %v, want 5s", d)
	}
}

func TestRetryPolicyShouldRetry(t *testing.T) {
	p := NewRetryPolicy(5, time.Second, time.Minute, 2)

	if !p.ShouldRetry(1, true) {
		t.Error("retryable error within attempts must retry")
	}
	if !p.ShouldRetry(4, true) {
		t.Error("last allowed attempt must still retry")
	}
	if p.ShouldRetry(5, true) {
		t.Error("max attempts exhausted must not retry")
	}
	if p.ShouldRetry(1, false) {
		t.Error("non-retryable error must not retry")
	}
}

func TestIsRetryableStatus(t *testing.T) {
	retryable := []int{408, 429, 500, 502, 503, 504}
	for _, code := range retryable {
		if !IsRetryableStatus(code) {
			t.Errorf("HTTP %d must be retryable", code)
		}
	}
	permanent := []int{400, 401, 403, 404, 418}
	for _, code := range permanent {
		if IsRetryableStatus(code) {
			t.Errorf("HTTP %d must not be retryable", code)
		}
	}
}
