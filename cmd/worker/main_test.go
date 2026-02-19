package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	cmdpkg "github.com/stupiduntilnot/autonous/internal/commander"
	"github.com/stupiduntilnot/autonous/internal/config"
	ctxpkg "github.com/stupiduntilnot/autonous/internal/context"
	"github.com/stupiduntilnot/autonous/internal/control"
	"github.com/stupiduntilnot/autonous/internal/db"
	"github.com/stupiduntilnot/autonous/internal/dummy"
	modelpkg "github.com/stupiduntilnot/autonous/internal/model"
	toolpkg "github.com/stupiduntilnot/autonous/internal/tool"
)

func testWorkerDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.OpenDB(t.TempDir() + "/worker.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InitSchema(database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestRetryReady(t *testing.T) {
	p := control.Policy{MaxRetries: 3}
	now := time.Now().Unix()
	if retryReady(1, now, now, p) {
		t.Fatal("attempt=1 should not be ready immediately (1s backoff)")
	}
	if !retryReady(1, now-2, now, p) {
		t.Fatal("attempt=1 should be ready after backoff")
	}
	if retryReady(4, now-100, now, p) {
		t.Fatal("attempt > max retries should never be ready")
	}
}

func TestClaimNextTask_RespectsRetryWindow(t *testing.T) {
	database := testWorkerDB(t)
	p := control.Policy{MaxRetries: 3}

	_, err := database.Exec(
		`INSERT INTO inbox (update_id, chat_id, text, message_date, status, attempts, updated_at)
		 VALUES (1001, 1, 'failed-task', 0, 'failed', 1, ?)`,
		time.Now().Unix(),
	)
	if err != nil {
		t.Fatal(err)
	}

	task, err := claimNextTask(database, p)
	if err != nil {
		t.Fatal(err)
	}
	if task != nil {
		t.Fatal("expected nil task because backoff not elapsed")
	}

	_, err = database.Exec("UPDATE inbox SET updated_at = ? WHERE update_id = 1001", time.Now().Unix()-2)
	if err != nil {
		t.Fatal(err)
	}
	task, err = claimNextTask(database, p)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("expected a runnable failed task after backoff")
	}
	if task.Attempts != 2 {
		t.Fatalf("expected attempts incremented to 2, got %d", task.Attempts)
	}
}

func TestProcessTask_RecordsLimitEvent(t *testing.T) {
	database := testWorkerDB(t)
	commander, err := dummy.NewCommander("ok", "ok")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := dummy.NewProvider("dummy", "ok")
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkerConfig{
		OpenAIModel:   "dummy",
		SystemPrompt:  "sys",
		HistoryWindow: 12,
	}
	task := &queueTask{
		ID:       1,
		ChatID:   1,
		UpdateID: 1,
		Text:     "hello",
	}
	ctxProvider := &ctxpkg.SQLiteProvider{DB: database}
	ctxCompressor := &ctxpkg.SimpleCompressor{MaxMessages: 12}
	ctxAssembler := &ctxpkg.StandardAssembler{}
	policy := control.Policy{
		MaxTurns:    0,
		MaxWallTime: 120 * time.Second,
		MaxRetries:  3,
	}

	agentEventID, err := db.LogEvent(database, nil, db.EventAgentStarted, map[string]any{"task_id": 1})
	if err != nil {
		t.Fatal(err)
	}

	reg := toolpkg.NewRegistry()
	runner := toolpkg.NewRunner(reg)
	err = processTask(database, commander, provider, cfg, task, agentEventID, ctxProvider, ctxCompressor, ctxAssembler, policy, reg, runner)
	if err == nil {
		t.Fatal("expected limit error")
	}

	var cnt int
	if qerr := database.QueryRow(
		"SELECT COUNT(*) FROM events WHERE event_type = ?",
		db.EventControlLimitReached,
	).Scan(&cnt); qerr != nil {
		t.Fatal(qerr)
	}
	if cnt != 1 {
		t.Fatalf("expected 1 control.limit_reached event, got %d", cnt)
	}
}

