# Consumer Registration + Delivery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let external services register a webhook per `origin` and receive an HTTP POST for every message processed on that origin, without adding any external infra beyond an embedded SQLite DB and the filesystem.

**Architecture:** Consumer registration (low volume, needs durability) lives in SQLite with a write-through in-memory cache for hot-path lookups. Delivery (high, bursty volume) reuses the repo's existing append-file-then-poll pattern (`messages/ready/` → `messages/processed/`) instead of SQLite: `deliveries/pending/{origin}-{date}.json` gets one line per (message, consumer), a new `DeliveryWorker` polls it, dispatches through a bounded worker pool, retries with backoff, and only persists terminal failures to `deliveries/failed/{origin}-{date}.json`.

**Tech Stack:** Go 1.24, `net/http` (stdlib `ServeMux` pattern routing), `database/sql` + `modernc.org/sqlite` (pure-Go, no CGo), `go.uber.org/zap` (`shared.Log`), `testify` (assert), existing worker/interval pattern from `workers/`.

**Spec:** `specs/consumer-registration-delivery/consumer-registration-delivery.design`

## Global Constraints

- No new external infrastructure dependency beyond SQLite (embedded) and the filesystem — no Redis/Kafka/etc.
- Delivery is pub/sub broadcast only — every active consumer on an origin gets every message, no consumer groups / load balancing.
- No auth on the new `/consumers` endpoints — matches the existing repo-wide "no auth" limitation.
- Delivery retry: exactly 3 attempts, backoff 1s / 2s / 4s between attempts.
- HTTP delivery timeout: 5s per attempt.
- Logging goes through `shared.Log` (zap) only — never `log` or `fmt`, per `AGENTS.md`.
- Tests use `testify` with Arrange/Act/Assert structure, test files live beside the code they test, per `AGENTS.md`.

---

### Task 1: Consumer store core — schema, register, cache read

**Files:**
- Create: `shared/consumer_store.go`
- Test: `shared/consumer_store_test.go`
- Modify: `go.mod`, `go.sum` (via `go get`)

**Interfaces:**
- Produces:
  - `type shared.Consumer struct { ID, Origin, WebhookURL, Status string; LastHeartbeatAt, CreatedAt time.Time }`
  - `type shared.ConsumerStore struct { ... }` (unexported fields: `db *sql.DB`, `mu sync.RWMutex`, `cache map[string][]Consumer`)
  - `func shared.NewConsumerStore(dbPath string) (*ConsumerStore, error)`
  - `func (s *ConsumerStore) Register(origin, webhookURL string) (Consumer, error)`
  - `func (s *ConsumerStore) ActiveConsumersForOrigin(origin string) []Consumer`
  - `func (s *ConsumerStore) Close() error`
  - `var shared.Consumers *ConsumerStore` (package-level, set by `InitConsumerStore`)
  - `func shared.InitConsumerStore(dbPath string) error`

- [ ] **Step 1: Add the SQLite dependency**

Run: `go get modernc.org/sqlite`

Expected: `go.mod` gains a `require modernc.org/sqlite vX.Y.Z` line (and `go.sum` is updated).

- [ ] **Step 2: Write the failing test**

Create `shared/consumer_store_test.go`:

```go
package shared

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConsumerStore_RegisterAndActiveConsumersForOrigin(t *testing.T) {
	t.Run("should register a consumer and make it visible via the cache", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "consumers.db")
		store, err := NewConsumerStore(dbPath)
		assert.NoError(t, err)
		defer store.Close()

		// Act
		consumer, err := store.Register("orders", "https://example.com/hook")

		// Assert
		assert.NoError(t, err)
		assert.NotEmpty(t, consumer.ID)
		assert.Equal(t, "active", consumer.Status)

		active := store.ActiveConsumersForOrigin("orders")
		assert.Len(t, active, 1)
		assert.Equal(t, consumer.ID, active[0].ID)
	})

	t.Run("should return empty slice for origin with no consumers", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "consumers.db")
		store, err := NewConsumerStore(dbPath)
		assert.NoError(t, err)
		defer store.Close()

		// Act
		active := store.ActiveConsumersForOrigin("unknown-origin")

		// Assert
		assert.Empty(t, active)
	})
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./shared/... -run TestConsumerStore_RegisterAndActiveConsumersForOrigin -v`
Expected: FAIL — `undefined: NewConsumerStore`

- [ ] **Step 4: Write the implementation**

Create `shared/consumer_store.go`:

