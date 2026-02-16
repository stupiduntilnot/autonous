# Milestone 1 计划: 可观测性

## 核心原则

- **统一事件日志 (Unified Event Log)**: 所有事件（基础设施 + Agent 执行）都记录在单一的 `events` 表中，扁平存储。
- **层级化追踪**: 通过 `parent_id` 字段从扁平的事件行中重建出树形结构。
- **极简 Schema**: `events` 表只包含 `id`, `timestamp`, `parent_id`, `event_type`, `payload`。
- **最小表集合**: 系统仅使用 3 张表——`events`、`inbox`、`history`。
- **Model Adapter 通用数据模型**: `input_tokens`、`output_tokens` 是通用概念，每个 adapter 从 provider response 中提取（如 OpenAI `usage` 字段），不使用 tokenizer。

## 数据库 Schema

### `events` — 统一事件日志

```sql
CREATE TABLE events (
    id INTEGER PRIMARY KEY,               -- 自增主键，同时作为各层级的唯一标识（process id / agent id / turn id 等）
    timestamp INTEGER NOT NULL            -- 事件发生的 Unix 时间戳
        DEFAULT (unixepoch()),
    parent_id INTEGER,                    -- 父级上下文的 event id；根事件为 NULL
    event_type TEXT NOT NULL,             -- 事件类型，如 'agent.started', 'turn.completed'
    payload TEXT                          -- JSON 格式的事件特定数据
);

CREATE INDEX idx_events_parent_id ON events(parent_id);
```

只追加，不更新，不删除。

### `inbox` — 消息队列

```sql
CREATE TABLE inbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,  -- 自增主键，也是 task_id
    update_id INTEGER NOT NULL UNIQUE,     -- Telegram update ID，用于去重和派生 polling offset
    chat_id INTEGER NOT NULL,              -- Telegram chat ID，标识对话来源
    text TEXT NOT NULL,                    -- 用户消息文本
    message_date INTEGER NOT NULL,         -- Telegram 消息时间戳
    status TEXT NOT NULL DEFAULT 'queued', -- 任务状态：queued / in_progress / done / failed
    attempts INTEGER NOT NULL DEFAULT 0,   -- 已尝试处理的次数
    locked_at INTEGER,                     -- 被 claim 的时间戳，用于超时检测
    error TEXT,                            -- 最近一次失败的错误信息
    created_at INTEGER NOT NULL            -- 入队时间
        DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL            -- 最后状态变更时间
        DEFAULT (unixepoch())
);

CREATE INDEX idx_inbox_status_id ON inbox(status, id);
```

Worker 从中 claim 任务并处理。Telegram polling offset 通过 `SELECT COALESCE(MAX(update_id) + 1, 0) FROM inbox` 派生，无需额外存储。

### `history` — LLM 对话上下文

```sql
CREATE TABLE history (
    id INTEGER PRIMARY KEY AUTOINCREMENT, -- 自增主键
    chat_id INTEGER NOT NULL,             -- Telegram chat ID，标识对话来源
    role TEXT NOT NULL,                   -- 消息角色：'user' 或 'assistant'
    text TEXT NOT NULL,                   -- 消息文本
    created_at INTEGER NOT NULL           -- 记录时间
        DEFAULT (unixepoch())
);
```

按 `chat_id` 分组的对话历史，用于构建 LLM 请求的 context。每条消息处理都需要读取，是热路径。

## 事件分类

events 表中有两类事件，各自有独立的 `parent_id` 树，互不干扰：

### 基础设施事件

进程生命周期管理，Agent 不关心：

| event_type | 说明 | payload |
|---|---|---|
| `process.started` | Supervisor 或 Worker 进程启动。没有对应的 `process.stopped`：Supervisor 被外部 kill 时无法自己写事件；Worker 的退出由 Supervisor 通过 `worker.exited` 记录。 | `role`, `pid`, `version` |
| `worker.spawned` | Supervisor spawn 了 Worker | `pid` |
| `worker.exited` | Worker 退出 | `exit_code`, `uptime_seconds` |
| `revision.promoted` | 标记当前 revision 为 good | `revision` |
| `crash_loop.detected` | 达到 crash threshold | `threshold`, `window_seconds` |
| `rollback.attempted` | 尝试回滚 | `target_revision`, `success` |

