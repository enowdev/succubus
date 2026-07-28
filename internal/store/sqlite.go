package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store owns the SQLite database. Writes go through a single connection
// (writeDB, MaxOpenConns=1) so they serialize in-process instead of bouncing
// off SQLITE_BUSY; reads use a pooled connection.
//
// Cross-process exclusion still relies on WAL + busy_timeout, because hooks and
// the CLI run as separate processes from the daemon.
type Store struct {
	writeDB *sql.DB
	readDB  *sql.DB
	path    string

	// notify fans out committed events to SSE subscribers.
	mu   sync.RWMutex
	subs map[int]chan Event
	next int

	// hooks delivers selected events to outbound webhooks.
	hooks *webhookSender
}

// DefaultPath is ~/.succubus/succubus.db.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".succubus")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "succubus.db"), nil
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// _txlock=immediate makes BEGIN acquire the write lock up front, which is
	// what we want for claim transactions: fail fast rather than upgrade-deadlock.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"

	w, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)

	r, err := sql.Open("sqlite", dsn)
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)

	s := &Store{
		writeDB: w, readDB: r, path: path,
		subs:  map[int]chan Event{},
		hooks: newWebhookSender(),
	}
	if _, err := w.Exec(schemaSQL); err != nil {
		w.Close()
		r.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	s.writeDB.Close()
	return s.readDB.Close()
}

func (s *Store) Path() string { return s.path }

func now() int64 { return time.Now().UnixMilli() }

// ---- event bus -------------------------------------------------------------

// Subscribe returns a channel of events plus a cancel func. The channel is
// buffered; a subscriber that falls behind drops events rather than blocking
// the writer (the client recovers via Last-Event-ID replay).
func (s *Store) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	s.mu.Lock()
	id := s.next
	s.next++
	s.subs[id] = ch
	s.mu.Unlock()

	return ch, func() {
		s.mu.Lock()
		if c, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(c)
		}
		s.mu.Unlock()
	}
}

func (s *Store) publish(ev Event) {
	s.mu.RLock()
	for _, ch := range s.subs {
		select {
		case ch <- ev:
		default: // slow subscriber: drop, it will resync on reconnect
		}
	}
	s.mu.RUnlock()

	// Outbound delivery is fire-and-forget, and deliberately after the lock:
	// a webhook endpoint that hangs must not stall the event bus.
	s.deliver(ev)
}

// emit writes an event row and fans it out. It takes an executor so it can run
// inside a transaction; the fan-out happens after the caller commits.
func (s *Store) emit(x execer, ev *Event) error {
	if ev.CreatedAt == 0 {
		ev.CreatedAt = now()
	}
	payload := "{}"
	if ev.Payload != nil {
		b, err := json.Marshal(ev.Payload)
		if err != nil {
			return err
		}
		payload = string(b)
	}
	res, err := x.Exec(`INSERT INTO events(project_id,type,agent_id,agent_name,subject_id,payload_json,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		ev.ProjectID, ev.Type, nz(ev.AgentID), nz(ev.AgentName), nz(ev.SubjectID), payload, ev.CreatedAt)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	ev.ID = id
	return nil
}

// Emit records an event outside any transaction and publishes it immediately.
func (s *Store) Emit(ev Event) {
	e := ev
	if err := s.emit(s.writeDB, &e); err == nil {
		s.publish(e)
	}
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// nz converts "" to NULL so UNIQUE/nullable columns behave as expected.
func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func ns(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}

func ni(v sql.NullInt64) int64 {
	if v.Valid {
		return v.Int64
	}
	return 0
}
