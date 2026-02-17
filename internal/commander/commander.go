package commander

// Commander is the instruction source abstraction used by worker.
type Commander interface {
	GetUpdates(offset int64, timeout int) ([]Update, error)
	SendMessage(chatID int64, text string) error
}

// Update represents an incoming command/update.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

// Message represents a source message.
type Message struct {
	Chat Chat    `json:"chat"`
	Text *string `json:"text,omitempty"`
	Date int64   `json:"date"`
}

// Chat identifies a conversation.
type Chat struct {
	ID int64 `json:"id"`
}