func TestProcessTask_RecordsTokenLimitEvent(t *testing.T) {
	database := testWorkerDB(t)
	commander, err := dummy.NewCommander("ok", "ok")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := dummy.NewProvider("dummy", "ok")
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkerConfig{
		OpenAIModel:   "dummy",
		SystemPrompt:  "sys",
		HistoryWindow: 12,
	}
	task := &queueTask{ID: 2, ChatID: 1, UpdateID: 2, Text: "hello"}
	ctxProvider := &ctxpkg.SQLiteProvider{DB: database}
	ctxCompressor := &ctxpkg.SimpleCompressor{MaxMessages: 12}
	ctxAssembler := &ctxpkg.StandardAssembler{}
	policy := control.Policy{
		MaxTurns:    1,
		MaxWallTime: 120 * time.Second,
		MaxTokens:   1,
		MaxRetries:  3,
	}

	agentEventID, err := db.LogEvent(database, nil, db.EventAgentStarted, map[string]any{"task_id": 2})
	if err != nil {
		t.Fatal(err)
	}
	reg := toolpkg.NewRegistry()
	runner := toolpkg.NewRunner(reg)
	err = processTask(database, commander, provider, cfg, task, agentEventID, ctxProvider, ctxCompressor, ctxAssembler, policy, reg, runner)
	if err == nil {
		t.Fatal("expected token limit error")
	}

	var cnt int
	if qerr := database.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = ?", db.EventControlLimitReached).Scan(&cnt); qerr != nil {
		t.Fatal(qerr)
	}
	if cnt != 1 {
		t.Fatalf("expected 1 control.limit_reached event, got %d", cnt)
	}
}

func TestProgressStalled_UsesRecentFingerprints(t *testing.T) {
	database := testWorkerDB(t)
	taskID := int64(42)
	fp := "task=42|hist=0|comp=0|err=provider_api|reply="

	payload, _ := json.Marshal(map[string]any{
		"task_id":           taskID,
		"state_fingerprint": fp,
	})
	if _, err := database.Exec(
		"INSERT INTO events (event_type, payload) VALUES (?, ?), (?, ?)",
		db.EventRetryScheduled, string(payload),
		db.EventRetryScheduled, string(payload),
	); err != nil {
		t.Fatal(err)
	}

	if !progressStalled(database, taskID, fp, 3) {
		t.Fatal("expected progress stalled for repeated fingerprints")
	}
}

type seqProvider struct {
	resps []modelpkg.CompletionResponse
	idx   int
}

func (s *seqProvider) ChatCompletion(messages []ctxpkg.Message) (modelpkg.CompletionResponse, error) {
	if s.idx >= len(s.resps) {
		return modelpkg.CompletionResponse{Content: "{\"tool_calls\":[],\"final_answer\":\"done\"}"}, nil
	}
	i := s.idx
	s.idx++
	return s.resps[i], nil
}

type captureCommander struct {
	last string
}

func (c *captureCommander) GetUpdates(offset int64, timeout int) ([]cmdpkg.Update, error) {
	return nil, nil
}

func (c *captureCommander) SendMessage(chatID int64, text string) error {
	c.last = text
	return nil
}

type approvalCaptureCommander struct {
	captureCommander
	approveTxID string
	approveText string
}

func (c *approvalCaptureCommander) SendApprovalRequest(chatID int64, text string, txID string) error {
	c.approveText = text
	c.approveTxID = txID
	return nil
}

