package model

import ctxpkg "github.com/stupiduntilnot/autonous/internal/context"

// CompletionResponse is the common response model for model providers.
type CompletionResponse struct {
	Content      string
	InputTokens  int
	OutputTokens int
}

// Provider is the model provider abstraction used by worker.
type Provider interface {
	ChatCompletion(messages []ctxpkg.Message) (CompletionResponse, error)
}
