// mockc2 is a minimal mock of the Concept2 Logbook API, for testing the full
// webhook → RowingService.ProcessResult → Discord pipeline against synthetic
// results that don't exist on the real Concept2 API.
//
// RowingService never trusts a webhook's own payload for result data — it
// always re-fetches the authoritative result from Concept2's API by ID (see
// internal/http/handlers/webhooks.go). That means a hand-edited/synthetic
// entry in cmd/mockc2/testdata/c2_results.json (e.g. one with heart rate
// data stripped out to test "not reported") can never be found via the real
// API, since it was never actually logged there. mockc2 serves results from
// that same fixture file, so pointing the app at this mock instead of the
// real API makes synthetic result IDs resolvable.
//
// Usage:
//
//	go run ./cmd/mockc2 [--port 4010] [--data cmd/mockc2/testdata/c2_results.json]
//
// Then, in a separate terminal, run the app against it for a test session:
//
//	CONCEPT2_API_BASE=http://localhost:4010 make run
//
// (or export CONCEPT2_API_BASE before `make dev`). Don't leave this set in
// your real .env — it's a one-off override for a testing session, not a
// standing config value; flip it back (or just unset it) once you're done.
//
// mockc2 serves an HTML fixture browser at http://localhost:<port>/ — every
// result in the data file, pretty-printed, with a "Send" button per entry
// that POSTs it as a webhook (matching Concept2's real, flat delivery shape —
// see concept2.Concept2Payload) to --target, triggering the real webhook →
// ProcessResult → Discord pipeline against this mock's data, all from the
// browser.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"

	"github.com/softsrv/rowbot/internal/concept2"
)

// envFilePath mirrors cmd/app's — see its comment for why it's a relative path.
const envFilePath = ".env"

// savedResults is the fixture file's top-level shape.
type savedResults struct {
	FetchedAt      time.Time         `json:"fetched_at"`
	Concept2UserID int64             `json:"concept2_user_id"`
	Results        []concept2.Result `json:"results"`
}

func main() {
	// Best-effort: mockc2 is a standalone local testing tool with its own
	// CLI flags, so unlike cmd/app it works fine with no .env at all.
	_ = godotenv.Load(envFilePath)

	port := flag.Int("port", 4010, "port to listen on")
	dataPath := flag.String("data", "cmd/mockc2/testdata/c2_results.json", "path to the results fixture file")
	target := flag.String("target", "", "webhook URL the UI's Send button POSTs to (default: http://localhost:PORT/webhooks/concept2, PORT from .env or 8080)")
	flag.Parse()

	if *target == "" {
		*target = fmt.Sprintf("http://localhost:%s/webhooks/concept2", envOrDefault("PORT", "8080"))
	}

	data, err := os.ReadFile(*dataPath)
	if err != nil {
		log.Fatalf("read %s: %v", *dataPath, err)
	}
	var saved savedResults
	if err := json.Unmarshal(data, &saved); err != nil {
		log.Fatalf("parse %s: %v", *dataPath, err)
	}

	byID := make(map[int64]concept2.Result, len(saved.Results))
	for _, r := range saved.Results {
		byID[r.ID] = r
	}

	mux := http.NewServeMux()

	// GET /api/users/me/results/{id} — the one endpoint
	// RowingService.ProcessResult actually calls (via concept2.GetResult).
	mux.HandleFunc("GET /api/users/me/results/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid result id")
			return
		}
		result, ok := byID[id]
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("result %d not found in %s", id, *dataPath))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": result})
	})

	// GET /api/users/me/results — list endpoint, for completeness/symmetry
	// with the real API. Nothing in the current pipeline calls this, but
	// it's cheap to keep serving from the same fixture.
	mux.HandleFunc("GET /api/users/me/results", func(w http.ResponseWriter, r *http.Request) {
		results := saved.Results
		if v := r.URL.Query().Get("per_page"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n < len(results) {
				results = results[:n]
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": results})
	})

	// GET /api/users/me — minimal profile stub, in case anything in a test
	// session also exercises the OAuth linking flow against this mock.
	mux.HandleFunc("GET /api/users/me", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
			"id":       saved.Concept2UserID,
			"username": "mockuser",
		}})
	})

	// POST /oauth/access_token — covers both the code exchange and the
	// refresh-token grant with one fake-but-valid-looking response, so
	// RowingService's "refresh if expiring soon" path doesn't hard-fail if
	// it happens to trigger during a test session.
	mux.HandleFunc("POST /oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token":  "mock-access-token",
			"refresh_token": "mock-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    86400,
			"scope":         "user:read,results:read",
		})
	})

	registerUIRoutes(mux, saved, byID, *dataPath, *target)

	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("mockc2: serving %d result(s) from %s\n", len(saved.Results), *dataPath)
	fmt.Printf("mockc2: listening on http://localhost%s\n", addr)
	fmt.Printf("mockc2: point the app at it via CONCEPT2_API_BASE=http://localhost%s\n", addr)
	fmt.Printf("mockc2: browse fixtures and fire test webhooks at http://localhost%s/\n", addr)
	fmt.Printf("mockc2: Send button POSTs to %s\n", *target)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
