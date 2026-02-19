package config

import (
	"os"
	"path/filepath"
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

func TestLoadWorkerConfig_ValidatesUpdatePipelineTimeout(t *testing.T) {
	setupWorkerEnv(t)
	t.Setenv("AUTONOUS_UPDATE_PIPELINE_TIMEOUT_SECONDS", "0")
	_, err := LoadWorkerConfig()
	if err == nil {
		t.Fatal("expected invalid pipeline timeout error")
	}
	if !strings.Contains(err.Error(), "AUTONOUS_UPDATE_PIPELINE_TIMEOUT_SECONDS") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestLoadWorkerConfig_ValidatesUpdateArtifactRoot(t *testing.T) {
	setupWorkerEnv(t)
	t.Setenv("AUTONOUS_UPDATE_ARTIFACT_ROOT", "state/artifacts")
	_, err := LoadWorkerConfig()
	if err == nil {
		t.Fatal("expected invalid update artifact root error")
	}
	if !strings.Contains(err.Error(), "AUTONOUS_UPDATE_ARTIFACT_ROOT") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestLoadSupervisorConfig_UsesUpdateActiveBinWhenProvided(t *testing.T) {
	t.Setenv("WORKER_BIN", "/workspace/bin/worker")
	t.Setenv("AUTONOUS_UPDATE_ACTIVE_BIN", "/state/bin/worker.current")
	cfg := LoadSupervisorConfig()
	if cfg.WorkerBin != "/state/bin/worker.current" {
		t.Fatalf("unexpected worker bin: %s", cfg.WorkerBin)
	}
}

func TestResolveConfigDir_Priority(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "explicit")
	t.Setenv("AUTONOUS_CONFIG_DIR", explicit)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))
	dir, explicitSet, err := resolveConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if !explicitSet {
		t.Fatal("expected explicit config dir")
	}
	if dir != explicit {
		t.Fatalf("unexpected explicit dir: %s", dir)
	}

	t.Setenv("AUTONOUS_CONFIG_DIR", "")
	xdg := filepath.Join(t.TempDir(), "xdg2")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir, explicitSet, err = resolveConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if explicitSet {
		t.Fatal("expected non-explicit config dir from XDG_CONFIG_HOME")
	}
	wantXDG := filepath.Join(xdg, "autonous")
	if dir != wantXDG {
		t.Fatalf("unexpected xdg dir: got=%s want=%s", dir, wantXDG)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, explicitSet, err = resolveConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if explicitSet {
		t.Fatal("expected non-explicit config dir from HOME")
	}
	wantHome := filepath.Join(home, ".config", "autonous")
	if dir != wantHome {
		t.Fatalf("unexpected home dir: got=%s want=%s", dir, wantHome)
	}
}

func TestLoadWorkerConfig_CreatesExplicitConfigDir(t *testing.T) {
	setupWorkerEnv(t)
	dir := filepath.Join(t.TempDir(), "cfg")
	t.Setenv("AUTONOUS_CONFIG_DIR", dir)
	cfg, err := LoadWorkerConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Fatalf("expected explicit config dir created: %v", statErr)
	}
	if cfg.ConfigDir != dir {
		t.Fatalf("unexpected config dir: %s", cfg.ConfigDir)
	}
	wantPromptFile := filepath.Join(dir, "AUTONOUS.md")
	if cfg.SystemPromptFile != wantPromptFile {
		t.Fatalf("unexpected system prompt file: %s", cfg.SystemPromptFile)
	}
}

func TestLoadWorkerConfig_DoesNotCreateDefaultConfigDir(t *testing.T) {
	setupWorkerEnv(t)
	t.Setenv("AUTONOUS_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	defaultDir := filepath.Join(home, ".config", "autonous")
	if _, err := os.Stat(defaultDir); !os.IsNotExist(err) {
		t.Fatalf("expected default dir absent before test, err=%v", err)
	}
	cfg, err := LoadWorkerConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(defaultDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected default dir not created, stat err=%v", statErr)
	}
	if cfg.ConfigDir != defaultDir {
		t.Fatalf("unexpected config dir: %s", cfg.ConfigDir)
	}
}
