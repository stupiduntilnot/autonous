package context

// StandardAssembler combines system prompt, history, and user message
// into a single ordered message list.
type StandardAssembler struct{}

// Assemble builds the final message list: system + history + user.
func (a *StandardAssembler) Assemble(system string, history []Message, userMsg string) []Message {
	messages := make([]Message, 0, 1+len(history)+1)
	messages = append(messages, Message{Role: "system", Content: system})
	messages = append(messages, history...)
	messages = append(messages, Message{Role: "user", Content: userMsg})
	return messages
}
