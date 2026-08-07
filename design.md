# RowBot — Design Specification

**Status:** Draft for implementation
**Audience:** Implementing developer
**Date:** 2026-06-05

---

## 1. Overview

`RowBot` is the rebrand and feature expansion of the existing Go authentication
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
