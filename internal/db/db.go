package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// Event type constants — infrastructure events
const (
	EventProcessStarted    = "process.started"
	EventWorkerSpawned     = "worker.spawned"
	EventWorkerExited      = "worker.exited"
	EventRevisionPromoted  = "revision.promoted"
	EventCrashLoopDetected = "crash_loop.detected"
	EventRollbackAttempted = "rollback.attempted"
)

// Event type constants — agent execution events
const (
	EventAgentStarted        = "agent.started"
	EventAgentCompleted      = "agent.completed"
	EventAgentFailed         = "agent.failed"
	EventTurnStarted         = "turn.started"
	EventTurnCompleted       = "turn.completed"
	EventToolCallStarted     = "tool_call.started"
	EventToolCallDone        = "tool_call.completed"
	EventToolCallFailed      = "tool_call.failed"
	EventReplySent           = "reply.sent"
	EventContextAssembled    = "context.assembled"
	EventControlLimitReached = "control.limit_reached"
	EventRetryScheduled      = "retry.scheduled"
	EventRetryExhausted      = "retry.exhausted"
	EventCircuitOpened       = "circuit.opened"
	EventCircuitHalfOpen     = "circuit.half_open"
	EventCircuitClosed       = "circuit.closed"
	EventProgressStalled     = "progress.stalled"
)

// OpenDB opens (or creates) a SQLite database at the given path, ensuring
// that the parent directory exists.
func OpenDB(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create db directory %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open db at %s: %w", path, err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping db at %s: %w", path, err)
	}

	return db, nil
}

// InitSchema creates all tables: events, inbox, history, artifacts.
func InitSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY,
			timestamp INTEGER NOT NULL DEFAULT (unixepoch()),
			parent_id INTEGER,
			event_type TEXT NOT NULL,
			payload TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_events_parent_id ON events(parent_id);

		CREATE TABLE IF NOT EXISTS inbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			update_id INTEGER NOT NULL UNIQUE,
			chat_id INTEGER NOT NULL,
			text TEXT NOT NULL,
			message_date INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'queued',
			attempts INTEGER NOT NULL DEFAULT 0,
			locked_at INTEGER,
			error TEXT,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		);
		CREATE INDEX IF NOT EXISTS idx_inbox_status_id ON inbox(status, id);

		CREATE TABLE IF NOT EXISTS history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			text TEXT NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		);

		CREATE TABLE IF NOT EXISTS artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tx_id TEXT NOT NULL UNIQUE,
			base_tx_id TEXT,
			bin_path TEXT NOT NULL,
			sha256 TEXT,
			git_revision TEXT,
			build_started_at INTEGER,
			build_finished_at INTEGER,
			test_summary TEXT,
			self_check_summary TEXT,
			approval_chat_id INTEGER,
			approval_message_id INTEGER,
			deploy_started_at INTEGER,
			deploy_finished_at INTEGER,
			status TEXT NOT NULL,
			last_error TEXT,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		);
		CREATE INDEX IF NOT EXISTS idx_artifacts_status_updated_at ON artifacts(status, updated_at);
		CREATE INDEX IF NOT EXISTS idx_artifacts_base_tx_id ON artifacts(base_tx_id);
	`)
	return err
}

// CurrentGoodRev returns the revision from the most recent revision.promoted event,
// or "" if none found.
func CurrentGoodRev(database *sql.DB) (string, error) {
	var payload string
	err := database.QueryRow(
		`SELECT payload FROM events WHERE event_type = ? ORDER BY id DESC LIMIT 1`,
		EventRevisionPromoted,
	).Scan(&payload)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return "", err
	}
	rev, _ := m["revision"].(string)
	return rev, nil
}

// NextWorkerSeq returns the next worker sequence number by counting
// worker.spawned events under the given supervisor event ID.
func NextWorkerSeq(database *sql.DB, supervisorEventID int64) (int, error) {
	var count int
	err := database.QueryRow(
		`SELECT COUNT(*) FROM events WHERE parent_id = ? AND event_type = ?`,
		supervisorEventID, EventWorkerSpawned,
	).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count + 1, nil
}

// DeriveOffset returns the next Telegram polling offset derived from the inbox table.
// Returns 0 if inbox is empty.
func DeriveOffset(database *sql.DB) (int64, error) {
	var offset int64
	err := database.QueryRow(`SELECT COALESCE(MAX(update_id) + 1, 0) FROM inbox`).Scan(&offset)
	return offset, err
}

// LogEvent inserts an event into the events table and returns its auto-generated id.
// parentID may be nil for root events. payload is serialized to JSON; nil payload stores NULL.
func LogEvent(db *sql.DB, parentID *int64, eventType string, payload map[string]any) (int64, error) {
	var payloadJSON any
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, fmt.Errorf("marshal event payload: %w", err)
		}
		payloadJSON = string(data)
	}

	res, err := db.Exec(
		`INSERT INTO events (parent_id, event_type, payload) VALUES (?, ?, ?)`,
		parentID, eventType, payloadJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("insert event %s: %w", eventType, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get event id: %w", err)
	}
	return id, nil
}
