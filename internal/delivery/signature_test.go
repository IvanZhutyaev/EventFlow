package delivery

import "testing"

func TestSignAndVerify(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"event":{"id":"evt_1"},"data":{"order_id":1}}`)

	sig := Sign(secret, body)
	if sig[:7] != "sha256=" {
		t.Fatalf("signature must be prefixed with sha256=, got %q", sig)
	}

	if !VerifySignature(secret, body, sig) {
		t.Error("valid signature must verify")
	}
	if VerifySignature("other-secret", body, sig) {
		t.Error("signature from a different secret must not verify")
	}
	if VerifySignature(secret, []byte(`tampered`), sig) {
		t.Error("signature over different body must not verify")
	}
}
