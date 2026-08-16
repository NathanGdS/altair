# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Run
go run ./main.go
# or
make run

# Test all
go test ./...

# Test single package
go test ./handlers/...

# Test single function
go test ./handlers/ -run TestPublishHandler

# Build
go build ./...
```

## Workflow Rules (from .cursor/rules)

**Before writing any code:** explain the plan and wait for approval.

Follow micro-task loop:
1. Write small code piece
2. Write tests (unit/integration/e2e as appropriate)
3. Verify principles (clean code, simplicity, meaningful names, concurrency safety)
4. Explain what was done, wait for instructions
5. Repeat

Always run tests after adding a new one.

## Architecture

Altair is a Kafka-inspired append-only message broker. Messages flow:

```
POST /publish → fileWriterChan (buffered 200k) → append-only .json files in messages/ready/
                                                        ↓
                                              ConsumerWorker (every 5s)
                                              Worker pool: NumCPU × 2
                                                        ↓
                                              messages/processed/ (hourly files)
```

**Storage layout:**
- `messages/ready/{origin}-{YYYY-MM-DD}.json` — incoming messages, one JSON per line
- `messages/processed/{YYYYMMDD_HH}.json` — processed messages, hourly rotation
- `messages/trash/` — staging for empty files before deletion
- `deliveries/pending/{origin}-{date}.json` — one line per (message, consumer) awaiting webhook delivery
- `deliveries/failed/{origin}-{date}.json` — deliveries that exhausted retries, for manual inspection
- `data/altair.db` — SQLite: consumer registrations (`consumers` table)

**Background workers (all goroutines, started in `main.go`):**
| Worker | Interval | Purpose |
|--------|----------|---------|
| `ConsumerWorker` | 5s | Reads ready files, fans out to worker pool, truncates file after reading |
| `DeliveryWorker` | 5s | Reads `deliveries/pending/`, POSTs each line to its consumer's webhook (retry 3x, backoff 1/2/4s), truncates file after reading; failures go to `deliveries/failed/` |
| `TTLSweeperWorker` | 10s | Deactivates consumers in SQLite + cache whose last heartbeat is older than `ConsumerHeartbeatTTL` (30s) |
| `PurgeMessagesWorker` | 15min | Removes messages older than 15min from processed files |
| `RemoveEmptyFilesWorker` | 5min | Moves empty files (ready + processed) to trash |
| `DeleteMakedFiles` | 15min | Deletes files in trash |

**Consumer registration + delivery:**

Consumers register a webhook to receive messages published to a given origin:
- `POST /consumers` — register a consumer (`{"origin": "...", "webhook_url": "..."}`), returns `{"id": "<uuid>"}`
- `POST /consumers/{id}/heartbeat` — keep a consumer alive; consumers must heartbeat within `ConsumerHeartbeatTTL` (30s) or `TTLSweeperWorker` deactivates them
- `DELETE /consumers/{id}` — unregister a consumer

Delivery is pub/sub broadcast: every active consumer registered for an origin gets a copy of every message published to that origin. There are no consumer groups (no competing-consumers/partitioned delivery), no authentication on the webhook endpoints or on the delivered payload, and no crash-safe redelivery for in-flight batches — a crash between reading and truncating a `deliveries/pending/` file can lose or duplicate in-flight deliveries, the same risk class as the existing `ready/` → `processed/` pipeline.

**Packages:**
- `handlers/` — HTTP handler + `Message` struct + async `fileWriterWorker`
- `workers/` — all background goroutines
- `web/` — HTMX dashboard (`:8080/`), templates in `web/templates/*.html`
- `shared/` — global zap logger (`shared.Log`), worker pool size, interval constants

## Logging

Use `shared.Log` (zap structured logger) — **not** `log` or `fmt`. It outputs JSON to stdout at INFO level.

```go
shared.Log.Info("message")
shared.Log.Error("message: " + err.Error())
```

## Tests

Tests use `testify` with Triple-A structure:

```go
t.Run("description", func(t *testing.T) {
    // Arrange
    // Act
    // Assert
})
```

Test files live alongside the code they test (e.g., `handlers/publish_handler_test.go`).
