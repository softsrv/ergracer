## Objective

Create a production-ready web app with:

- Go backend (stdlib-first)
- HTMX 4.0 + server-rendered HTML templates
- TailwindCSS with DaisyUI for themes and component library
- PostgreSQL via `DATABASE_URL`
- JWT auth with refresh tokens (short-lived access tokens)
- Dockerized deployment
- Makefile-driven workflows

Prioritize simplicity, maintainability, and explicit code.

## Tech Rules

1. Use Go standard library whenever possible.
2. If stdlib is not enough, propose 1-2 well-known libraries, explain why, and ask approval before adding.
3. Prefer vanilla JS with HTMX for dynamics; avoid frontend frameworks unless requested.
4. Use sqlc-dev/sqlc for typesafe compiled db access.
5. Use golang-migrate/migrate for database migrations.
6. Use google/uuid for UUIDv7 generation.
7. Use bcrypt (cost factor 12) for password hashing.

## Architecture Rules

Use this structure:

.
├─ cmd/app/main.go
├─ internal/
│ ├─ app/
│ ├─ auth/
│ ├─ db/
│ ├─ users/
│ ├─ email/
│ ├─ http/
│ │ ├─ middleware/
│ │ ├─ handlers/
│ │ └─ router.go
│ └─ views/
├─ web/
│ ├─ templates/
│ └─ static/
│ ├─ css/
│ └─ js/
├─ db/
│ ├─ migrations/
│ ├─ queries/
│ └─ sqlc.yaml
├─ Dockerfile
├─ .dockerignore
├─ Makefile
├─ .air.toml
├─ .env.example
└─ README.md

Constraints:

- `main.go` bootstraps only: parse config, initialize the DB pool, wire dependencies, start the HTTP server, and handle shutdown. No business logic.
- `internal/app/` is the service layer: business logic and orchestration that sits between HTTP handlers and the database. Handlers call service methods; service methods call repository functions. Keep handlers thin.
- Business/domain logic stays outside handlers.
- Keep modules small; avoid over-abstraction.

**HTTP server configuration:**
- `ReadTimeout`: 10 seconds
- `WriteTimeout`: 30 seconds
- `IdleTimeout`: 120 seconds
- `ReadHeaderTimeout`: 5 seconds

**Graceful shutdown:**
- Listen for `SIGTERM` and `SIGINT` via `signal.NotifyContext`
- Call `server.Shutdown(ctx)` with a 30-second deadline to drain in-flight requests
- Close the DB pool (`pgxpool.Pool.Close()`) after server shutdown completes
- Stop any background workers (e.g. token cleanup job) before exit
- Log shutdown start and completion at INFO level

## Makefile Requirements

Must include:

- `make dev` (run `air` and `make tailwind-watch` concurrently via `make -j2`; single command for full hot-reload)
- `make run`
- `make build`
- `make test`
- `make fmt`
- `make lint` (run `golangci-lint run`)
- `make tailwind` (one-shot Tailwind CSS build)
- `make tailwind-watch` (Tailwind CSS watch mode; called by `make dev`)
- `make migrate-up` (apply pending migrations)
- `make migrate-down` (rollback last migration)
- `make migrate-create NAME=<name>` (create new migration)
- `make migrate-status` (show applied and pending migrations)
- `make sqlc-generate` (generate Go code from SQL queries)
- `make docker-build`
- `make prod` (build production-ready Docker image)
- `make clean` (remove build artifacts: `./bin/`, generated binaries)

## Docker Requirements

1. Use multi-stage build.
2. Build stage: Use `golang:1.23-alpine` (or latest stable)
3. Runtime stage: Use `alpine:latest` or `gcr.io/distroless/static:latest` for minimal attack surface
4. Do not bake secrets into images.
5. Include `.dockerignore` excluding `.env*`, `.git`, caches, build artifacts.
6. Runtime config comes from env vars.
7. Run as non-root user (create app user with UID 1000)

## Database Requirements

1. Read Postgres connection from `DATABASE_URL`.
2. Use golang-migrate for schema versioning (run migrations via Makefile, not on startup).
3. Migrations live in `db/migrations/` as numbered `.sql` files.
4. SQL queries live in `db/queries/` as `.sql` files for sqlc code generation.

**Connection pooling:**
- Use `pgxpool` for connection pooling
- Max connections: 25 (adjustable via `DB_MAX_CONNS` env var)
- Min connections: 5
- Max connection lifetime: 1 hour
- Max connection idle time: 10 minutes
- Health check period: 1 minute

Minimum tables:

**users table:**
- `id` (UUIDv7, primary key)
- `email` (text, unique, indexed, required, stored as lowercase)
- `password_hash` (text, required, bcrypt cost 12)
- `email_verified` (boolean, default false)
- `failed_login_attempts` (integer, default 0)
- `locked_until` (timestamptz, nullable) - account lock timestamp
- `created_at` (timestamptz)
- `updated_at` (timestamptz)

