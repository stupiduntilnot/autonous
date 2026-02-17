package main

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	cmdpkg "github.com/stupiduntilnot/autonous/internal/commander"
	"github.com/stupiduntilnot/autonous/internal/config"
	ctxpkg "github.com/stupiduntilnot/autonous/internal/context"
	"github.com/stupiduntilnot/autonous/internal/control"
	"github.com/stupiduntilnot/autonous/internal/db"
	"github.com/stupiduntilnot/autonous/internal/dummy"
	modelpkg "github.com/stupiduntilnot/autonous/internal/model"
	"github.com/stupiduntilnot/autonous/internal/openai"
	"github.com/stupiduntilnot/autonous/internal/telegram"
	toolpkg "github.com/stupiduntilnot/autonous/internal/tool"
)

func main() {
	cfg, err := config.LoadWorkerConfig()
	if err != nil {
		log.Fatalf("[worker] %v", err)
	}

	database, err := db.OpenDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("[worker] %v", err)
	}
	defer database.Close()

	if err := db.InitSchema(database); err != nil {
		log.Fatalf("[worker] failed to init schema: %v", err)
	}

	// Log process.started for worker.
	var parentID *int64
	if cfg.ParentProcessID > 0 {
		parentID = &cfg.ParentProcessID
	}
	workerEventID, err := db.LogEvent(database, parentID, db.EventProcessStarted, map[string]any{
		"role":     "worker",
		"pid":      os.Getpid(),
		"provider": cfg.ModelProvider,
		"source":   cfg.Commander,
	})
	if err != nil {
		log.Printf("[worker] failed to log process.started: %v", err)
	}

	commander, err := newCommander(&cfg)
	if err != nil {
		log.Fatalf("[worker] failed to init commander: %v", err)
	}
	modelProvider, err := newModelProvider(&cfg)
	if err != nil {
		log.Fatalf("[worker] failed to init model provider: %v", err)
	}
	ctxProvider := &ctxpkg.SQLiteProvider{DB: database}
	ctxCompressor := &ctxpkg.SimpleCompressor{MaxMessages: cfg.HistoryWindow}
	ctxAssembler := &ctxpkg.StandardAssembler{}
	policy := control.Policy{
		MaxTurns:    cfg.ControlMaxTurns,
		MaxWallTime: time.Duration(cfg.ControlMaxWallTimeSeconds) * time.Second,
		MaxTokens:   control.DefaultPolicy().MaxTokens,
		MaxRetries:  cfg.ControlMaxRetries,
	}
	circuit := control.NewCircuitBreaker(5, 30*time.Second)
	const noProgressK = 3
	toolPolicy, err := toolpkg.NewPolicy(cfg.ToolAllowedRoots, cfg.ToolBashDenylist)
	if err != nil {
		log.Fatalf("[worker] invalid tool policy: %v", err)
	}
	registry := toolpkg.NewRegistry()
	if err := registry.Register(toolpkg.NewLS(
		toolPolicy,
		cfg.WorkspaceDir,
		time.Duration(cfg.ToolTimeoutSeconds)*time.Second,
		toolpkg.Limits{MaxLines: cfg.ToolMaxOutputLines, MaxBytes: cfg.ToolMaxOutputBytes},
	)); err != nil {
		log.Fatalf("[worker] failed to register tool ls: %v", err)
	}
	if err := registry.Register(toolpkg.NewFind(
		toolPolicy,
		cfg.WorkspaceDir,
		time.Duration(cfg.ToolTimeoutSeconds)*time.Second,
		toolpkg.Limits{MaxLines: cfg.ToolMaxOutputLines, MaxBytes: cfg.ToolMaxOutputBytes},
	)); err != nil {
		log.Fatalf("[worker] failed to register tool find: %v", err)
	}
	if err := registry.Register(toolpkg.NewGrep(
		toolPolicy,
		cfg.WorkspaceDir,
		time.Duration(cfg.ToolTimeoutSeconds)*time.Second,
		toolpkg.Limits{MaxLines: cfg.ToolMaxOutputLines, MaxBytes: cfg.ToolMaxOutputBytes},
	)); err != nil {
		log.Fatalf("[worker] failed to register tool grep: %v", err)
	}
	if err := registry.Register(toolpkg.NewRead(
		toolPolicy,
		cfg.WorkspaceDir,
		time.Duration(cfg.ToolTimeoutSeconds)*time.Second,
		toolpkg.Limits{MaxLines: cfg.ToolMaxOutputLines, MaxBytes: cfg.ToolMaxOutputBytes},
	)); err != nil {
		log.Fatalf("[worker] failed to register tool read: %v", err)
	}
	if err := registry.Register(toolpkg.NewWrite(
		toolPolicy,
		cfg.WorkspaceDir,
		time.Duration(cfg.ToolTimeoutSeconds)*time.Second,
		toolpkg.Limits{MaxLines: cfg.ToolMaxOutputLines, MaxBytes: cfg.ToolMaxOutputBytes},
	)); err != nil {
		log.Fatalf("[worker] failed to register tool write: %v", err)
	}
	if err := registry.Register(toolpkg.NewEdit(
		toolPolicy,
		cfg.WorkspaceDir,
		time.Duration(cfg.ToolTimeoutSeconds)*time.Second,
		toolpkg.Limits{MaxLines: cfg.ToolMaxOutputLines, MaxBytes: cfg.ToolMaxOutputBytes},
	)); err != nil {
		log.Fatalf("[worker] failed to register tool edit: %v", err)
	}
	if err := registry.Register(toolpkg.NewBash(
		toolPolicy,
		cfg.WorkspaceDir,
		time.Duration(cfg.ToolTimeoutSeconds)*time.Second,
		toolpkg.Limits{MaxLines: cfg.ToolMaxOutputLines, MaxBytes: cfg.ToolMaxOutputBytes},
	)); err != nil {
		log.Fatalf("[worker] failed to register tool bash: %v", err)
	}
	toolRunner := toolpkg.NewRunner(registry)
	if policy.MaxTurns < 2 {
		policy.MaxTurns = 2
	}

	// Derive offset from inbox, or bootstrap on first run.
	offset, err := db.DeriveOffset(database)
	if err != nil {
		log.Fatalf("[worker] failed to derive offset: %v", err)
	}

	if offset == 0 && cfg.DropPending {
		bootstrapped, err := bootstrapOffset(commander, cfg.PendingWindowSeconds, cfg.PendingMaxMessages)
		if err != nil {
			log.Printf("[worker] bootstrap offset error: %v", err)
		} else {
			offset = bootstrapped
		}
	}

	log.Printf(
		"worker running id=%s model=%s provider=%s source=%s",
		cfg.WorkerInstanceID,
		cfg.OpenAIModel,
		cfg.ModelProvider,
		cfg.Commander,
	)

	var handledCount uint64

	for {
		prevState := circuit.State()
		if !circuit.Allow(time.Now()) {
			time.Sleep(time.Duration(cfg.SleepSeconds) * time.Second)
			continue
		}
		if prevState == control.CircuitOpen && circuit.State() == control.CircuitHalfOpen {
			db.LogEvent(database, &workerEventID, db.EventCircuitHalfOpen, map[string]any{
				"error_class": circuit.OpenedClass(),
			})
		}

		pollTimeout := cfg.Timeout
		if hasRunnableTasks(database, policy) {
			pollTimeout = 0
		}

		updates, err := commander.GetUpdates(offset, pollTimeout)
		if err != nil {
			log.Printf("getUpdates error: %v", err)
			errClass := classifyError(err)
			prevCircuit := circuit.State()
			circuit.RecordFailure(errClass, time.Now())
			if prevCircuit != control.CircuitOpen && circuit.State() == control.CircuitOpen {
				db.LogEvent(database, &workerEventID, db.EventCircuitOpened, map[string]any{
					"error_class":      errClass,
					"threshold":        circuit.Threshold,
					"cooldown_seconds": int(circuit.Cooldown.Seconds()),
				})
			}
			time.Sleep(time.Duration(cfg.SleepSeconds) * time.Second)
			continue
		}
		if circuit.State() == control.CircuitHalfOpen && circuit.OpenedClass() == "command_source_api" {
			circuit.RecordSuccess()
			db.LogEvent(database, &workerEventID, db.EventCircuitClosed, map[string]any{"recovered": true})
		}

		for _, update := range updates {
			offset = update.UpdateID + 1

			if update.Message == nil {
				continue
			}
			if update.Message.Text == nil {
				continue
			}
			text := *update.Message.Text
			if len(text) == 0 {
				continue
			}

			chatID := update.Message.Chat.ID
			_, err := enqueueMessage(database, update.UpdateID, chatID, text, update.Message.Date)
			if err != nil {
				log.Printf("enqueue error update_id=%d: %v", update.UpdateID, err)
				continue
			}
		}

		task, err := claimNextTask(database, policy)
		if err != nil {
			log.Printf("claim_next_task error: %v", err)
			time.Sleep(time.Duration(cfg.SleepSeconds) * time.Second)
			continue
		}
		if task == nil {
			time.Sleep(time.Duration(cfg.SleepSeconds) * time.Second)
			continue
		}

		handledCount++
		log.Printf("process task_id=%d chat_id=%d text=%s", task.ID, task.ChatID, truncate(task.Text, 200))

		// Log agent.started (child of worker process.started).
		agentEventID, _ := db.LogEvent(database, &workerEventID, db.EventAgentStarted, map[string]any{
			"chat_id":   task.ChatID,
			"task_id":   task.ID,
			"update_id": task.UpdateID,
			"text":      truncate(task.Text, 1000),
		})

		processErr := processTask(database, commander, modelProvider, &cfg, task, agentEventID, ctxProvider, ctxCompressor, ctxAssembler, policy, registry, toolRunner)
		if processErr != nil {
			msg := processErr.Error()
			markTaskFailed(database, task.ID, msg)
			errClass := classifyError(processErr)
			prevCircuit := circuit.State()
			circuit.RecordFailure(errClass, time.Now())
			if prevCircuit != control.CircuitOpen && circuit.State() == control.CircuitOpen {
				db.LogEvent(database, &workerEventID, db.EventCircuitOpened, map[string]any{
					"error_class":      errClass,
					"threshold":        circuit.Threshold,
					"cooldown_seconds": int(circuit.Cooldown.Seconds()),
				})
			}

			if control.ShouldRetry(policy, int(task.Attempts)) {
				fp := buildStateFingerprint(database, cfg.HistoryWindow, task.ChatID, task.ID, errClass, "")
				if progressStalled(database, task.ID, fp, noProgressK) {
					db.LogEvent(database, &agentEventID, db.EventProgressStalled, map[string]any{
						"task_id":           task.ID,
						"k":                 noProgressK,
						"state_fingerprint": fp,
					})
					markTaskExhausted(database, task.ID, msg, policy.MaxRetries)
					db.LogEvent(database, &workerEventID, db.EventRetryExhausted, map[string]any{
						"task_id":          task.ID,
						"attempts":         task.Attempts,
						"last_error_class": errClass,
					})
				} else {
					backoff := control.RetryBackoffSeconds(int(task.Attempts))
					db.LogEvent(database, &workerEventID, db.EventRetryScheduled, map[string]any{
						"task_id":           task.ID,
						"attempt":           task.Attempts,
						"backoff_seconds":   backoff,
						"error_class":       errClass,
						"state_fingerprint": fp,
					})
				}
			} else {
				backoff := control.RetryBackoffSeconds(int(task.Attempts))
				db.LogEvent(database, &workerEventID, db.EventRetryExhausted, map[string]any{
					"task_id":          task.ID,
					"attempts":         task.Attempts,
					"last_error_class": errClass,
					"last_backoff":     backoff,
				})
			}
			db.LogEvent(database, &workerEventID, db.EventAgentFailed, map[string]any{
				"task_id": task.ID,
				"error":   truncate(msg, 1000),
			})
			notify := fmt.Sprintf("任务处理失败：%s", truncate(msg, 600))
			if err := commander.SendMessage(task.ChatID, notify); err != nil {
				log.Printf("task %d failed to notify chat_id=%d: %v", task.ID, task.ChatID, err)
			}
			log.Printf("task %d failed: %s", task.ID, msg)
		} else {
			prevCircuit := circuit.State()
			circuit.RecordSuccess()
			if prevCircuit != control.CircuitClosed {
				db.LogEvent(database, &workerEventID, db.EventCircuitClosed, map[string]any{
					"recovered": true,
				})
			}
			markTaskDone(database, task.ID)
			db.LogEvent(database, &workerEventID, db.EventAgentCompleted, map[string]any{
				"task_id": task.ID,
			})
		}

		if cfg.SuicideEvery > 0 && handledCount%cfg.SuicideEvery == 0 {
			log.Printf("worker id=%s handled %d messages; exiting intentionally", cfg.WorkerInstanceID, handledCount)
			os.Exit(17)
		}
	}
}

