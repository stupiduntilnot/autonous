package tool

import (
	"context"
	"encoding/json"
	"testing"
)

type mockTool struct {
	name string
}

func (m *mockTool) Name() string { return m.name }

func (m *mockTool) Validate(raw json.RawMessage) error { return nil }

func (m *mockTool) Execute(ctx context.Context, raw json.RawMessage) (Result, error) {
	return Result{OK: true, ExitCode: 0}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	mt := &mockTool{name: "ls"}
	if err := r.Register(mt); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	got, ok := r.Get("ls")
	if !ok {
		t.Fatal("expected tool ls")
	}
	if got.Name() != "ls" {
		t.Fatalf("expected ls, got %s", got.Name())
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&mockTool{name: "ls"}); err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	if err := r.Register(&mockTool{name: "ls"}); err == nil {
		t.Fatal("expected duplicate registration error")
	}
}

func TestRegistry_MustListSorted(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&mockTool{name: "grep"})
	_ = r.Register(&mockTool{name: "ls"})
	_ = r.Register(&mockTool{name: "find"})

	list := r.MustList()
	if len(list) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(list))
	}
	if list[0].Name != "find" || list[1].Name != "grep" || list[2].Name != "ls" {
		t.Fatalf("unexpected order: %+v", list)
	}
}

func TestRegistry_RegisterInvalid(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatal("expected nil tool error")
	}
	if err := r.Register(&mockTool{name: "   "}); err == nil {
		t.Fatal("expected empty-name error")
	}
}
