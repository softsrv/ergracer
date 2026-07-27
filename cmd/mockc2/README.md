# mockc2

A minimal mock of the Concept2 Logbook API, for testing the full
webhook → `RowingService.ProcessResult` → Discord pipeline against synthetic
results that don't exist on the real Concept2 API.

## Why this exists

`RowingService.ProcessResult` never trusts a webhook's own payload for result
data — it always re-fetches the authoritative result from Concept2's live API
by ID. That's the right call for real webhooks, but it means a hand-edited or
synthetic entry in `cmd/mockc2/testdata/c2_results.json` (e.g. a copy with
heart rate data stripped out, to test the "not reported" path) can never be
resolved through the real API, since it was never actually logged there —
you'll get a 404.

`mockc2` serves results from that same fixture file, so pointing the app at
this mock instead of `https://log.concept2.com` makes synthetic result IDs
resolvable, letting you exercise the entire pipeline end-to-end with made-up
data.

`cmd/mockc2/testdata/c2_results.json` is committed to the repo and meant to
be hand-edited: add, trim, or modify entries directly to cover test scenarios
(e.g. a bikeerg result, a result with no heart rate data, an interval
workout). Entries are plain JSON matching `concept2.Result`'s shape.

## Usage

Start the mock (defaults: port `4010`, data from
`cmd/mockc2/testdata/c2_results.json`):

```bash
go run ./cmd/mockc2
```

```bash
# Custom port or data file
go run ./cmd/mockc2 --port 4020 --data cmd/mockc2/testdata/c2_results.json
```

In a separate terminal, run the app against it for a test session:

```bash
CONCEPT2_API_BASE=http://localhost:4010 make run
```

(or export `CONCEPT2_API_BASE` before `make dev`). This is a one-off override
for a testing session — don't add it to your real `.env`; unset it or start
a fresh shell once you're done so the app goes back to talking to the real
Concept2 API.

## Fixture browser UI

`mockc2` serves a small HTML page at `http://localhost:4010/` (adjust for
`--port`) listing every result in the fixture file, pretty-printed, with a
**Send** button on each entry. Clicking it POSTs that result as a webhook —
matching Concept2's documented `{"data": {"type": "result-added", "result":
{...}}}` shape — straight from the browser, triggering the real webhook →
`ProcessResult` → Discord pipeline against this mock's data. This is the
fastest way to trigger a specific test scenario while you've got the fixture
file open for editing.

By default Send targets `http://localhost:PORT/webhooks/concept2` (`PORT`
from `.env`, fallback `8080`) — override with `--target`:

```bash
go run ./cmd/mockc2 --target http://localhost:3000/webhooks/concept2
```

A flash banner at the top of the page reports the result after each send
(the app's response status, or the connection error if it wasn't reachable).

## Endpoints served

| Endpoint | Notes |
|---|---|
| `GET /api/users/me/results/{id}` | The one endpoint `ProcessResult` actually calls. 404s (matching the fixture file's contents) if the ID isn't present. |
| `GET /api/users/me/results` | List endpoint, `?per_page=N` supported. Nothing in the current pipeline calls this, but it's cheap to serve from the same fixture. |
| `GET /api/users/me` | Minimal profile stub. |
| `POST /oauth/access_token` | Returns a fake-but-valid-shaped token response, covering both the code-exchange and refresh-token grants, so a token-refresh attempt during a test session doesn't hard-fail. |
