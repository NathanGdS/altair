# Consumer Registration + Delivery — Design

Status: Draft, pending user review
Branch: `feature/consumer-registration-delivery`
Date: 2026-08-16

## 1. Problem

Altair processes messages (`ConsumerWorker` → `processSingleMessage`) but only ever
writes them to `messages/processed/`. There is no way for an external service to
subscribe to an `origin` and receive messages as they're processed —
`workers/consumer_worker.go:98` marks this explicitly as a TODO. This spec adds
consumer registration and push (webhook) delivery on top of the existing pipeline
without introducing an external message broker/queue dependency, consistent with
Altair's single-process, filesystem-first design.

## 2. Goals

- External services register a webhook URL for an `origin` and receive an HTTP
  POST for every message published to that origin from then on (pub/sub broadcast
  — every active consumer on an origin gets every message).
- Survive bursty, unpredictable throughput (message rate depends on the calling
  system — could be low or very high) without unbounded memory growth or a
  single-writer bottleneck.
- Dead/unreachable webhooks are detected and stop being retried indefinitely.
- No new external infrastructure dependency (no Redis/Kafka/etc.) — SQLite
  (embedded) and the filesystem are the only new pieces, matching what's already
  used in the repo.

## 3. Non-goals

- Message replay / offsets (already a documented limitation, out of scope here).
- Consumer groups / load-balanced delivery (explicitly chose broadcast, not
  Kafka-style partitioned consumer groups).
- Auth on `/consumers` or `/publish` (repo-wide known limitation, unchanged).
- Ordering guarantees across consumers or across retries.

## 4. Architecture

Two concerns, two different storage strategies, chosen deliberately based on
volume:

- **Consumer registration** (register / heartbeat / TTL expiry): volume scales
  with *number of consumers*, not message volume. Low, steady. → **SQLite**
  (source of truth) + **in-memory cache** (hot-path reads, no DB hit per
  message).
- **Delivery** (queued → delivered/failed per message×consumer): volume scales
  with *messages × consumers per origin*, can be very high and bursty. → **same
  append-only file pattern already used for `messages/ready/` and
  `messages/processed/`**, not SQLite. A prior draft of this design put queued
  deliveries in SQLite; at an example load of ~1M messages/min × ~4 consumers,
  that's ~4M rows/min needing insert *and* a status update, well past what a
  single SQLite writer sustains. Files scale the same way ingestion already
  does today. Only **terminal failures** (rare, proportional to failure rate,
  not total volume) get persisted, and that goes to a plain file too — no
  SQLite involvement in the delivery hot path at all.

```
POST /consumers {origin, webhook_url}
        │
        ▼
  SQLite `consumers` table  ←──────────────┐
        │ write-through                    │ heartbeat renews TTL
        ▼                                  │
  in-memory cache: map[origin][]consumer   │
        ▲                            POST /consumers/{id}/heartbeat
        │ read (hot path, no I/O)
        │
ConsumerWorker (existing, unchanged)
  → processSingleMessage(msg):
      - write to messages/processed/ (unchanged)
      - look up cache[msg.Origin]
      - for each active consumer: append one line to
        deliveries/pending/{origin}-{date}.json

DeliveryWorker (new — same polling pattern as ConsumerWorker)
  → every DeliveryRunningInterval: read deliveries/pending/*.json, truncate
  → bounded worker pool (channel cap = DeliveryQueueCapacity)
      → POST webhook_url with message JSON, timeout 5s
      → retry 3x, backoff 1s/2s/4s, on the same goroutine (pool size bounds
        worst-case concurrent blocking, same trade-off as the existing
        ConsumerWorkingPool)
      → success: done, nothing else written
      → exhausted retries: append to deliveries/failed/{origin}-{date}.json

TTLSweeperWorker (new — same polling pattern as other workers)
  → every ConsumerTTLSweepInterval: find consumers in cache with
    last_heartbeat_at older than ConsumerHeartbeatTTL
  → mark `inactive` in SQLite, remove from in-memory cache
```

## 5. Components

### 5.1 `handlers/consumer_handler.go`
- `POST /consumers` — body `{origin, webhook_url}` → creates row in SQLite
  (status `active`, `last_heartbeat_at = now`), adds to in-memory cache,
  returns `{id}`.
- `POST /consumers/{id}/heartbeat` — updates `last_heartbeat_at` in SQLite and
  cache. 404 if id unknown or inactive (client must re-register).
- `DELETE /consumers/{id}` — manual unregister, marks `inactive`, removes from
  cache.

### 5.2 `shared/consumer_store.go`
- Opens SQLite DB (file, e.g. `data/altair.db` — `data/` dir already exists in
  repo root).
- Schema:
  ```sql
  CREATE TABLE consumers (
    id TEXT PRIMARY KEY,
    origin TEXT NOT NULL,
    webhook_url TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active', -- active | inactive
    last_heartbeat_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL
  );
  CREATE INDEX idx_consumers_origin_status ON consumers(origin, status);
  ```
- In-memory cache: `map[string][]Consumer` keyed by origin, guarded by
  `sync.RWMutex`. Loaded from SQLite (`status = 'active'`) on boot. All reads
  used by `processSingleMessage` hit only the cache, never SQLite.
- Register/heartbeat/deactivate write to SQLite first, then update the cache
  (write-through, matches how `writeToFile` / append-file durability already
  works elsewhere in the repo — write the durable copy, then update the fast
  path).

