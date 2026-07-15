# ergracer — Design Specification

**Status:** Draft for implementation
**Audience:** Implementing developer
**Date:** 2026-06-05

---

## 1. Overview

`ergracer` is the rebrand and feature expansion of the existing Go authentication
starter (currently module `github.com/softsrv/starter`). The application becomes
a dual-purpose backend: a **web application** (HTMX server-rendered) and a
**Discord application** (slash commands via HTTP interactions), plus an
integration with the **Concept2 Logbook API**.

This document specifies, for each feature, the changes required at three layers:

- **Database** — migrations and sqlc queries (`db/migrations/`, `db/queries/`).
- **Backend** — services (`internal/app/`), HTTP handlers/middleware (`internal/http/`), and supporting packages.
- **Frontend** — templates (`web/templates/`) and HTMX interactions.

It deliberately excludes steps performed outside the codebase (registering the
Discord application in the Discord developer portal, registering the Concept2
API client, configuring OAuth redirect URIs in those portals, etc.). Those are
prerequisites the operator completes and supplies as `.env` values.

### Existing architecture (unchanged constraints)

The new work must respect the current design, summarized from `CLAUDE.md`:

- Layering: HTTP handlers → service layer (`internal/app`) → sqlc DB layer (`internal/db`). No business logic in `cmd/app/main.go`.
- Go stdlib first; new third-party libraries require justification (see §9).
- `html/template` only; no frontend frameworks; HTMX for dynamics.
- Parameterized SQL only, via sqlc. Never hand-edit `internal/db/` — edit `db/queries/`, run `make sqlc-generate`.
- Access tokens: JWT HS256, 15 min, `access_token` httpOnly cookie. Refresh tokens: 32-byte random, SHA-256 hashed at rest, 30 day, no rotation.
- Cookies `SameSite=Lax`, httpOnly, `Secure` in production. CSRF mitigated by SameSite=Lax + same-origin POSTs.
- Auth middleware (`middleware.Authenticate`) applied per-route, not globally. Global chain: `RequestID → Logging → SecurityHeaders → BodyLimit → mux`.
- Migrations run manually (`make migrate-up`), never on startup.

---

## 2. Rename: `starter` → `ergracer`

**Goal:** Remove all "starter" naming. This section specifies the change; no code
is changed by this document.

### 2.1 Module path

Change the module path in `go.mod`:

```
module github.com/softsrv/starter   →   module github.com/softsrv/ergracer
```

Every internal import (`github.com/softsrv/starter/internal/...`, `.../web`)
must be updated to the new path. The import appears in essentially every `.go`
file (≈24 files, including all `_test.go`). Recommended mechanical approach:

```bash
# from repo root
grep -rl 'softsrv/starter' --include='*.go' . \
  | xargs sed -i '' 's|github.com/softsrv/starter|github.com/softsrv/ergracer|g'
go mod tidy
make fmt && make build && make test
```

### 2.2 Non-import references

Audit and update human-facing/string references to "starter" / "MyApp":

- `README.md`, `docs/*.md` — project name and examples.
- `.env.example` — `SMTP_FROM_NAME=MyApp` → `SMTP_FROM_NAME=ergracer` (this string is used as `AppName` in emails).
- `Makefile` / `Dockerfile` — image names or binary names if they reference "starter".
- `.air.toml` — build output names if applicable.
- Email templates in `internal/email/templates.go` if any contain the literal app name (they take `AppName` as a parameter, so likely no change beyond the env value).

### 2.3 Acceptance

`grep -ri 'starter' .` (excluding `.git`) returns only incidental matches (e.g.
the word in unrelated prose). `make build` and `make test` pass.

---

## 3. Data model changes (shared foundation)

Three features (Discord login, Discord linking, Concept2 linking) all attach an
external identity to a `users` row. Rather than widening the `users` table with
provider-specific columns, introduce a generic **linked accounts** table plus a
dedicated **OAuth token** table. This keeps `users` clean and supports a user
having both a Discord and a Concept2 link simultaneously.

### 3.1 New table: `oauth_identities`

Represents "user X is linked to provider P as external id Y". Used both for
login (Discord) and for linking (Discord, Concept2).

Migration `db/migrations/000008_create_oauth_identities.up.sql`:

```sql
CREATE TABLE IF NOT EXISTS oauth_identities (
    id               UUID        PRIMARY KEY,
    user_id          UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider         TEXT        NOT NULL,          -- 'discord' | 'concept2'
    provider_user_id TEXT        NOT NULL,          -- external account id (Discord snowflake, Concept2 user id)
    provider_username TEXT,                         -- display handle, for UI (nullable)
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (provider, provider_user_id),            -- one external account links to at most one user
    UNIQUE (user_id, provider)                      -- a user links at most one account per provider
);

CREATE INDEX IF NOT EXISTS idx_oauth_identities_user ON oauth_identities (user_id);
```

