package db

import (
	"database/sql"
	"encoding/json"
	"testing"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := InitSchema(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInitSchema(t *testing.T) {
	db := testDB(t)

	// Verify all three tables exist by querying sqlite_master.
	tables := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name IN ('events','inbox','history')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables[name] = true
	}

	for _, want := range []string{"events", "inbox", "history"} {
		if !tables[want] {
			t.Errorf("table %q not created", want)
		}
	}
}

func TestLogEvent_Basic(t *testing.T) {
	db := testDB(t)

	id1, err := LogEvent(db, nil, EventProcessStarted, map[string]any{"role": "supervisor", "pid": 123})
	if err != nil {
		t.Fatal(err)
	}
	if id1 <= 0 {
		t.Errorf("expected positive id, got %d", id1)
	}

	id2, err := LogEvent(db, nil, EventAgentStarted, map[string]any{"chat_id": 456})
	if err != nil {
		t.Fatal(err)
	}
	if id2 <= id1 {
		t.Errorf("expected id2 > id1, got %d <= %d", id2, id1)
	}

	// Verify timestamp is non-zero.
	var ts int64
	err = db.QueryRow(`SELECT timestamp FROM events WHERE id = ?`, id1).Scan(&ts)
	if err != nil {
		t.Fatal(err)
	}
	if ts == 0 {
		t.Error("expected non-zero timestamp")
	}

	// Verify payload is valid JSON.
	var payloadStr string
	err = db.QueryRow(`SELECT payload FROM events WHERE id = ?`, id1).Scan(&payloadStr)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		t.Fatalf("invalid payload JSON: %v", err)
	}
	if payload["role"] != "supervisor" {
		t.Errorf("expected role=supervisor, got %v", payload["role"])
	}
}

func TestLogEvent_WithParent(t *testing.T) {
	db := testDB(t)

	parentID, err := LogEvent(db, nil, EventAgentStarted, map[string]any{"chat_id": 1})
	if err != nil {
		t.Fatal(err)
	}

	childID, err := LogEvent(db, &parentID, EventTurnStarted, map[string]any{"model_name": "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}

	// Verify parent_id is stored correctly.
	var storedParent int64
	err = db.QueryRow(`SELECT parent_id FROM events WHERE id = ?`, childID).Scan(&storedParent)
	if err != nil {
		t.Fatal(err)
	}
	if storedParent != parentID {
		t.Errorf("expected parent_id=%d, got %d", parentID, storedParent)
	}

	// Verify root event has NULL parent_id.
	var nullParent sql.NullInt64
	err = db.QueryRow(`SELECT parent_id FROM events WHERE id = ?`, parentID).Scan(&nullParent)
	if err != nil {
		t.Fatal(err)
	}
	if nullParent.Valid {
		t.Errorf("expected NULL parent_id for root event, got %d", nullParent.Int64)
	}
}

func TestCurrentGoodRev(t *testing.T) {
	db := testDB(t)

	// No events yet -> empty string.
	rev, err := CurrentGoodRev(db)
	if err != nil {
		t.Fatal(err)
	}
	if rev != "" {
		t.Errorf("expected empty rev, got %q", rev)
	}

	// Insert a revision.promoted event.
	_, err = LogEvent(db, nil, EventRevisionPromoted, map[string]any{"revision": "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	rev, err = CurrentGoodRev(db)
	if err != nil {
		t.Fatal(err)
	}
	if rev != "abc123" {
		t.Errorf("expected abc123, got %q", rev)
	}

	// Insert a newer one -> should return the latest.
	_, err = LogEvent(db, nil, EventRevisionPromoted, map[string]any{"revision": "def456"})
	if err != nil {
		t.Fatal(err)
	}
	rev, err = CurrentGoodRev(db)
	if err != nil {
		t.Fatal(err)
	}
	if rev != "def456" {
		t.Errorf("expected def456, got %q", rev)
	}
}

func TestNextWorkerSeq(t *testing.T) {
	db := testDB(t)

	supID, err := LogEvent(db, nil, EventProcessStarted, map[string]any{"role": "supervisor"})
	if err != nil {
		t.Fatal(err)
	}

	// No workers spawned yet -> seq=1.
	seq, err := NextWorkerSeq(db, supID)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Errorf("expected 1, got %d", seq)
	}

	// Spawn a worker -> seq=2.
	_, err = LogEvent(db, &supID, EventWorkerSpawned, map[string]any{"pid": 100})
	if err != nil {
		t.Fatal(err)
	}
	seq, err = NextWorkerSeq(db, supID)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 2 {
		t.Errorf("expected 2, got %d", seq)
	}
}

func TestDeriveOffset(t *testing.T) {
	db := testDB(t)

	// Empty inbox -> 0.
	offset, err := DeriveOffset(db)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 0 {
		t.Errorf("expected 0, got %d", offset)
	}

	// Insert inbox rows.
	db.Exec(`INSERT INTO inbox (update_id, chat_id, text, message_date) VALUES (100, 1, 'a', 0)`)
	db.Exec(`INSERT INTO inbox (update_id, chat_id, text, message_date) VALUES (200, 1, 'b', 0)`)

	offset, err = DeriveOffset(db)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 201 {
		t.Errorf("expected 201, got %d", offset)
	}
}

func TestLogEvent_NilPayload(t *testing.T) {
	db := testDB(t)

	id, err := LogEvent(db, nil, EventAgentCompleted, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Errorf("expected positive id, got %d", id)
	}

	// Verify payload is NULL.
	var payload sql.NullString
	err = db.QueryRow(`SELECT payload FROM events WHERE id = ?`, id).Scan(&payload)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Valid {
		t.Errorf("expected NULL payload, got %q", payload.String)
	}
}
