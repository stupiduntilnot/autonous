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
- Token budget driven compression (chars/4 estimation, aligned with pi-mono)
- Naive implementations:
  - provider: select recent N messages from SQLite
  - compressor: token budget trim + per-message character truncation
  - assembler: system + history + user message
- Compression events logged to `events` table for observability

详细设计见 [milestone-2.md](./milestone-2.md)。

## Milestone 3 — Basic Control Plane

### Goal
Prevent runaways, budget overruns, and infinite loops.

### Deliverables
- Run limits: `max_steps`, `max_wall_time`, `max_tokens` or `max_cost`
- Error policy: bounded retries with exponential backoff; circuit breaker (same error N times -> stop)
- Progress checks: "no-progress" detection (K iterations without state change -> stop)

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
