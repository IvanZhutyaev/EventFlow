package auth

import "testing"

func TestGenerateKeyAndVerify(t *testing.T) {
	raw, hash, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if len(raw) <= len(KeyPrefix) || raw[:len(KeyPrefix)] != KeyPrefix {
		t.Errorf("key must start with %q, got %q", KeyPrefix, raw)
	}
	if hash != HashKey(raw) {
		t.Error("returned hash must equal HashKey(raw)")
	}
	if !Verify(raw, hash) {
		t.Error("Verify must accept the correct key")
	}
	if Verify("ef_live_wrong", hash) {
		t.Error("Verify must reject a wrong key")
	}
}

func TestGenerateKeyUnique(t *testing.T) {
	a, _, _ := GenerateKey()
	b, _, _ := GenerateKey()
	if a == b {
		t.Error("generated keys must be unique")
	}
}
