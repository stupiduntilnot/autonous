# Milestones

## [DONE] Milestone 0 — Basic Structure

### Goal
Implement a minimal MVP: supervisor + worker + telegram + OpenAI API

### Deliverables
- [DONE] Supervisor functionalities:
  - [DONE] spawn worker
  - [DONE] monitor worker
  - [DONE] restart worker
  - [DONE] record worker version/build id
- [DONE] Worker functionalities:
  - [DONE] pull Telegram messages
  - [DONE] call OpenAI API
  - [DONE] send Model response to Telegram
- [DONE] Security requirements:
  - [DONE] Secrets (e.g. API tokens) must be injected via ENVIRONMENT VARIABLES
  - [DONE] During development, secrets should be injected into Docker container instead of hard-coded in Dockerfile or any startup script.

## [DONE] Milestone 1 — Observability

### Goal
Ensure every run is traceable, reviewable, and explainable.

### Deliverables
- [DONE] Unified event log (`events` table):
  - [DONE] Flat storage with hierarchical reconstruction via `parent_id`
  - [DONE] Infrastructure events: process lifecycle, worker spawn/exit, crash loop, rollback
  - [DONE] Agent execution events (3-layer model aligned with pi-mono): Agent -> Turn -> ToolCall
- [DONE] Model Adapter interface:
  - [DONE] Common `CompletionResponse` with `input_tokens`, `output_tokens`
  - [DONE] Token usage extracted from provider API response (not tokenizer)
- [DONE] State derivation from existing data:
  - [DONE] `telegram_offset` from `inbox` table
  - [DONE] `current_good_rev` from `revision.promoted` event
  - [DONE] `worker_instance_seq` from `worker.spawned` event count

详细设计见 [milestone-1.md](./milestone-1.md)。

## Milestone 2 — Context Subsystem MVP

### Goal
A minimal context subsystem MVP but with proper interfaces abstraction.

### Deliverables
- Three independent interfaces: ContextProvider, ContextCompressor, PromptAssembler
- Provider-agnostic `context.Message` type (不依赖任何 LLM provider 类型)
- Naive implementations:
  - provider: select recent N messages from SQLite
  - compressor: max N messages
  - assembler: system + history + user message
- Compression events logged to `events` table for observability

详细设计见 [milestone-2.md](./milestone-2.md)。

## [DONE] Milestone 3 — Basic Control Plane

### Goal
Prevent runaways, budget overruns, and infinite loops.

### Deliverables
- [DONE] Run limits: `max_turns`, `max_wall_time`, `max_tokens`（当前 `max_tokens` 使用内置默认值）
- [DONE] Error policy: bounded retries + exponential backoff + circuit breaker
- [DONE] Progress checks: no-progress detection + `progress.stalled` event
- [DONE] Instruction source abstraction: `Commander`（`telegram` + `dummy`）
- [DONE] Model provider abstraction: `ModelProvider`（`openai` + `dummy`）
- [DONE] 同一 binary 运行时选择：`AUTONOUS_MODEL_PROVIDER` / `AUTONOUS_COMMANDER`
- [DONE] 可观测性：worker `process.started` payload 记录 `provider/source`
- [DONE] Failure-injection tests:
  - [DONE] unit/integration tests（`internal/control` + `cmd/worker`）
  - [DONE] dummy E2E script（`scripts/e2e_m3_dummy.sh`）

详细设计见 [milestone-3.md](./milestone-3.md)。

## Milestone 4 — Tool Subsystem

### Goal
Establish a minimal, orthogonal toolset that supports bootstrapping.

### Deliverables
- Tool registry with:
  - strict input schema (typed structs / JSON schema)
  - strict output envelope (stdout/stderr, truncated flags, exit code)
  - timeouts, output size caps, pagination where needed
- Tool safety policy: tool allowlist; NO two-phase writes
- Initial atomic tools (no user approval needed):
  - phase 1: `ls` only (用于先打通机制与 E2E)
  - phase 2: `find`, `grep`, `read`, `write`, `edit`, `bash`（逐个工具、逐个任务推进）

## Milestone 5 — Self-Update Transaction

### Goal
Achieve safe self-updates.

### Deliverables
- Upgrade pipeline: generate patch -> build -> test/self-check -> stage artifact -> approve -> deploy
- Artifact management: store build artifacts + metadata (SHA, build time, tests passed)
- Rollback: supervisor keeps last-known-good worker (N-1) and auto-reverts on failure
- Manual rollback command: `rollback <tx_id>` (direct command path, no LLM)

详细设计见 [milestone-5.md](./milestone-5.md)。

---

## TODO

待所有已定义 milestone 完成后，整理为新的 milestone：

- 单条消息字符截断：`Compressor` 对超长单条消息截断
- Token 预算压缩：`Compressor` 基于 `chars/4` 估算做 token 级裁剪
- LLM 摘要压缩：`Compressor` 调用 LLM 对旧消息生成摘要
- Tool output 处理：`Compressor` 识别 tool call 结果并单独截断/跳过
- 多 provider 支持：各 LLM provider 实现自己的 adapter
- 语义检索：Provider 基于向量相似度检索相关历史，而非简单时间窗口
- Milestone 3 后续可配置化（当前先使用内置默认值）：
  - `AUTONOUS_CONTROL_MAX_TOKENS`
  - `AUTONOUS_CONTROL_RETRY_BASE_SECONDS`
  - `AUTONOUS_CONTROL_RETRY_MAX_SECONDS`
  - `AUTONOUS_CONTROL_CIRCUIT_THRESHOLD`
  - `AUTONOUS_CONTROL_CIRCUIT_COOLDOWN_SECONDS`
  - `AUTONOUS_CONTROL_NO_PROGRESS_K`
- Milestone 3 后续验证（在多轮 agent loop 落地后）：
  - 多轮场景下 `max_turns` 语义与事件完整性回归
- Tool allowlist 后续能力：
  - 支持将用户批准的路径动态加入 `AUTONOUS_TOOL_ALLOWED_ROOTS`（含审计与过期策略）
- Tool safety 后续能力：
  - 引入 `risk_tier` 分层策略（Read/Write/Exec/Network）并接入审计与策略控制
- Tool reliability 后续能力：
  - 引入 dead-letter 机制（达到重试上限后进入隔离队列并支持人工/自动重放）
- Tool native implementation
  - 有些tool用Golang原生实现比调用cli工具好。应该替换现在的实现。
