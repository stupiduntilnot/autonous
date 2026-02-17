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

func TestLS_ExecuteSuccess(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := NewPolicy(base, "")
	if err != nil {
		t.Fatal(err)
	}
	ls := NewLS(p, base, 2*time.Second, Limits{MaxLines: 100, MaxBytes: 4096})
	input, _ := json.Marshal(LSInput{Path: ".", Recursive: false})

	res, execErr := ls.Execute(context.Background(), input)
	if execErr != nil {
		t.Fatalf("execute failed: %v", execErr)
	}
	if !res.OK || res.ExitCode != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.Stdout, "a.txt") || !strings.Contains(res.Stdout, "b.txt") {
		t.Fatalf("unexpected stdout: %s", res.Stdout)
	}
}

func TestLS_RejectOutsideAllowlist(t *testing.T) {
	base := t.TempDir()
	p, err := NewPolicy(base, "")
	if err != nil {
		t.Fatal(err)
	}
	ls := NewLS(p, base, 2*time.Second, Limits{MaxLines: 100, MaxBytes: 4096})
	input, _ := json.Marshal(LSInput{Path: "/etc"})

	_, execErr := ls.Execute(context.Background(), input)
	if execErr == nil {
		t.Fatal("expected allowlist error")
	}
}

func TestLS_OutputTruncation(t *testing.T) {
	base := t.TempDir()
	for i := 0; i < 10; i++ {
		name := filepath.Join(base, "f"+toDecimal(int64(i)))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p, err := NewPolicy(base, "")
	if err != nil {
		t.Fatal(err)
	}
	ls := NewLS(p, base, 2*time.Second, Limits{MaxLines: 2, MaxBytes: 20})
	input, _ := json.Marshal(LSInput{Path: ".", Recursive: false})

	res, execErr := ls.Execute(context.Background(), input)
	if execErr != nil {
		t.Fatalf("execute failed: %v", execErr)
	}
	if !res.TruncatedLines && !res.TruncatedBytes {
		t.Fatalf("expected truncation flags, got %+v", res)
	}
}
