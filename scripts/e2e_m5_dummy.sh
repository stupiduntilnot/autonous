#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "[FAIL] sqlite3 not found"
  exit 1
fi

RUN_ID="m5-$(date +%Y%m%d-%H%M%S)"
TMP_DIR="${TMPDIR:-/tmp}/autonous-${RUN_ID}"
mkdir -p "$TMP_DIR/bin" "$TMP_DIR/artifacts"

DB_PATH="$TMP_DIR/agent.db"
ACTIVE_BIN="$TMP_DIR/worker.current"
INITIAL_WORKER="$TMP_DIR/initial-worker"
SUP_LOG="$TMP_DIR/supervisor.log"

echo "[INFO] run_id=$RUN_ID"
echo "[INFO] tmp_dir=$TMP_DIR"

go build -o "$TMP_DIR/bin/supervisor" ./cmd/supervisor
go build -o "$INITIAL_WORKER" ./cmd/worker
ln -sf "$INITIAL_WORKER" "$ACTIVE_BIN"

TX_ID="tx-${RUN_ID}"
export AUTONOUS_DB_PATH="$DB_PATH"
export WORKSPACE_DIR="$ROOT_DIR"
export WORKER_BIN="$ACTIVE_BIN"
export AUTONOUS_UPDATE_ACTIVE_BIN="$ACTIVE_BIN"
export AUTONOUS_UPDATE_ARTIFACT_ROOT="$TMP_DIR/artifacts"
export AUTONOUS_UPDATE_TEST_CMD="true"
export AUTONOUS_UPDATE_SELF_CHECK_CMD=""
export AUTONOUS_UPDATE_PIPELINE_TIMEOUT_SECONDS=300

export AUTONOUS_MODEL_PROVIDER=dummy
export AUTONOUS_COMMANDER=dummy
export AUTONOUS_DUMMY_PROVIDER_SCRIPT="ok"
export AUTONOUS_DUMMY_COMMANDER_SCRIPT="msg:update stage ${TX_ID},msg:approve ${TX_ID},ok"
export AUTONOUS_DUMMY_COMMANDER_SEND_SCRIPT="ok"
export TG_DROP_PENDING=false

"$TMP_DIR/bin/supervisor" >"$SUP_LOG" 2>&1 &
SUP_PID=$!
trap 'kill "$SUP_PID" >/dev/null 2>&1 || true' EXIT

deadline=$((SECONDS + 90))
status=""
while (( SECONDS < deadline )); do
  status="$(sqlite3 "$DB_PATH" "SELECT status FROM artifacts WHERE tx_id='${TX_ID}' LIMIT 1;" 2>/dev/null || true)"
  if [[ "$status" == "deployed_unstable" || "$status" == "promoted" ]]; then
    break
  fi
  sleep 1
done

if [[ "$status" != "deployed_unstable" && "$status" != "promoted" ]]; then
  echo "[FAIL] timeout waiting deployed status; got='$status'"
  echo "[INFO] supervisor log:"
  tail -n 120 "$SUP_LOG" || true
  exit 1
fi

expected="$TMP_DIR/artifacts/${TX_ID}/worker"
resolved_active="$(readlink "$ACTIVE_BIN" || true)"
resolved_expected="$expected"
resolved_active="$(cd "$(dirname "$resolved_active")" && pwd -P)/$(basename "$resolved_active")"
resolved_expected="$(cd "$(dirname "$resolved_expected")" && pwd -P)/$(basename "$resolved_expected")"

if [[ "$resolved_active" != "$resolved_expected" ]]; then
  echo "[FAIL] active bin mismatch"
  echo "  active:   $resolved_active"
  echo "  expected: $resolved_expected"
  exit 1
fi

count_build="$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM events WHERE event_type='update.build.completed';")"
count_approve="$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM events WHERE event_type='update.approved';")"
count_deploy="$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM events WHERE event_type='update.deploy.completed';")"

if [[ "$count_build" -lt 1 || "$count_approve" -lt 1 || "$count_deploy" -lt 1 ]]; then
  echo "[FAIL] missing expected events build=$count_build approve=$count_approve deploy=$count_deploy"
  exit 1
fi

echo "[PASS] m5 dummy e2e succeeded"
echo "[INFO] db=$DB_PATH"
echo "[INFO] log=$SUP_LOG"
