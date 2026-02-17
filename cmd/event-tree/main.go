package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Event represents a row from the events table.
type Event struct {
	ID        int64
	Timestamp int64
	ParentID  sql.NullInt64
	EventType string
	Payload   sql.NullString
	Children  []*Event
}

func main() {
	var (
		dbPath    string
		eventID   int64
		maxDepth  int
		jsonOut   bool
		noPayload bool
	)

	flag.StringVar(&dbPath, "db", envOrDefault("TG_DB_PATH", "./autonous.db"), "SQLite database path")
	flag.Int64Var(&eventID, "id", 0, "show subtree of a specific event ID")
	flag.IntVar(&maxDepth, "L", 0, "limit display depth (0 = unlimited)")
	flag.BoolVar(&jsonOut, "json", false, "output JSON format")
	flag.BoolVar(&noPayload, "no-payload", false, "hide payload details")
	flag.Parse()

	db, err := sql.Open("sqlite3", dbPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	// Determine root event ID.
	rootID := eventID
	if rootID == 0 {
		rootID, err = latestSupervisorRoot(db)
		if err != nil {
			log.Fatalf("find supervisor root: %v", err)
		}
	}

	// Query the full subtree using recursive CTE.
	events, err := querySubtree(db, rootID)
	if err != nil {
		log.Fatalf("query subtree: %v", err)
	}

	// Build in-memory tree.
	root := buildTree(events, rootID)
	if root == nil {
		log.Fatal("root event not found")
	}

	// Output.
	if jsonOut {
		printJSON(root, maxDepth, noPayload)
	} else {
		printTree(root, "", true, 1, maxDepth, noPayload)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// latestSupervisorRoot finds the most recent process.started event with role=supervisor.
func latestSupervisorRoot(db *sql.DB) (int64, error) {
	var id int64
	err := db.QueryRow(
		`SELECT id FROM events WHERE event_type = 'process.started'
		 AND json_extract(payload, '$.role') = 'supervisor'
		 ORDER BY id DESC LIMIT 1`,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("no supervisor process.started event found")
	}
	return id, err
}

// querySubtree returns all events in the subtree rooted at rootID using a recursive CTE.
func querySubtree(db *sql.DB, rootID int64) ([]*Event, error) {
	rows, err := db.Query(`
		WITH RECURSIVE subtree(id) AS (
			SELECT id FROM events WHERE id = ?
			UNION ALL
			SELECT e.id FROM events e JOIN subtree s ON e.parent_id = s.id
		)
		SELECT e.id, e.timestamp, e.parent_id, e.event_type, e.payload
		FROM events e
		WHERE e.id IN (SELECT id FROM subtree)
		ORDER BY e.id ASC
	`, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		ev := &Event{}
		if err := rows.Scan(&ev.ID, &ev.Timestamp, &ev.ParentID, &ev.EventType, &ev.Payload); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// buildTree organizes a flat list of events into a tree rooted at rootID.
func buildTree(events []*Event, rootID int64) *Event {
	byID := make(map[int64]*Event, len(events))
	for _, ev := range events {
		byID[ev.ID] = ev
	}

	for _, ev := range events {
		if ev.ParentID.Valid && ev.ParentID.Int64 != ev.ID {
			if parent, ok := byID[ev.ParentID.Int64]; ok {
				parent.Children = append(parent.Children, ev)
			}
		}
	}

	// Sort children by ID for stable output.
	for _, ev := range events {
		sort.Slice(ev.Children, func(i, j int) bool {
			return ev.Children[i].ID < ev.Children[j].ID
		})
	}

	return byID[rootID]
}

// printTree renders the event tree using box-drawing characters.
func printTree(ev *Event, prefix string, isLast bool, depth, maxDepth int, noPayload bool) {
	// Print current node.
	connector := "├── "
	if isLast {
		connector = "└── "
	}
	line := formatEvent(ev, noPayload)
	if depth == 1 {
		fmt.Println(line)
	} else {
		fmt.Println(prefix + connector + line)
	}

	// Check depth limit.
	if maxDepth > 0 && depth >= maxDepth {
		if len(ev.Children) > 0 {
			// Indicate truncated children.
			childPrefix := prefix
			if depth > 1 {
				if isLast {
					childPrefix += "    "
				} else {
					childPrefix += "│   "
				}
			}
			fmt.Println(childPrefix + "└── [...]")
		}
		return
	}

	// Print children.
	childPrefix := prefix
	if depth > 1 {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}
	for i, child := range ev.Children {
		isLastChild := i == len(ev.Children)-1
		printTree(child, childPrefix, isLastChild, depth+1, maxDepth, noPayload)
	}
}

// formatEvent formats a single event line: [id] timestamp  event_type  key=value ...
func formatEvent(ev *Event, noPayload bool) string {
	ts := time.Unix(ev.Timestamp, 0).UTC().Format("2006-01-02 15:04:05")
	line := fmt.Sprintf("[%d] %s  %s", ev.ID, ts, ev.EventType)

	if !noPayload && ev.Payload.Valid && ev.Payload.String != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(ev.Payload.String), &m); err == nil {
			// Sort keys for stable output.
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				v := formatValue(m[k])
				line += fmt.Sprintf("  %s=%s", k, v)
			}
		}
	}

	return line
}

// formatValue converts a payload value to a display string, truncating long text.
func formatValue(v any) string {
	switch val := v.(type) {
	case string:
		if len(val) > 80 {
			return fmt.Sprintf("%q", val[:80]+"...")
		}
		return fmt.Sprintf("%v", val)
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// JSON output types.
type jsonEvent struct {
	ID        int64       `json:"id"`
	Timestamp int64       `json:"timestamp"`
	EventType string      `json:"event_type"`
	Payload   any         `json:"payload,omitempty"`
	Children  []jsonEvent `json:"children,omitempty"`
}

func toJSONEvent(ev *Event, depth, maxDepth int, noPayload bool) jsonEvent {
	je := jsonEvent{
		ID:        ev.ID,
		Timestamp: ev.Timestamp,
		EventType: ev.EventType,
	}

	if !noPayload && ev.Payload.Valid && ev.Payload.String != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(ev.Payload.String), &m); err == nil {
			je.Payload = m
		}
	}

	if maxDepth > 0 && depth >= maxDepth {
		return je
	}

	for _, child := range ev.Children {
		je.Children = append(je.Children, toJSONEvent(child, depth+1, maxDepth, noPayload))
	}
	return je
}

func printJSON(root *Event, maxDepth int, noPayload bool) {
	je := toJSONEvent(root, 1, maxDepth, noPayload)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(je); err != nil {
		log.Fatalf("encode json: %v", err)
	}
}

