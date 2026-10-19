package subscription

import "strings"

// Match reports whether an event of the given type satisfies the subscription
// pattern. Supported syntax:
//
//	order.created  — exact match
//	order.*        — single-segment wildcard
//	*              — match everything
//
// Patterns never cross segment boundaries: "order.*" matches
// "order.created" but not "order.item.created".
func Match(pattern, eventType string) bool {
	if pattern == "*" {
		return true
	}
	patternParts := strings.Split(pattern, ".")
	eventParts := strings.Split(eventType, ".")
	if len(patternParts) != len(eventParts) {
		return false
	}
	for i := range patternParts {
		if patternParts[i] == "*" {
			continue
		}
		if patternParts[i] != eventParts[i] {
			return false
		}
	}
	return true
}
