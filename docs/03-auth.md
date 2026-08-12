# Authentication System Design

## Overview

Auth is built on two token types: a short-lived JWT access token (15 minutes) and a long-lived opaque refresh token (30 days). The access token travels in a cookie and optionally in `Authorization: Bearer` headers. The refresh token travels only as an `HttpOnly` cookie and is never readable by JavaScript.

All sensitive values (refresh tokens, email verification tokens) are hashed with SHA-256 before storage. The raw value is transmitted once to the client and never persisted.

---

## Token Design

### Access Token (JWT)

| Property     | Value                                             |
| ------------ | ------------------------------------------------- |
| Algorithm    | HS256                                             |
| Secret       | `JWT_SECRET` env var (minimum 32 bytes)           |
| Lifetime     | 15 minutes (configurable via `JWT_ACCESS_EXPIRY`) |
| Cookie name  | `access_token`                                    |
| Cookie flags | `HttpOnly`, `Secure` (production), `SameSite=Lax` |

**Claims:**

```json
{
  "sub": "<UUIDv7 user ID>",
  "email": "user@example.com",
  "iat": 1700000000,
  "exp": 1700000900
}
```

The access token is also accepted from `Authorization: Bearer <token>` to support non-browser clients.

### Refresh Token

| Property     | Value                                                                  |
| ------------ | ---------------------------------------------------------------------- |
| Format       | 32 cryptographically random bytes, base64url-encoded                   |
| Storage      | SHA-256 hash stored in `refresh_tokens.token_hash`                     |
| Lifetime     | 5 days (configurable via `REFRESH_TOKEN_EXPIRY`)                       |
| Cookie name  | `refresh_token`                                                        |
| Cookie flags | `HttpOnly`, `Secure` (production), `SameSite=Lax`                      |
| Rotation     | None — the same refresh token is reused until it expires or is revoked |

**No rotation.** `Refresh` issues a fresh access token but returns the _same_ refresh token, updating `last_used_at` and device metadata in place. There is no token-family or replay-theft-detection machinery. A refresh token is invalidated only by logout or expiry.

---

## Email Requirements

- Stored lowercase; normalized on every write and every lookup
- Validated against RFC 5322 via library (not regex) before any DB operation
- Unique constraint enforced at the database level and pre-checked in the service layer to return a user-friendly error

---

## Login Flow

Account creation and login happen exclusively via Discord OAuth (see `OAuthService`) — there is no password-based login. The OAuth callback either finds an existing linked user or provisions a new one, then issues a token pair the same way the flows below describe.

**Device metadata captured at login:**

- `device_name` — derived from User-Agent string (best-effort)
- `ip_address` — from `X-Forwarded-For` (only when a trusted-proxy count is configured) or `RemoteAddr`, parsed via the shared `middleware.ClientIP` helper
- `user_agent` — raw User-Agent header value

---

## Token Refresh Flow

```
Client                        Handler              AuthService             DB
  |                              |                     |                    |
  |--- POST /auth/refresh ------->|                    |                    |
  |    cookie: refresh_token      |                    |                    |
  |                              |-- Refresh(token) -->|                    |
  |                              |                     |-- GetByTokenHash ->|
  |                              |                     |<-- row or nil -----|
  |                              |                     |                    |
  |                              |       [not found]            → 401       |
  |                              |       [revoked_at NOT NULL]  → 401       |
  |                              |       [expires_at < NOW()]   → 401       |
  |                              |                     |                    |
  |                              |       [valid token]                      |
  |                              |       load user                          |
  |                              |       update last_used_at + device meta  |
  |                              |       generate new access JWT            |
  |                              |       (refresh token unchanged)          |
  |                              |<-- new access token -|                   |
  |<-- set-cookie: access_token -|                     |                    |
  |<-- set-cookie: refresh_token |  (same value, refreshed expiry window)   |
  |<-- 200 OK --------------------|                    |                    |
```

A revoked or expired refresh token is simply rejected with 401, forcing a re-login. There is no cascading family revocation, because tokens are not rotated and therefore cannot be replayed-after-rotation in the first place.

---

## Auth Middleware

Applied to protected routes (per-route, not globally). Order of evaluation:

1. Read token from `access_token` cookie; fall back to `Authorization: Bearer <token>` header
2. Parse and validate JWT signature using `JWT_SECRET`
3. Verify `exp` claim — not expired
4. Extract `sub` claim → user ID (UUIDv7)
5. Load user from DB by ID
6. Verify user record exists
7. Attach user to `context.Context` under a typed key
8. Call `next` handler

**On expiry (401):** the middleware sets `HX-Trigger: token-expired` for HTMX requests. A listener in `web/static/js/app.js` catches the event, calls `/auth/refresh`, and retries on success or redirects to `/` on failure. On a missing token the middleware sets `HX-Redirect: /`.

---

## Logout

