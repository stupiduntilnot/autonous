package context

import "database/sql"

// SQLiteProvider reads conversation history from a SQLite database.
type SQLiteProvider struct {
	DB *sql.DB
}

// GetHistory returns the most recent `limit` messages for the given chat,
// ordered chronologically (oldest first).
func (p *SQLiteProvider) GetHistory(chatID int64, limit int) ([]Message, error) {
	rows, err := p.DB.Query(
		"SELECT role, text FROM history WHERE chat_id = ? ORDER BY id DESC LIMIT ?",
		chatID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Message
	for rows.Next() {
		var role, text string
		if err := rows.Scan(&role, &text); err != nil {
			continue
		}
		mapped := "user"
		if role == "assistant" {
			mapped = "assistant"
		}
		results = append(results, Message{Role: mapped, Content: text})
	}

	// Reverse to chronological order.
	for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
		results[i], results[j] = results[j], results[i]
	}
	return results, nil
}