func TestProcessTask_ToolLoopLS(t *testing.T) {
	database := testWorkerDB(t)
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	commander := &captureCommander{}
	provider := &seqProvider{
		resps: []modelpkg.CompletionResponse{
			{Content: "{\"tool_calls\":[{\"name\":\"ls\",\"arguments\":{\"path\":\".\"}}],\"final_answer\":\"\"}", InputTokens: 1, OutputTokens: 1},
			{Content: "{\"tool_calls\":[],\"final_answer\":\"tool done\"}", InputTokens: 1, OutputTokens: 1},
		},
	}
	cfg := &config.WorkerConfig{
		OpenAIModel:   "dummy",
		SystemPrompt:  "sys",
		HistoryWindow: 12,
	}
	task := &queueTask{ID: 3, ChatID: 1, UpdateID: 3, Text: "list files"}
	ctxProvider := &ctxpkg.SQLiteProvider{DB: database}
	ctxCompressor := &ctxpkg.SimpleCompressor{MaxMessages: 12}
	ctxAssembler := &ctxpkg.StandardAssembler{}
	policy := control.Policy{MaxTurns: 2, MaxWallTime: 120 * time.Second, MaxTokens: 1000, MaxRetries: 3}
	agentEventID, err := db.LogEvent(database, nil, db.EventAgentStarted, map[string]any{"task_id": 3})
	if err != nil {
		t.Fatal(err)
	}

	p, err := toolpkg.NewPolicy(base, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := toolpkg.NewRegistry()
	if err := reg.Register(toolpkg.NewLS(p, base, 2*time.Second, toolpkg.Limits{MaxLines: 100, MaxBytes: 4096})); err != nil {
		t.Fatal(err)
	}
	runner := toolpkg.NewRunner(reg)

	if err := processTask(database, commander, provider, cfg, task, agentEventID, ctxProvider, ctxCompressor, ctxAssembler, policy, reg, runner); err != nil {
		t.Fatalf("processTask failed: %v", err)
	}
	if commander.last != "tool done" {
		t.Fatalf("unexpected final reply: %q", commander.last)
	}
	var toolCnt int
	if qerr := database.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = ?", db.EventToolCallDone).Scan(&toolCnt); qerr != nil {
		t.Fatal(qerr)
	}
	if toolCnt != 1 {
		t.Fatalf("expected 1 tool_call.completed, got %d", toolCnt)
	}
}

func TestLoadSystemPrompt_FromFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "AUTONOUS.md")
	content := "系统指令\n配置目录占位符={AUTONOUS_CONFIG_DIR}"
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.WorkerConfig{
		ConfigDir:        dir,
		SystemPromptFile: file,
		SystemPromptEnv:  "env prompt",
	}
	prompt, source, size, readErr := loadSystemPrompt(cfg)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if source != "file" {
		t.Fatalf("unexpected source: %s", source)
	}
	if size != len(content) {
		t.Fatalf("unexpected size: %d", size)
	}
	if !strings.Contains(prompt, dir) {
		t.Fatalf("expected prompt contains config dir, got: %s", prompt)
	}
}

func TestLoadSystemPrompt_FromEnvWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.WorkerConfig{
		ConfigDir:        dir,
		SystemPromptFile: filepath.Join(dir, "AUTONOUS.md"),
		SystemPromptEnv:  "env-only-prompt",
	}
	prompt, source, _, readErr := loadSystemPrompt(cfg)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if source != "env" {
		t.Fatalf("unexpected source: %s", source)
	}
	if !strings.Contains(prompt, "env-only-prompt") {
		t.Fatalf("expected env prompt content, got: %s", prompt)
	}
	if !strings.Contains(prompt, "系统提示符文件:") {
		t.Fatalf("expected config metadata injected, got: %s", prompt)
	}
}

func TestLoadSystemPrompt_FileReadErrorFallsBackToEnv(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.WorkerConfig{
		ConfigDir:        dir,
		SystemPromptFile: dir, // read directory to trigger non-not-exist error.
		SystemPromptEnv:  "env-fallback",
	}
	prompt, source, _, readErr := loadSystemPrompt(cfg)
	if readErr == nil {
		t.Fatal("expected read error")
	}
	if source != "env" {
		t.Fatalf("unexpected source: %s", source)
	}
	if !strings.Contains(prompt, "env-fallback") {
		t.Fatalf("expected env fallback prompt, got: %s", prompt)
	}
}

