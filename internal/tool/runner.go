package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Call represents one tool invocation request.
type Call struct {
	Name      string
	Arguments json.RawMessage
}

// Runner executes registered tools.
type Runner struct {
	registry *Registry
}

func NewRunner(registry *Registry) *Runner {
	return &Runner{registry: registry}
}

func (r *Runner) RunOne(ctx context.Context, call Call) (Result, error) {
	if r == nil || r.registry == nil {
		return Result{}, fmt.Errorf("tool runner is not initialized")
	}
	toolName := strings.TrimSpace(call.Name)
	if toolName == "" {
		return Result{}, fmt.Errorf("validation: empty tool name")
	}
	t, ok := r.registry.Get(toolName)
	if !ok {
		return Result{}, fmt.Errorf("validation: unknown tool: %s", toolName)
	}
	if err := t.Validate(call.Arguments); err != nil {
		return Result{}, err
	}
	return t.Execute(ctx, call.Arguments)
}