Rationale for the two unique constraints:

- `(provider, provider_user_id)` prevents two ergracer users from claiming the same Discord/Concept2 account.
- `(user_id, provider)` enforces "one Discord link per user".

### 3.2 New table: `oauth_tokens`

Concept2 requires calling its API on the user's behalf later (the webhook tells
us _that_ something changed; reading detail needs the access token). Discord
login does **not** need long-lived tokens (we only use the OAuth code exchange
once to read identity), but storing Discord tokens is optional and harmless.
Keep tokens in a separate table so the linking record (`oauth_identities`) can
exist without secrets, and so token rows can be rotated/revoked independently.

Migration `db/migrations/000009_create_oauth_tokens.up.sql`:

```sql
CREATE TABLE IF NOT EXISTS oauth_tokens (
    id                 UUID        PRIMARY KEY,
    oauth_identity_id  UUID        NOT NULL REFERENCES oauth_identities(id) ON DELETE CASCADE,
    access_token_enc   BYTEA       NOT NULL,        -- encrypted at rest (see §3.4)
    refresh_token_enc  BYTEA,                        -- encrypted at rest (nullable)
    scope              TEXT,
    expires_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (oauth_identity_id)
);
```

### 3.3 `users.password_hash` nullability

A user who registers via Discord has no password. The current schema declares
`password_hash TEXT NOT NULL`. Two options:

- **Option A (recommended):** make it nullable.
  `ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;`
  Code that reads `password_hash` (login, change-password) must treat a NULL/empty
  hash as "password login not available for this account" and return
  `ErrInvalidCredentials` (or a clearer `ErrPasswordLoginUnavailable`). The
  bcrypt dummy-hash timing path in `Login` already covers the no-password branch
  if we route it through the same comparison.
- **Option B:** store a sentinel unusable hash (a bcrypt hash of random bytes that
  is never revealed). Simpler schema, but conflates "no password" with "has
  password", complicating the "set a password later" UX.

Recommend **Option A** in migration `000010_users_password_hash_nullable.up.sql`.

The sqlc model `User.PasswordHash string` becomes `pgtype.Text` (nullable) after
regeneration; update `internal/app` and `internal/users` call sites accordingly.

### 3.4 Token encryption

`oauth_tokens` holds third-party access/refresh tokens. These must be encrypted
at rest, not stored plaintext. Specify AES-256-GCM using a key derived from a new
env var `OAUTH_TOKEN_ENC_KEY` (32 bytes, base64). A small helper in a new
`internal/secrets` (or reuse `internal/auth`) package:

```go
func Encrypt(plaintext []byte) ([]byte, error)   // AES-256-GCM, random nonce prepended
func Decrypt(ciphertext []byte) ([]byte, error)
```

Use Go stdlib `crypto/aes` + `crypto/cipher`; no new dependency. This is
consistent with the existing approach of hashing refresh tokens before storage —
third-party tokens we cannot hash (we need to replay them), so we encrypt.

### 3.5 sqlc queries

Add `db/queries/oauth_identities.sql` and `db/queries/oauth_tokens.sql`. Minimum
query set:

- `CreateOAuthIdentity`, `GetOAuthIdentityByProviderUserID(provider, provider_user_id)`, `GetOAuthIdentityByUserAndProvider(user_id, provider)`, `ListOAuthIdentitiesByUser(user_id)`, `DeleteOAuthIdentity(id)`.
- `UpsertOAuthToken(oauth_identity_id, ...)`, `GetOAuthTokenByIdentity(oauth_identity_id)`.

Run `make sqlc-generate` after.

---

## 4. Feature: Discord OAuth login

Let a user authenticate with "Sign in with Discord" in addition to
email/password.

### 4.1 Flow (OAuth 2.0 Authorization Code)

1. User clicks **Sign in with Discord** on `/login`.
2. `GET /auth/discord/login` — backend generates a random `state` (32 bytes, base64), stores it in a short-lived signed/httpOnly cookie (`oauth_state`, 10 min, SameSite=Lax), and 302-redirects to Discord's authorize URL with `client_id`, `redirect_uri`, `response_type=code`, `scope=identify email`, and `state`.
3. Discord redirects back to `GET /auth/discord/callback?code=...&state=...`.
4. Callback handler:
   - Rejects if `state` query param ≠ `oauth_state` cookie (CSRF protection); clears the cookie.
   - Exchanges `code` for tokens at Discord's token endpoint (`POST /oauth2/token`).
   - Calls Discord `GET /users/@me` with the access token → `{ id, username, email, verified }`.
   - Resolves the user (see §4.2).
   - Issues the normal ergracer token pair (`AuthService.issueTokenPair`) and sets `access_token` + `refresh_token` cookies — identical to the email/password success path.
   - Redirects to `/dashboard`.

