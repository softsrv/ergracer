// simhook is a dev tool for simulating Concept2 webhook deliveries.
//
// Usage:
//
//	go run ./cmd/simhook fetch [--count N]
//	go run ./cmd/simhook send [--index N] [--id RESULT_ID] [--target URL]
//
// fetch  Reads the stored Concept2 OAuth token from the database, fetches
//
//	recent results from the Concept2 API, and saves them to tmp/c2_results.json.
//
// send   Loads tmp/c2_results.json, lets you pick a result (interactively or
//
//	via flag), builds a signed webhook payload, and POSTs it to the local server.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/softsrv/ergracer/internal/concept2"
	oauth "github.com/softsrv/ergracer/internal/oauth"
	"github.com/softsrv/ergracer/internal/secrets"
)

const savedFile = "tmp/c2_results.json"

type savedResults struct {
	FetchedAt      time.Time        `json:"fetched_at"`
	Concept2UserID int64            `json:"concept2_user_id"`
	Results        []concept2.Result `json:"results"`
}

func main() {
	loadDotEnv()

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "fetch":
		err = runFetch(os.Args[2:])
	case "send":
		err = runSend(os.Args[2:])
	case "inspect":
		err = runInspect(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: simhook <command> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  fetch   Fetch recent Concept2 results and save to", savedFile)
	fmt.Fprintln(os.Stderr, "          flags: --count N  (default 10)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  send    Post a saved result as a webhook to the local server")
	fmt.Fprintln(os.Stderr, "          flags: --index N, --id RESULT_ID, --target URL")
}

// ── fetch ─────────────────────────────────────────────────────────────────────

func runFetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	count := fs.Int("count", 10, "number of results to fetch")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, mustEnv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	enc, err := loadEncrypter()
	if err != nil {
		return err
	}

	concept2UserID, accessToken, err := loadConcept2Token(ctx, pool, enc)
	if err != nil {
		return err
	}

	apiBase := envOrDefault("CONCEPT2_API_BASE", "https://log.concept2.com")
	httpClient := &http.Client{Timeout: 15 * time.Second}

	fmt.Printf("Fetching last %d results from Concept2...\n", *count)
	results, err := concept2.ListResults(ctx, httpClient, apiBase, accessToken, *count)
	if err != nil {
		return fmt.Errorf("fetch results: %w", err)
	}
	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	if err := os.MkdirAll("tmp", 0755); err != nil {
		return fmt.Errorf("create tmp dir: %w", err)
	}
	saved := savedResults{
		FetchedAt:      time.Now(),
		Concept2UserID: concept2UserID,
		Results:        results,
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	if err := os.WriteFile(savedFile, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", savedFile, err)
	}

	fmt.Printf("Saved %d results to %s.\n\n", len(results), savedFile)
	printTable(results)
	return nil
}

// ── send ──────────────────────────────────────────────────────────────────────

func runSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	target := fs.String("target", "", "webhook URL (default: http://localhost:PORT/webhooks/concept2)")
	index := fs.Int("index", 0, "1-based index of result to send (0 = prompt)")
	id := fs.Int64("id", 0, "Concept2 result ID to send")
	if err := fs.Parse(args); err != nil {
		return err
	}

	webhookSecret := mustEnv("CONCEPT2_WEBHOOK_SECRET")

	if *target == "" {
		port := envOrDefault("PORT", "8080")
		*target = fmt.Sprintf("http://localhost:%s/webhooks/concept2", port)
	}

	data, err := os.ReadFile(savedFile)
	if err != nil {
		return fmt.Errorf("read %s: run 'fetch' first (%w)", savedFile, err)
	}
	var saved savedResults
	if err := json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("parse %s: %w", savedFile, err)
	}
	if len(saved.Results) == 0 {
		return fmt.Errorf("no results in %s", savedFile)
	}

	result, err := selectResult(saved, *index, *id)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(map[string]any{
		"type":      "result",
		"user_id":   saved.Concept2UserID,
		"result_id": result.ID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(payload)
	sig := hex.EncodeToString(mac.Sum(nil))

	fmt.Printf("\nSending result %d (%s · %s · %s)...\n",
		result.ID, result.Date, result.Type, formatDistance(result.Distance))
	fmt.Printf("POST %s\n", *target)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, *target, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Concept2-Signature", sig)

	resp, err := (&http.Client{Timeout: 35 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("POST: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	fmt.Printf("→ %s", resp.Status)
	if trimmed := bytes.TrimSpace(respBody); len(trimmed) > 0 {
		fmt.Printf(": %s", trimmed)
	}
	fmt.Println()
	return nil
}

func selectResult(saved savedResults, index int, id int64) (concept2.Result, error) {
	switch {
	case id != 0:
		for _, r := range saved.Results {
			if r.ID == id {
				return r, nil
			}
		}
		return concept2.Result{}, fmt.Errorf("result ID %d not found in %s", id, savedFile)

	case index != 0:
		if index < 1 || index > len(saved.Results) {
			return concept2.Result{}, fmt.Errorf("index %d out of range (1-%d)", index, len(saved.Results))
		}
		return saved.Results[index-1], nil

	default:
		printTable(saved.Results)
		fmt.Printf("\nSelect result (1-%d): ", len(saved.Results))
		var n int
		if _, err := fmt.Scan(&n); err != nil {
			return concept2.Result{}, fmt.Errorf("read selection: %w", err)
		}
		if n < 1 || n > len(saved.Results) {
			return concept2.Result{}, fmt.Errorf("selection %d out of range (1-%d)", n, len(saved.Results))
		}
		return saved.Results[n-1], nil
	}
}

// ── inspect ───────────────────────────────────────────────────────────────────

// runInspect fetches a single result by ID and prints the raw JSON response so
// we can inspect the full structure returned by the Concept2 API (splits, etc.).
func runInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	id := fs.Int64("id", 0, "Concept2 result ID to inspect")
	index := fs.Int("index", 0, "1-based index from saved results")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, mustEnv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	enc, err := loadEncrypter()
	if err != nil {
		return err
	}

	_, accessToken, err := loadConcept2Token(ctx, pool, enc)
	if err != nil {
		return err
	}

	var resultID int64
	switch {
	case *id != 0:
		resultID = *id
	case *index != 0:
		data, readErr := os.ReadFile(savedFile)
		if readErr != nil {
			return fmt.Errorf("read %s: run 'fetch' first (%w)", savedFile, readErr)
		}
		var saved savedResults
		if err := json.Unmarshal(data, &saved); err != nil {
			return fmt.Errorf("parse %s: %w", savedFile, err)
		}
		if *index < 1 || *index > len(saved.Results) {
			return fmt.Errorf("index %d out of range (1-%d)", *index, len(saved.Results))
		}
		resultID = saved.Results[*index-1].ID
	default:
		return fmt.Errorf("provide --id RESULT_ID or --index N")
	}

	apiBase := envOrDefault("CONCEPT2_API_BASE", "https://log.concept2.com")
	url := fmt.Sprintf("%s/api/users/me/results/%d", apiBase, resultID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Pretty-print the raw JSON.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		fmt.Println(string(body))
	} else {
		fmt.Println(pretty.String())
	}
	return nil
}

// ── DB / token helpers ────────────────────────────────────────────────────────

// loadConcept2Token finds the Concept2 OAuth token in the DB, decrypts it, and
// refreshes it if it has expired or is about to.
func loadConcept2Token(ctx context.Context, pool *pgxpool.Pool, enc *secrets.Encrypter) (concept2UserID int64, accessToken string, err error) {
	var (
		identityID      uuid.UUID
		providerUserID  string
		accessTokenEnc  []byte
		refreshTokenEnc []byte
		expiresAt       pgtype.Timestamptz
	)
	err = pool.QueryRow(ctx, `
		SELECT oi.id, oi.provider_user_id,
		       ot.access_token_enc, ot.refresh_token_enc, ot.expires_at
		FROM oauth_identities oi
		JOIN oauth_tokens ot ON ot.oauth_identity_id = oi.id
		WHERE oi.provider = 'concept2'
		LIMIT 1
	`).Scan(&identityID, &providerUserID, &accessTokenEnc, &refreshTokenEnc, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", fmt.Errorf("no Concept2 account linked — link one via the profile page first")
	}
	if err != nil {
		return 0, "", fmt.Errorf("query token: %w", err)
	}

	concept2UserID, err = strconv.ParseInt(providerUserID, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("parse concept2 user id %q: %w", providerUserID, err)
	}

	plain, err := enc.Decrypt(accessTokenEnc)
	if err != nil {
		return 0, "", fmt.Errorf("decrypt access token: %w", err)
	}
	accessToken = string(plain)

	if expiresAt.Valid && time.Until(expiresAt.Time) <= 60*time.Second {
		fmt.Println("Access token expired or expiring soon, refreshing...")
		accessToken, err = refreshToken(ctx, pool, enc, identityID, refreshTokenEnc)
		if err != nil {
			return 0, "", fmt.Errorf("refresh token: %w", err)
		}
	}

	return concept2UserID, accessToken, nil
}

func refreshToken(ctx context.Context, pool *pgxpool.Pool, enc *secrets.Encrypter, identityID uuid.UUID, refreshTokenEnc []byte) (string, error) {
	plain, err := enc.Decrypt(refreshTokenEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt refresh token: %w", err)
	}

	c2 := oauth.NewConcept2Client(
		mustEnv("CONCEPT2_CLIENT_ID"),
		mustEnv("CONCEPT2_CLIENT_SECRET"),
		"",
		envOrDefault("CONCEPT2_API_BASE", "https://log.concept2.com"),
		nil,
	)
	tok, err := c2.RefreshToken(ctx, string(plain))
	if err != nil {
		return "", err
	}

	accessEnc, err := enc.Encrypt([]byte(tok.AccessToken))
	if err != nil {
		return "", fmt.Errorf("encrypt access token: %w", err)
	}
	var refreshEnc []byte
	if tok.RefreshToken != "" {
		refreshEnc, err = enc.Encrypt([]byte(tok.RefreshToken))
		if err != nil {
			return "", fmt.Errorf("encrypt refresh token: %w", err)
		}
	}
	var expiresAt pgtype.Timestamptz
	if tok.ExpiresIn > 0 {
		expiresAt = pgtype.Timestamptz{
			Time:  time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
			Valid: true,
		}
	}

	_, err = pool.Exec(ctx, `
		UPDATE oauth_tokens
		SET access_token_enc  = $1,
		    refresh_token_enc = $2,
		    expires_at        = $3,
		    updated_at        = NOW()
		WHERE oauth_identity_id = $4
	`, accessEnc, refreshEnc, expiresAt, identityID)
	if err != nil {
		return "", fmt.Errorf("persist refreshed token: %w", err)
	}

	fmt.Println("Token refreshed.")
	return tok.AccessToken, nil
}

func loadEncrypter() (*secrets.Encrypter, error) {
	raw := mustEnv("OAUTH_TOKEN_ENC_KEY")
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("OAUTH_TOKEN_ENC_KEY must be a base64-encoded 32-byte key")
	}
	return secrets.New(key)
}

// ── display ───────────────────────────────────────────────────────────────────

func printTable(results []concept2.Result) {
	fmt.Printf(" %-4s  %-10s  %-12s  %-8s  %-10s  %s\n",
		"#", "Result ID", "Date", "Type", "Distance", "Time")
	fmt.Println(strings.Repeat("─", 66))
	for i, r := range results {
		fmt.Printf(" %-4d  %-10d  %-12s  %-8s  %-10s  %s\n",
			i+1, r.ID, r.Date, r.Type, formatDistance(r.Distance), formatDuration(r.Time))
	}
}

func formatDuration(tenths int64) string {
	if tenths <= 0 {
		return "0:00.0"
	}
	t := tenths % 10
	totalSeconds := tenths / 10
	seconds := totalSeconds % 60
	minutes := totalSeconds / 60
	return fmt.Sprintf("%d:%02d.%d", minutes, seconds, t)
}

func formatDistance(metres float64) string {
	if metres >= 10000 {
		return fmt.Sprintf("%.1f km", metres/1000)
	}
	m := int64(metres)
	if m >= 1000 {
		return fmt.Sprintf("%d,%03d m", m/1000, m%1000)
	}
	return fmt.Sprintf("%d m", m)
}

// ── env / .env ────────────────────────────────────────────────────────────────

// loadDotEnv reads key=value pairs from .env into the process environment.
// It does not override variables already set in the environment.
func loadDotEnv() {
	f, err := os.Open(".env")
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 {
			if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
				v = v[1 : len(v)-1]
			}
		}
		if os.Getenv(k) == "" {
			os.Setenv(k, v) //nolint:errcheck
		}
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "error: %s is not set (check .env)\n", key)
		os.Exit(1)
	}
	return v
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