func TestLoadSystemPrompt_FallsBackToBuiltin(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.WorkerConfig{
		ConfigDir:        dir,
		SystemPromptFile: filepath.Join(dir, "AUTONOUS.md"),
		SystemPromptEnv:  "",
	}
	prompt, source, _, readErr := loadSystemPrompt(cfg)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if source != "builtin" {
		t.Fatalf("unexpected source: %s", source)
	}
	if !strings.Contains(prompt, builtinSystemPrompt()) {
		t.Fatalf("expected builtin prompt content, got: %s", prompt)
	}
}

func TestProcessTask_ExtractsFinalAnswerFromJSON(t *testing.T) {
	database := testWorkerDB(t)
	commander := &captureCommander{}
	provider := &seqProvider{
		resps: []modelpkg.CompletionResponse{
			{Content: "{\"tool_calls\":[],\"final_answer\":\"direct final\"}", InputTokens: 1, OutputTokens: 1},
		},
	}
	cfg := &config.WorkerConfig{
		OpenAIModel:   "dummy",
		SystemPrompt:  "sys",
		HistoryWindow: 12,
	}
	task := &queueTask{ID: 4, ChatID: 1, UpdateID: 4, Text: "hello"}
	ctxProvider := &ctxpkg.SQLiteProvider{DB: database}
	ctxCompressor := &ctxpkg.SimpleCompressor{MaxMessages: 12}
	ctxAssembler := &ctxpkg.StandardAssembler{}
	policy := control.Policy{MaxTurns: 1, MaxWallTime: 120 * time.Second, MaxTokens: 1000, MaxRetries: 3}
	agentEventID, err := db.LogEvent(database, nil, db.EventAgentStarted, map[string]any{"task_id": 4})
	if err != nil {
		t.Fatal(err)
	}
	reg := toolpkg.NewRegistry()
	runner := toolpkg.NewRunner(reg)
	if err := processTask(database, commander, provider, cfg, task, agentEventID, ctxProvider, ctxCompressor, ctxAssembler, policy, reg, runner); err != nil {
		t.Fatalf("processTask failed: %v", err)
	}
	if commander.last != "direct final" {
		t.Fatalf("expected extracted final answer, got %q", commander.last)
	}
}

func TestProcessTask_ToolFailureThenRecover(t *testing.T) {
	database := testWorkerDB(t)
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "ok.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	commander := &captureCommander{}
	provider := &seqProvider{
		resps: []modelpkg.CompletionResponse{
			{
				Content:      "{\"tool_calls\":[{\"name\":\"ls\",\"arguments\":{\"path\":\"missing.txt\"}}],\"final_answer\":\"\"}",
				InputTokens:  1,
				OutputTokens: 1,
			},
			{
				Content:      "{\"tool_calls\":[{\"name\":\"ls\",\"arguments\":{\"path\":\".\"}}],\"final_answer\":\"\"}",
				InputTokens:  1,
				OutputTokens: 1,
			},
			{
				Content:      "{\"tool_calls\":[],\"final_answer\":\"recovered\"}",
				InputTokens:  1,
				OutputTokens: 1,
			},
		},
	}
	cfg := &config.WorkerConfig{
		OpenAIModel:   "dummy",
		SystemPrompt:  "sys",
		HistoryWindow: 12,
	}
	task := &queueTask{ID: 5, ChatID: 1, UpdateID: 5, Text: "recover"}
	ctxProvider := &ctxpkg.SQLiteProvider{DB: database}
	ctxCompressor := &ctxpkg.SimpleCompressor{MaxMessages: 12}
	ctxAssembler := &ctxpkg.StandardAssembler{}
	policy := control.Policy{MaxTurns: 4, MaxWallTime: 120 * time.Second, MaxTokens: 1000, MaxRetries: 3}
	agentEventID, err := db.LogEvent(database, nil, db.EventAgentStarted, map[string]any{"task_id": 5})
	if err != nil {
		t.Fatal(err)
	}

	p, err := toolpkg.NewPolicy(base, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := toolpkg.NewRegistry()
	if err := reg.Register(toolpkg.NewLS(p, base, 2*time.Second, toolpkg.Limits{MaxLines: 100, MaxBytes: 4096})); err != nil {
		t.Fatal(err)
	}
	runner := toolpkg.NewRunner(reg)

	if err := processTask(database, commander, provider, cfg, task, agentEventID, ctxProvider, ctxCompressor, ctxAssembler, policy, reg, runner); err != nil {
		t.Fatalf("processTask failed: %v", err)
	}
	if commander.last != "recovered" {
		t.Fatalf("unexpected final reply: %q", commander.last)
	}
	var failedCnt int
	if err := database.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = ?", db.EventToolCallFailed).Scan(&failedCnt); err != nil {
		t.Fatal(err)
	}
	if failedCnt < 1 {
		t.Fatalf("expected at least 1 tool_call.failed, got %d", failedCnt)
	}
	var completedCnt int
	if err := database.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = ?", db.EventToolCallDone).Scan(&completedCnt); err != nil {
		t.Fatal(err)
	}
	if completedCnt < 1 {
		t.Fatalf("expected at least 1 tool_call.completed, got %d", completedCnt)
	}
}

