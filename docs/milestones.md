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

## Milestone 3 — Basic Control Plane

### Goal
Prevent runaways, budget overruns, and infinite loops.

### Deliverables
- Run limits: `max_turns`, `max_wall_time`, `max_tokens` or `max_cost`
- Error policy: bounded retries with exponential backoff; circuit breaker (same error N times -> stop)
- Progress checks: "no-progress" detection (K iterations without state change -> stop)
- Instruction source abstraction: `Commander` interface（本 milestone 仅实现 Telegram）
- Model provider abstraction: `ModelProvider` interface（本 milestone 仅实现 OpenAI）
- Failure-injectable dummies for testing:
  - `DummyCommander`（测试用）
  - `DummyProvider`（测试用）
- 运行时选择策略：使用同一 binary，通过环境变量切换实现（`AUTONOUS_MODEL_PROVIDER=openai|dummy`，`AUTONOUS_COMMANDER=telegram|dummy`）。
- 可观测性要求：启动时必须将当前 `provider/source` 写入 `events`（记录在 worker 的 `process.started` payload）。

详细设计见 [milestone-3.md](./milestone-3.md)。

## Milestone 4 — Tool Subsystem

### Goal
Establish a minimal, orthogonal toolset that supports bootstrapping.

### Deliverables
- Tool registry with:
  - strict input schema (typed structs / JSON schema)
  - strict output envelope (stdout/stderr, truncated flags, exit code)
  - timeouts, output size caps, pagination where needed
- Tool safety policy: tool allowlist; risk tiers (Read / Write / Exec / Network); NO two-phase writes
- Initial atomic tools (no user approval needed): ls, find, grep, read, write, edit, bash

## Milestone 5 — Self-Update Transaction

### Goal
Achieve safe self-updates.

### Deliverables
- Upgrade pipeline: generate patch -> build -> test/self-check -> stage artifact -> approve -> deploy
- Artifact management: store build artifacts + metadata (SHA, build time, tests passed)
- Rollback: supervisor keeps last-known-good worker (N-1) and auto-reverts on failure

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
