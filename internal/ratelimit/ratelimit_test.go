package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterAllowsBurstThenBlocks(t *testing.T) {
	l := NewLimiter(10)

	// First second's worth of tokens must be available immediately.
	allowed := 0
	for i := 0; i < 15; i++ {
		if l.Allow("proj-1") {
			allowed++
		}
	}
	if allowed != 10 {
		t.Errorf("allowed = %d, want 10 (initial burst)", allowed)
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	l := NewLimiter(100)
	for i := 0; i < 100; i++ {
		l.Allow("proj")
	}
	// Exhausted; wait for refill of ~10 tokens (100ms at 100 rps).
	time.Sleep(150 * time.Millisecond)
	allowed := 0
	for i := 0; i < 20; i++ {
		if l.Allow("proj") {
			allowed++
		}
	}
	if allowed == 0 {
		t.Error("tokens must refill over time")
	}
	if allowed > 25 {
		t.Errorf("allowed = %d, want roughly <= 25", allowed)
	}
}

func TestLimiterKeysAreIsolated(t *testing.T) {
	l := NewLimiter(5)
	for i := 0; i < 10; i++ {
		l.Allow("a")
	}
	if !l.Allow("b") {
		t.Error("exhausting one key must not block another")
	}
}