### 4.2 Account resolution logic

Given the Discord profile `(discord_id, email)`:

1. **Existing Discord link** — `GetOAuthIdentityByProviderUserID('discord', discord_id)` hit → log that user in. Done.
2. **No link, but email matches an existing user** — found by `GetUserByEmail(discord_email)`:
   - Auto-link: create an `oauth_identities` row joining the Discord identity to that user, then log in. (Acceptable because Discord reports `verified: true` for the email and the email is unique in `users`. If `verified` is false, do **not** auto-link — show an error asking the user to log in with their password first and link from the profile page.)
3. **No link, no matching user** — provision a new account:
   - Insert a `users` row with `email = discord_email`, `email_verified = true` (Discord-verified), `password_hash = NULL`.
   - Insert the `oauth_identities` row.
   - Log in.

All three branches end by calling `issueTokenPair` and setting cookies.

### 4.3 Backend

- New `internal/oauth/discord.go`: an `oauth.DiscordClient` with `AuthorizeURL(state) string`, `Exchange(ctx, code) (Token, error)`, `CurrentUser(ctx, accessToken) (DiscordUser, error)`. Pure HTTP via stdlib `net/http`; no SDK needed for OAuth.
- New service `internal/app/oauth_service.go` (`OAuthService`) owning resolution logic in §4.2 and §3. It depends on `*db.Queries`, the `pgxBeginner` pool (for the provision-and-link transaction), and the `DiscordClient`. Reuses `AuthService.issueTokenPair` — either expose that method or move token issuance into a shared helper both services call. Recommend extracting an unexported `tokenIssuer` used by both, or having `OAuthService` hold a reference to `AuthService`.
- New handler `internal/http/handlers/oauth.go` with `DiscordLogin` and `DiscordCallback`. Sets cookies via the existing cookie helpers (`AuthHandler.setTokenCookies` and `clearAuthCookies` in `cookies.go`) so cookie attributes stay consistent — extract `setTokenCookies` into a shared package-level helper both `AuthHandler` and the new `oauthH` can call.
- Config: add to `cmd/app/main.go` config struct and `.env.example`:
  `DISCORD_CLIENT_ID`, `DISCORD_CLIENT_SECRET`, `DISCORD_REDIRECT_URI` (or derive from `APP_BASE_URL` + `/auth/discord/callback`).

### 4.4 Routes (add to `internal/http/router.go`, public section)

```
GET /auth/discord/login      → oauthH.DiscordLogin       (rate-limited, IP key)
GET /auth/discord/callback   → oauthH.DiscordCallback     (rate-limited, IP key)
```

Apply a new rate limiter (e.g. 10/15min per IP) mirroring the login limiter.

### 4.5 Frontend

- `web/templates/auth/login.html`: add a "Sign in with Discord" button/link to `GET /auth/discord/login`, styled with DaisyUI, separated from the email/password form by a divider. Mirror on `register.html`.
- Error states (denied consent, unverified email blocking auto-link, state mismatch) render through the existing `partials/error.html` / flash mechanism after redirect, or as a dedicated error page.

### 4.6 Security notes

- `state` is mandatory and single-use (cookie cleared on callback).
- Validate `redirect_uri` is fixed server-side (never reflected from the request).
- Treat the email from Discord as authoritative only when `verified == true`; otherwise require manual linking.
- Token exchange happens server-to-server; the Discord client secret never reaches the browser.

---

## 5. Feature: Link Discord from the profile page (already-registered users)

A user who signed up with email/password can link their Discord account from the
**Integrations** section of `/profile`.

### 5.1 Flow

Reuse the same Discord OAuth dance, but the callback behaves differently when the
user is **already authenticated**:

1. From `/profile`, **Connect Discord** → `GET /auth/discord/link` (this route is behind `verifiedMW`).
2. Same authorize redirect as §4, but the `state` cookie also encodes intent = "link" (e.g. store `oauth_state` plus an `oauth_intent` cookie, or use distinct callback paths).
3. Callback (`GET /auth/discord/callback`, or a separate `/auth/discord/link/callback`) detects link intent and the current user from the `access_token` cookie:
   - If the Discord account is already linked to **another** user → reject with "This Discord account is already linked to a different ergracer account."
   - Otherwise create the `oauth_identities` row for the current user. Do **not** issue new tokens (already logged in). Redirect back to `/profile` with a success flash.

