// Retry policy: exponential backoff with a hard cap (spec §9).
//
// Attempt 1 → +5 sec, 2 → +30 sec, 3 → +2 min, 4 → +10 min, 5 → +30 min
// with max_attempts=5, initial_delay=5s, max_delay=30m, multiplier=2.

package delivery

import "time"

// RetryPolicy configures exponential backoff between delivery attempts.
type RetryPolicy struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
}

// NewRetryPolicy builds the default policy from configuration values.
func NewRetryPolicy(maxAttempts int, initialDelay, maxDelay time.Duration, multiplier float64) RetryPolicy {
	return RetryPolicy{
		MaxAttempts:  maxAttempts,
		InitialDelay: initialDelay,
		MaxDelay:     maxDelay,
		Multiplier:   multiplier,
	}
}

// ShouldRetry reports whether another attempt is allowed after `attempts` completed tries.
func (p RetryPolicy) ShouldRetry(attempts int, retryable bool) bool {
	return retryable && attempts < p.MaxAttempts
}

// NextDelay returns the backoff delay before the next attempt.
// `attempts` is the number of already completed attempts (1-based for the
// first retry: after attempt 1 the delay equals InitialDelay).
func (p RetryPolicy) NextDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := float64(p.InitialDelay)
	for i := 1; i < attempts; i++ {
		delay *= p.Multiplier
	}
	if delay > float64(p.MaxDelay) {
		return p.MaxDelay
	}
	return time.Duration(delay)
}

// IsRetryableStatus reports whether an HTTP status is worth retrying (spec §10).
func IsRetryableStatus(code int) bool {
	switch code {
	case 408, 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}
