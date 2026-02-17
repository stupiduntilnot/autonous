# Milestone 1 实现计划

## Context

实现 `milestone-1.md` 中定义的可观测性系统。当前代码使用 `task_audit` 表做简单事件记录，`supervisor_state`/`supervisor_revisions`/`kv` 表做状态存储。Milestone 1 将这些替换为统一的 `events` 表 + 从已有数据派生状态，最终只保留 3 张表：`events`、`inbox`、`history`。

按 milestone-1.md 的 5 个任务逐一实现、测试、commit。

---

## Task 1: 创建 `events` 表和 `LogEvent` 函数

### 修改文件
- **`internal/db/db.go`**: 重写 schema。删除 `InitSupervisorSchema`/`InitWorkerSchema`，替换为单个 `InitSchema`，只创建 `events`、`inbox`、`history` 三张表。添加 `LogEvent` 函数 + event type 常量。
- **`internal/db/db_test.go`** (新建): 测试 `LogEvent`、`InitSchema`。

### 实现细节
```go
func LogEvent(db *sql.DB, parentID *int64, eventType string, payload map[string]any) (int64, error)
// payload 序列化为 JSON TEXT，nil -> NULL
// 返回自增 id
```

### 测试
- `TestInitSchema`: 三张表都能创建
- `TestLogEvent_Basic`: id 自增、timestamp 非零
- `TestLogEvent_WithParent`: parent_id 关联正确
- `TestLogEvent_NilPayload`: payload=nil 正常

### Commit: `feat(db): add events table and LogEvent function`

---

## Task 2: 基础设施事件

### 修改文件
- **`cmd/supervisor/main.go`**:
  - 启动时 `LogEvent(nil, "process.started", {role,pid,version})` -> 保存 `supervisorEventID`
  - spawn worker: `LogEvent(&supID, "worker.spawned", {pid})`
  - 传 `PARENT_PROCESS_ID` env 给 worker
  - worker 退出: `LogEvent(&supID, "worker.exited", {exit_code,uptime_seconds})`
  - crash loop: `LogEvent(&supID, "crash_loop.detected", {threshold,window_seconds})`
  - rollback: `LogEvent(&supID, "rollback.attempted", {target_revision,success})`
  - stable: `LogEvent(nil, "revision.promoted", {revision})` (替代 `markGoodRevision`)
  - 删除 `getState`/`setState`/`markGoodRevision`/`nextWorkerInstanceID` 旧函数
  - `nextWorkerInstanceID` -> 用 `db.NextWorkerSeq()`
  - `attemptRollback` -> 用 `db.CurrentGoodRev()`
- **`cmd/worker/main.go`**: 启动时读 `PARENT_PROCESS_ID`，记录 `process.started`
- **`internal/db/db.go`**: 添加 `CurrentGoodRev(db)` 和 `NextWorkerSeq(db, supEventID)`
- **`internal/config/config.go`**: 删除 `RunID` 字段

### 测试
- `internal/db/db_test.go`: `TestCurrentGoodRev`、`TestNextWorkerSeq`

### Commit: `feat(supervisor): add infrastructure events`

---

## Task 3: Agent 执行事件

### 修改文件
- **`cmd/worker/main.go`**:
  - `processTask` 中插入 agent 生命周期事件:
    - `agent.started` (parent_id=NULL) -> agentEventID
    - `turn.started` (parent_id=agentEventID) -> turnEventID
    - `turn.completed` (parent_id=agentEventID, {model,latency_ms,input_tokens,output_tokens})
    - `reply.sent` (parent_id=agentEventID, {chat_id})
    - `agent.completed` / `agent.failed` (parent_id=agentEventID)
  - 删除 `logTaskEvent` 函数和所有调用
  - 删除 `run_id`/`worker_instance_id` 相关逻辑

### 测试
- Token 信息暂时为 0（Task 5 才有真实值），但事件结构正确
- `go build ./...` + `go vet ./...` 验证编译

### Commit: `feat(worker): add agent execution events`

---

## Task 4: 状态派生

### 修改文件
- **`cmd/worker/main.go`**:
  - `loadOffset` -> `SELECT COALESCE(MAX(update_id)+1, 0) FROM inbox`
  - 删除 `saveOffset`（不再需要）
  - 简化 bootstrap 逻辑：首次启动 inbox 为空 -> offset=0 -> getUpdates 返回所有 pending -> 用 `TG_DROP_PENDING` 过滤旧消息
- **`internal/config/config.go`**: 删除 `Offset` 字段（改为运行时派生）

### 测试
- `internal/db/db_test.go`: `TestDeriveOffset`

### Commit: `feat(worker): derive state from existing data`

---

## Task 5: Model Adapter 接口

### 修改文件
- **`internal/openai/openai.go`**:
  - 新增 `CompletionResponse{Content, InputTokens, OutputTokens}`
  - `ChatCompletion` 返回 `(CompletionResponse, error)`
  - 解析 `usage.prompt_tokens` / `usage.completion_tokens`
- **`cmd/worker/main.go`**: 适配新返回值，`turn.completed` 使用真实 token 数
- **`internal/openai/openai_test.go`** (新建): httptest mock，验证 token 解析

### Commit: `feat(openai): return CompletionResponse with token usage`

---

## Verification

每个 task 后: `go build ./... && go vet ./... && go test ./...`

关键文件:
- `internal/db/db.go` (schema + LogEvent + helpers)
- `cmd/supervisor/main.go` (infrastructure events)
- `cmd/worker/main.go` (agent events + state derivation)
- `internal/openai/openai.go` (CompletionResponse)
- `internal/config/config.go` (cleanup)