func TestParseToolProtocol_ExtractsJSONFromMarkdownFence(t *testing.T) {
	content := "```json\n{\"tool_calls\":[],\"final_answer\":\"ok\"}\n```"
	got, ok := parseToolProtocol(content)
	if !ok {
		t.Fatal("expected parse success")
	}
	if got.FinalAnswer != "ok" {
		t.Fatalf("unexpected final_answer: %q", got.FinalAnswer)
	}
}

func TestClassifyToolError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{err: context.DeadlineExceeded, want: "timeout"},
		{err: errString("path outside allowlist: /"), want: "policy"},
		{err: errString("validation: read.limit must be > 0"), want: "validation"},
		{err: errString("ls execution failed: exit status 2"), want: "tool_exec"},
	}
	for _, c := range cases {
		got := classifyToolError(c.err)
		if got != c.want {
			t.Fatalf("classifyToolError(%v)=%s want=%s", c.err, got, c.want)
		}
	}
}

func TestRedactSecrets(t *testing.T) {
	in := "Authorization: Bearer abc123 TOKEN=xyz sk-test-secret"
	out, redacted := redactSecrets(in)
	if !redacted {
		t.Fatal("expected redacted=true")
	}
	if strings.Contains(out, "abc123") || strings.Contains(out, "xyz") || strings.Contains(out, "sk-test-secret") {
		t.Fatalf("secret leak after redaction: %q", out)
	}
}

func TestProcessDirectCommand_ApproveSuccess(t *testing.T) {
	database := testWorkerDB(t)
	if err := db.InsertArtifact(database, "tx-approve-1", "base-0", "/state/artifacts/tx-approve-1/worker", db.ArtifactStatusStaged); err != nil {
		t.Fatal(err)
	}
	task := &queueTask{ID: 10, ChatID: 1, Text: "approve tx-approve-1"}
	cfg := &config.WorkerConfig{}

	handled, reply, shouldExit, err := processDirectCommand(database, &captureCommander{}, cfg, task, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true")
	}
	if !shouldExit {
		t.Fatal("expected shouldExit=true")
	}
	if !strings.Contains(reply, "approve 成功") {
		t.Fatalf("unexpected reply: %s", reply)
	}
	got, err := db.GetArtifactByTxID(database, "tx-approve-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.ArtifactStatusApproved {
		t.Fatalf("unexpected status: %s", got.Status)
	}
}

func TestProcessDirectCommand_ApproveIgnoredWhenNotStaged(t *testing.T) {
	database := testWorkerDB(t)
	if err := db.InsertArtifact(database, "tx-approve-2", "", "/state/artifacts/tx-approve-2/worker", db.ArtifactStatusApproved); err != nil {
		t.Fatal(err)
	}
	task := &queueTask{ID: 11, ChatID: 1, Text: "approve tx-approve-2"}
	cfg := &config.WorkerConfig{}

	handled, reply, shouldExit, err := processDirectCommand(database, &captureCommander{}, cfg, task, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true")
	}
	if shouldExit {
		t.Fatal("expected shouldExit=false")
	}
	if !strings.Contains(reply, "approve 忽略") {
		t.Fatalf("unexpected reply: %s", reply)
	}
}

