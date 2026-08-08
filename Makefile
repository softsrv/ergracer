APP_NAME         := rowbot
BIN_DIR          := ./bin
MODULE           := github.com/softsrv/rowbot

# Load .env if present (for local dev convenience)
-include .env
export

.PHONY: dev stop run build test fmt lint \
        daisyui-install tailwind tailwind-watch \
        migrate-up migrate-down migrate-create migrate-status \
        sqlc-generate db-reset \
        docker-build docker-run prod clean

## ── Development ─────────────────────────────────────────────────────────────

# Full hot-reload: Go (air) + Tailwind watch in parallel. trap 'kill 0'
# ensures Ctrl+C kills the entire process group, including air's spawned
# ./tmp/main grandchild that recursive make -j2 would leave behind.
dev:
	@trap 'kill 0' INT TERM; \
	  air & \
	  tailwindcss -i ./web/static/css/app.css -o ./web/static/css/dist/app.css --watch & \
	  wait

# Kill any stray dev processes left over from a previous session.
stop:
	@pkill -f './tmp/main' 2>/dev/null || true
	@pkill -f 'air$$' 2>/dev/null || true
	@pkill -f 'tailwindcss.*--watch' 2>/dev/null || true

air:
	air

run:
	go run ./cmd/app

build:
	go build -ldflags="-s -w" -o $(BIN_DIR)/$(APP_NAME) ./cmd/app

## ── Quality ──────────────────────────────────────────────────────────────────

test:
	go test ./...

test-integration:
	go test -tags integration ./...

test-cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

fmt:
	gofmt -w .

lint:
	golangci-lint run

## ── CSS ──────────────────────────────────────────────────────────────────────

# Download DaisyUI .mjs bundles next to app.css so the @plugin directive
# can resolve them. Re-run this whenever you upgrade DaisyUI.
daisyui-install:
	curl -sLo web/static/css/daisyui.mjs \
	  https://github.com/saadeghi/daisyui/releases/latest/download/daisyui.mjs
	curl -sLo web/static/css/daisyui-theme.mjs \
	  https://github.com/saadeghi/daisyui/releases/latest/download/daisyui-theme.mjs
	@echo "DaisyUI bundles downloaded to web/static/css/"

tailwind:
	tailwindcss -i ./web/static/css/app.css -o ./web/static/css/dist/app.css --minify

tailwind-watch:
	tailwindcss -i ./web/static/css/app.css -o ./web/static/css/dist/app.css --watch

## ── Database ─────────────────────────────────────────────────────────────────

migrate-up:
	migrate -path db/migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path db/migrations -database "$(DATABASE_URL)" down 1

migrate-create:
	@test -n "$(NAME)" || (echo "Usage: make migrate-create NAME=<name>" && exit 1)
	migrate create -ext sql -dir db/migrations -seq $(NAME)

migrate-status:
	migrate -path db/migrations -database "$(DATABASE_URL)" version

sqlc-generate:
	sqlc generate -f db/sqlc.yaml

# Truncates every application table in DATABASE_URL, for wiping local dev
# data clean. Refuses to run unless APP_ENV=development.
db-reset:
	go run ./cmd/dbreset

## ── Docker ───────────────────────────────────────────────────────────────────

docker-build:
	docker build -t $(APP_NAME):dev .

docker-run:
	docker run --rm -d \
	  --env-file .env \
	  -p $(or $(PORT),8080):$(or $(PORT),8080) \
	  $(APP_NAME):dev

prod:
	docker build -t $(APP_NAME):prod --build-arg APP_ENV=production .

## ── Clean ────────────────────────────────────────────────────────────────────

clean:
	rm -rf $(BIN_DIR) tmp/
