// Package auth implements API key generation, hashing and lookup (spec §17).
// Keys are stored only as SHA-256 hashes; the raw key is shown to the user
// exactly once at creation time.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// KeyPrefix marks a live EventFlow API key.
const KeyPrefix = "ef_live_"

// GenerateKey returns a new random API key and its SHA-256 hex hash.
func GenerateKey() (raw string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("auth: generate key: %w", err)
	}
	raw = KeyPrefix + hex.EncodeToString(buf)
	return raw, HashKey(raw), nil
}

// HashKey computes the SHA-256 hex hash of a raw API key.
func HashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Verify reports whether raw matches the stored hash using constant-time comparison.
func Verify(raw, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashKey(raw)), []byte(storedHash)) == 1
}
