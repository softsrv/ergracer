# ── Stage 1: Builder ─────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Download dependencies first (layer cache)
COPY go.mod go.sum ./
RUN go mod download

# Build Tailwind CSS (requires Node)
RUN apk add --no-cache nodejs npm
COPY web/static/css/app.css ./web/static/css/app.css
COPY web/templates ./web/templates
RUN npm install -g tailwindcss && \
    npx tailwindcss -i ./web/static/css/app.css \
                    -o ./web/static/css/dist/app.css \
                    --minify

# Compile Go binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags="-s -w" \
      -o /build/bin/app \
      ./cmd/app

# ── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM gcr.io/distroless/static:latest

WORKDIR /app

# Create non-root user
# (distroless has uid 65532 as "nonroot"; we use that)
USER 65532:65532

# Copy compiled binary
COPY --from=builder --chown=65532:65532 /build/bin/app ./app

# Copy web assets (templates + static files)
COPY --from=builder --chown=65532:65532 /build/web ./web

EXPOSE 8080

ENTRYPOINT ["./app"]
