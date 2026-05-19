## Objective

Create a production-ready web app with:

- Go backend (stdlib-first)
- HTMX 4.0 + server-rendered HTML templates
- TailwindCSS with DaisyUI for themes and component library
- PostgreSQL via `DATABASE_URL`
- JWT auth (30-day expiry)
- Dockerized deployment
- Makefile-driven workflows

Prioritize simplicity, maintainability, and explicit code.

## Tech Rules

1. Use Go standard library whenever possible.
2. If stdlib is not enough, propose 1-2 well-known libraries, explain why, and ask approval before adding.
3. Prefer vanilla JS with HTMX for dynamics; avoid frontend frameworks unless requested.
4. Use sqlc-dev/sqlc for typesafe compiled db access.

## Architecture Rules

Use this structure:

.
├─ cmd/app/main.go
├─ internal/
│ ├─ app/
│ ├─ auth/
│ ├─ db/
│ ├─ users/
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
├─ migrations/
├─ Dockerfile
├─ .dockerignore
├─ Makefile
├─ .air.toml
├─ .env.example
└─ README.md

Constraints:

- `main.go` bootstraps only.
- Business/domain logic stays outside handlers.
- Keep modules small; avoid over-abstraction.

## Makefile Requirements

Must include:

- `make dev` (hot reload using `air`)
- `make run`
- `make build`
- `make test`
- `make fmt`
- `make docker-build`
- `make prod` (build production-ready Docker image)

## Docker Requirements

1. Use multi-stage build.
2. Do not bake secrets into images.
3. Include `.dockerignore` excluding `.env*`, `.git`, caches, build artifacts.
4. use lightweight
5. Runtime config comes from env vars.

## Database Requirements

1. Read Postgres connection from `DATABASE_URL`.
2. Auto-bootstrap required schema on startup (or run migrations automatically).
3. Ensure `users` table exists.

Minimum users columns:

- `id`
- `email` (unique, indexed, required)
- `password_hash` (nullable)
- `google_id` (nullable)
- `created_at`
- `updated_at`

## Auth Requirements

Support:

- Google OAuth login
- Email/password login
- Password reset workflow for email/password

On successful login:

- Issue JWT with 30-day expiration.
- `sub` claim = user email.
- Return token and set auth cookie.

Auth middleware:

1. Read token from `Authorization: Bearer <token>` OR cookie.
2. Validate signature + expiration.
3. Read `sub` email.
4. Load user from DB by email.
5. Attach user to request context.
6. Enforce protected routes; allow anonymous on public routes.

Logout:

- Clear auth cookie.
- Redirect to login page.

## Security Baseline

- Password hashing via strong algorithm (propose dependency if needed and ask approval).
- CSRF protection for cookie-auth form flows.
- Parameterized SQL only.
- Secure cookie settings in production.
- Do not leak sensitive auth/account info in errors.

## UI/HTMX/Tailwind Rules

- Server-render first.
- Use HTMX for partial updates and form interactions.
- Keep JS minimal.
- Use semantic, accessible HTML (labels, focus states, clear errors).

## Testing Requirements

At minimum add tests for:

- auth middleware
- JWT issue/validation
- login/logout handlers
- password reset flow
- users repository methods

Prefer table-driven tests.

## Environment Variables

Support:

- `DATABASE_URL` (required)
- `PORT`
- `JWT_SECRET` (required)
- `GOOGLE_OAUTH_CLIENT_ID`
- `GOOGLE_OAUTH_CLIENT_SECRET`
- `GOOGLE_OAUTH_REDIRECT_URL`
- `APP_BASE_URL`
- `SMTP_*` for reset emails

Provide `.env.example` with placeholders only.

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