Design recommendation: use **separate callback paths** (`/auth/discord/callback`
for login, `/auth/discord/link/callback` for linking) to keep intent
unambiguous and each handler simple, rather than overloading one callback with a
mode flag.

### 5.2 Backend

- Handlers `DiscordLinkStart` (behind auth) and `DiscordLinkCallback` (behind auth) in `oauth.go`.
- `OAuthService.LinkDiscord(ctx, userID, code) error` — exchanges code, fetches profile, enforces the "not already linked elsewhere" rule, inserts identity.
- `OAuthService.UnlinkDiscord(ctx, userID) error` — deletes the identity (and cascaded token). **Guard:** refuse to unlink if it would leave the account with no way to log in (i.e. `password_hash` is NULL and Discord is the only identity). Return a clear error so the UI can explain "set a password before unlinking Discord."

### 5.3 Routes (protected section)

```
GET  /auth/discord/link            → oauthH.DiscordLinkStart      (verifiedMW)
GET  /auth/discord/link/callback   → oauthH.DiscordLinkCallback   (verifiedMW)
POST /profile/integrations/discord/unlink → oauthH.DiscordUnlink  (verifiedMW)
```

### 5.4 Frontend

Add an **Integrations** card to `web/templates/profile.html`. For Discord show:

- **Not linked:** "Connect Discord" button → `GET /auth/discord/link`.
- **Linked:** the linked Discord username, an **Add to your server** button (see §7), and a **Disconnect** button (HTMX `POST` to the unlink route, swapping the card on success via `partials/flash.html`).

The `ProfilePage` handler must load the user's identities
(`ListOAuthIdentitiesByUser`) and pass them to the template so it can render
linked/unlinked state. This requires extending `ProfileHandler` to depend on the
new `OAuthService` (or a read method), and updating the template data map.

---

## 6. Feature: Link Concept2 Logbook via OAuth

Let users connect their Concept2 Logbook account from `/profile` Integrations.
Concept2 uses standard OAuth 2.0 (authorization code); see
https://log.concept2.com/developers/documentation/.

### 6.1 Flow

Mirrors §5 (linking-only; Concept2 is never a primary login method here):

1. `/profile` → **Connect Concept2** → `GET /auth/concept2/link` (behind `verifiedMW`).
2. Generate `state`, store in cookie, redirect to Concept2 authorize endpoint with `client_id`, `redirect_uri`, `response_type=code`, `scope` (request the scopes needed to read results and receive webhooks — e.g. `user:read,results:read`; confirm exact scope strings against the Concept2 docs), and `state`.
3. `GET /auth/concept2/link/callback?code&state`:
   - Verify `state`, clear cookie.
   - Exchange `code` → access + refresh tokens at the Concept2 token endpoint.
   - Fetch the Concept2 user id (their "me" endpoint) for `provider_user_id` / display.
   - Create `oauth_identities` (`provider='concept2'`) for the current user and store the encrypted tokens in `oauth_tokens` (§3.2/§3.4). Concept2 tokens are needed later to call its API, so storage here is required (unlike Discord login).
   - Redirect to `/profile` with success flash.

### 6.2 Backend

- New `internal/oauth/concept2.go`: `oauth.Concept2Client` with `AuthorizeURL`, `Exchange`, `CurrentUser`, and `RefreshToken(ctx, refresh) (Token, error)` (needed because Concept2 access tokens expire and we hold them for later use). Stdlib HTTP.
- `OAuthService.LinkConcept2(ctx, userID, code) error` and `UnlinkConcept2(ctx, userID) error`.
- A token-refresh helper used before any future Concept2 API call: if `expires_at` is past, call `RefreshToken`, re-encrypt, update `oauth_tokens`. (No caller yet beyond storage, but specify the helper so the webhook-driven enrichment in §8 can use it later.)
- Config / `.env.example`: `CONCEPT2_CLIENT_ID`, `CONCEPT2_CLIENT_SECRET`, `CONCEPT2_REDIRECT_URI`, and the base URLs (`CONCEPT2_API_BASE` defaulting to `https://log.concept2.com`). Note Concept2 also exposes a separate sandbox host; make the base URL configurable so dev can target the sandbox.

### 6.3 Routes (protected section)

```
GET  /auth/concept2/link            → oauthH.Concept2LinkStart     (verifiedMW)
GET  /auth/concept2/link/callback   → oauthH.Concept2LinkCallback  (verifiedMW)
POST /profile/integrations/concept2/unlink → oauthH.Concept2Unlink (verifiedMW)
```

