package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// entry holds the request count and the time the window expires.
type entry struct {
	count     atomic.Int64
	expiresAt time.Time
}

// RateLimiter is an in-memory rate limiter using sync.Map with TTL-based expiry.
// Not shared across processes; suitable for single-instance deployments.
type RateLimiter struct {
	store   sync.Map
	limit   int64
	window  time.Duration
	keyFunc func(*http.Request) string
}

// NewRateLimiter creates a RateLimiter with the given limit, window, and key extractor.
// The provided ctx controls the lifetime of the background sweep goroutine; cancel it
// during application shutdown to cleanly stop the sweeper.
func NewRateLimiter(ctx context.Context, limit int, window time.Duration, keyFunc func(*http.Request) string) *RateLimiter {
	rl := &RateLimiter{
		limit:   int64(limit),
		window:  window,
		keyFunc: keyFunc,
	}
	go rl.sweep(ctx)
	return rl
}

// Middleware returns an http.Handler middleware that enforces the rate limit.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.keyFunc(r)
		now := time.Now()

		val, _ := rl.store.LoadOrStore(key, &entry{expiresAt: now.Add(rl.window)})
		e := val.(*entry)

		// If the window has expired, reset the entry.
		if now.After(e.expiresAt) {
			newEntry := &entry{expiresAt: now.Add(rl.window)}
			newEntry.count.Store(1)
			rl.store.Store(key, newEntry)
			next.ServeHTTP(w, r)
			return
		}

		count := e.count.Add(1)
		if count > rl.limit {
			retryAfter := int(time.Until(e.expiresAt).Seconds()) + 1
			slog.WarnContext(r.Context(), "rate limit exceeded",
				"key", key,
				"path", r.URL.Path,
				"request_id", GetRequestID(r.Context()),
			)
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// sweep periodically removes expired entries to prevent unbounded memory growth.
// It exits when ctx is cancelled, which should happen during application shutdown.
func (rl *RateLimiter) sweep(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			rl.store.Range(func(k, v interface{}) bool {
				if e, ok := v.(*entry); ok && now.After(e.expiresAt) {
					rl.store.Delete(k)
				}
				return true
			})
		}
	}
}

// IPKeyFunc extracts the client IP for rate limiting.
// When an X-Forwarded-For header is present (reverse-proxy deployments), it takes
// only the first (leftmost) IP to avoid spoofing via appended values.
func IPKeyFunc(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return "ip:" + firstForwardedIP(fwd)
	}
	return fmt.Sprintf("ip:%s", r.RemoteAddr)
}

// FormEmailKeyFunc extracts the email field from the form body.
func FormEmailKeyFunc(r *http.Request) string {
	_ = r.ParseForm()
	return "email:" + r.FormValue("email")
}

// CookieRefreshTokenKeyFunc extracts the refresh token cookie as the rate-limit key.
func CookieRefreshTokenKeyFunc(r *http.Request) string {
	if c, err := r.Cookie("refresh_token"); err == nil {
		return "rt:" + c.Value[:min(len(c.Value), 16)] // use prefix only
	}
	return IPKeyFunc(r) // fallback to IP
}

// firstForwardedIP returns the leftmost (client) IP from an X-Forwarded-For value,
// which may be a comma-separated list: "client, proxy1, proxy2".
func firstForwardedIP(xff string) string {
	first, _, _ := strings.Cut(xff, ",")
	return strings.TrimSpace(first)
}
