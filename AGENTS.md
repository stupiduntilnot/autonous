# Autonous Agent Instructions

* 所有回复默认中文，除非我明确要求英文

This file is for coding agents running inside the container.

## 1) Project Mission
Build and evolve a Telegram-controlled autonomous coding agent with a stable runtime core.

Priority order:
1. Runtime reliability first.
2. Preserve conversation continuity.
3. Expand capabilities incrementally through small, reversible changes.

Reference docs:
- `docs/PI_MODEL.md`
- `docs/SUPERVISOR_DESIGN.md`

## 2) Runtime Paths (Important)
- Container workspace root: `/workspace`
- Persistent state directory: `/state`
- SQLite database path: `/state/agent.db`

All source code changes must happen under `/workspace`.

## 3) Runtime Shape (Current)
- Entrypoint launcher: `startup.sh`
- Supervisor binary: `src/bin/autonous-supervisor.rs`
- Worker binary: `src/bin/autonous-worker.rs`
- Build config: `Cargo.toml`
- Container image definition: `Dockerfile`

## 4) Component Boundaries (Hard Rules)
1. Telegram communicates only with `autonous-worker`.
2. `autonous-supervisor` only manages lifecycle: start/restart/health/rollback.
3. `autonous-supervisor` MUST NOT read, parse, store, route, or infer from prompts/messages.
4. Worker owns prompt/context/model API behavior.
5. SQLite is the runtime source of truth.

## 5) Required Worker Behavior
1. Poll Telegram updates.
2. Persist user/assistant turns to SQLite.
3. Build replies with recent history context.
4. Call model APIs directly (MVP path).
5. Send response back to Telegram.

## 6) Engineering Constraints
1. Use Rust for logic changes by default.
2. Keep `startup.sh` minimal (launch and handoff only).
3. Do not add Python unless explicitly requested.
4. Never write secrets into git-tracked files.
5. Avoid large rewrites unless explicitly requested.

## 7) Change Process
For each change:
1. Edit minimal files required.
2. Run `cargo check --bins`.
3. Verify runtime via container logs.
4. Keep logs/errors actionable and concise.

## 8) Safety Rules
1. Do not modify files outside `/workspace`.
2. Keep SQLite schema backward-compatible unless migration is requested.
3. Prefer conservative changes when requirements are ambiguous.

## 9) Definition of Done
A change is done when:
1. Code compiles (`cargo check --bins` passes).
2. Telegram round-trip still works.
3. History persistence still works.
4. No secret values are added to git-tracked files.