```go
package shared

import (
	"database/sql"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Consumer struct {
	ID              string
	Origin          string
	WebhookURL      string
	Status          string
	LastHeartbeatAt time.Time
	CreatedAt       time.Time
}

type ConsumerStore struct {
	db    *sql.DB
	mu    sync.RWMutex
	cache map[string][]Consumer // origin -> active consumers
}

const createConsumersTableSQL = `
CREATE TABLE IF NOT EXISTS consumers (
	id TEXT PRIMARY KEY,
	origin TEXT NOT NULL,
	webhook_url TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'active',
	last_heartbeat_at DATETIME NOT NULL,
	created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_consumers_origin_status ON consumers(origin, status);
`

func NewConsumerStore(dbPath string) (*ConsumerStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	if _, err := db.Exec(createConsumersTableSQL); err != nil {
		return nil, err
	}

	store := &ConsumerStore{
		db:    db,
		cache: make(map[string][]Consumer),
	}

	if err := store.loadActiveIntoCache(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *ConsumerStore) loadActiveIntoCache() error {
	rows, err := s.db.Query(`SELECT id, origin, webhook_url, status, last_heartbeat_at, created_at FROM consumers WHERE status = 'active'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	for rows.Next() {
		var c Consumer
		if err := rows.Scan(&c.ID, &c.Origin, &c.WebhookURL, &c.Status, &c.LastHeartbeatAt, &c.CreatedAt); err != nil {
			return err
		}
		s.cache[c.Origin] = append(s.cache[c.Origin], c)
	}

	return rows.Err()
}

