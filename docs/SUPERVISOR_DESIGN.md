# Supervisor Design

## Goal
Build a reliable Telegram-controlled agent with the minimum runtime shape:
1. `autonous-supervisor` keeps the service alive.
2. `autonous-worker` handles Telegram and task execution.
3. Worker calls model APIs directly in MVP.

## Runtime Roles
1. `autonous-supervisor` (control plane)
- Start worker process.
- Restart on crash.
- Detect crash loops.
- Manage upgrade/rollback state.

2. `autonous-worker` (data plane)
- Poll Telegram updates.
- Persist conversation history to SQLite.
- Build context from recent history.
- Call model APIs and return replies to Telegram.

## Hard Boundaries
1. Telegram talks only to `autonous-worker`.
2. `autonous-supervisor` MUST NOT read, parse, store, route, or infer from prompts/messages.
3. Supervisor only handles process lifecycle and health signals.
4. Worker does not mutate supervisor policy state except through defined DB records.
5. SQLite is the runtime source of truth.

## Why This Shape
1. Minimal moving parts.
2. Easier debugging and rollback.
3. Removes local Codex CLI sandbox/process complexity.
4. Keeps future extension path open.

## SQLite Model (MVP)
Required tables:
1. `history`
- chat turns: `chat_id`, `role`, `text`, `created_at`.

2. `kv`
- runtime cursors/config, including Telegram offset.

3. `supervisor_revisions`
- `revision`, `build_ok`, `health_ok`, `promoted_at`, `failure_reason`.

4. `supervisor_state`
- key/value:
  - `current_good_rev`
  - `candidate_rev`
  - `last_failure_reason`

## Lifecycle Policy
1. Supervisor starts worker.
2. If worker exits, supervisor restarts with backoff.
3. If crash threshold is exceeded in a time window, mark failure and attempt rollback to `current_good_rev`.
4. If worker stays healthy for `stable_run_seconds`, mark current revision as good.

## Upgrade / Rollback Rules
Promote candidate when:
1. build succeeds
2. worker starts and passes health window

Rollback when:
1. build fails
2. startup timeout
3. crash loop

Rollback target:
- `current_good_rev` from SQLite.

## Security Rules
1. Run as non-root user `autonous`.
2. Never commit secrets into git-tracked files.
3. Inject API keys via runtime environment.
4. Restrict write scope to workspace/state paths.

## MVP Delivery Steps
1. Keep Telegram loop working through worker.
2. Keep supervisor process management stable.
3. Keep worker on direct model API path.
4. Preserve history + offset persistence in SQLite.
5. Verify restart and crash-loop behavior.

## Next Extension (After MVP)
1. Add an internal orchestrator module inside worker only when required.
2. Add multi-model routing inside worker with explicit policy and audit logs.
3. Split orchestrator into a dedicated process only if scale/complexity justifies it.
