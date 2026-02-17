package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type GrepInput struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
	Glob    string `json:"glob"`
	Limit   int    `json:"limit"`
}

type Grep struct {
	Policy  *Policy
	BaseDir string
	Timeout time.Duration
	Limits  Limits
}

func NewGrep(policy *Policy, baseDir string, timeout time.Duration, limits Limits) *Grep {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if limits.MaxLines <= 0 {
		limits.MaxLines = 2000
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 51200
	}
	return &Grep{
		Policy:  policy,
		BaseDir: baseDir,
		Timeout: timeout,
		Limits:  limits,
	}
}

func (t *Grep) Name() string { return "grep" }

func (t *Grep) Validate(raw json.RawMessage) error {
	var in GrepInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("invalid grep input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return fmt.Errorf("grep.path is required")
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return fmt.Errorf("grep.pattern is required")
	}
	if in.Limit < 0 {
		return fmt.Errorf("grep.limit must be >= 0")
	}
	return nil
}

func (t *Grep) Execute(ctx context.Context, raw json.RawMessage) (Result, error) {
	if err := t.Validate(raw); err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}
	var in GrepInput
	_ = json.Unmarshal(raw, &in)

	resolved, err := t.Policy.ResolveAllowedPath(in.Path, t.BaseDir)
	if err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}

	toolCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	cmd := buildGrepCommand(toolCtx, resolved, in.Pattern, in.Glob, in.Limit)
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
	if in.Limit > 0 {
		outText = limitLines(outText, in.Limit)
	}

	result := Result{
		OK:             runErr == nil,
		ExitCode:       exitCode,
		Stdout:         outText,
		Stderr:         errText,
		TruncatedLines: truncLinesOut || truncLinesErr,
		TruncatedBytes: truncBytesOut || truncBytesErr,
	}
	if runErr != nil {
		return result, fmt.Errorf("grep execution failed: %w", runErr)
	}
	return result, nil
}

func buildGrepCommand(ctx context.Context, path, pattern, glob string, limit int) *exec.Cmd {
	if _, err := exec.LookPath("rg"); err == nil {
		args := []string{"--line-number", "--no-heading", "--color", "never"}
		if strings.TrimSpace(glob) != "" {
			args = append(args, "-g", glob)
		}
		if limit > 0 {
			args = append(args, "--max-count", strconv.Itoa(limit))
		}
		args = append(args, pattern, path)
		return exec.CommandContext(ctx, "rg", args...)
	}

	args := []string{"-R", "-n", "-H"}
	if limit > 0 {
		args = append(args, "-m", strconv.Itoa(limit))
	}
	if strings.TrimSpace(glob) != "" {
		args = append(args, "--include="+glob)
	}
	args = append(args, pattern, path)
	return exec.CommandContext(ctx, "grep", args...)
}