### 6.4 Frontend

In the profile **Integrations** card, a Concept2 row symmetric to Discord:
Connect / show linked username + Disconnect. Same HTMX swap pattern.

---

## 7. Feature: Add the application to your server

When a user has linked Discord, show a button that adds the ergracer Discord
application (bot) to a Discord server they manage.

### 7.1 Mechanism

This is **not** an API call — it is a link to Discord's OAuth2 authorize URL with
`scope=bot applications.commands` (plus a `permissions` integer for the bot's
required permissions). Discord renders its own "Add to Server" consent screen.
No backend exchange is required for the add-to-server action itself.

URL shape:

```
https://discord.com/oauth2/authorize
  ?client_id=<DISCORD_CLIENT_ID>
  &scope=bot+applications.commands
  &permissions=<PERMISSIONS_INT>
```

### 7.2 Backend

- No new route strictly required; the URL can be constructed in the template from
  config. Cleaner: expose the install URL via the template data (computed by a
  small helper using `DISCORD_CLIENT_ID` and a configured `DISCORD_BOT_PERMISSIONS`)
  so the client id/permissions aren't duplicated in HTML.
- Add `DISCORD_BOT_PERMISSIONS` to config/`.env.example` (integer; minimal —
  for slash commands `applications.commands` alone can suffice, so this may be `0`).

### 7.3 Frontend

A **Add to your server** button in the Discord integration row of `/profile`,
visible only when Discord is linked. It is an external link (`target="_blank"
rel="noopener"`) to the install URL.

---

## 8. Feature: Discord application backend (HTTP Interactions)

The backend serves Discord slash-command interactions over HTTP (the chosen
transport), not a Gateway websocket. Discord POSTs every interaction to a single
public endpoint; the app verifies the request signature and responds inline.

### 8.1 Endpoint

```
POST /discord/interactions   → discordH.Interactions   (PUBLIC, signature-verified)
```

This route is **public** (no JWT/`authMW`) because Discord, not a logged-in
browser, calls it. It is protected instead by **Ed25519 signature verification**
(see §8.3). It must be added to the public section of `router.go`.

### 8.2 Request lifecycle

1. Discord sends `POST` with headers `X-Signature-Ed25519` and
   `X-Signature-Timestamp` and a JSON body.
2. Handler verifies the signature against the app's **public key**
   (`DISCORD_PUBLIC_KEY` from `.env`) over `timestamp + rawBody`. **Reject early**
   (HTTP 401) on any verification failure, before parsing semantics.
3. Parse the interaction `type`:
   - `type == 1` (PING) → respond `{ "type": 1 }` (PONG). Discord uses this to validate the endpoint.
   - `type == 2` (APPLICATION_COMMAND) → dispatch by command name.
4. For command `register`, respond with a channel message:
   ```json
   { "type": 4, "data": { "content": "thanks for requesting registration" } }
   ```
   (`type 4` = CHANNEL_MESSAGE_WITH_SOURCE.)
5. Unknown command → respond with an ephemeral error message (`flags: 64`).

### 8.3 Signature verification (critical, must come before body parsing)

- Use Go stdlib `crypto/ed25519`. `DISCORD_PUBLIC_KEY` is a hex string; decode to bytes.
- The signed message is the concatenation of the `X-Signature-Timestamp` header value and the raw request body. **The raw body must be read before any JSON decoding** and before the `BodyLimit`/parsing consumes it — read with `io.ReadAll` into a buffer, verify, then `json.Unmarshal` the buffer.
- `BodyLimit` (1 MiB) still applies and is fine; interactions are tiny. Ensure the handler reads the already-limited body.
- On missing headers, malformed hex, wrong length, or failed verification → `http.Error(w, "invalid request signature", 401)` and return immediately. Do not log the body at error level (avoid leaking payloads); log only that verification failed.

### 8.4 Backend structure

- New `internal/discord/` package:
  - `verify.go` — `VerifySignature(pubKey ed25519.PublicKey, timestamp string, body []byte, sigHex string) bool`.
  - `interactions.go` — types for the interaction payload (subset: `Type`, `Data.Name`, `Data.Options`, `Member/User`), and response builders (`PongResponse`, `MessageResponse(content)`, `EphemeralResponse(content)`).
- New handler `internal/http/handlers/discord.go` (`DiscordHandler.Interactions`). It depends only on the public key (and later a service for real command logic). For now `/register` is a pure static reply, so no DB access is needed.
- Config / `.env.example`: `DISCORD_PUBLIC_KEY`, `DISCORD_APPLICATION_ID` (the application/bot id; needed for command registration tooling and the add-to-server URL).

