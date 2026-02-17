package context

// SimpleCompressor keeps only the last MaxMessages messages.
type SimpleCompressor struct {
	MaxMessages int
}

// Compress truncates messages to the most recent MaxMessages entries.
func (c *SimpleCompressor) Compress(messages []Message) []Message {
	if c.MaxMessages <= 0 || len(messages) <= c.MaxMessages {
		return messages
	}
	return messages[len(messages)-c.MaxMessages:]
}