`POST /auth/logout`:

1. Read `refresh_token` cookie
2. Hash and look up in DB
3. Set `revoked_at = NOW()` for that token (no-op if the token is unknown)
4. Clear both `access_token` and `refresh_token` cookies (zero-length, expired)
5. Redirect to `/dashboard`

---

## Session Management

### List Active Sessions

`GET /auth/sessions` (authenticated):

- Query all `refresh_tokens` for the current user where `revoked_at IS NULL AND expires_at > NOW()`
- Return rendered table: `device_name`, `ip_address`, `last_used_at`, `created_at`
- Mark current session (matched by current `refresh_token` cookie hash) visually

### Revoke Specific Session

`DELETE /auth/sessions/{id}` (authenticated):

- Verify the token belongs to the current user (authorization check — not just authentication). Ownership failures are masked as **404** so they don't confirm a token exists.
- Set `revoked_at = NOW()`
- If the revoked token is the caller's current session, respond with a logout redirect; otherwise return the updated session-list fragment for HTMX swap

---

## Email Verification

Verification is **link-based**, not code-based.

### Issuance (on registration, and via resend)

1. Generate a 32-byte cryptographically random token (`crypto/rand`), URL-safe base64-encoded
2. SHA-256 hash the token; store the hash in `email_verification_codes` with `expires_at = NOW() + 24 hours`
3. Email a link: `{APP_BASE_URL}/auth/verify-email?token={raw_token}`
4. Rate limit: max 3 verification emails per hour per user (`ResendVerification` returns a retry time when exceeded)

### Verification (`GET /auth/verify-email?token=...`, public)

1. SHA-256 hash the token from the query string and look up the record
2. Reject if: not found, `used_at IS NOT NULL`, or `expires_at < NOW()`
3. Mark the record used, set `users.email_verified = true`
4. Issue a fresh token pair so the click **auto-logs the user in**, then redirect to the dashboard

---

## CSRF Protection

There are **no CSRF tokens**. CSRF is mitigated by:

- `SameSite=Lax` on the auth cookies — cross-site POSTs do not carry the session cookies
- Same-origin form posts — all forms are served from and submitted to the same origin
- State-changing endpoints accept only their intended methods (`POST`/`DELETE`), and the access token must be present as a cookie set by this origin

If a stricter posture is needed later (e.g. embedding in third-party contexts), a synchronizer-token or double-submit scheme can be layered on, but it is intentionally omitted here to keep the surface small.

---

## Request Body Limit

A global `BodyLimit` middleware wraps the mux and caps request bodies at 1 MiB (`DefaultMaxBodyBytes`) via `http.MaxBytesReader`, protecting every endpoint from oversized-payload memory pressure before handlers run.

---

## Rate Limiting

Implemented as in-memory middleware using `sync.Map` with atomic counters and TTL-based expiry. Does not require Redis. Not shared across multiple instances (acceptable for initial deployment; Redis can be layered in later).

| Endpoint                                                 | Limit       | Window     | Key                                   |
| --------------------------------------------------------- | ----------- | ---------- | ------------------------------------- |
| `POST /auth/refresh`, `GET /auth/silent-refresh`           | 10 attempts | 1 minute   | refresh token hash (falls back to IP) |
| `GET /auth/discord/login`, `GET /auth/discord/callback`    | 10 attempts | 15 minutes | IP address                            |

**Response when limit exceeded:**

- HTTP 429 Too Many Requests
- `Retry-After: <seconds>` header set to remaining TTL

**TTL cleanup:** a background goroutine sweeps the `sync.Map` periodically to remove expired entries and prevent unbounded memory growth.

---

## Security Invariants

| Concern                     | Mitigation                                                                   |
| ---------------------------- | ----------------------------------------------------------------------------- |
| Refresh token storage       | SHA-256 hashed; raw token transmitted once, never persisted                 |
| Verification token storage  | SHA-256 hashed; raw token only in the emailed link                          |
| Token comparison            | Tokens are looked up by SHA-256 hash                                        |
| SQL injection                | Parameterized queries only (sqlc-generated)                                 |
| XSS                          | `html/template` auto-escaping; HTMX responses are also HTML-escaped         |
| CSRF                         | `SameSite=Lax` cookies + same-origin form posts (no CSRF tokens)            |
| Session theft                | HttpOnly cookies prevent JS access to tokens                                |
| Cookie security               | `Secure=true` gated on `APP_ENV=production`                                 |
| Brute force                   | IP-level rate limits on Discord OAuth login/callback and token refresh      |
| Token revocation              | Logout revokes the refresh token; expired/revoked tokens rejected at refresh |
| Oversized payloads                 | 1 MiB request body limit via `http.MaxBytesReader`                                          |
| CORS                               | Not configured; all clients served from same origin                                         |
| Secrets in images                  | Docker build args used only for tooling; secrets come from env vars at runtime              |
