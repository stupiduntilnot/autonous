package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SupervisorConfig holds configuration for the supervisor process.
type SupervisorConfig struct {
	WorkerBin           string
	WorkspaceDir        string
	StateDBPath         string
	RestartDelaySeconds int
	CrashWindowSeconds  int
	CrashThreshold      int
	StableRunSeconds    int
	AutoRollback        bool
	UpdateArtifactRoot  string
}

// LoadSupervisorConfig reads supervisor configuration from environment variables.
func LoadSupervisorConfig() SupervisorConfig {
	defaultWorkerBin := envOrDefault("WORKER_BIN", "/workspace/bin/worker")
	activeBin := envOrDefault("AUTONOUS_UPDATE_ACTIVE_BIN", defaultWorkerBin)
	return SupervisorConfig{
		WorkerBin:           activeBin,
		WorkspaceDir:        envOrDefault("WORKSPACE_DIR", "/workspace"),
		StateDBPath:         envOrDefault("AUTONOUS_DB_PATH", "/state/agent.db"),
		RestartDelaySeconds: envIntOrDefault("SUPERVISOR_RESTART_DELAY_SECONDS", 1),
		CrashWindowSeconds:  envIntOrDefault("SUPERVISOR_CRASH_WINDOW_SECONDS", 300),
		CrashThreshold:      envIntOrDefault("SUPERVISOR_CRASH_THRESHOLD", 3),
		StableRunSeconds:    envIntOrDefault("SUPERVISOR_STABLE_RUN_SECONDS", 30),
		AutoRollback:        envBoolOrDefault("SUPERVISOR_AUTO_ROLLBACK", false),
		UpdateArtifactRoot:  envOrDefault("AUTONOUS_UPDATE_ARTIFACT_ROOT", "/state/artifacts"),
	}
}

// WorkerConfig holds configuration for the worker process.
type WorkerConfig struct {
	TelegramAPIBase           string
	Timeout                   int
	SleepSeconds              int
	DropPending               bool
	PendingWindowSeconds      int64
	PendingMaxMessages        int
	HistoryWindow             int
	WorkerInstanceID          string
	ParentProcessID           int64
	SuicideEvery              uint64
	OpenAIAPIKey              string
	OpenAIChatCompURL         string
	OpenAIModel               string
	SystemPrompt              string
	DBPath                    string
	WorkspaceDir              string
	ModelProvider             string
	Commander                 string
	DummyProviderScript       string
	DummyCommanderScript      string
	DummySendScript           string
	ControlMaxTurns           int
	ControlMaxWallTimeSeconds int
	ControlMaxRetries         int
	ToolTimeoutSeconds        int
	ToolMaxOutputLines        int
	ToolMaxOutputBytes        int
	ToolBashDenylist          string
	ToolAllowedRoots          string
	UpdateArtifactRoot        string
	UpdateTestCmd             string
	UpdateSelfCheckCmd        string
	UpdatePipelineTimeoutSec  int
}

