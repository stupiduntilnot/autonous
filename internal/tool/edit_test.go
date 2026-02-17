package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEdit_ReplaceFirstAndAll(t *testing.T) {
	base := t.TempDir()
	p := filepath.Join(base, "a.txt")
	if err := os.WriteFile(p, []byte("hello hello\n"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	policy, err := NewPolicy(base, "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	editTool := NewEdit(policy, base, 2*time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(EditInput{Path: "a.txt", OldText: "hello", NewText: "hi", All: false})
	if _, execErr := editTool.Execute(context.Background(), raw); execErr != nil {
		t.Fatalf("edit first err: %v", execErr)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read err: %v", err)
	}
	if string(got) != "hi hello\n" {
		t.Fatalf("unexpected content after first replace: %q", string(got))
	}

	raw, _ = json.Marshal(EditInput{Path: "a.txt", OldText: "hello", NewText: "ok", All: true})
	if _, execErr := editTool.Execute(context.Background(), raw); execErr != nil {
		t.Fatalf("edit all err: %v", execErr)
	}
	got, err = os.ReadFile(p)
	if err != nil {
		t.Fatalf("read err: %v", err)
	}
	if string(got) != "hi ok\n" {
		t.Fatalf("unexpected content after all replace: %q", string(got))
	}
}

func TestEdit_OutsideAllowlist(t *testing.T) {
	base := t.TempDir()
	other := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	policy, err := NewPolicy(base, "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	editTool := NewEdit(policy, base, time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(EditInput{Path: other, OldText: "x", NewText: "y"})
	_, execErr := editTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected allowlist error")
	}
	if !strings.Contains(execErr.Error(), "outside allowlist") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}

func TestEdit_ValidateInput(t *testing.T) {
	policy, err := NewPolicy("/", "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	editTool := NewEdit(policy, "/", time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(EditInput{Path: "a.txt", OldText: ""})
	_, execErr := editTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(execErr.Error(), "edit.old_text is required") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}
