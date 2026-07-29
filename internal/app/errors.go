package app

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrUserNotFound  = errors.New("user not found")
	ErrTokenNotFound = errors.New("token not found")
	ErrTokenRevoked  = errors.New("token has been revoked")
	ErrTokenExpired  = errors.New("token has expired")
	ErrForbidden     = errors.New("access denied")
)

// RateLimitedError is returned when an operation is attempted too frequently.
// RetryAt is the earliest time the caller may try again.
type RateLimitedError struct {
	RetryAt time.Time
}

func (e RateLimitedError) Error() string {
	return fmt.Sprintf("rate limited: retry after %s", e.RetryAt.Format(time.RFC3339))
}
