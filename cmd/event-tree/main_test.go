package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/autonous/autonous/internal/db"
)

// testDB creates a temporary SQLite database with schema initialized.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InitSchema(database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// seedUnifiedTree inserts a realistic unified event tree and returns the root (supervisor) event ID.
//
// Tree structure:
//
//	process.started (supervisor)       id=1
//	├── revision.promoted              id=2
//	├── worker.spawned                 id=3
//	├── process.started (worker)       id=4
//	│   ├── agent.started              id=5
//	│   │   ├── turn.started           id=6
//	│   │   ├── turn.completed         id=7
//	│   │   └── reply.sent             id=8
//	│   └── agent.completed            id=9
//	└── worker.exited                  id=10
func seedUnifiedTree(t *testing.T, database *sql.DB) int64 {
	t.Helper()

	supID, _ := db.LogEvent(database, nil, db.EventProcessStarted, map[string]any{"role": "supervisor", "pid": 100})
	db.LogEvent(database, &supID, db.EventRevisionPromoted, map[string]any{"revision": "abc123"})
	db.LogEvent(database, &supID, db.EventWorkerSpawned, map[string]any{"pid": 101})
	workerID, _ := db.LogEvent(database, &supID, db.EventProcessStarted, map[string]any{"role": "worker", "pid": 101})
	agentID, _ := db.LogEvent(database, &workerID, db.EventAgentStarted, map[string]any{"chat_id": 123, "task_id": 5})
	db.LogEvent(database, &agentID, db.EventTurnStarted, map[string]any{"model_name": "gpt-4o"})
	db.LogEvent(database, &agentID, db.EventTurnCompleted, map[string]any{"latency_ms": 1820, "input_tokens": 42, "output_tokens": 7})
	db.LogEvent(database, &agentID, db.EventReplySent, map[string]any{"chat_id": 123})
	db.LogEvent(database, &workerID, db.EventAgentCompleted, map[string]any{"task_id": 5})
	db.LogEvent(database, &supID, db.EventWorkerExited, map[string]any{"exit_code": 0})

	return supID
}

func TestLatestSupervisorRoot(t *testing.T) {
	database := testDB(t)
	supID := seedUnifiedTree(t, database)

	got, err := latestSupervisorRoot(database)
	if err != nil {
		t.Fatal(err)
	}
	if got != supID {
		t.Errorf("expected root id=%d, got %d", supID, got)
	}
}

func TestLatestSupervisorRoot_NoEvents(t *testing.T) {
	database := testDB(t)
	_, err := latestSupervisorRoot(database)
	if err == nil {
		t.Fatal("expected error for empty database")
	}
}

func TestQuerySubtree(t *testing.T) {
	database := testDB(t)
	supID := seedUnifiedTree(t, database)

	events, err := querySubtree(database, supID)
	if err != nil {
		t.Fatal(err)
	}
	// We inserted 10 events total.
	if len(events) != 10 {
		t.Errorf("expected 10 events, got %d", len(events))
	}
}

