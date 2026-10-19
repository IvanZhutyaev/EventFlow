package subscription

import "testing"

func TestMatch(t *testing.T) {
	tests := []struct {
		name      string
		pattern   string
		eventType string
		want      bool
	}{
		{"exact match", "order.created", "order.created", true},
		{"exact mismatch", "order.created", "order.cancelled", false},
		{"single-segment wildcard", "order.*", "order.created", true},
		{"wildcard does not cross segments", "order.*", "order.item.created", false},
		{"wildcard segment mismatch", "order.*", "user.created", false},
		{"match all", "*", "anything.here", true},
		{"different segment count", "a.b", "a", false},
		{"multi wildcard tail", "a.*.c", "a.b.c", true},
		{"multi wildcard middle mismatch", "a.*.c", "a.b.d", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Match(tt.pattern, tt.eventType); got != tt.want {
				t.Errorf("Match(%q, %q) = %v, want %v", tt.pattern, tt.eventType, got, tt.want)
			}
		})
	}
}