### Agent 执行事件（3 层模型）

与 pi-mono 保持一致的术语：

```
Agent  (agent.started -> agent.completed)                 <- 用户发消息到最终回复
  Turn  (turn.started -> turn.completed)                  <- 一次 LLM 调用 + tool executions
    ToolCall  (tool_call.started -> tool_call.completed)  <- 单个 tool 执行
```

| event_type | 层级 | payload |
|---|---|---|
| `agent.started` | Agent | `chat_id`, `task_id`, `update_id`, `text` |
| `agent.completed` | Agent | `task_id` |
| `agent.failed` | Agent | `task_id`, `error` |
| `turn.started` | Turn | `model_name` |
| `turn.completed` | Turn | `model_name`, `latency_ms`, `input_tokens`, `output_tokens` |
| `tool_call.started` | ToolCall | `tool_name`, `arguments` |
| `tool_call.completed` | ToolCall | `tool_name`, `output`, `latency_ms` |
| `tool_call.failed` | ToolCall | `tool_name`, `error` |
| `reply.sent` | Agent | `chat_id` |

Parent 关系：

```
process.started (id=1, parent_id=NULL)           <- Supervisor
  process.started (id=2, parent_id=1)            <- Worker

agent.started (id=5, parent_id=NULL)             <- 独立的树
  turn.started (id=6, parent_id=5)
    llm_call.completed (id=7, parent_id=6)
    tool_call.started (id=8, parent_id=6)        <- future: Milestone 2+
    tool_call.completed (id=9, parent_id=6)
  turn.completed (id=10, parent_id=5)
  turn.started (id=11, parent_id=5)              <- 第二轮（有 tool call 时）
    llm_call.completed (id=12, parent_id=11)
  turn.completed (id=13, parent_id=5)
  reply.sent (id=14, parent_id=5)
  agent.completed (id=15, parent_id=5)
```

## 任务分解

### 1. 创建 `events` 表和 `LogEvent` 函数

- [ ] 在 `internal/db/db.go` 中创建 `events` 表 schema + 索引。
- [ ] 实现 `LogEvent(parentID *int64, eventType string, payload map[string]any) (int64, error)`，返回自动生成的 `id`。
- [ ] 在 Go 代码中定义 event type 常量。

### 2. 基础设施事件

- [ ] Supervisor 启动时记录 `process.started`，用返回的 `id` 作为自身标识。
- [ ] Supervisor spawn Worker 时记录 `worker.spawned`，通过 `PARENT_PROCESS_ID` 环境变量传递。
- [ ] Worker 启动时记录 `process.started`（`parent_id` = supervisor 的 process event id）。
- [ ] Worker 退出时 Supervisor 记录 `worker.exited`。
- [ ] Crash loop、rollback、revision promoted 事件。

### 3. Agent 执行事件

- [ ] Worker 开始处理 task 时记录 `agent.started`。
- [ ] LLM 调用前后记录 `turn.started` / `turn.completed`（含 `latency_ms`, `input_tokens`, `output_tokens`）。
- [ ] 回复发送后记录 `reply.sent`。
- [ ] 处理完成/失败记录 `agent.completed` / `agent.failed`。

### 4. 状态派生

- [ ] `telegram_offset`：`SELECT COALESCE(MAX(update_id) + 1, 0) FROM inbox`。
- [ ] `current_good_rev`：查找最近的 `revision.promoted` event 的 `payload.revision`。
- [ ] `worker_instance_seq`：`COUNT` 当前 supervisor 下的 `worker.spawned` event。

### 5. Model Adapter 接口

- [ ] 定义通用 `CompletionResponse` 结构：`Content string`, `InputTokens int`, `OutputTokens int`。
- [ ] 重构 OpenAI client 返回 `CompletionResponse`，从 `usage` 字段提取 token 数。
