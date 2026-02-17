package context

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		chat_id INTEGER NOT NULL,
		role TEXT NOT NULL,
		text TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func insertHistory(t *testing.T, db *sql.DB, chatID int64, role, text string) {
	t.Helper()
	_, err := db.Exec("INSERT INTO history (chat_id, role, text) VALUES (?, ?, ?)", chatID, role, text)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteProvider_GetHistory(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertHistory(t, db, 1, "user", "hello")
	insertHistory(t, db, 1, "assistant", "hi there")
	insertHistory(t, db, 1, "user", "how are you")
	insertHistory(t, db, 2, "user", "other chat")

	p := &SQLiteProvider{DB: db}

	msgs, err := p.GetHistory(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	// Should be chronological order.
	if msgs[0].Content != "hello" {
		t.Errorf("expected first message 'hello', got %q", msgs[0].Content)
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "hi there" {
		t.Errorf("unexpected second message: %+v", msgs[1])
	}
	if msgs[2].Content != "how are you" {
		t.Errorf("expected third message 'how are you', got %q", msgs[2].Content)
	}
}

func TestSQLiteProvider_GetHistory_Limit(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertHistory(t, db, 1, "user", "msg1")
	insertHistory(t, db, 1, "assistant", "msg2")
	insertHistory(t, db, 1, "user", "msg3")
	insertHistory(t, db, 1, "assistant", "msg4")

	p := &SQLiteProvider{DB: db}

	msgs, err := p.GetHistory(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	// Should return the last 2 in chronological order.
	if msgs[0].Content != "msg3" {
		t.Errorf("expected 'msg3', got %q", msgs[0].Content)
	}
	if msgs[1].Content != "msg4" {
		t.Errorf("expected 'msg4', got %q", msgs[1].Content)
	}
}

func TestSQLiteProvider_GetHistory_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	p := &SQLiteProvider{DB: db}

	msgs, err := p.GetHistory(999, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}
