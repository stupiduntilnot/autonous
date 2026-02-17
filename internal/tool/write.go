package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

type WriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Append  bool   `json:"append"`
}

type Write struct {
	Policy  *Policy
	BaseDir string
	Timeout time.Duration
	Limits  Limits
}

func NewWrite(policy *Policy, baseDir string, timeout time.Duration, limits Limits) *Write {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if limits.MaxLines <= 0 {
		limits.MaxLines = 2000
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 51200
	}
	return &Write{
		Policy:  policy,
		BaseDir: baseDir,
		Timeout: timeout,
		Limits:  limits,
	}
}

func (t *Write) Name() string { return "write" }

func (t *Write) Validate(raw json.RawMessage) error {
	var in WriteInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("invalid write input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return fmt.Errorf("write.path is required")
	}
	return nil
}

func (t *Write) Execute(ctx context.Context, raw json.RawMessage) (Result, error) {
	if err := t.Validate(raw); err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}
	var in WriteInput
	_ = json.Unmarshal(raw, &in)

	resolved, err := t.Policy.ResolveAllowedPath(in.Path, t.BaseDir)
	if err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}

	toolCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	args := []string{}
	if in.Append {
		args = append(args, "-a")
	}
	args = append(args, resolved)
	cmd := exec.CommandContext(toolCtx, "tee", args...)
	cmd.Stdin = strings.NewReader(in.Content)
	cmd.Stdout = io.Discard

	var stderr bytes.Buffer
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

	errText, truncLinesErr, truncBytesErr := ApplyOutputLimits(stderr.String(), t.Limits)
	result := Result{
		OK:             runErr == nil,
		ExitCode:       exitCode,
		Stdout:         "",
		Stderr:         errText,
		TruncatedLines: truncLinesErr,
		TruncatedBytes: truncBytesErr,
	}
	if runErr != nil {
		return result, fmt.Errorf("write execution failed: %w", runErr)
	}
	return result, nil
}
