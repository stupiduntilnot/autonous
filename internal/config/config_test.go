package config

import (
	"strings"
	"testing"
)

func setupWorkerEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("AUTONOUS_MODEL_PROVIDER", "openai")
	t.Setenv("AUTONOUS_COMMANDER", "telegram")
}

func TestLoadWorkerConfig_ValidatesAllowedRoots(t *testing.T) {
	setupWorkerEnv(t)
	t.Setenv("AUTONOUS_TOOL_ALLOWED_ROOTS", "workspace,/state")
	_, err := LoadWorkerConfig()
	if err == nil {
		t.Fatal("expected invalid allowlist error")
	}
	if !strings.Contains(err.Error(), "AUTONOUS_TOOL_ALLOWED_ROOTS") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestLoadWorkerConfig_NormalizesAllowedRoots(t *testing.T) {
	setupWorkerEnv(t)
	t.Setenv("AUTONOUS_TOOL_ALLOWED_ROOTS", "/workspace,/state,/workspace")
	cfg, err := LoadWorkerConfig()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.ToolAllowedRoots != "/workspace,/state" {
		t.Fatalf("unexpected normalized roots: %s", cfg.ToolAllowedRoots)
	}
}

func TestLoadWorkerConfig_ValidatesToolLimits(t *testing.T) {
	setupWorkerEnv(t)
	t.Setenv("AUTONOUS_TOOL_TIMEOUT_SECONDS", "0")
	_, err := LoadWorkerConfig()
	if err == nil {
		t.Fatal("expected invalid timeout error")
	}
	if !strings.Contains(err.Error(), "AUTONOUS_TOOL_TIMEOUT_SECONDS") {
		t.Fatalf("unexpected err: %v", err)
	}
}
