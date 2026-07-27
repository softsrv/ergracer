package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRowingServicer is a minimal rowingServicer implementation that records
// the last call it received, for assertions in tests.
type fakeRowingServicer struct {
	mu       sync.Mutex
	called   bool
	c2UserID int64
	resultID int64
	done     chan struct{} // closed when ProcessResult is invoked, for synchronizing with the async goroutine
}

func newFakeRowingServicer() *fakeRowingServicer {
	return &fakeRowingServicer{done: make(chan struct{}, 1)}
}

func (f *fakeRowingServicer) ProcessResult(ctx context.Context, concept2UserID int64, resultID int64) error {
	f.mu.Lock()
	f.called = true
	f.c2UserID = concept2UserID
	f.resultID = resultID
	f.mu.Unlock()
	select {
	case f.done <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeRowingServicer) snapshot() (called bool, c2UserID, resultID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.called, f.c2UserID, f.resultID
}

func postWebhook(h *WebhookHandler, body, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/concept2", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.Concept2(rec, req)
	return rec
}

func TestWebhookConcept2_ResultAdded(t *testing.T) {
	svc := newFakeRowingServicer()
	h := NewWebhookHandler(svc)

	body := `{"data":{"type":"result-added","result":{"id":3,"user_id":1,"date":"2013-06-21 00:00:00","distance":23000,"type":"rower","time":152350,"time_formatted":"4:13:55.0","workout_type":"unknown","source":"Web","weight_class":"H","verified":false,"ranked":false,"comments":null}}}`

	rec := postWebhook(h, body, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	select {
	case <-svc.done:
	case <-time.After(2 * time.Second):
		t.Fatal("ProcessResult was never called")
	}

	called, c2UserID, resultID := svc.snapshot()
	if !called {
		t.Fatal("expected ProcessResult to be called")
	}
	if c2UserID != 1 {
		t.Errorf("expected concept2UserID=1, got %d", c2UserID)
	}
	if resultID != 3 {
		t.Errorf("expected resultID=3, got %d", resultID)
	}
}

func TestWebhookConcept2_ResultUpdated_NotProcessed(t *testing.T) {
	svc := newFakeRowingServicer()
	h := NewWebhookHandler(svc)

	body := `{"data":{"type":"result-updated","result":{"id":3,"user_id":1}}}`

	rec := postWebhook(h, body, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	called, _, _ := svc.snapshot()
	if called {
		t.Fatal("expected ProcessResult NOT to be called for result-updated")
	}
}

func TestWebhookConcept2_ResultDeleted_NotProcessed(t *testing.T) {
	svc := newFakeRowingServicer()
	h := NewWebhookHandler(svc)

	body := `{"data":{"type":"result-deleted","result_id":745}}`

	rec := postWebhook(h, body, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	called, _, _ := svc.snapshot()
	if called {
		t.Fatal("expected ProcessResult NOT to be called for result-deleted")
	}
}

func TestWebhookConcept2_MalformedJSON(t *testing.T) {
	svc := newFakeRowingServicer()
	h := NewWebhookHandler(svc)

	rec := postWebhook(h, `{not valid json`, "application/json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookConcept2_EmptyType(t *testing.T) {
	svc := newFakeRowingServicer()
	h := NewWebhookHandler(svc)

	rec := postWebhook(h, `{"data":{}}`, "application/json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookConcept2_WrongContentType(t *testing.T) {
	svc := newFakeRowingServicer()
	h := NewWebhookHandler(svc)

	rec := postWebhook(h, `{"data":{"type":"result-added"}}`, "text/plain")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookConcept2_ResultAddedMissingResult(t *testing.T) {
	svc := newFakeRowingServicer()
	h := NewWebhookHandler(svc)

	// A "result-added" event with no nested result object is malformed and
	// must be rejected rather than panicking on a nil pointer dereference.
	rec := postWebhook(h, `{"data":{"type":"result-added"}}`, "application/json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	called, _, _ := svc.snapshot()
	if called {
		t.Fatal("expected ProcessResult NOT to be called")
	}
}