**refresh_tokens table:**
- `id` (UUIDv7, primary key)
- `user_id` (UUIDv7, foreign key to users.id)
- `token_hash` (SHA-256 hash of refresh token, indexed)
- `token_family` (UUIDv7, indexed) - links all tokens in rotation chain for theft detection
- `previous_token_id` (UUIDv7, nullable, foreign key to refresh_tokens.id) - parent token reference
- `device_name` (text, nullable) - e.g., "iPhone 15", "Chrome on MacOS"
- `ip_address` (inet, nullable) - for audit trail
- `user_agent` (text, nullable) - for device identification
- `expires_at` (timestamptz, indexed)
- `created_at` (timestamptz)
- `last_used_at` (timestamptz) - updated on each refresh
- `revoked_at` (timestamptz, nullable)

**password_reset_tokens table:**
- `id` (UUIDv7, primary key)
- `user_id` (UUIDv7, foreign key to users.id, indexed)
- `token_hash` (SHA-256 hash of reset token, indexed)
- `expires_at` (timestamptz, indexed) - 1 hour from creation
- `created_at` (timestamptz)
- `used_at` (timestamptz, nullable) - set when token is used

**email_verification_codes table:**
- `id` (UUIDv7, primary key)
- `user_id` (UUIDv7, foreign key to users.id, indexed)
- `code` (text, 6-digit numeric code, stored plaintext; short expiry and rate limiting are the primary protections)
- `expires_at` (timestamptz, indexed) - 15 minutes from creation
- `created_at` (timestamptz)
- `used_at` (timestamptz, nullable) - set when code is verified

sqlc configuration:
- Generate code to `internal/db/`
- Use `pgx/v5` for driver
- Enable `:one`, `:many`, `:exec`, `:execrows` query annotations

## Auth Requirements

Support:

- Email/password login
- Password reset workflow for email/password
- Email verification for new accounts (optional but recommended)

### Password Requirements

**Password validation:**
- Minimum 8 characters (configurable via env var)
- No maximum length (allow passphrases)
- No complexity requirements (length > complexity for security)
- Check against compromised password lists (e.g., HaveIBeenPwned API, optional)

**Password storage:**
- Bcrypt with cost factor 12
- Never log or return passwords in responses

### Email Requirements

**Email validation:**
- Case-insensitive storage and lookup (store lowercase)
- RFC 5322 compliant validation (use library, not regex)
- Unique constraint on lowercase email

**Email verification (optional but recommended):**
- Send verification code on signup
- 6-digit numeric code, expires in 15 minutes
- Store in DB with user_id and expiry
- Account marked as unverified until code confirmed
- Resend limit: 3 codes per hour per email

### Token Strategy

**Access Token (JWT):**
- Short-lived: 15 minutes
- Claims: `sub` (user ID as UUIDv7), `email`, `exp`, `iat`
- Signed with `JWT_SECRET` (HS256)
- Sent in response body and as httpOnly cookie

**Refresh Token:**
- Long-lived: 30 days
- Cryptographically random (32 bytes), SHA-256 hashed before storage
- Stored in `refresh_tokens` table with user_id and expiration
- Sent as httpOnly, secure, sameSite cookie

### Login Flow

**Failed login handling:**
- On every failed login attempt (wrong password or unrecognized email): increment `failed_login_attempts` for the matching user, if one exists
- After 10 failed attempts: set `locked_until = NOW() + INTERVAL '1 hour'`
- At the start of every login attempt: if `locked_until` is set and in the future, reject immediately with 423 Locked
- On successful login: reset `failed_login_attempts = 0` and clear `locked_until = NULL`

On successful login:
1. Generate UUIDv7 access token JWT (15min expiry)
2. Generate new token_family (UUIDv7) for this login session
3. Generate cryptographically random refresh token (32 bytes)
4. Hash refresh token with SHA-256, store in DB (30d expiry) with token_family
5. Capture device metadata: device_name, ip_address, user_agent
6. Return both tokens; set both as httpOnly cookies
7. Access token cookie name: `access_token`
8. Refresh token cookie name: `refresh_token`

### Token Refresh Endpoint

`POST /auth/refresh`:
1. Read refresh token from cookie
2. Hash and lookup in DB
3. Verify not revoked and not expired
4. **Theft detection**: If token is revoked but reused, revoke entire token_family (indicates stolen token)
5. Load associated user
6. Issue new access token (15min)
7. **Mandatory rotation**: Issue new refresh token (same token_family, reference current token as previous_token_id)
8. **Atomic operation**: Save new token and revoke old token in single transaction
9. Update last_used_at, device_name, ip_address, user_agent
10. Return new access token and refresh token; set both as cookies

