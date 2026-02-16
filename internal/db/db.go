package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
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

// InitSupervisorSchema creates the tables used by the supervisor process.
func InitSupervisorSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS supervisor_revisions (
			revision TEXT PRIMARY KEY,
			build_ok INTEGER NOT NULL DEFAULT 0,
			health_ok INTEGER NOT NULL DEFAULT 0,
			promoted_at INTEGER,
			failure_reason TEXT
		);
		CREATE TABLE IF NOT EXISTS supervisor_state (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`)
	return err
}

// InitWorkerSchema creates the tables used by the worker process.
func InitWorkerSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			text TEXT NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		);
		CREATE TABLE IF NOT EXISTS kv (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
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
		CREATE TABLE IF NOT EXISTS task_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL,
			chat_id INTEGER NOT NULL,
			update_id INTEGER,
			phase TEXT NOT NULL,
			status TEXT NOT NULL,
			message TEXT,
			error TEXT,
			run_id TEXT NOT NULL,
			worker_instance_id TEXT NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		);
		CREATE INDEX IF NOT EXISTS idx_task_audit_task_id ON task_audit(task_id, id);
		CREATE INDEX IF NOT EXISTS idx_task_audit_phase ON task_audit(phase, created_at);
	`)
	return err
}