func (s *ConsumerStore) Register(origin, webhookURL string) (Consumer, error) {
	now := time.Now().UTC()
	c := Consumer{
		ID:              uuid.New().String(),
		Origin:          origin,
		WebhookURL:      webhookURL,
		Status:          "active",
		LastHeartbeatAt: now,
		CreatedAt:       now,
	}

	_, err := s.db.Exec(
		`INSERT INTO consumers (id, origin, webhook_url, status, last_heartbeat_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.Origin, c.WebhookURL, c.Status, c.LastHeartbeatAt, c.CreatedAt,
	)
	if err != nil {
		return Consumer{}, err
	}

	s.mu.Lock()
	s.cache[c.Origin] = append(s.cache[c.Origin], c)
	s.mu.Unlock()

	return c, nil
}

func (s *ConsumerStore) ActiveConsumersForOrigin(origin string) []Consumer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	consumers := s.cache[origin]
	out := make([]Consumer, len(consumers))
	copy(out, consumers)
	return out
}

func (s *ConsumerStore) Close() error {
	return s.db.Close()
}

// Consumers is the process-wide consumer store, set by InitConsumerStore.
var Consumers *ConsumerStore

func InitConsumerStore(dbPath string) error {
	store, err := NewConsumerStore(dbPath)
	if err != nil {
		return err
	}
	Consumers = store
	return nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./shared/... -run TestConsumerStore_RegisterAndActiveConsumersForOrigin -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum shared/consumer_store.go shared/consumer_store_test.go
git commit -m "feat: add consumer store with SQLite-backed registration and in-memory cache"
```

---

### Task 2: Heartbeat, deactivate, TTL expiry lookup

**Files:**
- Modify: `shared/consumer_store.go`
- Modify: `shared/consumer_store_test.go`

**Interfaces:**
- Consumes: `shared.ConsumerStore` (Task 1), its `db`, `mu`, `cache` fields.
- Produces:
  - `var shared.ErrConsumerNotFound error`
  - `func (s *ConsumerStore) Heartbeat(id string) error`
  - `func (s *ConsumerStore) Deactivate(id string) error`
  - `func (s *ConsumerStore) ExpiredConsumers(ttl time.Duration) []Consumer`

- [ ] **Step 1: Write the failing tests**

Append to `shared/consumer_store_test.go`:

```go
func TestConsumerStore_HeartbeatDeactivateExpired(t *testing.T) {
	t.Run("heartbeat updates last_heartbeat_at for a known consumer", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "consumers.db")
		store, err := NewConsumerStore(dbPath)
		assert.NoError(t, err)
		defer store.Close()

		consumer, err := store.Register("orders", "https://example.com/hook")
		assert.NoError(t, err)

		// Act
		err = store.Heartbeat(consumer.ID)

		// Assert
		assert.NoError(t, err)
	})

	t.Run("heartbeat returns ErrConsumerNotFound for unknown id", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "consumers.db")
		store, err := NewConsumerStore(dbPath)
		assert.NoError(t, err)
		defer store.Close()

		// Act
		err = store.Heartbeat("does-not-exist")

		// Assert
		assert.ErrorIs(t, err, ErrConsumerNotFound)
	})

	t.Run("deactivate removes consumer from active cache and heartbeat then fails", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "consumers.db")
		store, err := NewConsumerStore(dbPath)
		assert.NoError(t, err)
		defer store.Close()

		consumer, err := store.Register("orders", "https://example.com/hook")
		assert.NoError(t, err)

		// Act
		err = store.Deactivate(consumer.ID)
		assert.NoError(t, err)

		// Assert
		assert.Empty(t, store.ActiveConsumersForOrigin("orders"))
		err = store.Heartbeat(consumer.ID)
		assert.ErrorIs(t, err, ErrConsumerNotFound)
	})

	t.Run("expired consumers are returned once past ttl", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "consumers.db")
		store, err := NewConsumerStore(dbPath)
		assert.NoError(t, err)
		defer store.Close()

		fresh, err := store.Register("orders", "https://example.com/fresh")
		assert.NoError(t, err)
		stale, err := store.Register("orders", "https://example.com/stale")
		assert.NoError(t, err)

		store.mu.Lock()
		for i, c := range store.cache["orders"] {
			if c.ID == stale.ID {
				store.cache["orders"][i].LastHeartbeatAt = time.Now().UTC().Add(-time.Hour)
			}
		}
		store.mu.Unlock()

		// Act
		expired := store.ExpiredConsumers(30 * time.Second)

		// Assert
		assert.Len(t, expired, 1)
		assert.Equal(t, stale.ID, expired[0].ID)
		_ = fresh
	})
}
```

Update the `import` block at the top of `shared/consumer_store_test.go` (added in Task 1) to include `"time"`, since this batch of tests uses `time.Now`, `time.Hour`, and `30 * time.Second` directly:

```go
import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./shared/... -run TestConsumerStore_HeartbeatDeactivateExpired -v`
Expected: FAIL — `undefined: ErrConsumerNotFound` (compile error)

- [ ] **Step 3: Write the implementation**

Add to `shared/consumer_store.go`. First update its `import` block (added in Task 1) to include `"errors"`:

```go
import (
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)
```

Then append below the existing `Register`/`ActiveConsumersForOrigin`/`Close` methods:

```go
var ErrConsumerNotFound = errors.New("consumer not found or inactive")

func (s *ConsumerStore) Heartbeat(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for origin, consumers := range s.cache {
		for i, c := range consumers {
			if c.ID == id {
				now := time.Now().UTC()
				if _, err := s.db.Exec(`UPDATE consumers SET last_heartbeat_at = ? WHERE id = ?`, now, id); err != nil {
					return err
				}
				consumers[i].LastHeartbeatAt = now
				s.cache[origin] = consumers
				return nil
			}
		}
	}

	return ErrConsumerNotFound
}

func (s *ConsumerStore) Deactivate(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.Exec(`UPDATE consumers SET status = 'inactive' WHERE id = ?`, id); err != nil {
		return err
	}

	for origin, consumers := range s.cache {
		for i, c := range consumers {
			if c.ID == id {
				s.cache[origin] = append(consumers[:i], consumers[i+1:]...)
				return nil
			}
		}
	}

	return nil
}

func (s *ConsumerStore) ExpiredConsumers(ttl time.Duration) []Consumer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cutoff := time.Now().UTC().Add(-ttl)
	var expired []Consumer
	for _, consumers := range s.cache {
		for _, c := range consumers {
			if c.LastHeartbeatAt.Before(cutoff) {
				expired = append(expired, c)
			}
		}
	}

	return expired
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./shared/... -v`
Expected: PASS (all `ConsumerStore` tests, including Task 1's)

- [ ] **Step 5: Commit**

```bash
git add shared/consumer_store.go shared/consumer_store_test.go
git commit -m "feat: add consumer heartbeat, deactivation, and TTL expiry lookup"
```

---

### Task 3: Consumer registration HTTP handlers

**Files:**
- Create: `handlers/consumer_handler.go`
- Test: `handlers/consumer_handler_test.go`

**Interfaces:**
- Consumes: `shared.Consumers` (var), `shared.InitConsumerStore`, `shared.ErrConsumerNotFound`, `(*shared.ConsumerStore).Register/Heartbeat/Deactivate` (Tasks 1-2).
- Produces:
  - `func handlers.RegisterConsumerHandler(w http.ResponseWriter, r *http.Request)`
  - `func handlers.ConsumerHeartbeatHandler(w http.ResponseWriter, r *http.Request)`
  - `func handlers.UnregisterConsumerHandler(w http.ResponseWriter, r *http.Request)`
  - Registered routes (wired in Task 7): `POST /consumers`, `POST /consumers/{id}/heartbeat`, `DELETE /consumers/{id}`.

- [ ] **Step 1: Write the failing tests**

Create `handlers/consumer_handler_test.go`:

```go
package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nathangds/altair/shared"
)

func TestConsumerHandlers(t *testing.T) {
	t.Run("register returns 200 and an id", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)

		body := `{"origin": "orders", "webhook_url": "https://example.com/hook"}`
		request := httptest.NewRequest(http.MethodPost, "/consumers", bytes.NewBufferString(body))
		response := httptest.NewRecorder()

		// Act
		RegisterConsumerHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusOK, response.Code)
		var resp struct {
			ID string `json:"id"`
		}
		err = json.Unmarshal(response.Body.Bytes(), &resp)
		assert.NoError(t, err)
		assert.NotEmpty(t, resp.ID)
	})

	t.Run("register returns 400 when origin is missing", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)

		body := `{"webhook_url": "https://example.com/hook"}`
		request := httptest.NewRequest(http.MethodPost, "/consumers", bytes.NewBufferString(body))
		response := httptest.NewRecorder()

		// Act
		RegisterConsumerHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusBadRequest, response.Code)
	})

	t.Run("heartbeat returns 200 for a registered consumer", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)

		consumer, err := shared.Consumers.Register("orders", "https://example.com/hook")
		assert.NoError(t, err)

		request := httptest.NewRequest(http.MethodPost, "/consumers/"+consumer.ID+"/heartbeat", nil)
		request.SetPathValue("id", consumer.ID)
		response := httptest.NewRecorder()

		// Act
		ConsumerHeartbeatHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusOK, response.Code)
	})

	t.Run("heartbeat returns 404 for unknown consumer", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)

		request := httptest.NewRequest(http.MethodPost, "/consumers/unknown/heartbeat", nil)
		request.SetPathValue("id", "unknown")
		response := httptest.NewRecorder()

		// Act
		ConsumerHeartbeatHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusNotFound, response.Code)
	})

	t.Run("unregister deactivates the consumer", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)

		consumer, err := shared.Consumers.Register("orders", "https://example.com/hook")
		assert.NoError(t, err)

		request := httptest.NewRequest(http.MethodDelete, "/consumers/"+consumer.ID, nil)
		request.SetPathValue("id", consumer.ID)
		response := httptest.NewRecorder()

		// Act
		UnregisterConsumerHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Empty(t, shared.Consumers.ActiveConsumersForOrigin("orders"))
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./handlers/... -run TestConsumerHandlers -v`
Expected: FAIL — `undefined: RegisterConsumerHandler`

- [ ] **Step 3: Write the implementation**

Create `handlers/consumer_handler.go`:

```go
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/nathangds/altair/shared"
)

type registerConsumerRequest struct {
	Origin     string `json:"origin"`
	WebhookURL string `json:"webhook_url"`
}

type registerConsumerResponse struct {
	ID string `json:"id"`
}

func RegisterConsumerHandler(w http.ResponseWriter, r *http.Request) {
	var req registerConsumerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}

	if req.Origin == "" || req.WebhookURL == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("origin and webhook_url are required"))
		return
	}

	consumer, err := shared.Consumers.Register(req.Origin, req.WebhookURL)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	resp, _ := json.Marshal(registerConsumerResponse{ID: consumer.ID})
	w.WriteHeader(http.StatusOK)
	w.Write(resp)
}

func ConsumerHeartbeatHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	err := shared.Consumers.Heartbeat(id)
	if errors.Is(err, shared.ErrConsumerNotFound) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(err.Error()))
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	w.WriteHeader(http.StatusOK)
}

func UnregisterConsumerHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := shared.Consumers.Deactivate(id); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	w.WriteHeader(http.StatusOK)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./handlers/... -v`
Expected: PASS (all handler tests, including existing `TestPublishHandler`)

- [ ] **Step 5: Commit**

```bash
git add handlers/consumer_handler.go handlers/consumer_handler_test.go
git commit -m "feat: add consumer registration, heartbeat, and unregister HTTP handlers"
```

---

### Task 4: Enqueue deliveries from the consumer pipeline

**Files:**
- Create: `workers/delivery.go`
- Modify: `workers/consumer_worker.go`
- Test: `workers/consumer_worker_test.go`

**Interfaces:**
- Consumes: `shared.Consumers.ActiveConsumersForOrigin(origin string) []shared.Consumer` (Task 1).
- Produces:
  - `const workers.DeliveryPendingDir = "./deliveries/pending"`
  - `const workers.DeliveryFailedDir = "./deliveries/failed"`
  - `type workers.deliveryLine struct { MessageID, Origin, ConsumerID, WebhookURL, Payload, LastError, FailedAt string; Attempts int }`
  - `func workers.initDeliveryDirectories()`
  - `func workers.appendDeliveryLine(path string, line deliveryLine) error`
  - `func workers.readAndTruncatePendingLines(path string) ([]deliveryLine, error)` (consumed by Task 5)
  - `func workers.enqueueDeliveries(messageID, origin, payload string, consumers []shared.Consumer)`
  - Test helper `func withTempWorkDir(t *testing.T)` (reused by Task 5 and Task 6 test files, same package)

- [ ] **Step 1: Write the failing test**

Create `workers/consumer_worker_test.go`:

```go
package workers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nathangds/altair/shared"
)

func withTempWorkDir(t *testing.T) {
	t.Helper()
	originalDir, err := os.Getwd()
	assert.NoError(t, err)

	tempDir := t.TempDir()
	assert.NoError(t, os.Chdir(tempDir))

	t.Cleanup(func() {
		assert.NoError(t, os.Chdir(originalDir))
	})
}

func TestProcessSingleMessage(t *testing.T) {
	t.Run("enqueues one delivery line per active consumer on the message origin", func(t *testing.T) {
		// Arrange
		withTempWorkDir(t)
		initDirectories()
		initDeliveryDirectories()

		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)

		_, err = shared.Consumers.Register("orders", "https://example.com/hook-a")
		assert.NoError(t, err)
		_, err = shared.Consumers.Register("orders", "https://example.com/hook-b")
		assert.NoError(t, err)

		line := `{"origin":"orders","id":"msg-1","data":{"key":"value"}}`

		// Act
		processSingleMessage(message{FileName: "orders-2026-08-16.json", Line: line})

		// Assert
		files, err := os.ReadDir(DeliveryPendingDir)
		assert.NoError(t, err)
		assert.Len(t, files, 1)

		content, err := os.ReadFile(filepath.Join(DeliveryPendingDir, files[0].Name()))
		assert.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(string(content)), "\n")
		assert.Len(t, lines, 2)
	})

	t.Run("does not create a pending file when the origin has no consumers", func(t *testing.T) {
		// Arrange
		withTempWorkDir(t)
		initDirectories()
		initDeliveryDirectories()

		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)

		line := `{"origin":"no-subscribers","id":"msg-2","data":{}}`

		// Act
		processSingleMessage(message{FileName: "no-subscribers-2026-08-16.json", Line: line})

		// Assert
		files, err := os.ReadDir(DeliveryPendingDir)
		assert.NoError(t, err)
		assert.Empty(t, files)
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./workers/... -run TestProcessSingleMessage -v`
Expected: FAIL — `undefined: initDeliveryDirectories` (compile error)

- [ ] **Step 3: Write the implementation**

Create `workers/delivery.go`:

```go
package workers

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/nathangds/altair/shared"
)

const (
	DeliveryPendingDir = "./deliveries/pending"
	DeliveryFailedDir  = "./deliveries/failed"
)

type deliveryLine struct {
	MessageID  string `json:"message_id"`
	Origin     string `json:"origin"`
	ConsumerID string `json:"consumer_id"`
	WebhookURL string `json:"webhook_url"`
	Payload    string `json:"payload"`
	Attempts   int    `json:"attempts"`
	LastError  string `json:"last_error,omitempty"`
	FailedAt   string `json:"failed_at,omitempty"`
}

type messageEnvelope struct {
	Origin string `json:"origin"`
	Id     string `json:"id"`
}

func initDeliveryDirectories() {
	dirs := []string{DeliveryPendingDir, DeliveryFailedDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			shared.Log.Error("Error creating directory", zap.String("dir", dir), zap.Error(err))
		}
	}
}

func pendingFilePath(origin string) string {
	fileName := time.Now().Format("2006-01-02")
	return filepath.Join(DeliveryPendingDir, origin+"-"+fileName+".json")
}

func failedFilePath(origin string) string {
	fileName := time.Now().Format("2006-01-02")
	return filepath.Join(DeliveryFailedDir, origin+"-"+fileName+".json")
}

func appendDeliveryLine(path string, line deliveryLine) error {
	jsonBytes, err := json.Marshal(line)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(append(jsonBytes, '\n'))
	return err
}

func enqueueDeliveries(messageID, origin, payload string, consumers []shared.Consumer) {
	if len(consumers) == 0 {
		return
	}

	path := pendingFilePath(origin)
	for _, c := range consumers {
		line := deliveryLine{
			MessageID:  messageID,
			Origin:     origin,
			ConsumerID: c.ID,
			WebhookURL: c.WebhookURL,
			Payload:    payload,
		}
		if err := appendDeliveryLine(path, line); err != nil {
			shared.Log.Error("failed to enqueue delivery", zap.String("consumer_id", c.ID), zap.Error(err))
		}
	}
}

func readAndTruncatePendingLines(path string) ([]deliveryLine, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	var lines []deliveryLine
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var line deliveryLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			shared.Log.Error("failed to parse delivery line", zap.Error(err))
			continue
		}
		lines = append(lines, line)
	}
	file.Close()

	_ = os.Truncate(path, 0)
	return lines, scanner.Err()
}

func parseOriginAndID(line string) (origin string, id string) {
	var env messageEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		shared.Log.Error("failed to parse message for delivery routing", zap.Error(err))
		return "", ""
	}
	return env.Origin, env.Id
}
```

Modify `workers/consumer_worker.go`: replace the `processSingleMessage` function (currently at line 97-100) —

```go
func processSingleMessage(msg message) {
	// TODO: handle http delivery in future
	saveProcessed(msg.Line)
}
```

with:

```go
func processSingleMessage(msg message) {
	saveProcessed(msg.Line)

	origin, messageID := parseOriginAndID(msg.Line)
	if origin == "" {
		return
	}

	consumers := shared.Consumers.ActiveConsumersForOrigin(origin)
	enqueueDeliveries(messageID, origin, msg.Line, consumers)
}
```

Also modify `ConsumerWorker()` (currently at line 28-31) to also create the delivery directories, so they exist before `processSingleMessage` can write to them, regardless of goroutine start order relative to `DeliveryWorker` (Task 5):

```go
func ConsumerWorker() {
	initDirectories()
	initDeliveryDirectories()
	go startConsumerLoop()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./workers/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add workers/delivery.go workers/consumer_worker.go workers/consumer_worker_test.go
git commit -m "feat: enqueue per-consumer deliveries when processing messages"
```

---

### Task 5: Delivery worker — poll, dispatch, retry, record failures

**Files:**
- Create: `workers/delivery_worker.go`
- Modify: `shared/constants.go`
- Test: `workers/delivery_worker_test.go`

**Interfaces:**
- Consumes: `workers.DeliveryPendingDir`, `workers.DeliveryFailedDir`, `workers.deliveryLine`, `workers.appendDeliveryLine`, `workers.readAndTruncatePendingLines`, `workers.failedFilePath`, `workers.initDeliveryDirectories` (Task 4); `shared.ConsumerWorkingPool` (existing).
- Produces:
  - `const shared.DeliveryRunningInterval = 5 * time.Second`
  - `const shared.DeliveryQueueCapacity = 5000`
  - `const shared.DeliveryMaxRetries = 3`
  - `const shared.DeliveryHTTPTimeout = 5 * time.Second`
  - `func workers.DeliveryWorker()` (wired in Task 7)
  - `func workers.deliverPending()` (test entry point)

- [ ] **Step 1: Add delivery constants**

Modify `shared/constants.go`, adding a new const block after the existing one:

```go
const (
	DeliveryRunningInterval = 5 * time.Second
	DeliveryQueueCapacity   = 5000
	DeliveryMaxRetries      = 3
	DeliveryHTTPTimeout     = 5 * time.Second
)
```

- [ ] **Step 2: Write the failing tests**

Create `workers/delivery_worker_test.go`:

```go
package workers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeliverPending(t *testing.T) {
	t.Run("delivers a pending message successfully and empties the pending file", func(t *testing.T) {
		// Arrange
		withTempWorkDir(t)
		initDeliveryDirectories()

		var receivedBody string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			receivedBody = string(body)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		path := filepath.Join(DeliveryPendingDir, "orders-2026-08-16.json")
		assert.NoError(t, appendDeliveryLine(path, deliveryLine{
			MessageID:  "msg-1",
			Origin:     "orders",
			ConsumerID: "c1",
			WebhookURL: server.URL,
			Payload:    `{"id":"msg-1"}`,
		}))

		// Act
		deliverPending()

		// Assert
		assert.Equal(t, `{"id":"msg-1"}`, receivedBody)

		pendingFiles, err := os.ReadDir(DeliveryPendingDir)
		assert.NoError(t, err)
		for _, f := range pendingFiles {
			info, statErr := os.Stat(filepath.Join(DeliveryPendingDir, f.Name()))
			assert.NoError(t, statErr)
			assert.Equal(t, int64(0), info.Size())
		}

		failedFiles, err := os.ReadDir(DeliveryFailedDir)
		assert.NoError(t, err)
		assert.Empty(t, failedFiles)
	})

	t.Run("retries on failure and writes to the failed file after exhausting retries", func(t *testing.T) {
		// Arrange
		withTempWorkDir(t)
		initDeliveryDirectories()

		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		path := filepath.Join(DeliveryPendingDir, "orders-2026-08-16.json")
		assert.NoError(t, appendDeliveryLine(path, deliveryLine{
			MessageID:  "msg-2",
			Origin:     "orders",
			ConsumerID: "c1",
			WebhookURL: server.URL,
			Payload:    `{"id":"msg-2"}`,
		}))

		// Act
		deliverPending()

		// Assert
		assert.Equal(t, 3, attempts)

		failedFiles, err := os.ReadDir(DeliveryFailedDir)
		assert.NoError(t, err)
		assert.Len(t, failedFiles, 1)

		content, err := os.ReadFile(filepath.Join(DeliveryFailedDir, failedFiles[0].Name()))
		assert.NoError(t, err)

		var failed deliveryLine
		assert.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(string(content))), &failed))
		assert.Equal(t, "msg-2", failed.MessageID)
	})
}
```

Note: the retry test sleeps through the real 1s + 2s backoff (~3s total) — that's expected, not a bug.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./workers/... -run TestDeliverPending -v`
Expected: FAIL — `undefined: deliverPending` (compile error)

- [ ] **Step 4: Write the implementation**

Create `workers/delivery_worker.go`:

```go
package workers

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/nathangds/altair/shared"
)

var deliveryHTTPClient = &http.Client{
	Timeout: shared.DeliveryHTTPTimeout,
}

var deliveryBackoffSteps = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

func DeliveryWorker() {
	initDeliveryDirectories()
	shared.Log.Info("[delivery] initialized, running every " + shared.DeliveryRunningInterval.String())

	for {
		shared.Log.Info("[delivery] delivering pending messages")
		deliverPending()
		time.Sleep(shared.DeliveryRunningInterval)
	}
}

func deliverPending() {
	files, err := os.ReadDir(DeliveryPendingDir)
	if err != nil {
		shared.Log.Error("failed to read delivery pending dir", zap.Error(err))
		return
	}

	lineChan := make(chan deliveryLine, shared.DeliveryQueueCapacity)
	wg := sync.WaitGroup{}

	for range shared.ConsumerWorkingPool {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for line := range lineChan {
				deliverOne(line)
			}
		}()
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		path := filepath.Join(DeliveryPendingDir, f.Name())
		lines, err := readAndTruncatePendingLines(path)
		if err != nil {
			shared.Log.Error("failed to read pending delivery file", zap.String("file", path), zap.Error(err))
			continue
		}
		for _, line := range lines {
			lineChan <- line
		}
	}

	close(lineChan)
	wg.Wait()
}

func deliverOne(line deliveryLine) {
	for attempt := 0; attempt < shared.DeliveryMaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(deliveryBackoffSteps[attempt-1])
		}

		req, err := http.NewRequest(http.MethodPost, line.WebhookURL, bytes.NewBufferString(line.Payload))
		if err != nil {
			shared.Log.Error("failed to build delivery request", zap.String("consumer_id", line.ConsumerID), zap.Error(err))
			line.Attempts = attempt + 1
			line.LastError = err.Error()
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := deliveryHTTPClient.Do(req)
		line.Attempts = attempt + 1
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return
			}
			line.LastError = "unexpected status code: " + resp.Status
		} else {
			line.LastError = err.Error()
		}
	}

	shared.Log.Error("delivery failed after max retries",
		zap.String("consumer_id", line.ConsumerID),
		zap.String("message_id", line.MessageID),
		zap.String("last_error", line.LastError),
	)

	line.FailedAt = time.Now().UTC().Format(time.RFC3339)
	if err := appendDeliveryLine(failedFilePath(line.Origin), line); err != nil {
		shared.Log.Error("failed to write failed delivery record", zap.Error(err))
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./workers/... -v`
Expected: PASS (all workers tests, ~3s slower due to the retry-backoff test)

- [ ] **Step 6: Commit**

```bash
git add shared/constants.go workers/delivery_worker.go workers/delivery_worker_test.go
git commit -m "feat: add delivery worker with bounded worker pool, retry, and failed-delivery file"
```

---

### Task 6: TTL sweeper for dead consumers

**Files:**
- Create: `workers/ttl_sweeper_worker.go`
- Modify: `shared/constants.go`
- Test: `workers/ttl_sweeper_worker_test.go`

**Interfaces:**
- Consumes: `shared.Consumers.ExpiredConsumers`, `shared.Consumers.Deactivate`, `shared.Consumers.ActiveConsumersForOrigin`, `shared.Consumers.Heartbeat`, `shared.ErrConsumerNotFound` (Tasks 1-2).
- Produces:
  - `var shared.ConsumerHeartbeatTTL = 30 * time.Second` (var, not const — overridable in tests)
  - `var shared.ConsumerTTLSweepInterval = 10 * time.Second`
  - `func workers.TTLSweeperWorker()` (wired in Task 7)
  - `func workers.sweepExpiredConsumers()` (test entry point)

- [ ] **Step 1: Add TTL constants as vars**

Modify `shared/constants.go`, adding near the existing `var ConsumerWorkingPool = getWorkingPool()` line:

```go
var (
	ConsumerHeartbeatTTL     = 30 * time.Second
	ConsumerTTLSweepInterval = 10 * time.Second
)
```

These are `var`, not `const` (unlike the other interval constants), specifically so tests can shrink `ConsumerHeartbeatTTL` instead of sleeping for the real 30s.

- [ ] **Step 2: Write the failing tests**

Create `workers/ttl_sweeper_worker_test.go`:

```go
package workers

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/nathangds/altair/shared"
)

func TestSweepExpiredConsumers(t *testing.T) {
	t.Run("deactivates a consumer whose heartbeat is older than the ttl", func(t *testing.T) {
		// Arrange
		originalTTL := shared.ConsumerHeartbeatTTL
		shared.ConsumerHeartbeatTTL = 50 * time.Millisecond
		defer func() { shared.ConsumerHeartbeatTTL = originalTTL }()

		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)

		stale, err := shared.Consumers.Register("orders", "https://example.com/stale")
		assert.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		// Act
		sweepExpiredConsumers()

		// Assert
		assert.Empty(t, shared.Consumers.ActiveConsumersForOrigin("orders"))
		err = shared.Consumers.Heartbeat(stale.ID)
		assert.ErrorIs(t, err, shared.ErrConsumerNotFound)
	})

	t.Run("leaves a consumer with a recent heartbeat active", func(t *testing.T) {
		// Arrange
		originalTTL := shared.ConsumerHeartbeatTTL
		shared.ConsumerHeartbeatTTL = time.Minute
		defer func() { shared.ConsumerHeartbeatTTL = originalTTL }()

		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)

		fresh, err := shared.Consumers.Register("orders", "https://example.com/fresh")
		assert.NoError(t, err)

		// Act
		sweepExpiredConsumers()

		// Assert
		active := shared.Consumers.ActiveConsumersForOrigin("orders")
		assert.Len(t, active, 1)
		assert.Equal(t, fresh.ID, active[0].ID)
	})
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./workers/... -run TestSweepExpiredConsumers -v`
Expected: FAIL — `undefined: sweepExpiredConsumers` (compile error)

- [ ] **Step 4: Write the implementation**

Create `workers/ttl_sweeper_worker.go`:

```go
package workers

import (
	"time"

	"go.uber.org/zap"

	"github.com/nathangds/altair/shared"
)

func TTLSweeperWorker() {
	shared.Log.Info("[ttl-sweeper] initialized")

	for {
		sweepExpiredConsumers()
		time.Sleep(shared.ConsumerTTLSweepInterval)
	}
}

func sweepExpiredConsumers() {
	if shared.Consumers == nil {
		return
	}

	expired := shared.Consumers.ExpiredConsumers(shared.ConsumerHeartbeatTTL)
	for _, c := range expired {
		if err := shared.Consumers.Deactivate(c.ID); err != nil {
			shared.Log.Error("failed to deactivate expired consumer", zap.String("consumer_id", c.ID), zap.Error(err))
			continue
		}
		shared.Log.Info("deactivated expired consumer", zap.String("consumer_id", c.ID), zap.String("origin", c.Origin))
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./workers/... -v`
Expected: PASS (all workers tests)

- [ ] **Step 6: Commit**

```bash
git add shared/constants.go workers/ttl_sweeper_worker.go workers/ttl_sweeper_worker_test.go
git commit -m "feat: add TTL sweeper worker to deactivate dead consumers"
```

---

### Task 7: Wire everything into main.go and document it

**Files:**
- Modify: `main.go`
- Modify: `AGENTS.md`

**Interfaces:**
- Consumes: `shared.InitConsumerStore`, `shared.Consumers.Close` (Tasks 1-2); `handlers.RegisterConsumerHandler`, `handlers.ConsumerHeartbeatHandler`, `handlers.UnregisterConsumerHandler` (Task 3); `workers.DeliveryWorker` (Task 5); `workers.TTLSweeperWorker` (Task 6); `workers.RemoveEmptyFilesWorker` (existing).
- Produces: a running process exposing `POST /consumers`, `POST /consumers/{id}/heartbeat`, `DELETE /consumers/{id}`, with delivery and TTL-sweep workers running.

- [ ] **Step 1: Wire consumer store init, routes, and workers in `main.go`**

Modify `main.go`. Replace:

```go
func main() {
	defer shared.Log.Sync()

	http.HandleFunc("POST /publish", handlers.PublishHandler)
	web.RegisterWebHandlers()
	go workers.ConsumerWorker()
	go workers.PurgeMessagesWorker()
	go workers.RemoveEmptyFilesWorker("messages/processed")
	go workers.RemoveEmptyFilesWorker("messages/ready")
	go workers.DeleteMakedFiles()
```

with:

```go
func main() {
	defer shared.Log.Sync()

	if err := shared.InitConsumerStore("data/altair.db"); err != nil {
		shared.Log.Fatal("failed to init consumer store", zap.Error(err))
	}
	defer shared.Consumers.Close()

	http.HandleFunc("POST /publish", handlers.PublishHandler)
	http.HandleFunc("POST /consumers", handlers.RegisterConsumerHandler)
	http.HandleFunc("POST /consumers/{id}/heartbeat", handlers.ConsumerHeartbeatHandler)
	http.HandleFunc("DELETE /consumers/{id}", handlers.UnregisterConsumerHandler)
	web.RegisterWebHandlers()
	go workers.ConsumerWorker()
	go workers.DeliveryWorker()
	go workers.TTLSweeperWorker()
	go workers.PurgeMessagesWorker()
	go workers.RemoveEmptyFilesWorker("messages/processed")
	go workers.RemoveEmptyFilesWorker("messages/ready")
	go workers.RemoveEmptyFilesWorker("deliveries/pending")
	go workers.RemoveEmptyFilesWorker("deliveries/failed")
	go workers.DeleteMakedFiles()
```

`zap` is already imported in `main.go` (used by `shared.Log.Fatal("Server error", zap.Error(err))` further down), so no new import is needed.

- [ ] **Step 2: Build and run the full test suite**

Run: `go build ./...`
Expected: builds with no errors.

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 3: Manual smoke test**

Run: `go run ./main.go` (in one terminal), then in another:

```bash
curl -s -X POST localhost:8080/consumers -d '{"origin":"orders","webhook_url":"https://httpbin.org/post"}'
# -> {"id":"<uuid>"}

curl -s -X POST localhost:8080/consumers/<uuid>/heartbeat
# -> 200 OK, empty body

curl -s -X POST localhost:8080/publish -d '{"origin":"orders","data":{"key":"value"}}'
# -> 200 OK, echoes the published message

# within ~5-10s, deliveries/pending/orders-<date>.json should appear then empty out,
# and the webhook (httpbin.org/post) should have received the message body.
```

Stop the server with Ctrl+C and confirm the log lines `"[delivery] initialized..."` and `"[ttl-sweeper] initialized"` appeared alongside the existing worker startup logs.

- [ ] **Step 4: Update `AGENTS.md`**

Modify the "Background workers" table in `AGENTS.md` (currently listing `ConsumerWorker`, `PurgeMessagesWorker`, `RemoveEmptyFilesWorker`, `DeleteMakedFiles`) to add two rows:

```markdown
| `DeliveryWorker` | 5s | Reads `deliveries/pending/`, POSTs each line to its consumer's webhook (retry 3x, backoff 1/2/4s), truncates file after reading; failures go to `deliveries/failed/` |
| `TTLSweeperWorker` | 10s | Deactivates consumers in SQLite + cache whose last heartbeat is older than `ConsumerHeartbeatTTL` (30s) |
```

Add to the "Storage layout" bullet list:

```markdown
- `deliveries/pending/{origin}-{date}.json` — one line per (message, consumer) awaiting webhook delivery
- `deliveries/failed/{origin}-{date}.json` — deliveries that exhausted retries, for manual inspection
- `data/altair.db` — SQLite: consumer registrations (`consumers` table)
```

Add a short "Consumer registration + delivery" subsection under "Architecture" documenting the three new endpoints (`POST /consumers`, `POST /consumers/{id}/heartbeat`, `DELETE /consumers/{id}`) and that delivery is pub/sub broadcast with no consumer groups, no auth, and no crash-safe redelivery for in-flight batches (same risk class as the existing `ready/` → `processed/` pipeline).

- [ ] **Step 5: Commit**

```bash
git add main.go AGENTS.md
git commit -m "feat: wire consumer registration and delivery workers into main"
```
