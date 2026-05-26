# Authentication System Design

## Overview

Auth is built on two token types: a short-lived JWT access token (15 minutes) and a long-lived opaque refresh token (30 days). The access token travels in a cookie and optionally in `Authorization: Bearer` headers. The refresh token travels only as an `HttpOnly` cookie and is never readable by JavaScript.

All sensitive values (refresh tokens, password reset tokens) are hashed with SHA-256 before storage. The raw value is transmitted once to the client and never persisted.

---

## Token Design

### Access Token (JWT)

| Property | Value |
|---|---|
| Algorithm | HS256 |
| Secret | `JWT_SECRET` env var (minimum 32 bytes) |
| Lifetime | 15 minutes (configurable via `JWT_ACCESS_EXPIRY`) |
| Cookie name | `access_token` |
| Cookie flags | `HttpOnly`, `Secure` (production), `SameSite=Strict` |

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

| Property | Value |
|---|---|
| Format | 32 cryptographically random bytes, base64url-encoded |
| Storage | SHA-256 hash stored in `refresh_tokens.token_hash` |
| Lifetime | 30 days (configurable via `REFRESH_TOKEN_EXPIRY`) |
| Cookie name | `refresh_token` |
| Cookie flags | `HttpOnly`, `Secure` (production), `SameSite=Strict` |
| Rotation | Mandatory on every use |

---

## Password Requirements

**Validation rules:**
- Minimum 8 characters (configurable via `PASSWORD_MIN_LENGTH`)
- No maximum length — passphrases are encouraged
- No complexity requirements — length beats complexity for user-chosen passwords
- Optional: check against HaveIBeenPwned API (controlled by feature flag; disabled by default)

**Storage:**
- bcrypt with cost factor 12 (configurable via `BCRYPT_COST`)
- The raw password is zeroed from memory immediately after hashing
- Passwords are never logged, never returned in responses, never stored in plaintext

---

## Email Requirements

- Stored lowercase; normalized on every write and every lookup
- Validated against RFC 5322 via library (not regex) before any DB operation
- Unique constraint enforced at the database level and pre-checked in the service layer to return a user-friendly error

---

## Login Flow

```
Client                        Handler              AuthService             DB
  |                              |                     |                    |
  |--- POST /auth/login -------->|                     |                    |
  |    {email, password}         |                     |                    |
  |                              |-- Login(req) ------->|                    |
  |                              |                     |-- GetUserByEmail -->|
  |                              |                     |<-- User / nil ------|
  |                              |                     |                    |
  |                              |              [locked_until in future?]   |
  |                              |              YES → return 423            |
  |                              |                     |                    |
  |                              |              [email not found?]          |
  |                              |              → increment attempts (if    |
  |                              |                user exists), return 401  |
  |                              |                     |                    |
  |                              |              bcrypt.Compare(password)    |
  |                              |              FAIL → increment attempts   |
  |                              |                     after 10 → lock 1h  |
  |                              |              OK  → reset attempts + lock |
  |                              |                     |                    |
  |                              |              generate JWT (15m)          |
  |                              |              generate token_family UUID  |
  |                              |              generate refresh token      |
  |                              |              SHA-256 hash refresh token  |
  |                              |                     |-- InsertRefreshToken|
  |                              |                     |   (with metadata)  |
  |                              |<-- tokens -----------|                    |
  |                              |                     |                    |
  |<-- set-cookie: access_token -|                     |                    |
  |<-- set-cookie: refresh_token |                     |                    |
  |<-- 200 OK + user data -------|                     |                    |
```

**Failed login handling:**
- Every failed attempt increments `failed_login_attempts` for the matching user
- After 10 failures: `locked_until = NOW() + INTERVAL '1 hour'`
- At the start of any login attempt: if `locked_until IS NOT NULL AND locked_until > NOW()` → return 423 immediately, do not check password
- On success: set `failed_login_attempts = 0`, set `locked_until = NULL`
- Email enumeration is prevented: the error message for "email not found" is identical to "wrong password"