func TestProcessDirectCommand_UpdateStage(t *testing.T) {
	database := testWorkerDB(t)
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	cfg := &config.WorkerConfig{
		WorkspaceDir:             repoRoot,
		UpdateArtifactRoot:       artifactRoot,
		UpdateTestCmd:            "true",
		UpdateSelfCheckCmd:       "",
		UpdatePipelineTimeoutSec: 120,
	}
	task := &queueTask{ID: 12, ChatID: 1, Text: "update stage tx-stage-1"}

	cmdr := &approvalCaptureCommander{}
	handled, reply, shouldExit, err := processDirectCommand(database, cmdr, cfg, task, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true")
	}
	if shouldExit {
		t.Fatal("expected shouldExit=false")
	}
	if strings.TrimSpace(reply) != "" {
		t.Fatalf("expected empty reply because approval message is merged, got: %q", reply)
	}
	if cmdr.approveTxID != "tx-stage-1" {
		t.Fatalf("expected approval request for tx-stage-1, got %q", cmdr.approveTxID)
	}
	if !strings.Contains(cmdr.approveText, "update stage 成功") {
		t.Fatalf("expected merged approval message text, got: %q", cmdr.approveText)
	}
	artifact, err := db.GetArtifactByTxID(database, "tx-stage-1")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Status != db.ArtifactStatusStaged {
		t.Fatalf("unexpected status: %s", artifact.Status)
	}
	if _, err := os.Stat(filepath.Join(artifactRoot, "tx-stage-1", "worker")); err != nil {
		t.Fatalf("expected worker binary artifact: %v", err)
	}
}

func TestProcessDirectCommand_UpdateStageDuplicateTxID(t *testing.T) {
	database := testWorkerDB(t)
	if err := db.InsertArtifact(database, "tx-dup-1", "", "/state/artifacts/tx-dup-1/worker", db.ArtifactStatusStaged); err != nil {
		t.Fatal(err)
	}
	cfg := &config.WorkerConfig{
		WorkspaceDir:             t.TempDir(),
		UpdateArtifactRoot:       t.TempDir(),
		UpdateTestCmd:            "true",
		UpdateSelfCheckCmd:       "",
		UpdatePipelineTimeoutSec: 30,
	}
	task := &queueTask{ID: 13, ChatID: 1, Text: "update stage tx-dup-1"}

	handled, reply, shouldExit, err := processDirectCommand(database, &captureCommander{}, cfg, task, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true")
	}
	if shouldExit {
		t.Fatal("expected shouldExit=false")
	}
	if !strings.Contains(reply, "update stage 忽略") {
		t.Fatalf("unexpected reply: %s", reply)
	}
}

func TestProcessDirectCommand_CancelSuccess(t *testing.T) {
	database := testWorkerDB(t)
	if err := db.InsertArtifact(database, "tx-cancel-1", "", "/state/artifacts/tx-cancel-1/worker", db.ArtifactStatusStaged); err != nil {
		t.Fatal(err)
	}
	cfg := &config.WorkerConfig{}
	task := &queueTask{ID: 14, ChatID: 1, Text: "cancel tx-cancel-1"}

	handled, reply, shouldExit, err := processDirectCommand(database, &captureCommander{}, cfg, task, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !handled || shouldExit {
		t.Fatalf("unexpected handled/shouldExit: %v/%v", handled, shouldExit)
	}
	if !strings.Contains(reply, "cancel 成功") {
		t.Fatalf("unexpected reply: %s", reply)
	}
	a, err := db.GetArtifactByTxID(database, "tx-cancel-1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != db.ArtifactStatusCancelled {
		t.Fatalf("unexpected status: %s", a.Status)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
