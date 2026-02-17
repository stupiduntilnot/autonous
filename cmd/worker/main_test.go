package main

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stupiduntilnot/autonous/internal/config"
	ctxpkg "github.com/stupiduntilnot/autonous/internal/context"
	"github.com/stupiduntilnot/autonous/internal/control"
	"github.com/stupiduntilnot/autonous/internal/db"
	"github.com/stupiduntilnot/autonous/internal/dummy"
)

func testWorkerDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.OpenDB(t.TempDir() + "/worker.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InitSchema(database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestRetryReady(t *testing.T) {
	p := control.Policy{MaxRetries: 3}
	now := time.Now().Unix()
	if retryReady(1, now, now, p) {
		t.Fatal("attempt=1 should not be ready immediately (1s backoff)")
	}
	if !retryReady(1, now-2, now, p) {
		t.Fatal("attempt=1 should be ready after backoff")
	}
	if retryReady(4, now-100, now, p) {
		t.Fatal("attempt > max retries should never be ready")
	}
}

func TestClaimNextTask_RespectsRetryWindow(t *testing.T) {
	database := testWorkerDB(t)
	p := control.Policy{MaxRetries: 3}

	_, err := database.Exec(
		`INSERT INTO inbox (update_id, chat_id, text, message_date, status, attempts, updated_at)
		 VALUES (1001, 1, 'failed-task', 0, 'failed', 1, ?)`,
		time.Now().Unix(),
	)
	if err != nil {
		t.Fatal(err)
	}

	task, err := claimNextTask(database, p)
	if err != nil {
		t.Fatal(err)
	}
	if task != nil {
		t.Fatal("expected nil task because backoff not elapsed")
	}

	_, err = database.Exec("UPDATE inbox SET updated_at = ? WHERE update_id = 1001", time.Now().Unix()-2)
	if err != nil {
		t.Fatal(err)
	}
	task, err = claimNextTask(database, p)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("expected a runnable failed task after backoff")
	}
	if task.Attempts != 2 {
		t.Fatalf("expected attempts incremented to 2, got %d", task.Attempts)
	}
}

func TestProcessTask_RecordsLimitEvent(t *testing.T) {
	database := testWorkerDB(t)
	commander, err := dummy.NewCommander("ok", "ok")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := dummy.NewProvider("dummy", "ok")
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkerConfig{
		OpenAIModel:   "dummy",
		SystemPrompt:  "sys",
		HistoryWindow: 12,
	}
	task := &queueTask{
		ID:       1,
		ChatID:   1,
		UpdateID: 1,
		Text:     "hello",
	}
	ctxProvider := &ctxpkg.SQLiteProvider{DB: database}
	ctxCompressor := &ctxpkg.SimpleCompressor{MaxMessages: 12}
	ctxAssembler := &ctxpkg.StandardAssembler{}
	policy := control.Policy{
		MaxTurns:    0,
		MaxWallTime: 120 * time.Second,
		MaxRetries:  3,
	}

	agentEventID, err := db.LogEvent(database, nil, db.EventAgentStarted, map[string]any{"task_id": 1})
	if err != nil {
		t.Fatal(err)
	}

	err = processTask(database, commander, provider, cfg, task, agentEventID, ctxProvider, ctxCompressor, ctxAssembler, policy)
	if err == nil {
		t.Fatal("expected limit error")
	}

	var cnt int
	if qerr := database.QueryRow(
		"SELECT COUNT(*) FROM events WHERE event_type = ?",
		db.EventControlLimitReached,
	).Scan(&cnt); qerr != nil {
		t.Fatal(qerr)
	}
	if cnt != 1 {
		t.Fatalf("expected 1 control.limit_reached event, got %d", cnt)
	}
}
