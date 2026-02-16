# Tool Call 设计文档（Autonous）

更新时间：2026-02-15

## 1. 结论先说

问题：下一步做 Tool Call 合理吗？
结论：合理，但建议按 **P0 -> P1** 顺序推进。

1. `P0`（先做）
- queue reliability（retry/backoff/dead-letter）
- execution safety boundary（workspace allowlist + command policy）
- observability（task/tool audit）

2. `P1`（再做）
- Tool Call MVP（`read` / `write` / `bash`）

原因：
- Tool Call 会直接引入本地读写和命令执行风险。
- 没有 P0 的情况下，错误恢复和安全边界不足，风险高于收益。

## 2. 参考 `pi-mono` 的关键实现点

参考位置：
- `pi-mono/packages/mom/src/tools/*.ts`
- `pi-mono/packages/mom/src/slack.ts`
- `pi-mono/packages/agent/src/agent-loop.ts`

借鉴点：
1. per-channel sequential queue（顺序执行，避免并发乱序）
2. tool registry（`read/bash/edit/write`）
3. output truncation（line/byte 双阈值）
4. append-only history + runtime context 分层
5. 明确的错误文本与可继续执行提示

## 3. 目标与非目标

## 3.1 目标
1. 在当前 `Supervisor + Worker + SQLite inbox` 之上支持 tool-augmented execution。
2. 保持 single consumer 串行语义。
3. 工具执行可审计、可回放、可限制。

## 3.2 非目标（当前阶段）
1. 不做 multi-worker parallel tool execution。
2. 不做复杂 orchestration process 拆分。
3. 不做跨容器分布式调度。

## 4. 目标架构（MVP）

`Telegram -> inbox(queue) -> Worker(task loop) -> Model -> ToolRunner -> Model -> Telegram`

关键点：
1. Worker 一次只处理一个 task。
2. Model 可返回 tool call 请求。
3. Worker 执行工具后，把 tool result 回填给模型，直到生成 final answer。
4. 全过程写入 SQLite audit。

## 5. 数据模型扩展（SQLite）

在现有 `inbox/history/kv` 基础上新增：

1. `tool_calls`
- `id`
- `task_id`（关联 `inbox.id`）
- `tool_name`
- `arguments_json`
- `status`（`queued|running|done|failed`）
- `output_text`
- `error`
- `started_at`, `finished_at`

2. `task_audit`
- `task_id`
- `phase`（`ingress|model|tool|reply|done|failed`）
- `message`
- `created_at`

作用：
- 让任务轨迹和工具执行轨迹可查询。

## 6. Tool Runtime 设计

## 6.1 Tool Registry（MVP）
第一批仅 3 个：
1. `read`
2. `write`
3. `bash`

后续再加：
- `edit`（exact replace）
- `glob` / `rg`
- `attach`（文件回传）

## 6.2 Tool Contract
统一接口：
- 输入：`{name, arguments_json, task_id}`
- 输出：`{ok, text, meta}`

错误规范：
- 业务错误（参数错/权限错）返回可读错误文本，不 panic。
- 系统错误写 `tool_calls.error`，并回填模型决定下一步。

## 6.3 安全边界（必须）
1. path allowlist：仅允许 `/workspace`、`/state` 下可控路径。
2. command denylist：拒绝明显 destructive 命令（如 `rm -rf /`）。
3. timeout：每个 tool call 必须有超时。
4. output truncation：默认 line/byte 限制（参考 `pi-mono` 双阈值设计）。
5. secret redaction：日志中掩码常见 key pattern。

## 7. Worker 执行状态机

单 task 状态机：
1. `queued`
2. `in_progress`
3. `model_round`
4. `tool_round`（可重复）
5. `reply_sent`
6. `done | failed`

规则：
1. 任意步骤失败写审计并可重试。
2. 达到最大重试进入 dead-letter。

## 8. 优先级建议（最终）

## P0（高于 Tool Call 的前置）
1. `retry/backoff`（task 级）
2. `dead-letter`（防无限失败重试）
3. `audit tables`（可观测）
4. `tool safety boundary`（allowlist + timeout + truncation）

## P1（Tool Call MVP）
1. `read` + `write`
2. `bash`
3. model-tool loop（最多 `N` 轮，防死循环）

## P2（增强）
1. `edit` + 更强 patch 语义
2. richer policy（按 chat/user 配额与权限）
3. metrics dashboard / ops commands

## 9. 推荐实施顺序（可直接执行）

1. 增加 `tool_calls` / `task_audit` 表。
2. 给现有 `inbox` 增加 retry 字段策略（或复用 `attempts` + backoff）。
3. 实现 ToolRunner skeleton（先不接模型 tool call）。
4. 实现 `read`、`write`。
5. 接入 model tool-call protocol（单轮）。
6. 增加 `bash` 与 timeout/truncation。
7. 加入 max tool rounds + dead-letter。

## 10. 默认参数建议

1. `TG_PENDING_WINDOW_SECONDS=600`
2. `TG_PENDING_MAX_MESSAGES=50`
3. `TASK_MAX_ATTEMPTS=3`
4. `TASK_RETRY_BASE_MS=2000`
5. `TOOL_TIMEOUT_SECONDS=30`
6. `TOOL_MAX_OUTPUT_LINES=2000`
7. `TOOL_MAX_OUTPUT_BYTES=51200`

## 11. 验收标准（MVP）

1. 连续发送多条 Telegram 消息，按顺序处理并回复。
2. 工具调用全链路可在 SQLite 查询。
3. 工具失败不会导致 Worker 崩溃。
4. 无权限路径/危险命令被拒绝并返回清晰错误。
5. Supervisor 重启后可继续处理未完成任务。