**Token Family Theft Detection:**
- Each login creates new token_family (UUIDv7)
- All rotated tokens share same token_family
- If revoked token from family is reused → attacker detected → revoke all tokens in family
- Legitimate client will get 401, prompt re-login

### Auth Middleware

1. Read access token from `Authorization: Bearer <token>` OR `access_token` cookie
2. Validate JWT signature + expiration
3. Extract `sub` claim (user ID)
4. Load user from DB by ID
5. Attach user to request context
6. On expiry: return 401 with hint to refresh (for HTMX: see HTMX rules)
7. Enforce protected routes; allow anonymous on public routes

### Logout

- Mark current refresh token as revoked in DB (set `revoked_at`)
- Clear both `access_token` and `refresh_token` cookies
- Redirect to login page

### Session Management

**View Active Sessions:**
- `GET /auth/sessions`: Return list of active refresh tokens for current user
- Show: device_name, ip_address, last_used_at, created_at
- Allow user to see all devices where they're logged in

**Revoke Specific Session:**
- `DELETE /auth/sessions/{token_id}`: Revoke specific refresh token
- Useful for "I don't recognize this device" scenarios

**Revoke All Sessions:**
- On password change: revoke all refresh tokens for user (force re-login everywhere)
- On account compromise: admin can revoke all user sessions

### Password Reset Flow

**Request Reset:**

`POST /auth/forgot-password`:
1. Validate email format
2. Check if email exists in DB (always return success to prevent enumeration)
3. If email exists:
   - Generate cryptographically random reset token (32 bytes), URL-safe encode
   - Hash token with SHA-256, store in `password_reset_tokens` table
   - Set expiry: 1 hour from creation
   - Send reset link via email: `{APP_BASE_URL}/reset-password?token={raw_token}`
4. Always return 200 OK with "If email exists, reset link sent" message
5. Rate limit: 3 requests per hour per email (server-side tracking)

**Complete Reset:**

`POST /auth/reset-password`:
1. Receive token and new password
2. Hash token and lookup in `password_reset_tokens` table
3. Verify token exists, not expired (<1 hour old), and not already used
4. Validate new password meets requirements
5. Update user's `password_hash`; reset `failed_login_attempts = 0` and clear `locked_until = NULL`
6. Mark reset token as used (set `used_at`)
7. **Revoke all refresh tokens for user** (force re-login on all devices)
8. Send password change confirmation email
9. Return success, redirect to login

**Security considerations:**
- Reset tokens are single-use only
- Tokens expire after 1 hour
- Email enumeration prevented (always return success)
- Rate limiting prevents abuse
- All sessions invalidated after password change

## Security Baseline

- Password hashing via bcrypt with cost factor 12.
- Refresh tokens hashed with SHA-256 before storage; use `crypto/subtle.ConstantTimeCompare` for lookup.
- CSRF protection for cookie-auth form flows.
- Parameterized SQL only.
- Secure cookie settings in production.
- Do not leak sensitive auth/account info in errors.
- CORS: not configured; all clients are served from the same origin.

### Rate Limiting

Implement rate limiting for authentication endpoints to prevent abuse:

**Login endpoints:**
- `POST /auth/login`: 5 attempts per 15 minutes per IP address
- `POST /auth/register`: 3 attempts per hour per IP address
- Failed login tracking: Lock account after 10 failed attempts within 1 hour (require password reset)

**Token endpoints:**
- `POST /auth/refresh`: 10 attempts per minute per refresh token
- `POST /auth/forgot-password`: 3 requests per hour per email address
- `POST /auth/reset-password`: 5 attempts per hour per IP address

**Implementation:**
- Use in-memory store (`sync.Map` with atomic counters and TTL-based expiry)
- Return `429 Too Many Requests` with `Retry-After` header
- Log rate limit violations for monitoring
- Note: in-memory state is not shared across multiple instances; Redis can be layered in later if horizontal scaling is required

### CSRF Protection

**Token generation:**
- Generate CSRF token using `crypto/rand` (32 bytes, base64-encoded)
- Store in server-side session or signed cookie
- Include in all rendered forms as hidden input: `<input type="hidden" name="csrf_token" value="{token}">`

**Validation:**
- Validate on all state-changing requests (POST, PUT, PATCH, DELETE)
- Compare submitted token with stored token (constant-time comparison)
- For HTMX requests: accept token from custom header `X-CSRF-Token` OR form field
- Reject requests with missing/invalid tokens (403 Forbidden)

**Cookie settings:**
- CSRF cookie: `SameSite=Strict` or `SameSite=Lax`
- Auth cookies: `HttpOnly=true`, `Secure=true` (production), `SameSite=Strict`

### Token Cleanup

**Background job for token maintenance:**

