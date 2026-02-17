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

type BashInput struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd"`
}

type Bash struct {
	Policy  *Policy
	BaseDir string
	Timeout time.Duration
	Limits  Limits
}

func NewBash(policy *Policy, baseDir string, timeout time.Duration, limits Limits) *Bash {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if limits.MaxLines <= 0 {
		limits.MaxLines = 2000
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 51200
	}
	return &Bash{
		Policy:  policy,
		BaseDir: baseDir,
		Timeout: timeout,
		Limits:  limits,
	}
}

func (t *Bash) Name() string { return "bash" }

func (t *Bash) Validate(raw json.RawMessage) error {
	var in BashInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("invalid bash input: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return fmt.Errorf("bash.command is required")
	}
	return nil
}

func (t *Bash) Execute(ctx context.Context, raw json.RawMessage) (Result, error) {
	if err := t.Validate(raw); err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}
	var in BashInput
	_ = json.Unmarshal(raw, &in)

	if t.Policy.IsBashDenied(in.Command) {
		err := fmt.Errorf("bash command denied by policy")
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}

	cwd := strings.TrimSpace(in.Cwd)
	if cwd == "" {
		cwd = "."
	}
	resolvedCwd, err := t.Policy.ResolveAllowedPath(cwd, t.BaseDir)
	if err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}

	toolCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	cmd := exec.CommandContext(toolCtx, "bash", "-lc", in.Command)
	cmd.Dir = resolvedCwd

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
		return result, fmt.Errorf("bash execution failed: %w", runErr)
	}
	return result, nil
}
