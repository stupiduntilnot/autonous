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

func TestRead_ExecuteSuccess(t *testing.T) {
	base := t.TempDir()
	p := filepath.Join(base, "a.txt")
	if err := os.WriteFile(p, []byte("line1\nline2\nline3\nline4\n"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	policy, err := NewPolicy(base, "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	readTool := NewRead(policy, base, 2*time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(ReadInput{Path: "a.txt", Offset: 1, Limit: 2})
	res, execErr := readTool.Execute(context.Background(), raw)
	if execErr != nil {
		t.Fatalf("exec err: %v", execErr)
	}
	if !res.OK || res.ExitCode != 0 {
		t.Fatalf("unexpected result ok=%v exit=%d stderr=%s", res.OK, res.ExitCode, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "line2\nline3" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
}

func TestRead_OutsideAllowlist(t *testing.T) {
	base := t.TempDir()
	other := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(other, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	policy, err := NewPolicy(base, "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	readTool := NewRead(policy, base, time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(ReadInput{Path: other, Offset: 0, Limit: 1})
	_, execErr := readTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected allowlist error")
	}
	if !strings.Contains(execErr.Error(), "outside allowlist") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}

func TestRead_ValidateInput(t *testing.T) {
	policy, err := NewPolicy("/", "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	readTool := NewRead(policy, "/", time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(ReadInput{Path: "a.txt", Offset: -1, Limit: 0})
	_, execErr := readTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(execErr.Error(), "read.offset must be >= 0") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}
