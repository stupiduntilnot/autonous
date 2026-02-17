package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type LSInput struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
	Limit     int    `json:"limit"`
}

type LS struct {
	Policy  *Policy
	BaseDir string
	Timeout time.Duration
	Limits  Limits
}

func NewLS(policy *Policy, baseDir string, timeout time.Duration, limits Limits) *LS {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if limits.MaxLines <= 0 {
		limits.MaxLines = 2000
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 51200
	}
	return &LS{
		Policy:  policy,
		BaseDir: baseDir,
		Timeout: timeout,
		Limits:  limits,
	}
}

func (t *LS) Name() string { return "ls" }

func (t *LS) Validate(raw json.RawMessage) error {
	var in LSInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("invalid ls input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return fmt.Errorf("ls.path is required")
	}
	if in.Limit < 0 {
		return fmt.Errorf("ls.limit must be >= 0")
	}
	return nil
}

func (t *LS) Execute(ctx context.Context, raw json.RawMessage) (Result, error) {
	if err := t.Validate(raw); err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}
	var in LSInput
	_ = json.Unmarshal(raw, &in)

	resolved, err := t.Policy.ResolveAllowedPath(in.Path, t.BaseDir)
	if err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}

	toolCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	args := []string{"-1"}
	if in.Recursive {
		args = append(args, "-R")
	}
	args = append(args, resolved)

	cmd := exec.CommandContext(toolCtx, "ls", args...)
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
		return result, fmt.Errorf("ls execution failed: %w", runErr)
	}
	return result, nil
}
