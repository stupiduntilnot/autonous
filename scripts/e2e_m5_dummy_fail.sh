#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "[FAIL] sqlite3 not found"
  exit 1
fi

RUN_ID="m5f-$(date +%Y%m%d-%H%M%S)"
TMP_DIR="${TMPDIR:-/tmp}/autonous-${RUN_ID}"
mkdir -p "$TMP_DIR/bin" "$TMP_DIR/artifacts"

DB_PATH="$TMP_DIR/agent.db"
ACTIVE_BIN="$TMP_DIR/worker.current"
INITIAL_WORKER="$TMP_DIR/initial-worker"
SUP_LOG="$TMP_DIR/supervisor.log"
TX_ID="tx-${RUN_ID}"

go build -o "$TMP_DIR/bin/supervisor" ./cmd/supervisor
go build -o "$INITIAL_WORKER" ./cmd/worker
ln -sf "$INITIAL_WORKER" "$ACTIVE_BIN"

export AUTONOUS_DB_PATH="$DB_PATH"
export WORKSPACE_DIR="$ROOT_DIR"
export WORKER_BIN="$ACTIVE_BIN"
export AUTONOUS_UPDATE_ACTIVE_BIN="$ACTIVE_BIN"
export AUTONOUS_UPDATE_ARTIFACT_ROOT="$TMP_DIR/artifacts"
export AUTONOUS_UPDATE_TEST_CMD="false"
export AUTONOUS_UPDATE_SELF_CHECK_CMD=""
export AUTONOUS_UPDATE_PIPELINE_TIMEOUT_SECONDS=120
export AUTONOUS_MODEL_PROVIDER=dummy
export AUTONOUS_COMMANDER=dummy
export AUTONOUS_DUMMY_PROVIDER_SCRIPT="ok"
export AUTONOUS_DUMMY_COMMANDER_SCRIPT="msg:update stage ${TX_ID},ok"
export AUTONOUS_DUMMY_COMMANDER_SEND_SCRIPT="ok"
export TG_DROP_PENDING=false

"$TMP_DIR/bin/supervisor" >"$SUP_LOG" 2>&1 &
SUP_PID=$!
trap 'kill "$SUP_PID" >/dev/null 2>&1 || true' EXIT

deadline=$((SECONDS + 60))
status=""
while (( SECONDS < deadline )); do
  status="$(sqlite3 "$DB_PATH" "SELECT status FROM artifacts WHERE tx_id='${TX_ID}' LIMIT 1;" 2>/dev/null || true)"
  if [[ "$status" == "test_failed" ]]; then
    break
  fi
  sleep 1
done

if [[ "$status" != "test_failed" ]]; then
  echo "[FAIL] expected test_failed; got='${status}'"
  tail -n 120 "$SUP_LOG" || true
  exit 1
fi

deploy_cnt="$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM events WHERE event_type='update.deploy.completed';" 2>/dev/null || echo 0)"
if [[ "$deploy_cnt" -ne 0 ]]; then
  echo "[FAIL] deploy should not happen on test_failed, got deploy_completed=${deploy_cnt}"
  exit 1
fi

echo "[PASS] m5 failure e2e succeeded"
echo "[INFO] db=$DB_PATH"
echo "[INFO] log=$SUP_LOG"
