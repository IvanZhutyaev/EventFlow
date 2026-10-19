package delivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// SignatureHeader is the request header carrying the webhook signature (spec §12).
const SignatureHeader = "X-EventFlow-Signature"

// Sign computes the HMAC-SHA256 signature of the request body, returned in
// the "sha256=<hex>" form. Clients verify with HMAC(secret, request_body).
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature checks a webhook signature in constant time.
func VerifySignature(secret string, body []byte, signature string) bool {
	expected := Sign(secret, body)
	return hmac.Equal([]byte(expected), []byte(signature))
}
