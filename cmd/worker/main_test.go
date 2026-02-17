package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	cmdpkg "github.com/stupiduntilnot/autonous/internal/commander"
	"github.com/stupiduntilnot/autonous/internal/config"
	ctxpkg "github.com/stupiduntilnot/autonous/internal/context"
	"github.com/stupiduntilnot/autonous/internal/control"
	"github.com/stupiduntilnot/autonous/internal/db"
	"github.com/stupiduntilnot/autonous/internal/dummy"
	modelpkg "github.com/stupiduntilnot/autonous/internal/model"
	toolpkg "github.com/stupiduntilnot/autonous/internal/tool"
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

	reg := toolpkg.NewRegistry()
	err = processTask(database, commander, provider, cfg, task, agentEventID, ctxProvider, ctxCompressor, ctxAssembler, policy, reg)
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

func TestProcessTask_RecordsTokenLimitEvent(t *testing.T) {
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
	task := &queueTask{ID: 2, ChatID: 1, UpdateID: 2, Text: "hello"}
	ctxProvider := &ctxpkg.SQLiteProvider{DB: database}
	ctxCompressor := &ctxpkg.SimpleCompressor{MaxMessages: 12}
	ctxAssembler := &ctxpkg.StandardAssembler{}
	policy := control.Policy{
		MaxTurns:    1,
		MaxWallTime: 120 * time.Second,
		MaxTokens:   1,
		MaxRetries:  3,
	}

	agentEventID, err := db.LogEvent(database, nil, db.EventAgentStarted, map[string]any{"task_id": 2})
	if err != nil {
		t.Fatal(err)
	}
	reg := toolpkg.NewRegistry()
	err = processTask(database, commander, provider, cfg, task, agentEventID, ctxProvider, ctxCompressor, ctxAssembler, policy, reg)
	if err == nil {
		t.Fatal("expected token limit error")
	}

	var cnt int
	if qerr := database.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = ?", db.EventControlLimitReached).Scan(&cnt); qerr != nil {
		t.Fatal(qerr)
	}
	if cnt != 1 {
		t.Fatalf("expected 1 control.limit_reached event, got %d", cnt)
	}
}

func TestProgressStalled_UsesRecentFingerprints(t *testing.T) {
	database := testWorkerDB(t)
	taskID := int64(42)
	fp := "task=42|hist=0|comp=0|err=provider_api|reply="

	payload, _ := json.Marshal(map[string]any{
		"task_id":           taskID,
		"state_fingerprint": fp,
	})
	if _, err := database.Exec(
		"INSERT INTO events (event_type, payload) VALUES (?, ?), (?, ?)",
		db.EventRetryScheduled, string(payload),
		db.EventRetryScheduled, string(payload),
	); err != nil {
		t.Fatal(err)
	}

	if !progressStalled(database, taskID, fp, 3) {
		t.Fatal("expected progress stalled for repeated fingerprints")
	}
}

type seqProvider struct {
	resps []modelpkg.CompletionResponse
	idx   int
}

func (s *seqProvider) ChatCompletion(messages []ctxpkg.Message) (modelpkg.CompletionResponse, error) {
	if s.idx >= len(s.resps) {
		return modelpkg.CompletionResponse{Content: "{\"tool_calls\":[],\"final_answer\":\"done\"}"}, nil
	}
	i := s.idx
	s.idx++
	return s.resps[i], nil
}

type captureCommander struct {
	last string
}

func (c *captureCommander) GetUpdates(offset int64, timeout int) ([]cmdpkg.Update, error) {
	return nil, nil
}

func (c *captureCommander) SendMessage(chatID int64, text string) error {
	c.last = text
	return nil
}

func TestProcessTask_ToolLoopLS(t *testing.T) {
	database := testWorkerDB(t)
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	commander := &captureCommander{}
	provider := &seqProvider{
		resps: []modelpkg.CompletionResponse{
			{Content: "{\"tool_calls\":[{\"name\":\"ls\",\"arguments\":{\"path\":\".\"}}],\"final_answer\":\"\"}", InputTokens: 1, OutputTokens: 1},
			{Content: "{\"tool_calls\":[],\"final_answer\":\"tool done\"}", InputTokens: 1, OutputTokens: 1},
		},
	}
	cfg := &config.WorkerConfig{
		OpenAIModel:   "dummy",
		SystemPrompt:  "sys",
		HistoryWindow: 12,
	}
	task := &queueTask{ID: 3, ChatID: 1, UpdateID: 3, Text: "list files"}
	ctxProvider := &ctxpkg.SQLiteProvider{DB: database}
	ctxCompressor := &ctxpkg.SimpleCompressor{MaxMessages: 12}
	ctxAssembler := &ctxpkg.StandardAssembler{}
	policy := control.Policy{MaxTurns: 2, MaxWallTime: 120 * time.Second, MaxTokens: 1000, MaxRetries: 3}
	agentEventID, err := db.LogEvent(database, nil, db.EventAgentStarted, map[string]any{"task_id": 3})
	if err != nil {
		t.Fatal(err)
	}

	p, err := toolpkg.NewPolicy(base, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := toolpkg.NewRegistry()
	if err := reg.Register(toolpkg.NewLS(p, base, 2*time.Second, toolpkg.Limits{MaxLines: 100, MaxBytes: 4096})); err != nil {
		t.Fatal(err)
	}

	if err := processTask(database, commander, provider, cfg, task, agentEventID, ctxProvider, ctxCompressor, ctxAssembler, policy, reg); err != nil {
		t.Fatalf("processTask failed: %v", err)
	}
	if commander.last != "tool done" {
		t.Fatalf("unexpected final reply: %q", commander.last)
	}
	var toolCnt int
	if qerr := database.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = ?", db.EventToolCallDone).Scan(&toolCnt); qerr != nil {
		t.Fatal(qerr)
	}
	if toolCnt != 1 {
		t.Fatalf("expected 1 tool_call.completed, got %d", toolCnt)
	}
}
