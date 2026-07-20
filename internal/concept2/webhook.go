package concept2

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// VerifySignature reports whether sigHeader is the valid HMAC-SHA256 signature
// of body using secret as the key. sigHeader must be a hex-encoded digest.
//
// An empty secret always returns false — an unconfigured secret must never
// pass validation.
func VerifySignature(secret string, body []byte, sigHeader string) bool {
	if secret == "" {
		return false
	}
	sig, err := hex.DecodeString(sigHeader)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(expected, sig)
}

// Concept2Payload is the top-level webhook body from Concept2.
type Concept2Payload struct {
	Type      string `json:"type"`
	UserID    int64  `json:"user_id"`
	ResultID  int64  `json:"result_id"`
	Timestamp string `json:"timestamp"`
	Challenge string `json:"challenge,omitempty"` // present during subscription verification
}