func processTask(
	database *sql.DB,
	commander cmdpkg.Commander,
	modelProvider modelpkg.Provider,
	cfg *config.WorkerConfig,
	task *queueTask,
	agentEventID int64,
	provider ctxpkg.Provider,
	compressor ctxpkg.Compressor,
	assembler ctxpkg.Assembler,
	policy control.Policy,
	registry *toolpkg.Registry,
	runner *toolpkg.Runner,
) error {
	startedAt := time.Now()
	usedTurns := 0
	if err := control.CheckTurnLimit(policy, usedTurns); err != nil {
		recordLimitEvent(database, agentEventID, task.ID, err)
		return err
	}
	if err := control.CheckWallTime(policy, startedAt, time.Now()); err != nil {
		recordLimitEvent(database, agentEventID, task.ID, err)
		return err
	}

	history, err := provider.GetHistory(task.ChatID, cfg.HistoryWindow)
	if err != nil {
		return err
	}
	compressed := compressor.Compress(history)
	messages := assembler.Assemble(cfg.SystemPrompt, compressed, task.Text)
	toolInstruction := buildToolProtocolInstruction(registry, cfg.ToolAllowedRoots)
	messages = injectToolInstruction(messages, toolInstruction)

	db.LogEvent(database, &agentEventID, db.EventContextAssembled, map[string]any{
		"original_count":   len(history),
		"compressed_count": len(compressed),
		"max_messages":     cfg.HistoryWindow,
		"system_tokens":    estimateTokens(cfg.SystemPrompt) + estimateTokens(toolInstruction),
		"history_tokens":   estimateTokensFromMessages(compressed),
		"user_tokens":      estimateTokens(task.Text),
	})

	// Log turn.started.
	turnEventID, _ := db.LogEvent(database, &agentEventID, db.EventTurnStarted, map[string]any{
		"model_name": cfg.OpenAIModel,
	})
	usedTurns++

	turnStart := time.Now()
	resp, err := modelProvider.ChatCompletion(messages)
	if err != nil {
		return err
	}
	if err := control.CheckWallTime(policy, startedAt, time.Now()); err != nil {
		recordLimitEvent(database, agentEventID, task.ID, err)
		return err
	}
	latencyMs := time.Since(turnStart).Milliseconds()

	// Log turn.completed.
	db.LogEvent(database, &agentEventID, db.EventTurnCompleted, map[string]any{
		"model_name":    cfg.OpenAIModel,
		"latency_ms":    latencyMs,
		"input_tokens":  resp.InputTokens,
		"output_tokens": resp.OutputTokens,
	})
	totalTokens := resp.InputTokens + resp.OutputTokens
	if err := control.CheckTokenLimit(policy, totalTokens); err != nil {
		recordLimitEvent(database, agentEventID, task.ID, err)
		return err
	}

	finalReply := strings.TrimSpace(resp.Content)
	lastAssistantContent := finalReply
	toolEnvelope, hasToolProtocol := parseToolProtocol(finalReply)
	if hasToolProtocol && len(toolEnvelope.ToolCalls) == 0 {
		finalReply = strings.TrimSpace(toolEnvelope.FinalAnswer)
	}
	for hasToolProtocol && len(toolEnvelope.ToolCalls) > 0 {
		toolResultsText := executeToolCalls(database, turnEventID, runner, toolEnvelope.ToolCalls)
		if err := control.CheckTurnLimit(policy, usedTurns); err != nil {
			recordLimitEvent(database, agentEventID, task.ID, err)
			return err
		}
		usedTurns++
		messages = append(messages,
			ctxpkg.Message{Role: "assistant", Content: finalReply},
			ctxpkg.Message{Role: "user", Content: "Tool results:\n" + toolResultsText + "\nReturn JSON: {\"tool_calls\":[],\"final_answer\":\"...\"}"},
		)
		toolTurnEventID, _ := db.LogEvent(database, &agentEventID, db.EventTurnStarted, map[string]any{
			"model_name": cfg.OpenAIModel,
		})
		nextTurnStart := time.Now()
		nextResp, nextErr := modelProvider.ChatCompletion(messages)
		if nextErr != nil {
			return nextErr
		}
		db.LogEvent(database, &agentEventID, db.EventTurnCompleted, map[string]any{
			"model_name":    cfg.OpenAIModel,
			"latency_ms":    time.Since(nextTurnStart).Milliseconds(),
			"input_tokens":  nextResp.InputTokens,
			"output_tokens": nextResp.OutputTokens,
		})
		if err := control.CheckWallTime(policy, startedAt, time.Now()); err != nil {
			recordLimitEvent(database, agentEventID, task.ID, err)
			return err
		}
		totalTokens += nextResp.InputTokens + nextResp.OutputTokens
		if err := control.CheckTokenLimit(policy, totalTokens); err != nil {
			recordLimitEvent(database, agentEventID, task.ID, err)
			return err
		}
		finalReply = strings.TrimSpace(nextResp.Content)
		lastAssistantContent = finalReply
		if parsed, ok := parseToolProtocol(finalReply); ok {
			toolEnvelope = parsed
			hasToolProtocol = true
			if len(parsed.ToolCalls) == 0 {
				finalReply = strings.TrimSpace(parsed.FinalAnswer)
			}
			continue
		}
		hasToolProtocol = false
		_ = toolTurnEventID
	}
	if finalReply == "" && hasToolProtocol {
		if err := control.CheckTurnLimit(policy, usedTurns); err != nil {
			recordLimitEvent(database, agentEventID, task.ID, err)
			return err
		}
		usedTurns++
		lastTurnEventID, _ := db.LogEvent(database, &agentEventID, db.EventTurnStarted, map[string]any{
			"model_name": cfg.OpenAIModel,
		})
		messages = append(messages,
			ctxpkg.Message{Role: "assistant", Content: lastAssistantContent},
			ctxpkg.Message{Role: "user", Content: "Previous final_answer was empty. Return strict JSON with tool_calls=[] and a non-empty final_answer."},
		)
		lastTurnStart := time.Now()
		lastResp, lastErr := modelProvider.ChatCompletion(messages)
		if lastErr != nil {
			return lastErr
		}
		db.LogEvent(database, &agentEventID, db.EventTurnCompleted, map[string]any{
			"model_name":    cfg.OpenAIModel,
			"latency_ms":    time.Since(lastTurnStart).Milliseconds(),
			"input_tokens":  lastResp.InputTokens,
			"output_tokens": lastResp.OutputTokens,
		})
		if err := control.CheckWallTime(policy, startedAt, time.Now()); err != nil {
			recordLimitEvent(database, agentEventID, task.ID, err)
			return err
		}
		totalTokens += lastResp.InputTokens + lastResp.OutputTokens
		if err := control.CheckTokenLimit(policy, totalTokens); err != nil {
			recordLimitEvent(database, agentEventID, task.ID, err)
			return err
		}
		finalReply = strings.TrimSpace(lastResp.Content)
		if parsed, ok := parseToolProtocol(finalReply); ok {
			finalReply = strings.TrimSpace(parsed.FinalAnswer)
		}
		_ = lastTurnEventID
	}
	if finalReply == "" {
		return fmt.Errorf("validation: empty final reply")
	}
	if err := commander.SendMessage(task.ChatID, finalReply); err != nil {
		return err
	}

	// Log reply.sent.
	db.LogEvent(database, &agentEventID, db.EventReplySent, map[string]any{
		"chat_id": task.ChatID,
	})

	appendHistory(database, task.ChatID, "user", task.Text)
	appendHistory(database, task.ChatID, "assistant", finalReply)
	return nil
}

type toolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolProtocol struct {
	ToolCalls   []toolCall `json:"tool_calls"`
	FinalAnswer string     `json:"final_answer"`
}

func parseToolProtocol(content string) (toolProtocol, bool) {
	var parsed toolProtocol
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &parsed); err == nil {
		return parsed, true
	}
	jsonObj, ok := extractJSONObject(content)
	if !ok {
		return toolProtocol{}, false
	}
	if err := json.Unmarshal([]byte(jsonObj), &parsed); err != nil {
		return toolProtocol{}, false
	}
	return parsed, true
}

func buildToolProtocolInstruction(registry *toolpkg.Registry, allowedRoots string) string {
	names := registry.MustList()
	toolNames := make([]string, 0, len(names))
	for _, meta := range names {
		toolNames = append(toolNames, meta.Name)
	}
	roots := strings.TrimSpace(allowedRoots)
	if roots == "" {
		roots = "/workspace,/state"
	}
	return "You can use tools in this environment. " +
		"Available tools: " + strings.Join(toolNames, ", ") + ". " +
		"Allowed roots: " + roots + ". " +
		"For ls/find/read/write/edit, arguments must include a valid \"path\". " +
		"Use \".\" for current directory; never use \"/\". " +
		"For read, always set \"limit\" > 0 and optional \"offset\" >= 0. " +
		"For write, always set non-empty \"content\". " +
		"Always respond with strict JSON: " +
		"{\"tool_calls\":[{\"name\":\"...\",\"arguments\":{...}}],\"final_answer\":\"...\"}. " +
		"If a tool is needed, set final_answer to empty and fill tool_calls. " +
		"If no tool is needed, set tool_calls to [] and provide final_answer."
}

