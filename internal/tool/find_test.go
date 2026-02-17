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

func TestFind_ExecuteSuccess(t *testing.T) {
	base := t.TempDir()
	mustWrite := func(rel string) {
		t.Helper()
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write failed: %v", err)
		}
	}
	mustWrite("a.go")
	mustWrite("b.txt")
	mustWrite(filepath.Join("sub", "c.go"))

	policy, err := NewPolicy(base, "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	findTool := NewFind(policy, base, 2*time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(FindInput{Path: ".", NamePattern: "*.go"})
	res, execErr := findTool.Execute(context.Background(), raw)
	if execErr != nil {
		t.Fatalf("exec err: %v", execErr)
	}
	if !res.OK || res.ExitCode != 0 {
		t.Fatalf("unexpected result ok=%v exit=%d stderr=%s", res.OK, res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "a.go") {
		t.Fatalf("expected a.go in stdout, got: %s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, filepath.Join("sub", "c.go")) {
		t.Fatalf("expected sub/c.go in stdout, got: %s", res.Stdout)
	}
}

func TestFind_OutsideAllowlist(t *testing.T) {
	base := t.TempDir()
	other := t.TempDir()
	policy, err := NewPolicy(base, "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	findTool := NewFind(policy, base, time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(FindInput{Path: other, NamePattern: "*.go"})
	_, execErr := findTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected allowlist error")
	}
	if !strings.Contains(execErr.Error(), "outside allowlist") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}

func TestFind_ValidateInput(t *testing.T) {
	policy, err := NewPolicy("/", "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	findTool := NewFind(policy, "/", time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(FindInput{Path: ".", Limit: -1})
	_, execErr := findTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(execErr.Error(), "find.limit must be >= 0") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}
