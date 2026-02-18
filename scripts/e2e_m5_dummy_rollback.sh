#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "[FAIL] sqlite3 not found"
  exit 1
fi

RUN_ID="m5r-$(date +%Y%m%d-%H%M%S)"
TMP_DIR="${TMPDIR:-/tmp}/autonous-${RUN_ID}"
mkdir -p "$TMP_DIR/bin" "$TMP_DIR/artifacts/base" "$TMP_DIR/artifacts/new" "$TMP_DIR/state-bin"

DB_PATH="$TMP_DIR/agent.db"
ACTIVE_BIN="$TMP_DIR/state-bin/worker.current"
SUP_LOG="$TMP_DIR/supervisor.log"
BASE_TX="tx-base-${RUN_ID}"
NEW_TX="tx-new-${RUN_ID}"
BASE_BIN="$TMP_DIR/artifacts/base/worker"
NEW_BIN="$TMP_DIR/artifacts/new/worker"

go build -o "$TMP_DIR/bin/supervisor" ./cmd/supervisor
go build -o "$BASE_BIN" ./cmd/worker
cat >"$NEW_BIN" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$NEW_BIN"
ln -sf "$NEW_BIN" "$ACTIVE_BIN"

sqlite3 "$DB_PATH" <<SQL
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY,
  timestamp INTEGER NOT NULL DEFAULT (unixepoch()),
  parent_id INTEGER,
  event_type TEXT NOT NULL,
  payload TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_parent_id ON events(parent_id);
CREATE TABLE IF NOT EXISTS inbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  update_id INTEGER NOT NULL UNIQUE,
  chat_id INTEGER NOT NULL,
  text TEXT NOT NULL,
  message_date INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'queued',
  attempts INTEGER NOT NULL DEFAULT 0,
  locked_at INTEGER,
  error TEXT,
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_inbox_status_id ON inbox(status, id);
CREATE TABLE IF NOT EXISTS history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_id INTEGER NOT NULL,
  role TEXT NOT NULL,
  text TEXT NOT NULL,
  created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE IF NOT EXISTS artifacts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tx_id TEXT NOT NULL UNIQUE,
  base_tx_id TEXT,
  bin_path TEXT NOT NULL,
  sha256 TEXT,
  git_revision TEXT,
  build_started_at INTEGER,
  build_finished_at INTEGER,
  test_summary TEXT,
  self_check_summary TEXT,
  approval_chat_id INTEGER,
  approval_message_id INTEGER,
  deploy_started_at INTEGER,
  deploy_finished_at INTEGER,
  status TEXT NOT NULL,
  last_error TEXT,
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_artifacts_status_updated_at ON artifacts(status, updated_at);
CREATE INDEX IF NOT EXISTS idx_artifacts_base_tx_id ON artifacts(base_tx_id);
INSERT INTO artifacts (tx_id, base_tx_id, bin_path, status) VALUES
  ('${BASE_TX}', NULL, '${BASE_BIN}', 'promoted'),
  ('${NEW_TX}', '${BASE_TX}', '${NEW_BIN}', 'deployed_unstable');
SQL

export AUTONOUS_DB_PATH="$DB_PATH"
export WORKSPACE_DIR="$ROOT_DIR"
export WORKER_BIN="$ACTIVE_BIN"
export AUTONOUS_UPDATE_ACTIVE_BIN="$ACTIVE_BIN"
export AUTONOUS_UPDATE_ARTIFACT_ROOT="$TMP_DIR/artifacts"
export AUTONOUS_MODEL_PROVIDER=dummy
export AUTONOUS_COMMANDER=dummy
export AUTONOUS_DUMMY_PROVIDER_SCRIPT="ok"
export AUTONOUS_DUMMY_COMMANDER_SCRIPT="ok"
export AUTONOUS_DUMMY_COMMANDER_SEND_SCRIPT="ok"
export TG_DROP_PENDING=false
export SUPERVISOR_CRASH_THRESHOLD=1
export SUPERVISOR_CRASH_WINDOW_SECONDS=60
export SUPERVISOR_STABLE_RUN_SECONDS=1000
export SUPERVISOR_RESTART_DELAY_SECONDS=1
export SUPERVISOR_AUTO_ROLLBACK=true

"$TMP_DIR/bin/supervisor" >"$SUP_LOG" 2>&1 &
SUP_PID=$!
trap 'kill "$SUP_PID" >/dev/null 2>&1 || true' EXIT

deadline=$((SECONDS + 40))
status=""
while (( SECONDS < deadline )); do
  status="$(sqlite3 "$DB_PATH" "SELECT status FROM artifacts WHERE tx_id='${NEW_TX}' LIMIT 1;" 2>/dev/null || true)"
  if [[ "$status" == "rolled_back" ]]; then
    break
  fi
  sleep 1
done

if [[ "$status" != "rolled_back" ]]; then
  echo "[FAIL] expected rolled_back; got='${status}'"
  tail -n 120 "$SUP_LOG" || true
  exit 1
fi

resolved_active="$(readlink "$ACTIVE_BIN" || true)"
resolved_active="$(cd "$(dirname "$resolved_active")" && pwd -P)/$(basename "$resolved_active")"
resolved_base="$(cd "$(dirname "$BASE_BIN")" && pwd -P)/$(basename "$BASE_BIN")"
if [[ "$resolved_active" != "$resolved_base" ]]; then
  echo "[FAIL] active bin not rolled back to base"
  echo "  active=$resolved_active"
  echo "  base=$resolved_base"
  exit 1
fi

echo "[PASS] m5 rollback e2e succeeded"
echo "[INFO] db=$DB_PATH"
echo "[INFO] log=$SUP_LOG"
