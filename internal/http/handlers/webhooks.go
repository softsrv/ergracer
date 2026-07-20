package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/softsrv/ergracer/internal/concept2"
)

// rowingServicer is the narrow interface the webhook handler needs to process
// an incoming Concept2 result asynchronously.
type rowingServicer interface {
	ProcessResult(ctx context.Context, concept2UserID int64, resultID int64) error
}

// WebhookHandler handles inbound webhook deliveries from external services.
type WebhookHandler struct {
	concept2Secret string
	svc            rowingServicer
}

// NewWebhookHandler constructs a WebhookHandler. If concept2Secret is empty the
// Concept2 handler will reject every request with 401 — the correct safe default
// for an unconfigured secret. svc may be nil; when nil, results are logged but
// not processed.
func NewWebhookHandler(concept2Secret string, svc rowingServicer) *WebhookHandler {
	return &WebhookHandler{concept2Secret: concept2Secret, svc: svc}
}

// Concept2 handles POST /webhooks/concept2.
//
// Validation order (failing closed at the first problem):
//  1. Content-Type must contain "application/json" — 415 otherwise.
//  2. Raw body read — 400 on error.
//  3. HMAC-SHA256 signature verified against CONCEPT2_WEBHOOK_SECRET — 401 on mismatch.
//  4. JSON unmarshal — 400 on error.
//  5. Subscription verification handshake — echo challenge and return 200.
//  6. Required field validation (type must be non-empty) — 400 otherwise.
//  7. Log, kick off async processing, and return 200.
func (h *WebhookHandler) Concept2(w http.ResponseWriter, r *http.Request) {
	// 1. Content-Type check.
	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
		return
	}

	// 2. Read raw body. BodyLimit middleware has already capped it at 1 MiB.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// 3. Signature verification.
	if !concept2.VerifySignature(h.concept2Secret, body, r.Header.Get("X-Concept2-Signature")) {
		slog.WarnContext(r.Context(), "concept2 webhook: signature verification failed")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// 4. JSON decode.
	var payload concept2.Concept2Payload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// 5. Subscription verification handshake.
	if payload.Challenge != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"challenge": payload.Challenge})
		return
	}

	// 6. Validate required fields.
	if payload.Type == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// 7. Log and respond immediately; process asynchronously.
	slog.Info("concept2 webhook received",
		"event_type", payload.Type,
		"concept2_user_id", payload.UserID,
		"result_id", payload.ResultID,
		"logged_at", payload.Timestamp,
	)

	if h.svc != nil && payload.ResultID != 0 {
		go func() {
			// Use a fresh context — the request context is cancelled after the
			// handler returns, which would immediately abort the processing.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := h.svc.ProcessResult(ctx, payload.UserID, payload.ResultID); err != nil {
				slog.Error("concept2 webhook: process result failed",
					"result_id", payload.ResultID,
					"error", err,
				)
			}
		}()
	}

	w.WriteHeader(http.StatusOK)
}
