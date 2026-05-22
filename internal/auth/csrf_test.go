package auth_test

import (
	"testing"

	"github.com/softsrv/starter/internal/auth"
)

func TestCSRFToken(t *testing.T) {
	t.Parallel()

	t.Run("generated tokens are unique", func(t *testing.T) {
		t.Parallel()
		a, _ := auth.GenerateCSRFToken()
		b, _ := auth.GenerateCSRFToken()
		if a == b {
			t.Error("two generated CSRF tokens should not be equal")
		}
	})

	t.Run("matching tokens pass validation", func(t *testing.T) {
		t.Parallel()
		token, err := auth.GenerateCSRFToken()
		if err != nil {
			t.Fatalf("GenerateCSRFToken: %v", err)
		}
		if err := auth.ValidateCSRFToken(token, token); err != nil {
			t.Errorf("expected valid token to pass, got %v", err)
		}
	})

	t.Run("mismatched tokens fail validation", func(t *testing.T) {
		t.Parallel()
		a, _ := auth.GenerateCSRFToken()
		b, _ := auth.GenerateCSRFToken()
		if err := auth.ValidateCSRFToken(a, b); err == nil {
			t.Error("expected mismatched tokens to fail")
		}
	})

	t.Run("empty submitted token fails", func(t *testing.T) {
		t.Parallel()
		stored, _ := auth.GenerateCSRFToken()
		if err := auth.ValidateCSRFToken("", stored); err == nil {
			t.Error("expected empty submitted token to fail")
		}
	})

	t.Run("empty stored token fails", func(t *testing.T) {
		t.Parallel()
		if err := auth.ValidateCSRFToken("anything", ""); err == nil {
			t.Error("expected empty stored token to fail")
		}
	})
}
