# Milestone 0 - Basic Structure
## Goal
Implement a minimal MVP: supervisor + worker + telegram + OpenAI API

## Deliverables
* sumpervisor
  - spawn worker
  - monitor worker
  - restart worker
  - record worker version/build id
* worker
  - pull Telegram messages
  - call OpenAI API
  - send Model response to Telegram
* Security
  - Secrets (e.g. API tokens) must be injected via ENVIRONMENT VARIABLES
  - During development, secrets should be injected into Docker container instead of hard-coded in Dockerfile or any startup script.

# Milestone 1 - Observability

## Goal
Ensure every run is traceable, reviewable, and explainable.

## Deliverables

* structured logging: SQLite
  - `run_id`, `conversation_id`, timestamps
  - Track Supervisor lifecycle events: Record worker versions, start/stop rationales, and upgrade metadata (initiator, run_id, git_commit, reason, etc).
  - model calls: model name, latency, input/output tokens, errors/retries
* Context Component Accounting:
  - record system, history, context, tool output tokens separately
  - record context_profile (?)

# Milestone 2 - Context Subsystem MVP

## Goal
A minimal context subsystem MVP but with proper interfaces abstraction.

## Deliverables
* A plugable Context subsystem framework (three independent interfaces)
  - context provider
  - context compressor
  - prompt assembler
* A naive implementation
  - provider: select recent N messages from SQLite
  - compressor:
    - max N messages;
    - max string characters trim;
    - tool output (skip + truncate)
  - assembler: system + recent history + user message

# Milestone 3 - Basic Control Plane

## Goal
Prevent runaways, budget overruns, and infinite loops.

## Deliverables
* Run limits:
  - `max_steps`, `max_wall_time`, `max_tokens` or `max_cost`
* Error policy:
  - bounded retries with exponential backoff
  - circuit breaker (same error N times → stop)
* Progress checks:
  - “no-progress” detection (K iterations without state change → stop)


# Milestone 4 - Tool Subsystem

## Goal
Establish a minimal, orthogonal toolset that supports bootstrapping.

## Deliverables
* Tool registry with:
  - strict input schema (typed structs / JSON schema)
  - strict output envelope (stdout/stderr, truncated flags, exit code)
  - timeouts, output size caps, pagination where needed
* Tool safety policy:
  - tool allowlist
  - risk tiers (Read / Write / Exec / Network)
  - NO **two-phase** writes
* Initial atomic tools (No need user approval):
  - ls
  - find
  - grep
  - read
  - write
  - edit
  - bash

# Milestone 5 — Self-Update Transaction

## Goal
Achieve safe self-updates.

## Deliverables
* Upgrade pipeline:
  - generate patch -> build -> test/self-check -> stage artifact -> approve -> deploy
* Artifact management:
  - store build artifacts + metadata (SHA, build time, tests passed)
* Rollback:
  - supervisor keeps last-known-good worker (N-1) and auto-reverts on failure