**Device metadata captured at login:**
- `device_name` — derived from User-Agent string (best-effort)
- `ip_address` — from `X-Forwarded-For` header (trusted proxy required) or `RemoteAddr`
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
  |                              |              [not found] → 401           |
  |                              |              [expired]   → 401           |
  |                              |                     |                    |
  |                              |              [revoked_at IS NOT NULL]    |
  |                              |              THEFT DETECTED:             |
  |                              |              → RevokeTokenFamily         |
  |                              |                (all tokens, same family) |
  |                              |              → 401 force re-login        |
  |                              |                     |                    |
  |                              |              [valid token]               |
  |                              |              load user (verify active)   |
  |                              |              generate new JWT            |
  |                              |              generate new refresh token  |
  |                              |              BEGIN TRANSACTION:          |
  |                              |              - insert new refresh token  |
  |                              |                (same family, prev_id =   |
  |                              |                 old token id)            |
  |                              |              - set old revoked_at        |
  |                              |              - update last_used_at       |
  |                              |              COMMIT                      |
  |                              |<-- new tokens ------|                    |
  |<-- set-cookie: access_token -|                     |                    |
  |<-- set-cookie: refresh_token |                     |                    |
  |<-- 200 OK --------------------|                    |                    |
```

**Token family theft detection logic:**
- Each login creates a new `token_family` (fresh UUIDv7)
- All rotated tokens in the same session share that `token_family`
- If a token is presented that is already `revoked_at IS NOT NULL`, it means someone replayed a used token — this is a theft signal
- Response: revoke every token in the entire `token_family` → attacker and legitimate user both lose access → legitimate user must re-login

---

## Auth Middleware

Applied to all protected routes. Order of evaluation:

1. Read token from `access_token` cookie; fall back to `Authorization: Bearer <token>` header
2. Parse and validate JWT signature using `JWT_SECRET`
3. Verify `exp` claim — not expired
4. Extract `sub` claim → user ID (UUIDv7)
5. Load user from DB by ID
6. Verify user record exists and is not locked
7. Attach user to `context.Context` under a typed key
8. Call `next` handler

**On expiry (401):**
- Set `HX-Trigger: token-expired` response header (for HTMX requests)
- Client-side JS listener catches this event, calls `/auth/refresh`, retries the original request on success, redirects to `/login` on failure
- For non-HTMX requests: return standard 401 JSON response

---

## Logout

`POST /auth/logout`:
1. Read `refresh_token` cookie
2. Hash and look up in DB
3. Set `revoked_at = NOW()` for that token
4. Clear both `access_token` and `refresh_token` cookies (zero-length, expired)
5. Redirect to `/login`

---

## Session Management

### List Active Sessions

`GET /auth/sessions` (authenticated):
- Query all `refresh_tokens` for the current user where `revoked_at IS NULL AND expires_at > NOW()`
- Return rendered table: `device_name`, `ip_address`, `last_used_at`, `created_at`
- Mark current session (matched by current `refresh_token` cookie) visually

### Revoke Specific Session

`DELETE /auth/sessions/{token_id}` (authenticated):
- Verify `token_id` belongs to the current user (authorization check — not just authentication)
- Set `revoked_at = NOW()`
- Return updated session list fragment for HTMX swap

### Revoke All Sessions

Triggered automatically on:
- **Password change**: revoke all refresh tokens for the user → forces re-login on all devices
- **Account compromise** (future admin endpoint): revoke all tokens for a user

---

## Password Reset Flow

### Request Reset (`POST /auth/forgot-password`)

1. Validate email format (RFC 5322)
2. Always return `200 OK` with "If that email is registered, a reset link has been sent." — prevents email enumeration
3. If email exists in DB:
   - Check rate limit: max 3 requests per hour per email (in-memory, keyed by lowercase email)
   - Generate 32-byte cryptographically random token; URL-safe base64-encode for the link
   - SHA-256 hash the raw token; store hash in `password_reset_tokens` with `expires_at = NOW() + 1 hour`
   - Send email: `{APP_BASE_URL}/reset-password?token={raw_token}`

### Complete Reset (`POST /auth/reset-password`)

1. Receive `token` (from URL query param or form field) and `new_password`
2. SHA-256 hash the token; look up in `password_reset_tokens`
3. Reject if: not found, `expires_at < NOW()`, or `used_at IS NOT NULL`
4. Validate new password against policy
5. In a transaction:
   - `bcrypt.GenerateFromPassword(newPassword, cost)` → update `users.password_hash`
   - Reset `failed_login_attempts = 0`, clear `locked_until = NULL`
   - Set `password_reset_tokens.used_at = NOW()`
   - Revoke all `refresh_tokens` for the user (`revoked_at = NOW()`)
6. Send password-change confirmation email
7. Redirect to `/login`

---

## Email Verification

### Issuance (on registration)

1. Generate 6-digit numeric code (`crypto/rand` → mod 1,000,000, zero-padded)
2. Store plaintext code in `email_verification_codes` with `expires_at = NOW() + 15 minutes`
3. Send code via email
4. Rate limit: max 3 codes per hour per email

### Verification (`POST /auth/verify-email`)

1. User submits the 6-digit code
2. Look up latest unused, unexpired code for the user
3. Compare submitted code (constant-time string comparison)
4. If valid: set `users.email_verified = true`, set `used_at = NOW()`
5. If invalid: return error; do not increment failure counter (rate limiting protects this endpoint)

---

## CSRF Protection

**Token generation:**
- 32-byte value from `crypto/rand`, base64-encoded
- Stored in a signed `HttpOnly` cookie (`csrf_token`) with `SameSite=Strict`
- Also embedded in every rendered HTML form as `<input type="hidden" name="csrf_token" value="...">`

**Validation (all POST / PUT / PATCH / DELETE requests):**
1. Read token from form field `csrf_token` OR from `X-CSRF-Token` request header (HTMX compatibility)
2. Compare with token in the `csrf_token` cookie using `subtle.ConstantTimeCompare`
3. Mismatch → 403 Forbidden; do not proceed

**HTMX forms** include the CSRF token via a global `htmx.config.headers` entry injected in the base template so HTMX automatically sends `X-CSRF-Token` on every request.

---

## Rate Limiting

Implemented as in-memory middleware using `sync.Map` with atomic counters and TTL-based expiry. Does not require Redis. Not shared across multiple instances (acceptable for initial deployment; Redis can be layered in later).

| Endpoint | Limit | Window | Key |
|---|---|---|---|
| `POST /auth/login` | 5 attempts | 15 minutes | IP address |
| `POST /auth/register` | 3 attempts | 1 hour | IP address |
| `POST /auth/refresh` | 10 attempts | 1 minute | refresh token hash |
| `POST /auth/forgot-password` | 3 requests | 1 hour | lowercase email |
| `POST /auth/reset-password` | 5 attempts | 1 hour | IP address |

**Response when limit exceeded:**
- HTTP 429 Too Many Requests
- `Retry-After: <seconds>` header set to remaining TTL
- Rate limit violations logged at WARN level with request ID, IP, and endpoint

**TTL cleanup:** a background goroutine sweeps the `sync.Map` periodically (every 5 minutes) to remove expired entries and prevent unbounded memory growth.

---

## Security Invariants

| Concern | Mitigation |
|---|---|
| Password storage | bcrypt cost 12; raw password never logged or returned |
| Refresh token storage | SHA-256 hashed; raw token transmitted once, never persisted |
| Reset token storage | SHA-256 hashed; raw token only in the emailed link |
| Token comparison | `crypto/subtle.ConstantTimeCompare` for all hash comparisons |
| SQL injection | Parameterized queries only (sqlc-generated) |
| XSS | `html/template` auto-escaping; HTMX responses are also HTML-escaped |
| CSRF | Signed cookie + form hidden field + constant-time comparison |
| Session theft | HttpOnly cookies prevent JS access to tokens |
| Cookie security | `Secure=true` gated on `APP_ENV=production` |
| Email enumeration | All auth endpoints return identical messages for found/not-found |
| Brute force | Account lockout after 10 failures; IP-level rate limits |
| Token replay | Refresh tokens single-use; rotation is mandatory; theft → family revocation |
| CORS | Not configured; all clients served from same origin |
| Secrets in images | Docker build args used only for tooling; secrets come from env vars at runtime |
