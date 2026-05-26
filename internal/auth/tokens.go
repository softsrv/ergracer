package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// generateRandomToken is the shared implementation for all 32-byte URL-safe tokens.
// It returns both the raw base64 form (for delivery) and its SHA-256 hash (for storage).
func generateRandomToken(label string) (raw, hashed string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate %s: %w", label, err)
	}
	raw = base64.URLEncoding.EncodeToString(b)
	hashed = HashToken(raw)
	return raw, hashed, nil
}

// GenerateRefreshToken creates a cryptographically random 32-byte refresh token
// and returns both the raw (for the cookie) and hashed (for storage) forms.
func GenerateRefreshToken() (raw, hashed string, err error) {
	return generateRandomToken("refresh token")
}

// GenerateResetToken creates a cryptographically random URL-safe reset token.
func GenerateResetToken() (raw, hashed string, err error) {
	return generateRandomToken("reset token")
}

// HashToken returns the hex-encoded SHA-256 hash of the given token string.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CompareTokenHash performs a constant-time comparison of a raw token against a stored hash.
func CompareTokenHash(raw, storedHash string) bool {
	computed := HashToken(raw)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedHash)) == 1
}

// GenerateVerificationCode returns a 6-digit numeric code as a zero-padded string.
func GenerateVerificationCode() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate verification code: %w", err)
	}
	// Use first 3 bytes to produce a value 0–999999
	n := (uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])) % 1_000_000
	return fmt.Sprintf("%06d", n), nil
}