### 8.5 Command registration

Slash commands must be registered with Discord before they appear in clients. See
§9 for the full implementation spec. Registration runs on startup using Discord's
bulk-overwrite endpoint, which is idempotent — safe to call on every deploy.

### 8.6 Frontend

None. This endpoint is machine-to-machine.

---

## 9. Feature: Discord slash command registration on startup

Discord slash commands must be declared to Discord's API before they appear in
clients. This section specifies registering all commands during application
startup using Discord's bulk-overwrite endpoint. The operation is idempotent —
Discord atomically replaces the full command set — so running it on every start
is safe and keeps the deployed command list in sync with the code without any
manual steps.

### 9.1 Mechanism

Discord's REST bulk-overwrite endpoint replaces the application's global command
set in one call:

```
PUT https://discord.com/api/v10/applications/{application_id}/commands
Authorization: Bot {bot_token}
Content-Type: application/json
Body: JSON array of ApplicationCommand objects
```

Global commands propagate to all guilds within ~1 hour. A bot token is required
(distinct from the OAuth client secret); obtain it from the Discord developer
portal → Bot → Token.

### 9.2 Command definitions

Add `internal/discord/commands.go`:

```go
package discord

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
)

const discordAPIBase = "https://discord.com/api/v10"

// ApplicationCommand is the subset of Discord's ApplicationCommand object needed
// for registration. Type 1 = CHAT_INPUT (slash command).
type ApplicationCommand struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    Type        int    `json:"type"`
}

// Commands is the authoritative list of slash commands this application exposes.
// Add new commands here; they are registered on every startup.
var Commands = []ApplicationCommand{
    {Type: 1, Name: "register", Description: "Request registration for ergracer"},
}

// RegisterCommands bulk-overwrites global application commands via Discord's REST
// API. If client is nil, http.DefaultClient is used. A non-2xx response from
// Discord is returned as an error.
func RegisterCommands(ctx context.Context, client *http.Client, botToken, applicationID string, commands []ApplicationCommand) error {
    if client == nil {
        client = http.DefaultClient
    }
    body, err := json.Marshal(commands)
    if err != nil {
        return fmt.Errorf("marshal commands: %w", err)
    }
    url := fmt.Sprintf("%s/applications/%s/commands", discordAPIBase, applicationID)
    req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
    if err != nil {
        return fmt.Errorf("build request: %w", err)
    }
    req.Header.Set("Authorization", "Bot "+botToken)
    req.Header.Set("Content-Type", "application/json")
    resp, err := client.Do(req)
    if err != nil {
        return fmt.Errorf("discord api: %w", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return fmt.Errorf("discord api responded %d", resp.StatusCode)
    }
    return nil
}
```

Adding a new slash command in future only requires appending an entry to
`Commands` and redeploying. No other wiring is needed.

### 9.3 Startup integration

In `cmd/app/main.go`, after wiring the Discord interactions handler and before
calling `srv.ListenAndServe`:

```go
if discordInteractionsH != nil && cfg.DiscordBotToken != "" {
    regCtx, regCancel := context.WithTimeout(context.Background(), 10*time.Second)
    regErr := discord.RegisterCommands(regCtx, nil, cfg.DiscordBotToken, cfg.DiscordApplicationID, discord.Commands)
    regCancel()
    if regErr != nil {
        slog.Error("discord command registration failed", "error", regErr)
        // Non-fatal: log and continue. Previously-registered commands remain
        // active in Discord until the next successful registration.
    } else {
        slog.Info("discord commands registered", "count", len(discord.Commands))
    }
}
```

Registration is **non-fatal** — a transient Discord API failure should not prevent
the application from starting. The gate requires both `DISCORD_PUBLIC_KEY`
(interactions enabled, so `discordInteractionsH != nil`) and `DISCORD_BOT_TOKEN`
(token present). If either is absent, registration is skipped silently.

### 9.4 Config

Add to the `config` struct in `cmd/app/main.go`:

```go
DiscordBotToken string
```

Load in `mustLoadConfig`:

```go
cfg.DiscordBotToken = os.Getenv("DISCORD_BOT_TOKEN")
```

Add to `.env.example`:

```dotenv
DISCORD_BOT_TOKEN=   # Bot token from Discord developer portal → Bot → Token
```

### 9.5 Testing

Add `internal/discord/commands_test.go`:

