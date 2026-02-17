package tool

import (
	"context"
	"encoding/json"
)

// Tool is the common abstraction for all atomic tools.
type Tool interface {
	Name() string
	Validate(raw json.RawMessage) error
	Execute(ctx context.Context, raw json.RawMessage) (Result, error)
}
