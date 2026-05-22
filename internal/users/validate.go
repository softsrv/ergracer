package users

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

var (
	ErrEmailInvalid    = errors.New("email address is invalid")
	ErrPasswordTooShort = errors.New("password is too short")
)

// NormalizeEmail lowercases and trims an email address.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidateEmail returns nil if the email is RFC 5322 compliant after normalization.
func ValidateEmail(email string) error {
	if _, err := mail.ParseAddress(email); err != nil {
		return fmt.Errorf("%w: %s", ErrEmailInvalid, email)
	}
	return nil
}

// ValidatePassword checks the password meets minimum requirements.
func ValidatePassword(password string, minLength int) error {
	if len(password) < minLength {
		return fmt.Errorf("%w: minimum %d characters", ErrPasswordTooShort, minLength)
	}
	// No maximum length — passphrases are encouraged.
	// No complexity requirements — length > complexity for user-chosen passwords.
	return nil
}
