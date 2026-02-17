package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type EditInput struct {
	Path    string `json:"path"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
	All     bool   `json:"all"`
}

type Edit struct {
	Policy  *Policy
	BaseDir string
	Timeout time.Duration
	Limits  Limits
}

func NewEdit(policy *Policy, baseDir string, timeout time.Duration, limits Limits) *Edit {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if limits.MaxLines <= 0 {
		limits.MaxLines = 2000
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 51200
	}
	return &Edit{
		Policy:  policy,
		BaseDir: baseDir,
		Timeout: timeout,
		Limits:  limits,
	}
}

func (t *Edit) Name() string { return "edit" }

func (t *Edit) Validate(raw json.RawMessage) error {
	var in EditInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("invalid edit input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return fmt.Errorf("edit.path is required")
	}
	if in.OldText == "" {
		return fmt.Errorf("edit.old_text is required")
	}
	return nil
}

func (t *Edit) Execute(ctx context.Context, raw json.RawMessage) (Result, error) {
	if err := t.Validate(raw); err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}
	var in EditInput
	_ = json.Unmarshal(raw, &in)

	resolved, err := t.Policy.ResolveAllowedPath(in.Path, t.BaseDir)
	if err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}

	toolCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	script := "s|" + escapeSed(in.OldText) + "|" + escapeSed(in.NewText) + "|"
	if in.All {
		script += "g"
	}
	cmd := exec.CommandContext(toolCtx, "sed", "-i.bak", "-e", script, resolved)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}

	_ = os.Remove(resolved + ".bak")

	outText, truncLinesOut, truncBytesOut := ApplyOutputLimits(stdout.String(), t.Limits)
	errText, truncLinesErr, truncBytesErr := ApplyOutputLimits(stderr.String(), t.Limits)
	result := Result{
		OK:             runErr == nil,
		ExitCode:       exitCode,
		Stdout:         outText,
		Stderr:         errText,
		TruncatedLines: truncLinesOut || truncLinesErr,
		TruncatedBytes: truncBytesOut || truncBytesErr,
	}
	if runErr != nil {
		return result, fmt.Errorf("edit execution failed: %w", runErr)
	}
	return result, nil
}

func escapeSed(s string) string {
	replacer := strings.NewReplacer(
		`\\`, `\\\\`,
		`|`, `\|`,
		`&`, `\&`,
		"\n", `\n`,
	)
	return replacer.Replace(s)
}