- Spin up an `httptest.Server` that records the request. Assert:
  - Method is `PUT`.
  - Path is `/applications/{id}/commands`.
  - `Authorization` header is `Bot <token>`.
  - Request body deserializes to the expected `[]ApplicationCommand`.
  - A 2xx response → `RegisterCommands` returns nil.
  - A non-2xx response (e.g. 401) → `RegisterCommands` returns a non-nil error.

No new integration test is needed for the startup wiring — the guard conditions
are trivially verified by inspection and the function itself is covered above.

### 9.6 Frontend

None. Command registration is server-to-Discord only.

---

## 10. Feature: Concept2 webhook receiver

A public route accepts webhook deliveries from the Concept2 Logbook API
(notifying us when a user logs a new result). For now it validates and logs.

### 10.1 Endpoint

```
POST /webhooks/concept2   → webhookH.Concept2   (PUBLIC, payload-validated)
```

Public — cannot use the JWT/`authMW` flow because Concept2's servers, not a
browser, call it. Must be added to the public section of `router.go`.

### 10.2 Validation — reject early, reject hard

Concept2 webhooks support a verification mechanism (a shared secret / signature
and/or a subscription verification handshake — confirm exact scheme against the
Concept2 webhook docs). The handler must, **in order, failing closed at the first
problem with a 4xx and no side effects**:

1. **Method & content-type** — must be `POST` with `application/json`; else 415/405.
2. **Size** — body already capped by `BodyLimit` (1 MiB); webhook bodies are small. Reject anything that fails to read within the limit.
3. **Signature / shared secret** — verify the Concept2-provided signature header against `CONCEPT2_WEBHOOK_SECRET` (HMAC over the raw body, or whatever Concept2 specifies). Read the raw body first (like §8.3) so the exact bytes are verified. Reject with 401 on mismatch. **No logging of the raw body on failure.**
4. **Subscription verification handshake** — if Concept2 sends a verification/challenge request when the subscription is created (some providers do `GET ?challenge=` or a typed POST), echo the challenge as required. Specify a branch for this.
5. **Schema** — unmarshal into a strict typed struct; reject (400) on unknown-critical-field absence or type mismatch. Use `json.Decoder` with `DisallowUnknownFields` only if Concept2's payload is stable; otherwise validate required fields explicitly (`type`, `result_id`, `user_id`, etc.).

Only after all checks pass is the payload considered "good."

### 10.3 Handling a good webhook (current scope)

For now: **log it to the console in a readable way** and return `200 OK` quickly.

```go
slog.Info("concept2 webhook received",
    "event_type", payload.Type,
    "concept2_user_id", payload.UserID,
    "result_id", payload.ResultID,
    "logged_at", payload.Timestamp,
)
```

Respond `200` promptly (webhook senders retry on non-2xx / timeouts). Do **not**
do heavy work synchronously; even now, keep the handler fast. (Future: look up the
`oauth_identities` row by `('concept2', payload.UserID)`, refresh the stored
token via §6.2, and fetch result detail — specify as a TODO, not built now.)

### 10.4 Backend structure

- New `internal/http/handlers/webhooks.go` (`WebhookHandler.Concept2`).
- A `internal/concept2/webhook.go` for the typed payload struct(s) and
  `VerifySignature(secret string, body []byte, sigHeader string) bool`.
- Config / `.env.example`: `CONCEPT2_WEBHOOK_SECRET`.

### 10.5 Frontend

None.

---

## 11. Routing summary

New routes, grouped by protection level, added to `internal/http/router.go`.

**Public (no auth middleware):**

| Method & path                | Handler                  | Protection                      |
| ---------------------------- | ------------------------ | ------------------------------- |
| `GET /auth/discord/login`    | `oauthH.DiscordLogin`    | rate limit (IP), `state` cookie |
| `GET /auth/discord/callback` | `oauthH.DiscordCallback` | `state` match                   |
| `POST /discord/interactions` | `discordH.Interactions`  | Ed25519 signature               |
| `POST /webhooks/concept2`    | `webhookH.Concept2`      | shared-secret/HMAC signature    |

**Protected (`verifiedMW`):**

| Method & path                                | Handler                       |
| -------------------------------------------- | ----------------------------- |
| `GET /auth/discord/link`                     | `oauthH.DiscordLinkStart`     |
| `GET /auth/discord/link/callback`            | `oauthH.DiscordLinkCallback`  |
| `POST /profile/integrations/discord/unlink`  | `oauthH.DiscordUnlink`        |
| `GET /auth/concept2/link`                    | `oauthH.Concept2LinkStart`    |
| `GET /auth/concept2/link/callback`           | `oauthH.Concept2LinkCallback` |
| `POST /profile/integrations/concept2/unlink` | `oauthH.Concept2Unlink`       |

