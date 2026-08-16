package shared

import (
	"database/sql"
	"errors"
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
