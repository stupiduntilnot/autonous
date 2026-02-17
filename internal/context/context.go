package context

// Provider retrieves conversation history from a persistent store.
type Provider interface {
	GetHistory(chatID int64, limit int) ([]Message, error)
}

// Compressor reduces a list of messages to fit within constraints.
type Compressor interface {
	Compress(messages []Message) []Message
}

// Assembler combines system prompt, history, and user message into a final message list.
type Assembler interface {
	Assemble(system string, history []Message, userMsg string) []Message
}