// LoadWorkerConfig reads worker configuration from environment variables.
func LoadWorkerConfig() (WorkerConfig, error) {
	modelProvider := envOrDefault("AUTONOUS_MODEL_PROVIDER", "openai")
	commander := envOrDefault("AUTONOUS_COMMANDER", "telegram")

	telegramToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if commander == "telegram" && telegramToken == "" {
		return WorkerConfig{}, fmt.Errorf("TELEGRAM_BOT_TOKEN is required in environment when AUTONOUS_COMMANDER=telegram")
	}
	openaiKey := os.Getenv("OPENAI_API_KEY")
	if modelProvider == "openai" && openaiKey == "" {
		return WorkerConfig{}, fmt.Errorf("OPENAI_API_KEY is required in environment when AUTONOUS_MODEL_PROVIDER=openai")
	}

	workerInstanceID := envOrDefault("WORKER_INSTANCE_ID", "W000000")
	parentProcessID := int64(envIntOrDefault("PARENT_PROCESS_ID", 0))

	cfg := WorkerConfig{
		TelegramAPIBase:           fmt.Sprintf("https://api.telegram.org/bot%s", telegramToken),
		Timeout:                   envIntOrDefault("TG_TIMEOUT", 30),
		SleepSeconds:              envIntOrDefault("TG_SLEEP_SECONDS", 1),
		DropPending:               envBoolOrDefault("TG_DROP_PENDING", true),
		PendingWindowSeconds:      int64(envIntOrDefault("TG_PENDING_WINDOW_SECONDS", 600)),
		PendingMaxMessages:        envIntOrDefault("TG_PENDING_MAX_MESSAGES", 50),
		HistoryWindow:             envIntOrDefault("TG_HISTORY_WINDOW", 12),
		WorkerInstanceID:          workerInstanceID,
		ParentProcessID:           parentProcessID,
		SuicideEvery:              uint64(envIntOrDefault("WORKER_SUICIDE_EVERY", 0)),
		OpenAIAPIKey:              openaiKey,
		OpenAIChatCompURL:         envOrDefault("OPENAI_CHAT_COMPLETIONS_URL", "https://api.openai.com/v1/chat/completions"),
		OpenAIModel:               envOrDefault("OPENAI_MODEL", "gpt-4o-mini"),
		SystemPrompt:              envOrDefault("WORKER_SYSTEM_PROMPT", "你是 autonous 的执行 Worker。回复简洁、准确；需要时给出可执行步骤。"),
		DBPath:                    envOrDefault("AUTONOUS_DB_PATH", "/state/agent.db"),
		WorkspaceDir:              envOrDefault("WORKSPACE_DIR", "/workspace"),
		ModelProvider:             modelProvider,
		Commander:                 commander,
		DummyProviderScript:       envOrDefault("AUTONOUS_DUMMY_PROVIDER_SCRIPT", "ok"),
		DummyCommanderScript:      envOrDefault("AUTONOUS_DUMMY_COMMANDER_SCRIPT", "ok"),
		DummySendScript:           envOrDefault("AUTONOUS_DUMMY_COMMANDER_SEND_SCRIPT", "ok"),
		ControlMaxTurns:           envIntOrDefault("AUTONOUS_CONTROL_MAX_TURNS", 1),
		ControlMaxWallTimeSeconds: envIntOrDefault("AUTONOUS_CONTROL_MAX_WALL_TIME_SECONDS", 120),
		ControlMaxRetries:         envIntOrDefault("AUTONOUS_CONTROL_MAX_RETRIES", 3),
		ToolTimeoutSeconds:        envIntOrDefault("AUTONOUS_TOOL_TIMEOUT_SECONDS", 30),
		ToolMaxOutputLines:        envIntOrDefault("AUTONOUS_TOOL_MAX_OUTPUT_LINES", 2000),
		ToolMaxOutputBytes:        envIntOrDefault("AUTONOUS_TOOL_MAX_OUTPUT_BYTES", 51200),
		ToolBashDenylist:          envOrDefault("AUTONOUS_TOOL_BASH_DENYLIST", ""),
		ToolAllowedRoots:          envOrDefault("AUTONOUS_TOOL_ALLOWED_ROOTS", "/workspace,/state"),
		UpdateArtifactRoot:        envOrDefault("AUTONOUS_UPDATE_ARTIFACT_ROOT", "/state/artifacts"),
		UpdateTestCmd:             envOrDefault("AUTONOUS_UPDATE_TEST_CMD", "go test ./..."),
		UpdateSelfCheckCmd:        envOrDefault("AUTONOUS_UPDATE_SELF_CHECK_CMD", ""),
		UpdatePipelineTimeoutSec:  envIntOrDefault("AUTONOUS_UPDATE_PIPELINE_TIMEOUT_SECONDS", 1800),
	}
	if err := validateWorkerConfig(&cfg); err != nil {
		return WorkerConfig{}, err
	}
	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envBoolOrDefault(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v == "1" || strings.EqualFold(v, "true")
}

func validateWorkerConfig(cfg *WorkerConfig) error {
	if cfg.ControlMaxTurns <= 0 {
		return fmt.Errorf("AUTONOUS_CONTROL_MAX_TURNS must be > 0")
	}
	if cfg.ControlMaxWallTimeSeconds <= 0 {
		return fmt.Errorf("AUTONOUS_CONTROL_MAX_WALL_TIME_SECONDS must be > 0")
	}
	if cfg.ControlMaxRetries < 0 {
		return fmt.Errorf("AUTONOUS_CONTROL_MAX_RETRIES must be >= 0")
	}
	if cfg.ToolTimeoutSeconds <= 0 {
		return fmt.Errorf("AUTONOUS_TOOL_TIMEOUT_SECONDS must be > 0")
	}
	if cfg.ToolMaxOutputLines <= 0 {
		return fmt.Errorf("AUTONOUS_TOOL_MAX_OUTPUT_LINES must be > 0")
	}
	if cfg.ToolMaxOutputBytes <= 0 {
		return fmt.Errorf("AUTONOUS_TOOL_MAX_OUTPUT_BYTES must be > 0")
	}
	if cfg.UpdatePipelineTimeoutSec <= 0 {
		return fmt.Errorf("AUTONOUS_UPDATE_PIPELINE_TIMEOUT_SECONDS must be > 0")
	}
	if strings.TrimSpace(cfg.UpdateArtifactRoot) == "" {
		return fmt.Errorf("AUTONOUS_UPDATE_ARTIFACT_ROOT cannot be empty")
	}
	if !filepath.IsAbs(cfg.UpdateArtifactRoot) {
		return fmt.Errorf("AUTONOUS_UPDATE_ARTIFACT_ROOT must be absolute")
	}
	roots, err := parseAllowedRoots(cfg.ToolAllowedRoots)
	if err != nil {
		return err
	}
	cfg.ToolAllowedRoots = strings.Join(roots, ",")
	return nil
}

func parseAllowedRoots(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		root := strings.TrimSpace(p)
		if root == "" {
			continue
		}
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("AUTONOUS_TOOL_ALLOWED_ROOTS requires absolute paths: %s", root)
		}
		clean := filepath.Clean(root)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("AUTONOUS_TOOL_ALLOWED_ROOTS cannot be empty")
	}
	return out, nil
}
