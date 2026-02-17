package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/stupiduntilnot/autonous/internal/config"
	ctxpkg "github.com/stupiduntilnot/autonous/internal/context"
	"github.com/stupiduntilnot/autonous/internal/db"
	"github.com/stupiduntilnot/autonous/internal/openai"
	"github.com/stupiduntilnot/autonous/internal/telegram"
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
		"role": "worker",
		"pid":  os.Getpid(),
	})
	if err != nil {
		log.Printf("[worker] failed to log process.started: %v", err)
	}

	tgClient := telegram.NewClient(cfg.TelegramAPIBase, time.Duration(cfg.Timeout+20)*time.Second)
	aiClient := openai.NewClient(cfg.OpenAIAPIKey, cfg.OpenAIChatCompURL, cfg.OpenAIModel, 120*time.Second)
	ctxProvider := &ctxpkg.SQLiteProvider{DB: database}
	ctxCompressor := &ctxpkg.SimpleCompressor{MaxMessages: cfg.HistoryWindow}
	ctxAssembler := &ctxpkg.StandardAssembler{}

	// Derive offset from inbox, or bootstrap on first run.
	offset, err := db.DeriveOffset(database)
	if err != nil {
		log.Fatalf("[worker] failed to derive offset: %v", err)
	}

	if offset == 0 && cfg.DropPending {
		bootstrapped, err := bootstrapOffset(tgClient, cfg.PendingWindowSeconds, cfg.PendingMaxMessages)
		if err != nil {
			log.Printf("[worker] bootstrap offset error: %v", err)
		} else {
			offset = bootstrapped
		}
	}

	log.Printf("worker running id=%s model=%s", cfg.WorkerInstanceID, cfg.OpenAIModel)

	var handledCount uint64

	for {
		pollTimeout := cfg.Timeout
		if hasRunnableTasks(database) {
			pollTimeout = 0
		}

		updates, err := tgClient.GetUpdates(offset, pollTimeout)
		if err != nil {
			log.Printf("getUpdates error: %v", err)
			time.Sleep(time.Duration(cfg.SleepSeconds) * time.Second)
			continue
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

		task, err := claimNextTask(database)
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

		processErr := processTask(database, tgClient, aiClient, &cfg, task, agentEventID, ctxProvider, ctxCompressor, ctxAssembler)
		if processErr != nil {
			msg := processErr.Error()
			markTaskFailed(database, task.ID, msg)
			db.LogEvent(database, &workerEventID, db.EventAgentFailed, map[string]any{
				"task_id": task.ID,
				"error":   truncate(msg, 1000),
			})
			notify := fmt.Sprintf("任务处理失败：%s", truncate(msg, 600))
			if err := tgClient.SendMessage(task.ChatID, notify); err != nil {
				log.Printf("task %d failed to notify chat_id=%d: %v", task.ID, task.ChatID, err)
			}
			log.Printf("task %d failed: %s", task.ID, msg)
		} else {
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
	tg *telegram.Client,
	ai *openai.Client,
	cfg *config.WorkerConfig,
	task *queueTask,
	agentEventID int64,
	provider ctxpkg.Provider,
	compressor ctxpkg.Compressor,
	assembler ctxpkg.Assembler,
) error {
	history, err := provider.GetHistory(task.ChatID, cfg.HistoryWindow)
	if err != nil {
		return err
	}
	compressed := compressor.Compress(history)
	messages := assembler.Assemble(cfg.SystemPrompt, compressed, task.Text)

	db.LogEvent(database, &agentEventID, db.EventContextAssembled, map[string]any{
		"original_count":   len(history),
		"compressed_count": len(compressed),
		"max_messages":     cfg.HistoryWindow,
		"system_tokens":    estimateTokens(cfg.SystemPrompt),
		"history_tokens":   estimateTokensFromMessages(compressed),
		"user_tokens":      estimateTokens(task.Text),
	})

	// Log turn.started.
	db.LogEvent(database, &agentEventID, db.EventTurnStarted, map[string]any{
		"model_name": cfg.OpenAIModel,
	})

	turnStart := time.Now()
	resp, err := ai.ChatCompletion(messages)
	if err != nil {
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

	if err := tg.SendMessage(task.ChatID, resp.Content); err != nil {
		return err
	}

	// Log reply.sent.
	db.LogEvent(database, &agentEventID, db.EventReplySent, map[string]any{
		"chat_id": task.ChatID,
	})

	appendHistory(database, task.ChatID, "user", task.Text)
	appendHistory(database, task.ChatID, "assistant", resp.Content)
	return nil
}

// --- DB helper functions ---

type queueTask struct {
	ID       int64
	ChatID   int64
	UpdateID int64
	Text     string
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

func hasRunnableTasks(database *sql.DB) bool {
	var exists int64
	err := database.QueryRow("SELECT 1 FROM inbox WHERE status IN ('queued', 'failed') ORDER BY id LIMIT 1").Scan(&exists)
	return err == nil
}

func claimNextTask(database *sql.DB) (*queueTask, error) {
	tx, err := database.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var task queueTask
	err = tx.QueryRow(
		`SELECT id, chat_id, update_id, text FROM inbox
		 WHERE status IN ('queued', 'failed')
		 ORDER BY CASE status WHEN 'queued' THEN 0 ELSE 1 END, id
		 LIMIT 1`,
	).Scan(&task.ID, &task.ChatID, &task.UpdateID, &task.Text)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
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
	return &task, nil
}

func markTaskDone(database *sql.DB, taskID int64) {
	database.Exec("UPDATE inbox SET status = 'done', updated_at = unixepoch(), error = NULL WHERE id = ?", taskID)
}

func markTaskFailed(database *sql.DB, taskID int64, errMsg string) {
	database.Exec("UPDATE inbox SET status = 'failed', updated_at = unixepoch(), error = ? WHERE id = ?",
		truncate(errMsg, 1000), taskID)
}

func bootstrapOffset(tg *telegram.Client, pendingWindowSeconds int64, pendingMaxMessages int) (int64, error) {
	updates, err := tg.GetUpdates(0, 0)
	if err != nil {
		return 0, err
	}
	if len(updates) == 0 {
		return 0, nil
	}

	now := time.Now().Unix()
	cutoff := now - pendingWindowSeconds

	var inWindow []telegram.Update
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