### 5.3 `deliveries/pending/{origin}-{date}.json` and `deliveries/failed/{origin}-{date}.json`
- Same line-delimited JSON convention as `messages/ready/`.
- Pending line: `{message_id, origin, consumer_id, webhook_url, payload, attempts}`.
  `payload` is the already-serialized `Message` JSON (denormalized on write —
  cheap, avoids a lookup back into `processed/` at delivery time).
- Failed line: pending line + `{last_error, failed_at}`.
- `workers.RemoveEmptyFilesWorker` (existing, already parameterized by dir) is
  additionally invoked for `deliveries/pending` and `deliveries/failed` in
  `main.go` — no new code needed there.

### 5.4 `workers/delivery_worker.go`
- Same shape as `ConsumerWorker`: `initDirectories` (adds `deliveries/pending`,
  `deliveries/failed`), interval loop, read+truncate, worker pool over a
  bounded channel (`DeliveryQueueCapacity`, same idea as the existing
  `msgChan := make(chan message, 5000)`).
- Shared `http.Client` (connection reuse / keep-alive) across all delivery
  workers, `Timeout: 5s` per attempt.
- Retry: 3 attempts, sleep 1s/2s/4s between attempts, in the worker goroutine.
  Bounded impact because the pool itself is bounded — a stuck webhook occupies
  at most one worker slot for ~7s worst case, not the whole pipeline (delivery
  is already decoupled from `ConsumerWorker`'s file processing).

### 5.5 `workers/ttl_sweeper_worker.go`
- Interval loop (`ConsumerTTLSweepInterval`), scans the in-memory cache for
  entries with `last_heartbeat_at` older than `ConsumerHeartbeatTTL`, updates
  SQLite `status = 'inactive'` for those ids, removes them from the cache.

### 5.6 `shared/constants.go` additions
```go
DeliveryRunningInterval  = 5 * time.Second   // DeliveryWorker poll interval
DeliveryQueueCapacity    = 5000              // bounded channel size
DeliveryMaxRetries       = 3
DeliveryHTTPTimeout      = 5 * time.Second
ConsumerHeartbeatTTL     = 30 * time.Second  // configurable per earlier discussion
ConsumerTTLSweepInterval = 10 * time.Second
```

## 6. Data flow, happy path

1. Consumer registers: `POST /consumers {origin: "orders", webhook_url: "https://x/hook"}` → id `c1`, active, in cache.
2. `POST /publish` with `origin: "orders"` → existing pipeline writes to `messages/ready/orders-2026-08-16.json`.
3. `ConsumerWorker` reads it, `processSingleMessage` writes to `messages/processed/...`, looks up `cache["orders"]` → finds `c1`, appends one line to `deliveries/pending/orders-2026-08-16.json`.
4. `DeliveryWorker` reads that file, truncates it, dispatches the line to the worker pool, POSTs the message JSON to `c1`'s webhook. 200 response → done.

## 7. Failure handling

- Webhook returns non-2xx or times out → retry (1s, 2s, 4s). Still failing after
  3 attempts → append to `deliveries/failed/{origin}-{date}.json`, log error via
  `shared.Log`. No automatic reprocessing of failed lines (manual/ops concern,
  matches how `messages/trash/` is handled today).
- Crash between truncating `deliveries/pending/*.json` and finishing the
  in-flight batch: in-flight deliveries for that batch are lost. This is the
  same class of risk the existing `ready/` → `processed/` pipeline already has
  (truncate-then-process) — not a new category of data-loss risk, just applied
  to a new pipeline.
- Consumer webhook down long-term with no heartbeat: `TTLSweeperWorker` marks it
  `inactive` after `ConsumerHeartbeatTTL` with no heartbeat; `processSingleMessage`
  stops enqueueing new deliveries for it (cache no longer has it). Existing
  pending lines already written before deactivation still get attempted once by
  `DeliveryWorker` (they don't check cache; simplest correct behavior — small
  bounded extra work, not worth guarding against).

## 8. Dependencies

- New Go dependency: a SQLite driver (e.g. `mattn/go-sqlite3` or
  `modernc.org/sqlite` for a CGo-free build — pick one during implementation;
  `modernc.org/sqlite` avoids requiring CGO/gcc for `go build`, worth preferring
  given the repo currently builds with plain `go build ./...`).
- `data/` directory already exists in the repo root — reuse it for the SQLite
  file.

## 9. Testing

Following the repo's existing testify/Triple-A convention, test files beside
the code they test:

- `handlers/consumer_handler_test.go` — register, heartbeat (valid/unknown id),
  unregister.
- `shared/consumer_store_test.go` — write-through cache behavior, TTL-based
  filtering on load.
- `workers/delivery_worker_test.go` — using `httptest.Server`: success path
  (no file left behind), retry-then-success, retry-exhausted-then-failed-file,
  concurrent consumers on the same origin (broadcast fan-out).
- `workers/ttl_sweeper_worker_test.go` — consumer past TTL gets deactivated and
  removed from cache; consumer within TTL is untouched.

## 10. Open items for implementation plan

- Exact SQLite driver choice (leaning `modernc.org/sqlite`, confirm during
  implementation).
- Whether `DeliveryWorker`'s retry sleep should move off the worker goroutine
  later (e.g. requeue with a scheduled time) if `DeliveryQueueCapacity` proves
  too small under real load — deferred, not needed for initial version.