Note: the two public Discord/Concept2 callback routes that perform linking are
listed under protected because linking requires an authenticated session; the
login callback is public. Keep login and link callbacks on **distinct paths** as
recommended in §5.1.

---

## 12. Configuration summary (`.env.example` additions)

```dotenv
# OAuth token encryption (32 bytes, base64)
OAUTH_TOKEN_ENC_KEY=<base64-32-bytes>

# Discord application
DISCORD_APPLICATION_ID=
DISCORD_CLIENT_ID=
DISCORD_CLIENT_SECRET=
DISCORD_PUBLIC_KEY=
DISCORD_REDIRECT_URI=http://localhost:8080/auth/discord/callback
DISCORD_BOT_PERMISSIONS=0

# Concept2 Logbook
CONCEPT2_CLIENT_ID=
CONCEPT2_CLIENT_SECRET=
CONCEPT2_REDIRECT_URI=http://localhost:8080/auth/concept2/link/callback
CONCEPT2_API_BASE=https://log.concept2.com
CONCEPT2_WEBHOOK_SECRET=
```

All loaded in `cmd/app/main.go`'s `mustLoadConfig` (use `mustGetEnv` for values
the app cannot run without in the environments that enable the feature; consider
making Discord/Concept2 optional so the app still boots without them in minimal
dev — gate feature wiring on presence of the relevant vars).

---

## 13. Dependencies

Prefer stdlib per project constraints. The entire design is achievable with
stdlib:

- OAuth flows: `net/http` for redirects and token exchange; `encoding/json`.
- Discord signature: `crypto/ed25519`, `encoding/hex`.
- Concept2 webhook signature: `crypto/hmac`, `crypto/sha256`.
- Token encryption: `crypto/aes`, `crypto/cipher`.

**Optional**, only if the developer prefers ergonomics over stdlib (must be
justified per `CLAUDE.md` before adding):

- `golang.org/x/oauth2` — standardizes the auth-code dance and token refresh. Reasonable, well-vetted, and reduces hand-rolled token handling for Concept2 refresh. Recommend proposing this one; skip a full Discord SDK (`discordgo`) since we use HTTP interactions, not the Gateway.

No Discord Gateway library is needed given the HTTP-interactions transport.

---

## 14. Testing

Follow existing patterns (`*_test.go` unit tests; `-tags integration` with
testcontainers for DB-touching paths).

- **Unit:** Discord signature verification (valid, tampered body, bad timestamp, malformed header); Concept2 HMAC verification; token encrypt/decrypt round-trip; OAuth state generation/compare; account-resolution branch logic in `OAuthService` with a faked OAuth client.
- **Integration (DB):** create/link/unlink identities; unique-constraint enforcement (same Discord account can't link to two users); unlink guard when no password is set; Discord-login provisioning of a brand-new user.
- **Handler:** `/discord/interactions` PING→PONG and `/register`→static reply (with a valid signature fixture); `/webhooks/concept2` happy path logs and returns 200, bad signature returns 401, malformed JSON returns 400.
- **Mock external HTTP:** wrap Discord/Concept2 HTTP calls behind interfaces so tests inject a fake transport rather than calling real endpoints.

---

## 15. Implementation order (suggested)

1. **Rename** (§2) — do this first so all new code uses the final module path.
2. **Data model** (§3) — migrations, sqlc queries, token encryption helper, `password_hash` nullable.
3. **OAuth scaffolding** (`internal/oauth`, `OAuthService`, shared token-issuer extraction).
4. **Discord login** (§4) + login/register template buttons.
5. **Profile Integrations UI** + **Discord linking/unlinking** (§5).
6. **Concept2 linking** (§6).
7. **Add-to-server button** (§7).
8. **Discord interactions endpoint** (§8) with `/register`.
9. **Discord slash command registration on startup** (§9).
10. **Concept2 webhook receiver** (§10).
11. Tests throughout; `make fmt lint test` green at each step.

---

## 16. Open questions for the operator / next iteration

- Exact Concept2 scopes and webhook signature scheme — confirm against current Concept2 developer docs before implementing §6/§9.
- Whether Discord login should be allowed to **create** new accounts (§4.2 branch 3) or only link to pre-existing ones. This spec assumes auto-provisioning is allowed; flip a config flag (`DISCORD_ALLOW_SIGNUP`) if not.
- Whether the add-to-server button needs a `guild_id` pre-selection or analytics callback (currently a plain external link).
- Future webhook enrichment (fetch result detail via stored Concept2 token) is specified as a TODO in §10.3, not built now.
