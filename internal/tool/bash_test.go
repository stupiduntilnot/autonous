package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBash_ExecuteSuccess(t *testing.T) {
	base := t.TempDir()
	policy, err := NewPolicy(base, "rm -rf")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	bashTool := NewBash(policy, base, 2*time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(BashInput{Command: "echo ok", Cwd: "."})
	res, execErr := bashTool.Execute(context.Background(), raw)
	if execErr != nil {
		t.Fatalf("exec err: %v", execErr)
	}
	if !res.OK || res.ExitCode != 0 {
		t.Fatalf("unexpected result ok=%v exit=%d stderr=%s", res.OK, res.ExitCode, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "ok" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
}

func TestBash_Denylist(t *testing.T) {
	base := t.TempDir()
	policy, err := NewPolicy(base, "rm -rf")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	bashTool := NewBash(policy, base, time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(BashInput{Command: "rm -rf /tmp/x"})
	_, execErr := bashTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected denylist error")
	}
	if !strings.Contains(execErr.Error(), "denied") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}

func TestBash_OutsideAllowlistCwd(t *testing.T) {
	base := t.TempDir()
	other := t.TempDir()
	policy, err := NewPolicy(base, "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	bashTool := NewBash(policy, base, time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(BashInput{Command: "echo x", Cwd: other})
	_, execErr := bashTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected allowlist error")
	}
	if !strings.Contains(execErr.Error(), "outside allowlist") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}

func TestBash_ValidateInput(t *testing.T) {
	policy, err := NewPolicy("/", "")
	if err != nil {
		t.Fatalf("policy err: %v", err)
	}
	bashTool := NewBash(policy, "/", time.Second, Limits{MaxLines: 100, MaxBytes: 4096})

	raw, _ := json.Marshal(BashInput{Command: ""})
	_, execErr := bashTool.Execute(context.Background(), raw)
	if execErr == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(execErr.Error(), "bash.command is required") {
		t.Fatalf("unexpected err: %v", execErr)
	}
}
