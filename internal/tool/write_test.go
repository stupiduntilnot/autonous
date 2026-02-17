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

func TestWrite_OverwriteAndAppend(t *testing.T) {
	base := t.TempDir()
	policy, err := NewPolicy(base, "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	writeTool := NewWrite(policy, base, 2*time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(WriteInput{Path: "a.txt", Content: "hello\n", Append: false})
	if _, execErr := writeTool.Execute(context.Background(), raw); execErr != nil {
		t.Fatalf("overwrite err: %v", execErr)
	}
	got, err := os.ReadFile(filepath.Join(base, "a.txt"))
	if err != nil {
		t.Fatalf("read err: %v", err)
	}
	if string(got) != "hello\n" {
		t.Fatalf("unexpected content after overwrite: %q", string(got))
	}

	raw, _ = json.Marshal(WriteInput{Path: "a.txt", Content: "world\n", Append: true})
	if _, execErr := writeTool.Execute(context.Background(), raw); execErr != nil {
		t.Fatalf("append err: %v", execErr)
	}
	got, err = os.ReadFile(filepath.Join(base, "a.txt"))
	if err != nil {
		t.Fatalf("read err: %v", err)
	}
	if string(got) != "hello\nworld\n" {
		t.Fatalf("unexpected content after append: %q", string(got))
	}
}

func TestWrite_OutsideAllowlist(t *testing.T) {
	base := t.TempDir()
	other := filepath.Join(t.TempDir(), "x.txt")
	policy, err := NewPolicy(base, "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	writeTool := NewWrite(policy, base, time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(WriteInput{Path: other, Content: "x"})
	_, execErr := writeTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected allowlist error")
	}
	if !strings.Contains(execErr.Error(), "outside allowlist") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}

func TestWrite_ValidateInput(t *testing.T) {
	policy, err := NewPolicy("/", "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	writeTool := NewWrite(policy, "/", time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(WriteInput{Path: "", Content: "x"})
	_, execErr := writeTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(execErr.Error(), "write.path is required") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}