func TestQuerySubtree_SubtreeFromWorker(t *testing.T) {
	database := testDB(t)
	seedUnifiedTree(t, database)

	// Worker is id=4, should have 6 events: worker, agent.started, 3 turns, agent.completed
	events, err := querySubtree(database, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 {
		t.Errorf("expected 6 events in worker subtree, got %d", len(events))
		for _, ev := range events {
			t.Logf("  id=%d type=%s parent=%v", ev.ID, ev.EventType, ev.ParentID)
		}
	}
}

func TestBuildTree(t *testing.T) {
	database := testDB(t)
	supID := seedUnifiedTree(t, database)

	events, _ := querySubtree(database, supID)
	root := buildTree(events, supID)

	if root == nil {
		t.Fatal("root is nil")
	}
	if root.EventType != "process.started" {
		t.Errorf("expected process.started, got %s", root.EventType)
	}

	// Root should have 4 direct children: revision.promoted, worker.spawned, process.started(worker), worker.exited
	if len(root.Children) != 4 {
		t.Errorf("expected 4 root children, got %d", len(root.Children))
		for _, c := range root.Children {
			t.Logf("  child: id=%d type=%s", c.ID, c.EventType)
		}
	}

	// Find the worker process.started node.
	var workerNode *Event
	for _, c := range root.Children {
		if c.EventType == "process.started" {
			workerNode = c
			break
		}
	}
	if workerNode == nil {
		t.Fatal("worker process.started not found")
	}

	// Worker should have 2 children: agent.started, agent.completed
	if len(workerNode.Children) != 2 {
		t.Errorf("expected 2 worker children, got %d", len(workerNode.Children))
	}

	// agent.started should have 3 children: turn.started, turn.completed, reply.sent
	var agentNode *Event
	for _, c := range workerNode.Children {
		if c.EventType == "agent.started" {
			agentNode = c
			break
		}
	}
	if agentNode == nil {
		t.Fatal("agent.started not found")
	}
	if len(agentNode.Children) != 3 {
		t.Errorf("expected 3 agent children, got %d", len(agentNode.Children))
	}
}

func TestFormatEvent(t *testing.T) {
	ev := &Event{
		ID:        42,
		Timestamp: 1739781001,
		EventType: "agent.started",
		Payload:   sql.NullString{String: `{"chat_id":123,"task_id":5}`, Valid: true},
	}

	line := formatEvent(ev, false)
	if !strings.Contains(line, "[42]") {
		t.Errorf("expected [42] in output: %s", line)
	}
	if !strings.Contains(line, "agent.started") {
		t.Errorf("expected agent.started in output: %s", line)
	}
	if !strings.Contains(line, "chat_id=123") {
		t.Errorf("expected chat_id=123 in output: %s", line)
	}
	if !strings.Contains(line, "task_id=5") {
		t.Errorf("expected task_id=5 in output: %s", line)
	}
}

func TestFormatEvent_NoPayload(t *testing.T) {
	ev := &Event{
		ID:        42,
		Timestamp: 1739781001,
		EventType: "agent.started",
		Payload:   sql.NullString{String: `{"chat_id":123}`, Valid: true},
	}

	line := formatEvent(ev, true)
	if strings.Contains(line, "chat_id") {
		t.Errorf("expected no payload in output: %s", line)
	}
}

func TestFormatEvent_NullPayload(t *testing.T) {
	ev := &Event{
		ID:        1,
		Timestamp: 1739781001,
		EventType: "worker.exited",
		Payload:   sql.NullString{Valid: false},
	}

	line := formatEvent(ev, false)
	if !strings.Contains(line, "worker.exited") {
		t.Errorf("expected worker.exited in output: %s", line)
	}
}

func TestFormatValue_LongString(t *testing.T) {
	long := strings.Repeat("a", 100)
	v := formatValue(long)
	if len(v) > 100 {
		// Quoted and truncated.
		if !strings.Contains(v, "...") {
			t.Errorf("expected truncation: %s", v)
		}
	}
}

func TestFormatValue_Integer(t *testing.T) {
	v := formatValue(float64(42))
	if v != "42" {
		t.Errorf("expected 42, got %s", v)
	}
}

// captureStdout runs fn and captures its stdout output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestPrintTree_Full(t *testing.T) {
	database := testDB(t)
	supID := seedUnifiedTree(t, database)

	events, _ := querySubtree(database, supID)
	root := buildTree(events, supID)

	output := captureStdout(t, func() {
		printTree(root, "", true, 1, 0, false)
	})

	// Should contain all event types.
	for _, want := range []string{
		"process.started", "revision.promoted", "worker.spawned",
		"agent.started", "turn.started", "turn.completed",
		"reply.sent", "agent.completed", "worker.exited",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output:\n%s", want, output)
		}
	}

	// Should contain tree-drawing characters.
	if !strings.Contains(output, "├──") && !strings.Contains(output, "└──") {
		t.Errorf("expected tree characters in output:\n%s", output)
	}
}

func TestPrintTree_DepthLimit(t *testing.T) {
	database := testDB(t)
	supID := seedUnifiedTree(t, database)

	events, _ := querySubtree(database, supID)
	root := buildTree(events, supID)

	output := captureStdout(t, func() {
		printTree(root, "", true, 1, 2, false)
	})

	// Should contain depth=1 and depth=2 events.
	if !strings.Contains(output, "process.started") {
		t.Errorf("expected process.started at depth 1")
	}
	if !strings.Contains(output, "worker.spawned") {
		t.Errorf("expected worker.spawned at depth 2")
	}

	// Should NOT contain depth=3 events like agent.started.
	if strings.Contains(output, "agent.started") {
		t.Errorf("agent.started should be truncated at -L 2:\n%s", output)
	}

	// Should show [...] for truncated nodes with children.
	if !strings.Contains(output, "[...]") {
		t.Errorf("expected [...] indicator for truncated nodes:\n%s", output)
	}
}

