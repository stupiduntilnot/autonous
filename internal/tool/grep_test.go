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

func TestGrep_ExecuteSuccess(t *testing.T) {
	base := t.TempDir()
	mustWrite := func(rel string, content string) {
		t.Helper()
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write failed: %v", err)
		}
	}
	mustWrite("a.txt", "hello\nneedle-one\nbye\n")
	mustWrite(filepath.Join("sub", "b.txt"), "needle-two\nother\n")
	mustWrite("c.log", "needle-log\n")

	policy, err := NewPolicy(base, "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	grepTool := NewGrep(policy, base, 2*time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(GrepInput{Path: ".", Pattern: "needle", Glob: "*.txt"})
	res, execErr := grepTool.Execute(context.Background(), raw)
	if execErr != nil {
		t.Fatalf("exec err: %v", execErr)
	}
	if !res.OK || res.ExitCode != 0 {
		t.Fatalf("unexpected result ok=%v exit=%d stderr=%s", res.OK, res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "a.txt") || !strings.Contains(res.Stdout, "b.txt") {
		t.Fatalf("expected txt matches in stdout, got: %s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "c.log") {
		t.Fatalf("unexpected log match in stdout: %s", res.Stdout)
	}
}

func TestGrep_OutsideAllowlist(t *testing.T) {
	base := t.TempDir()
	other := t.TempDir()
	policy, err := NewPolicy(base, "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	grepTool := NewGrep(policy, base, time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(GrepInput{Path: other, Pattern: "x"})
	_, execErr := grepTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected allowlist error")
	}
	if !strings.Contains(execErr.Error(), "outside allowlist") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}

func TestGrep_ValidateInput(t *testing.T) {
	policy, err := NewPolicy("/", "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	grepTool := NewGrep(policy, "/", time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(GrepInput{Path: ".", Pattern: "", Limit: -1})
	_, execErr := grepTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(execErr.Error(), "grep.pattern is required") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}
