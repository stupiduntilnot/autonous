# 结构化日志设计文档（Structured Logging）

更新时间：2026-02-15

## 1. 目标
在当前 `Supervisor + Worker + SQLite inbox` 架构中引入可查询、可审计、可回放的结构化日志。

本阶段目标：
1. 记录 `task lifecycle`。
2. 记录 `tool invocation`。
3. 失败后直接通过 Telegram 通知，不做自动 retry/dead-letter。

## 2. 范围（Scope）

## 2.1 包含
1. `task_audit`：任务级事件日志。
2. `tool_calls`：工具调用级日志。
3. `run_id` + `worker_instance_id` 关联一次 Worker 生命周期。

## 2.2 不包含（本阶段）
1. 自动 retry/backoff。
2. dead-letter 队列。
3. 完整 RBAC 或复杂 policy engine。

## 3. 设计原则
1. `append-only`：日志只追加，不覆盖历史。
2. `structured first`：字段化存储，便于 SQL 查询。
3. `human readable`：关键错误保留简短文本。
4. `low overhead`：写日志不阻塞主链路。

## 4. 数据模型（SQLite）

## 4.1 `task_audit`
用于记录任务处理过程中的关键事件。

建议 schema：
```sql
CREATE TABLE IF NOT EXISTS task_audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id INTEGER NOT NULL,
  chat_id INTEGER NOT NULL,
  update_id INTEGER,
  phase TEXT NOT NULL,
  status TEXT NOT NULL,
  message TEXT,
  error TEXT,
  run_id TEXT NOT NULL,
  worker_instance_id TEXT NOT NULL,
  created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE INDEX IF NOT EXISTS idx_task_audit_task_id ON task_audit(task_id, id);
CREATE INDEX IF NOT EXISTS idx_task_audit_phase ON task_audit(phase, created_at);
```

字段说明：
- `phase`：`ingress|queued|claimed|model_request|model_response|reply_sent|task_done|task_failed`
- `status`：`ok|failed|info`

## 4.2 `tool_calls`
用于记录每次 tool call 的输入输出与结果。

建议 schema：
```sql
CREATE TABLE IF NOT EXISTS tool_calls (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id INTEGER NOT NULL,
  chat_id INTEGER NOT NULL,
  tool_name TEXT NOT NULL,
  arguments_json TEXT,
  status TEXT NOT NULL,
  output_text TEXT,
  error TEXT,
  latency_ms INTEGER,
  run_id TEXT NOT NULL,
  worker_instance_id TEXT NOT NULL,
  started_at INTEGER,
  finished_at INTEGER,
  created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE INDEX IF NOT EXISTS idx_tool_calls_task_id ON tool_calls(task_id, id);
CREATE INDEX IF NOT EXISTS idx_tool_calls_status ON tool_calls(status, created_at);
CREATE INDEX IF NOT EXISTS idx_tool_calls_tool_name ON tool_calls(tool_name, created_at);
```

字段说明：
- `status`：`running|ok|failed`
- `arguments_json`：建议保存脱敏后的参数（或摘要）
- `output_text`：建议保存截断文本（避免膨胀）

## 5. 写入时机

## 5.1 Task 事件
1. 收到 Telegram update 并入 `inbox`：写 `phase=ingress/queued`。
2. 从 `inbox` claim：写 `phase=claimed`。
3. 调用模型前后：写 `model_request` / `model_response`。
4. 发送 Telegram 回复成功：写 `reply_sent`。
5. 任务完成或失败：写 `task_done` / `task_failed`。

## 5.2 Tool 事件
1. 开始调用 tool：插入 `tool_calls(status=running)`。
2. 调用成功：更新为 `status=ok`，写 `output_text`、`latency_ms`、`finished_at`。
3. 调用失败：更新为 `status=failed`，写 `error`、`latency_ms`、`finished_at`。

## 6. 错误处理策略（本阶段）
1. 任务失败后：
- 写 `task_audit(task_failed)`。
- 更新 `inbox.status=failed`。
- 直接 `sendMessage` 到 Telegram 告知失败原因（可截断）。

2. 不自动重试：
- 用户手动触发新消息进行“重试”。

## 7. 运行关联字段

建议新增两个上下文字段：
1. `run_id`
- 每次 Worker 进程启动生成一个 UUID/随机字符串。
- 用于跨任务关联同一次进程生命周期。

2. `worker_instance_id`
- 由 Supervisor 注入（如 `W000023`）。
- 用于观测重启前后行为。

## 8. 查询示例

最近失败任务：
```sql
SELECT task_id, phase, error, created_at
FROM task_audit
WHERE status = 'failed'
ORDER BY id DESC
LIMIT 20;
```

某个任务的完整轨迹：
```sql
SELECT phase, status, message, error, created_at
FROM task_audit
WHERE task_id = ?
ORDER BY id;
```

最近 tool 失败：
```sql
SELECT task_id, tool_name, error, latency_ms, created_at
FROM tool_calls
WHERE status = 'failed'
ORDER BY id DESC
LIMIT 20;
```

某个 Worker 实例处理统计：
```sql
SELECT worker_instance_id, COUNT(*) AS event_count
FROM task_audit
GROUP BY worker_instance_id
ORDER BY event_count DESC;
```

## 9. 数据保留策略（建议）
1. `task_audit`、`tool_calls` 保留最近 N 天（如 30 天）。
2. 定期清理可用简单 SQL + cron（后续再做）。
3. 在清理前可导出为归档文件（可选）。

## 10. 最小实现顺序
1. 增加表：`task_audit`、`tool_calls`。
2. 给 Worker 增加 `log_task_event()`。
3. 在现有主流程接入 task 审计点。
4. 接入 Tool Call 时再接 `tool_calls` 写入。
5. 失败路径加入 Telegram 通知。

## 11. 验收标准
1. 每个已处理任务都能在 `task_audit` 查到完整 phase。
2. 任务失败时，Telegram 能收到失败通知。
3. 引入 Tool Call 后，每次工具调用都有对应记录。
4. 查询性能在近期数据量下可接受（有索引）。
