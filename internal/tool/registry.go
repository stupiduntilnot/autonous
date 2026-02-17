package tool

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Meta is lightweight metadata for a registered tool.
type Meta struct {
	Name string
}

// Registry stores tools by unique name.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{
		tools: map[string]Tool{},
	}
}

func (r *Registry) Register(t Tool) error {
	if t == nil {
		return fmt.Errorf("tool is nil")
	}
	name := strings.TrimSpace(t.Name())
	if name == "" {
		return fmt.Errorf("tool name is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}
	r.tools[name] = t
	return nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) MustList() []Meta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Meta, 0, len(names))
	for _, name := range names {
		out = append(out, Meta{Name: name})
	}
	return out
}