Run scheduled job (cron or background worker):
- **Frequency**: Daily at low-traffic hours (e.g., 3 AM)
- **Cleanup expired refresh tokens**: Delete where `expires_at < NOW() - INTERVAL '90 days'`
- **Cleanup revoked tokens**: Delete where `revoked_at < NOW() - INTERVAL '90 days'`
- **Cleanup used password reset tokens**: Delete where `used_at < NOW() - INTERVAL '7 days'`
- **Cleanup expired password reset tokens**: Delete where `expires_at < NOW() - INTERVAL '7 days'`
- **Cleanup used email verification codes**: Delete where `used_at < NOW() - INTERVAL '7 days'`
- **Cleanup expired email verification codes**: Delete where `expires_at < NOW() - INTERVAL '1 day'`

**Retention rationale:**
- Keep revoked tokens for 90 days for audit/forensics
- Remove reset tokens after 7 days (no value after expiry/use)
- Remove verification codes after 1 day of expiry (15-minute codes have no forensic value)

## UI/HTMX/Tailwind Rules

- Server-render first.
- Use Go's `html/template` (not `text/template`) for all server-side rendering; it auto-escapes output by default, preventing XSS.
- Use HTMX for partial updates and form interactions.
- Keep JS minimal.
- Use semantic, accessible HTML (labels, focus states, clear errors).

### HTMX Response Patterns

**Success responses:**
- Return HTML fragment for `hx-target` swaps
- Use `HX-Redirect: /path` header for full page redirects
- Use `HX-Trigger: eventName` header to trigger client-side events
- Use `HX-Retarget: #selector` to change swap target dynamically

**Error handling:**
- For validation errors: return 422 with error HTML fragment (swap into form)
- For auth failures (401): return `HX-Redirect: /login` header
- For server errors (500): return error HTML fragment or use `HX-Reswap: innerHTML` to show error message
- Set appropriate status codes; HTMX respects 4xx/5xx for error handling

**Token expiry handling:**
- On 401 from expired access token, return `HX-Trigger: token-expired` header
- Client-side JS listener calls `/auth/refresh` endpoint
- On successful refresh, retry original request automatically
- On refresh failure, redirect to login

**CSRF protection:**
- Include CSRF token in forms as hidden input
- Validate on POST/PUT/DELETE requests
- For HTMX requests, can also use custom header (e.g., `X-CSRF-Token`)

## Testing Requirements

At minimum add tests for:

- auth middleware
- JWT issue/validation
- token refresh flow with rotation
- token family theft detection
- refresh token revocation
- login/logout handlers
- password reset flow (request + complete)
- rate limiting middleware
- CSRF token validation
- users repository methods
- database migrations (up/down)
- session management endpoints

Prefer table-driven tests.

Include integration tests that run against real Postgres (use testcontainers or docker-compose).
Target ≥70% coverage for auth and users packages.

## Environment Variables

Support:

- `APP_ENV` (default: `"development"`; set to `"production"` in deployed environments — gates log format, secure cookie flag, and debug error detail)
- `DATABASE_URL` (required)
- `DB_MAX_CONNS` (default: "25")
- `PORT` (default: "8080")
- `JWT_SECRET` (required, min 32 bytes)
- `JWT_ACCESS_EXPIRY` (default: "15m")
- `REFRESH_TOKEN_EXPIRY` (default: "720h")
- `BCRYPT_COST` (default: "12")
- `PASSWORD_MIN_LENGTH` (default: "8")
- `APP_BASE_URL` (required for password reset links)
- `SMTP_HOST` (required for emails)
- `SMTP_PORT` (required)
- `SMTP_USERNAME` (required)
- `SMTP_PASSWORD` (required)
- `SMTP_FROM_EMAIL` (required)
- `SMTP_FROM_NAME` (default: app name)

Provide `.env.example` with placeholders only.

## Observability Requirements

**Logging:**
- Use `log/slog` from stdlib for structured logging
- Log levels: DEBUG, INFO, WARN, ERROR
- Include request ID in all logs (middleware-generated)
- Log format: JSON when `APP_ENV=production`, human-readable text otherwise

**Health checks:**
- `GET /health`: always returns 200 OK (liveness)
- `GET /ready`: returns 200 if DB is reachable, 503 otherwise (readiness)

**Error tracking:**
- Log all 500 errors with stack traces
- Consider external error tracking (Sentry, etc.) but not required by default

**Metrics:**
- `GET /metrics`: placeholder endpoint; returns 200 OK for now
- Intended future integration point for Prometheus or similar

## Output Contract (Every Implementation Response)

Return:

1. What you changed
2. File tree of changed/created files
3. Key decisions
4. Commands to run (`make dev`, `make test`, `make prod`)
5. Any approval-needed dependencies with concise rationale

If blocked, return:

- root cause
- minimal fix
- exact required code changes
