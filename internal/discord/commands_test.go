package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterCommands(t *testing.T) {
	const (
		testAppID   = "123456789"
		testToken   = "my-bot-token"
		wantPath    = "/applications/" + testAppID + "/commands"
		wantAuthHdr = "Bot " + testToken
	)

	testCommands := []ApplicationCommand{
		{Type: 1, Name: "register", Description: "Request registration for RowBot"},
	}

	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{
			name:       "2xx response returns nil error",
			statusCode: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "201 Created returns nil error",
			statusCode: http.StatusCreated,
			wantErr:    false,
		},
		{
			name:       "401 Unauthorized returns error",
			statusCode: http.StatusUnauthorized,
			wantErr:    true,
		},
		{
			name:       "500 Internal Server Error returns error",
			statusCode: http.StatusInternalServerError,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				gotMethod string
				gotPath   string
				gotAuth   string
				gotBody   []ApplicationCommand
			)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")

				rawBody, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}
				if err := json.Unmarshal(rawBody, &gotBody); err != nil {
					t.Errorf("unmarshal request body: %v", err)
				}

				w.WriteHeader(tc.statusCode)
			}))
			defer srv.Close()

			// Override the API base so requests hit the test server.
			origBase := discordAPIBase
			discordAPIBase = srv.URL
			defer func() { discordAPIBase = origBase }()

			err := RegisterCommands(context.Background(), srv.Client(), testToken, testAppID, testCommands)

			if tc.wantErr && err == nil {
				t.Error("expected non-nil error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected nil error, got %v", err)
			}

			// Only assert request details for cases where the server was reached.
			if gotMethod == "" {
				return
			}

			if gotMethod != http.MethodPut {
				t.Errorf("method: got %q, want %q", gotMethod, http.MethodPut)
			}
			if gotPath != wantPath {
				t.Errorf("path: got %q, want %q", gotPath, wantPath)
			}
			if gotAuth != wantAuthHdr {
				t.Errorf("Authorization header: got %q, want %q", gotAuth, wantAuthHdr)
			}
			if len(gotBody) != len(testCommands) {
				t.Fatalf("body length: got %d, want %d", len(gotBody), len(testCommands))
			}
			for i, cmd := range testCommands {
				got := gotBody[i]
				if got.Name != cmd.Name || got.Description != cmd.Description || got.Type != cmd.Type {
					t.Errorf("command[%d]: got %+v, want %+v", i, got, cmd)
				}
			}
		})
	}
}

func TestRegisterCommands_NilClientUsesDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origBase := discordAPIBase
	discordAPIBase = srv.URL
	defer func() { discordAPIBase = origBase }()

	// Pass nil client — RegisterCommands must substitute http.DefaultClient.
	// We can't easily redirect DefaultClient to the test server without
	// swapping its transport, so instead we just confirm no panic and that
	// the function reaches the server by checking error is nil.
	//
	// To actually route http.DefaultClient through our test server we temporarily
	// swap its transport.
	origTransport := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	defer func() { http.DefaultTransport = origTransport }()

	err := RegisterCommands(context.Background(), nil, "tok", "appid", Commands)
	if err != nil {
		t.Errorf("expected nil error with nil client, got %v", err)
	}
}