func injectToolInstruction(messages []ctxpkg.Message, instruction string) []ctxpkg.Message {
	if strings.TrimSpace(instruction) == "" {
		return messages
	}
	inst := ctxpkg.Message{Role: "system", Content: instruction}
	if len(messages) == 0 {
		return []ctxpkg.Message{inst}
	}
	if messages[0].Role == "system" {
		out := make([]ctxpkg.Message, 0, len(messages)+1)
		out = append(out, messages[0], inst)
		out = append(out, messages[1:]...)
		return out
	}
	out := make([]ctxpkg.Message, 0, len(messages)+1)
	out = append(out, inst)
	out = append(out, messages...)
	return out
}

func executeToolCalls(database *sql.DB, turnEventID int64, runner *toolpkg.Runner, calls []toolCall) string {
	var out strings.Builder
	for _, c := range calls {
		toolName := strings.TrimSpace(c.Name)
		argsText, argsRedacted := redactSecrets(string(c.Arguments))
		if toolName == "" {
			toolEventID, _ := db.LogEvent(database, &turnEventID, db.EventToolCallStarted, map[string]any{
				"tool_name": "",
				"arguments": truncate(argsText, 500),
			})
			errText := "validation: empty tool name"
			errText, errRedacted := redactSecrets(errText)
			db.LogEvent(database, &toolEventID, db.EventToolCallFailed, map[string]any{
				"tool_name":   "",
				"error":       errText,
				"error_class": "validation",
				"redacted":    argsRedacted || errRedacted,
			})
			out.WriteString("tool=\n")
			out.WriteString("error:\n" + errText + "\n")
			continue
		}
		toolEventID, _ := db.LogEvent(database, &turnEventID, db.EventToolCallStarted, map[string]any{
			"tool_name": toolName,
			"arguments": truncate(argsText, 500),
		})
		started := time.Now()
		res, err := runner.RunOne(context.Background(), toolpkg.Call{
			Name:      toolName,
			Arguments: c.Arguments,
		})
		stdoutText, stdoutRedacted := redactSecrets(res.Stdout)
		stderrText, stderrRedacted := redactSecrets(res.Stderr)
		if err != nil {
			errText, errRedacted := redactSecrets(err.Error())
			errClass := classifyToolError(err)
			redacted := argsRedacted || errRedacted || stdoutRedacted || stderrRedacted
			db.LogEvent(database, &toolEventID, db.EventToolCallFailed, map[string]any{
				"tool_name":   toolName,
				"error":       truncate(errText, 500),
				"error_class": errClass,
				"redacted":    redacted,
			})
			out.WriteString("tool=" + toolName + "\n")
			out.WriteString("error:\n" + truncate(errText, 2000) + "\n")
			if strings.TrimSpace(stdoutText) != "" {
				out.WriteString("stdout:\n" + stdoutText + "\n")
			}
			if strings.TrimSpace(stderrText) != "" {
				out.WriteString("stderr:\n" + stderrText + "\n")
			}
			continue
		}
		db.LogEvent(database, &toolEventID, db.EventToolCallDone, map[string]any{
			"tool_name":       toolName,
			"latency_ms":      time.Since(started).Milliseconds(),
			"exit_code":       res.ExitCode,
			"truncated_lines": res.TruncatedLines,
			"truncated_bytes": res.TruncatedBytes,
		})
		out.WriteString("tool=" + toolName + "\n")
		if strings.TrimSpace(stdoutText) != "" {
			out.WriteString("stdout:\n" + stdoutText + "\n")
		}
		if strings.TrimSpace(stderrText) != "" {
			out.WriteString("stderr:\n" + stderrText + "\n")
		}
	}
	return out.String()
}

