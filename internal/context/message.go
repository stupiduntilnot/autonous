package context

// Message is a model-agnostic chat message used across the context pipeline.
type Message struct {
	Role    string
	Content string
}
