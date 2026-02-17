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

type ReadInput struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

type Read struct {
	Policy  *Policy
	BaseDir string
	Timeout time.Duration
	Limits  Limits
}

func NewRead(policy *Policy, baseDir string, timeout time.Duration, limits Limits) *Read {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if limits.MaxLines <= 0 {
		limits.MaxLines = 2000
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 51200
	}
	return &Read{
		Policy:  policy,
		BaseDir: baseDir,
		Timeout: timeout,
		Limits:  limits,
	}
}

func (t *Read) Name() string { return "read" }

func (t *Read) Validate(raw json.RawMessage) error {
	var in ReadInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("invalid read input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return fmt.Errorf("read.path is required")
	}
	if in.Offset < 0 {
		return fmt.Errorf("read.offset must be >= 0")
	}
	if in.Limit <= 0 {
		return fmt.Errorf("read.limit must be > 0")
	}
	return nil
}

func (t *Read) Execute(ctx context.Context, raw json.RawMessage) (Result, error) {
	if err := t.Validate(raw); err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}
	var in ReadInput
	_ = json.Unmarshal(raw, &in)

	resolved, err := t.Policy.ResolveAllowedPath(in.Path, t.BaseDir)
	if err != nil {
		return Result{OK: false, ExitCode: 2, Stderr: err.Error()}, err
	}

	startLine := in.Offset + 1
	endLine := in.Offset + in.Limit
	rangeArg := strconv.Itoa(startLine) + "," + strconv.Itoa(endLine) + "p"

	toolCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	cmd := exec.CommandContext(toolCtx, "sed", "-n", rangeArg, resolved)
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
		return result, fmt.Errorf("read execution failed: %w", runErr)
	}
	return result, nil
}