func classifyToolError(err error) string {
	if err == nil {
		return "unknown"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "deadline exceeded"), strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.Contains(msg, "outside allowlist"), strings.Contains(msg, "denied by policy"):
		return "policy"
	case strings.Contains(msg, "validation"), strings.Contains(msg, "required"), strings.Contains(msg, "invalid"), strings.Contains(msg, "unknown tool"), strings.Contains(msg, "must be"):
		return "validation"
	default:
		return "tool_exec"
	}
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._\-=/+]+`),
	regexp.MustCompile(`(?i)\b(sk-[A-Za-z0-9\-_]{8,})\b`),
	regexp.MustCompile(`(?i)\b([A-Za-z0-9_]*(TOKEN|SECRET|PASSWORD|API_KEY))\b\s*[:=]\s*["']?([^\s"']+)`),
}

func redactSecrets(text string) (string, bool) {
	out := text
	redacted := false
	for _, p := range secretPatterns {
		next := p.ReplaceAllStringFunc(out, func(m string) string {
			redacted = true
			parts := strings.SplitN(m, "=", 2)
			if len(parts) == 2 && strings.Contains(m, "=") {
				return parts[0] + "=***REDACTED***"
			}
			if strings.Contains(m, ":") {
				kv := strings.SplitN(m, ":", 2)
				return kv[0] + ": ***REDACTED***"
			}
			return "***REDACTED***"
		})
		out = next
	}
	return out, redacted
}

