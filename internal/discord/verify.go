package discord

import (
	"crypto/ed25519"
	"encoding/hex"
)

// VerifySignature verifies a Discord Ed25519 interaction signature.
// The signed message is the UTF-8 encoding of timestamp concatenated with body.
func VerifySignature(pubKey ed25519.PublicKey, timestamp string, body []byte, sigHex string) bool {
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	msg := make([]byte, len(timestamp)+len(body))
	copy(msg, timestamp)
	copy(msg[len(timestamp):], body)
	return ed25519.Verify(pubKey, msg, sig)
}
