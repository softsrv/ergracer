# simhook

A dev tool for simulating Concept2 webhook deliveries to your local server.

It reads your stored Concept2 OAuth token from the database, fetches real results
from the Concept2 API, and posts them as signed webhooks — triggering the full
processing pipeline including Discord embed delivery.

## Prerequisites

- The app must be running locally (`make dev` or `make run`)
- Your Concept2 account must be linked via the profile page
- `.env` must be present with `DATABASE_URL`, `OAUTH_TOKEN_ENC_KEY`, and `CONCEPT2_WEBHOOK_SECRET`

## Commands

### fetch

Fetches your recent Concept2 results and saves them to `tmp/c2_results.json`.

```bash
go run ./cmd/simhook fetch
```

```bash
# Fetch a different number of results (default: 10)
go run ./cmd/simhook fetch --count 5
```

### send

Loads `tmp/c2_results.json`, lets you pick a result, and POSTs it as a signed
webhook to your local server. Run `fetch` first.

```bash
# Interactive — shows a numbered list and prompts for selection
go run ./cmd/simhook send

# Send a specific result by position in the list
go run ./cmd/simhook send --index 3

# Send a specific result by Concept2 result ID
go run ./cmd/simhook send --id 12345678

# Send to a non-default port
go run ./cmd/simhook send --target http://localhost:3000/webhooks/concept2
```

The default target is `http://localhost:PORT/webhooks/concept2` using the `PORT`
value from your `.env` (fallback: 8080).

## What happens when you send

1. The webhook is signed with HMAC-SHA256 using `CONCEPT2_WEBHOOK_SECRET` and posted to the local server
2. The server verifies the signature and extracts the `result_id`
3. `RowingService.ProcessResult` runs asynchronously: fetches the full result from Concept2, resolves the Discord registration and guild settings, and sends the embed to the configured channel
