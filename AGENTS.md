# Autonous Agent Instructions

This file is for coding agents running inside the container.

## 1) Project Mission
Build and evolve a Telegram-controlled autonomous coding agent.

Priority order:
1. Runtime reliability first.
2. Preserve conversation continuity.
3. Incrementally expand capabilities through small code changes.

## 2) Runtime Paths (Important)
- Container workspace root: `/workspace`
- Host repo mounted to `/workspace`
- Persistent state directory: `/state`
- SQLite database path: `/state/agent.db`

All source code changes must happen under `/workspace`.

## 3) Core Files
- Main loop: `src/main.rs`
- Startup supervisor: `startup.sh`
- Build config: `Cargo.toml`
- Container image definition: `Dockerfile`
- Design notes/blog: `docs/`

## 4) Required Behavior
The runtime must:
1. Poll Telegram updates.
2. Persist user/assistant turns to SQLite.
3. Build prompts with recent history context.
4. Call Codex CLI in workspace-write mode.
5. Send response back to Telegram.

## 5) Engineering Constraints
1. Use Rust for logic changes by default.
2. Keep `startup.sh` minimal (process launch/supervision only).
3. Do not add Python unless explicitly requested.
4. Never write secrets into repository files.
5. Avoid large rewrites unless explicitly requested.

## 6) Change Process
For each change:
1. Edit minimal files required.
2. Run `cargo check`.
3. Verify runtime via container logs.
4. Keep logs/errors actionable and concise.

## 7) Safety Rules
1. Do not modify files outside `/workspace`.
2. Keep SQLite schema backward-compatible unless migration is requested.
3. If uncertain, prefer conservative changes and clear error messages.

## 8) Definition of Done
A change is done when:
1. Code compiles (`cargo check` passes).
2. Telegram round-trip still works.
3. History persistence still works.
4. No secret values are added to git-tracked files.
