package users_test

import (
	"testing"

	"github.com/softsrv/rowbot/internal/users"
)

func TestNormalizeEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"User@Example.COM", "user@example.com"},
		{"  user@example.com  ", "user@example.com"},
		{"UPPER@DOMAIN.ORG", "upper@domain.org"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := users.NormalizeEmail(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeEmail(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
