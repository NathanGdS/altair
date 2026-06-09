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

**Background workers (all goroutines, started in `main.go`):**
| Worker | Interval | Purpose |
|--------|----------|---------|
| `ConsumerWorker` | 5s | Reads ready files, fans out to worker pool, truncates file after reading |
| `PurgeMessagesWorker` | 15min | Removes messages older than 15min from processed files |
| `RemoveEmptyFilesWorker` | 5min | Moves empty files (ready + processed) to trash |
| `DeleteMakedFiles` | 15min | Deletes files in trash |

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
