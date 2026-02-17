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

type FindInput struct {
	Path        string `json:"path"`
	NamePattern string `json:"name_pattern"`
	MaxDepth    int    `json:"max_depth"`
	Limit       int    `json:"limit"`
}

type Find struct {
	Policy  *Policy
	BaseDir string
	Timeout time.Duration
	Limits  Limits
}

func NewFind(policy *Policy, baseDir string, timeout time.Duration, limits Limits) *Find {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if limits.MaxLines <= 0 {
		limits.MaxLines = 2000
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 51200
	}
	return &Find{
		Policy:  policy,
		BaseDir: baseDir,
		Timeout: timeout,
		Limits:  limits,
	}
}

func (t *Find) Name() string { return "find" }

func (t *Find) Validate(raw json.RawMessage) error {
	var in FindInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("invalid find input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return fmt.Errorf("find.path is required")
	}
	if in.MaxDepth < 0 {
		return fmt.Errorf("find.max_depth must be >= 0")
	}
	if in.Limit < 0 {
		return fmt.Errorf("find.limit must be >= 0")
	}
	return nil
}

func (t *Find) Execute(ctx context.Context, raw json.RawMessage) (Result, error) {
	if err := t.Validate(raw); err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}
	var in FindInput
	_ = json.Unmarshal(raw, &in)

	resolved, err := t.Policy.ResolveAllowedPath(in.Path, t.BaseDir)
	if err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}

	pattern := strings.TrimSpace(in.NamePattern)
	if pattern == "" {
		pattern = "*"
	}

	toolCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	cmd := buildFindCommand(toolCtx, resolved, pattern, in.MaxDepth, in.Limit)
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
		return result, fmt.Errorf("find execution failed: %w", runErr)
	}
	if in.Limit > 0 {
		result.Stdout = limitLines(result.Stdout, in.Limit)
	}
	return result, nil
}

func buildFindCommand(ctx context.Context, path, pattern string, maxDepth, limit int) *exec.Cmd {
	if _, err := exec.LookPath("fd"); err == nil {
		args := []string{"--color", "never", "--glob"}
		if maxDepth > 0 {
			args = append(args, "-d", strconv.Itoa(maxDepth))
		}
		if limit > 0 {
			args = append(args, "--max-results", strconv.Itoa(limit))
		}
		args = append(args, pattern, path)
		return exec.CommandContext(ctx, "fd", args...)
	}
	if _, err := exec.LookPath("fdfind"); err == nil {
		args := []string{"--color", "never", "--glob"}
		if maxDepth > 0 {
			args = append(args, "-d", strconv.Itoa(maxDepth))
		}
		if limit > 0 {
			args = append(args, "--max-results", strconv.Itoa(limit))
		}
		args = append(args, pattern, path)
		return exec.CommandContext(ctx, "fdfind", args...)
	}

	args := []string{path}
	if maxDepth > 0 {
		args = append(args, "-maxdepth", strconv.Itoa(maxDepth))
	}
	if pattern != "*" {
		args = append(args, "-name", pattern)
	}
	return exec.CommandContext(ctx, "find", args...)
}

func limitLines(text string, max int) string {
	if max <= 0 || strings.TrimSpace(text) == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	count := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		out = append(out, line)
		count++
		if count >= max {
			break
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}