func extractJSONObject(content string) (string, bool) {
	s := strings.TrimSpace(content)
	if s == "" {
		return "", false
	}
	start := strings.Index(s, "{")
	if start < 0 {
		return "", false
	}
	inString := false
	escapeNext := false
	depth := 0
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			if escapeNext {
				escapeNext = false
				continue
			}
			if ch == '\\' {
				escapeNext = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if ch == '{' {
			depth++
			continue
		}
		if ch == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// --- DB helper functions ---

type queueTask struct {
	ID        int64
	ChatID    int64
	UpdateID  int64
	Text      string
	Attempts  int64
	UpdatedAt int64
}

func appendHistory(database *sql.DB, chatID int64, role, text string) {
	database.Exec("INSERT INTO history (chat_id, role, text) VALUES (?, ?, ?)", chatID, role, text)
}

func enqueueMessage(database *sql.DB, updateID, chatID int64, text string, messageDate int64) (bool, error) {
	result, err := database.Exec(
		"INSERT OR IGNORE INTO inbox (update_id, chat_id, text, message_date, status, updated_at) VALUES (?, ?, ?, ?, 'queued', unixepoch())",
		updateID, chatID, text, messageDate,
	)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

func hasRunnableTasks(database *sql.DB, policy control.Policy) bool {
	var exists int64
	err := database.QueryRow("SELECT 1 FROM inbox WHERE status = 'queued' ORDER BY id LIMIT 1").Scan(&exists)
	if err == nil {
		return true
	}
	rows, err := database.Query("SELECT attempts, updated_at FROM inbox WHERE status='failed' ORDER BY id LIMIT 100")
	if err != nil {
		return false
	}
	defer rows.Close()
	now := time.Now().Unix()
	for rows.Next() {
		var attempts, updatedAt int64
		if scanErr := rows.Scan(&attempts, &updatedAt); scanErr != nil {
			continue
		}
		if retryReady(attempts, updatedAt, now, policy) {
			return true
		}
	}
	return false
}

func claimNextTask(database *sql.DB, policy control.Policy) (*queueTask, error) {
	tx, err := database.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var task queueTask
	err = tx.QueryRow(
		`SELECT id, chat_id, update_id, text, attempts, updated_at FROM inbox
		 WHERE status = 'queued'
		 LIMIT 1`,
	).Scan(&task.ID, &task.ChatID, &task.UpdateID, &task.Text, &task.Attempts, &task.UpdatedAt)
	if err == sql.ErrNoRows {
		// Try failed tasks with retry window.
		rows, qerr := tx.Query(
			`SELECT id, chat_id, update_id, text, attempts, updated_at
			 FROM inbox WHERE status='failed' ORDER BY id LIMIT 200`,
		)
		if qerr != nil {
			return nil, qerr
		}
		defer rows.Close()
		now := time.Now().Unix()
		found := false
		for rows.Next() {
			var cand queueTask
			if scanErr := rows.Scan(&cand.ID, &cand.ChatID, &cand.UpdateID, &cand.Text, &cand.Attempts, &cand.UpdatedAt); scanErr != nil {
				continue
			}
			if retryReady(cand.Attempts, cand.UpdatedAt, now, policy) {
				task = cand
				found = true
				break
			}
		}
		if !found {
			return nil, nil
		}
	} else if err != nil {
		return nil, err
	}

	_, err = tx.Exec(
		`UPDATE inbox SET status = 'in_progress', attempts = attempts + 1,
		 locked_at = unixepoch(), error = NULL, updated_at = unixepoch()
		 WHERE id = ?`, task.ID,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	task.Attempts++
	return &task, nil
}

func markTaskDone(database *sql.DB, taskID int64) {
	database.Exec("UPDATE inbox SET status = 'done', updated_at = unixepoch(), error = NULL WHERE id = ?", taskID)
}

func markTaskFailed(database *sql.DB, taskID int64, errMsg string) {
	database.Exec("UPDATE inbox SET status = 'failed', updated_at = unixepoch(), error = ? WHERE id = ?",
		truncate(errMsg, 1000), taskID)
}

func markTaskExhausted(database *sql.DB, taskID int64, errMsg string, maxRetries int) {
	exhaustedAttempts := maxRetries + 1
	if exhaustedAttempts < 1 {
		exhaustedAttempts = 1
	}
	database.Exec(
		"UPDATE inbox SET status = 'failed', attempts = ?, updated_at = unixepoch(), error = ? WHERE id = ?",
		exhaustedAttempts,
		truncate(errMsg, 1000),
		taskID,
	)
}

func bootstrapOffset(commander cmdpkg.Commander, pendingWindowSeconds int64, pendingMaxMessages int) (int64, error) {
	updates, err := commander.GetUpdates(0, 0)
	if err != nil {
		return 0, err
	}
	if len(updates) == 0 {
		return 0, nil
	}

	now := time.Now().Unix()
	cutoff := now - pendingWindowSeconds

	var inWindow []cmdpkg.Update
	for _, u := range updates {
		if u.Message != nil && u.Message.Date >= cutoff {
			inWindow = append(inWindow, u)
		}
	}

	if len(inWindow) == 0 {
		return updates[len(updates)-1].UpdateID + 1, nil
	}

	if len(inWindow) > pendingMaxMessages {
		inWindow = inWindow[len(inWindow)-pendingMaxMessages:]
	}

	return inWindow[0].UpdateID, nil
}

func newCommander(cfg *config.WorkerConfig) (cmdpkg.Commander, error) {
	switch cfg.Commander {
	case "telegram":
		return telegram.NewClient(cfg.TelegramAPIBase, time.Duration(cfg.Timeout+20)*time.Second), nil
	case "dummy":
		return dummy.NewCommander(cfg.DummyCommanderScript, cfg.DummySendScript)
	default:
		return nil, fmt.Errorf("unsupported commander: %s", cfg.Commander)
	}
}

func newModelProvider(cfg *config.WorkerConfig) (modelpkg.Provider, error) {
	switch cfg.ModelProvider {
	case "openai":
		return openai.NewClient(cfg.OpenAIAPIKey, cfg.OpenAIChatCompURL, cfg.OpenAIModel, 120*time.Second), nil
	case "dummy":
		return dummy.NewProvider(cfg.OpenAIModel, cfg.DummyProviderScript)
	default:
		return nil, fmt.Errorf("unsupported model provider: %s", cfg.ModelProvider)
	}
}

func retryReady(attempts int64, updatedAt int64, nowUnix int64, policy control.Policy) bool {
	if attempts <= 0 {
		return true
	}
	if !control.ShouldRetry(policy, int(attempts)) {
		return false
	}
	backoff := int64(control.RetryBackoffSeconds(int(attempts)))
	return nowUnix-updatedAt >= backoff
}

func classifyError(err error) string {
	if err == nil {
		return "unknown"
	}
	msg := err.Error()
	switch {
	case containsAny(msg, "telegram ", "commander"):
		return "command_source_api"
	case containsAny(msg, "openai ", "provider", "model"):
		return "provider_api"
	case containsAny(msg, "sqlite", "db", "database"):
		return "db"
	default:
		return "unknown"
	}
}

func buildStateFingerprint(database *sql.DB, historyWindow int, chatID int64, taskID int64, errClass string, reply string) string {
	historyCount := historyCount(database, chatID)
	compressedCount := historyCount
	if historyWindow > 0 && compressedCount > historyWindow {
		compressedCount = historyWindow
	}
	replyHash := ""
	if reply != "" {
		h := sha1.Sum([]byte(reply))
		replyHash = hex.EncodeToString(h[:8])
	}
	return fmt.Sprintf("task=%d|hist=%d|comp=%d|err=%s|reply=%s",
		taskID, historyCount, compressedCount, errClass, replyHash)
}

func progressStalled(database *sql.DB, taskID int64, current string, k int) bool {
	if k <= 1 {
		return false
	}
	prev := recentFingerprints(database, taskID, k-1)
	fps := make([]string, 0, len(prev)+1)
	fps = append(fps, prev...)
	fps = append(fps, current)
	return control.NoProgress(fps, k)
}

func recentFingerprints(database *sql.DB, taskID int64, limit int) []string {
	if limit <= 0 {
		return nil
	}
	rows, err := database.Query(
		`SELECT payload FROM events
		 WHERE event_type = ?
		 ORDER BY id DESC LIMIT ?`,
		db.EventRetryScheduled, 200,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	result := make([]string, 0, limit)
	for rows.Next() && len(result) < limit {
		var payload sql.NullString
		if scanErr := rows.Scan(&payload); scanErr != nil || !payload.Valid {
			continue
		}
		var p map[string]any
		if unmarshalErr := json.Unmarshal([]byte(payload.String), &p); unmarshalErr != nil {
			continue
		}
		pTaskID, ok := numberToInt64(p["task_id"])
		if !ok || pTaskID != taskID {
			continue
		}
		fp, _ := p["state_fingerprint"].(string)
		if fp == "" {
			continue
		}
		result = append([]string{fp}, result...)
	}
	return result
}

func historyCount(database *sql.DB, chatID int64) int {
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM history WHERE chat_id = ?", chatID).Scan(&count); err != nil {
		return 0
	}
	return count
}

func numberToInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if p != "" && stringContainsFold(s, p) {
			return true
		}
	}
	return false
}

func stringContainsFold(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && (indexFold(s, substr) >= 0))
}

func indexFold(s, sep string) int {
	ls := len(s)
	lp := len(sep)
	for i := 0; i+lp <= ls; i++ {
		if strings.EqualFold(s[i:i+lp], sep) {
			return i
		}
	}
	return -1
}

func recordLimitEvent(database *sql.DB, agentEventID int64, taskID int64, err error) {
	limitErr, ok := err.(*control.LimitError)
	if !ok {
		return
	}
	db.LogEvent(database, &agentEventID, db.EventControlLimitReached, map[string]any{
		"task_id":    taskID,
		"limit_type": string(limitErr.Type),
		"value":      limitErr.Value,
		"threshold":  limitErr.Threshold,
	})
}

func truncate(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars])
}

func estimateTokens(text string) int {
	chars := len([]rune(text))
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

func estimateTokensFromMessages(messages []ctxpkg.Message) int {
	totalChars := 0
	for _, msg := range messages {
		totalChars += len([]rune(msg.Content))
	}
	if totalChars <= 0 {
		return 0
	}
	return (totalChars + 3) / 4
}
