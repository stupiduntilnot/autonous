package tool

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRunner_RunOne_Success(t *testing.T) {
	base := t.TempDir()
	p, err := NewPolicy(base, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	if err := reg.Register(NewLS(p, base, 2*time.Second, Limits{MaxLines: 100, MaxBytes: 4096})); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(reg)
	raw, _ := json.Marshal(map[string]any{"path": "."})
	res, err := r.RunOne(context.Background(), Call{Name: "ls", Arguments: raw})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected ok result: %+v", res)
	}
}

func TestRunner_RunOne_UnknownTool(t *testing.T) {
	r := NewRunner(NewRegistry())
	_, err := r.RunOne(context.Background(), Call{Name: "unknown", Arguments: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected unknown tool error")
	}
}