func TestPrintTree_DepthLimit1(t *testing.T) {
	database := testDB(t)
	supID := seedUnifiedTree(t, database)

	events, _ := querySubtree(database, supID)
	root := buildTree(events, supID)

	output := captureStdout(t, func() {
		printTree(root, "", true, 1, 1, false)
	})

	lines := strings.Split(strings.TrimSpace(output), "\n")
	// Depth 1: just root + [...] indicator.
	if len(lines) != 2 {
		t.Errorf("expected 2 lines (root + [...]), got %d:\n%s", len(lines), output)
	}
}

func TestPrintJSON(t *testing.T) {
	database := testDB(t)
	supID := seedUnifiedTree(t, database)

	events, _ := querySubtree(database, supID)
	root := buildTree(events, supID)

	output := captureStdout(t, func() {
		printJSON(root, 0, false)
	})

	var je jsonEvent
	if err := json.Unmarshal([]byte(output), &je); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, output)
	}

	if je.EventType != "process.started" {
		t.Errorf("expected process.started, got %s", je.EventType)
	}
	if len(je.Children) != 4 {
		t.Errorf("expected 4 children, got %d", len(je.Children))
	}
}

func TestPrintJSON_DepthLimit(t *testing.T) {
	database := testDB(t)
	supID := seedUnifiedTree(t, database)

	events, _ := querySubtree(database, supID)
	root := buildTree(events, supID)

	output := captureStdout(t, func() {
		printJSON(root, 2, false)
	})

	var je jsonEvent
	if err := json.Unmarshal([]byte(output), &je); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Root children should exist (depth=2).
	if len(je.Children) == 0 {
		t.Error("expected children at depth 2")
	}

	// But children of children should be truncated.
	for _, child := range je.Children {
		if len(child.Children) > 0 {
			t.Errorf("expected no grandchildren at -L 2, but %s (id=%d) has %d",
				child.EventType, child.ID, len(child.Children))
		}
	}
}

func TestPrintJSON_NoPayload(t *testing.T) {
	database := testDB(t)
	supID := seedUnifiedTree(t, database)

	events, _ := querySubtree(database, supID)
	root := buildTree(events, supID)

	output := captureStdout(t, func() {
		printJSON(root, 0, true)
	})

	// Should not contain "role" or "pid" which are payload fields.
	if strings.Contains(output, `"role"`) {
		t.Errorf("expected no payload in output:\n%s", output)
	}
}

func TestMultipleSupervisors_PicksLatest(t *testing.T) {
	database := testDB(t)

	// First supervisor.
	db.LogEvent(database, nil, db.EventProcessStarted, map[string]any{"role": "supervisor", "pid": 100})

	// Second supervisor (should be picked).
	sup2, _ := db.LogEvent(database, nil, db.EventProcessStarted, map[string]any{"role": "supervisor", "pid": 200})

	got, err := latestSupervisorRoot(database)
	if err != nil {
		t.Fatal(err)
	}
	if got != sup2 {
		t.Errorf("expected latest supervisor id=%d, got %d", sup2, got)
	}
}

func TestSubtreeFromSpecificID(t *testing.T) {
	database := testDB(t)
	seedUnifiedTree(t, database)

	// Query subtree from agent.started (id=5).
	events, err := querySubtree(database, 5)
	if err != nil {
		t.Fatal(err)
	}

	// agent.started + 3 turn-level children = 4 events.
	if len(events) != 4 {
		t.Errorf("expected 4 events in agent subtree, got %d", len(events))
		for _, ev := range events {
			t.Logf("  id=%d type=%s", ev.ID, ev.EventType)
		}
	}

	root := buildTree(events, 5)
	if root == nil {
		t.Fatal("agent root is nil")
	}
	if root.EventType != "agent.started" {
		t.Errorf("expected agent.started, got %s", root.EventType)
	}
	if len(root.Children) != 3 {
		t.Errorf("expected 3 children, got %d", len(root.Children))
	}
}

