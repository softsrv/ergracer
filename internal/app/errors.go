package app

import "errors"

var (
	ErrInvalidCredentials   = errors.New("invalid email or password")
	ErrAccountLocked        = errors.New("account is temporarily locked")
	ErrUserNotFound         = errors.New("user not found")
	ErrTokenNotFound        = errors.New("token not found")
	ErrTokenRevoked         = errors.New("token has been revoked")
	ErrTokenExpired         = errors.New("token has expired")
	ErrTokenInvalid         = errors.New("token is invalid")
	ErrTokenUsed            = errors.New("token has already been used")
	ErrEmailAlreadyVerified = errors.New("email is already verified")
	ErrRateLimited          = errors.New("too many requests, please try again later")
	ErrForbidden            = errors.New("access denied")
)
