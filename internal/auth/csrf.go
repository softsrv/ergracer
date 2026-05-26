package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
)

const csrfTokenBytes = 32

var ErrCSRFInvalid = errors.New("CSRF token invalid or missing")

// GenerateCSRFToken returns a cryptographically random base64-encoded CSRF token.
func GenerateCSRFToken() (string, error) {
	b := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate csrf token: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// ValidateCSRFToken performs a constant-time comparison of submitted vs stored token.
func ValidateCSRFToken(submitted, stored string) error {
	if submitted == "" || stored == "" {
		return ErrCSRFInvalid
	}
	if subtle.ConstantTimeCompare([]byte(submitted), []byte(stored)) != 1 {
		return ErrCSRFInvalid
	}
	return nil
}
