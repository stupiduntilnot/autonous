#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
WORKER_BIN="${WORKER_BIN:-$ROOT_DIR/bin/worker}"

build_worker() {
  echo "[e2e-m3] building worker binary..."
  (cd "$ROOT_DIR" && CGO_ENABLED=1 go build -o "$WORKER_BIN" ./cmd/worker)
}

kill_if_running() {
  local pid="$1"
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
}

run_worker_for() {
  local seconds="$1"
  shift

  "$@" >"$RUN_LOG" 2>&1 &
  local pid=$!
  sleep "$seconds"
  kill_if_running "$pid"
}

sql_scalar() {
  local db="$1"
  local q="$2"
  sqlite3 "$db" "$q"
}

assert_ge() {
  local got="$1"
  local want="$2"
  local msg="$3"
  if (( got < want )); then
    echo "[e2e-m3][FAIL] $msg: got=$got want>=$want"
    echo "---- worker log ----"
    cat "$RUN_LOG"
    exit 1
  fi
}

assert_eq() {
  local got="$1"
  local want="$2"
  local msg="$3"
  if [[ "$got" != "$want" ]]; then
    echo "[e2e-m3][FAIL] $msg: got=$got want=$want"
    echo "---- worker log ----"
    cat "$RUN_LOG"
    exit 1
  fi
}

run_case_retry_exhausted() {
  echo "[e2e-m3] case: retry.exhausted"
  local db="$TMP_DIR/retry.db"
  RUN_LOG="$TMP_DIR/retry.log"
  run_worker_for 8 \
    env \
      AUTONOUS_DB_PATH="$db" \
      AUTONOUS_MODEL_PROVIDER=dummy \
      AUTONOUS_COMMANDER=dummy \
      AUTONOUS_DUMMY_COMMANDER_SCRIPT='msg:retry-case,ok' \
      AUTONOUS_DUMMY_COMMANDER_SEND_SCRIPT='ok' \
      AUTONOUS_DUMMY_PROVIDER_SCRIPT='err:provider_api' \
      AUTONOUS_CONTROL_MAX_TURNS=1 \
      AUTONOUS_CONTROL_MAX_WALL_TIME_SECONDS=120 \
      AUTONOUS_CONTROL_MAX_RETRIES=2 \
      TG_DROP_PENDING=false \
      TG_TIMEOUT=0 \
      TG_SLEEP_SECONDS=0 \
      WORKER_SYSTEM_PROMPT='test prompt' \
      "$WORKER_BIN"

  local exhausted
  exhausted="$(sql_scalar "$db" "SELECT COUNT(*) FROM events WHERE event_type='retry.exhausted';")"
  assert_ge "$exhausted" 1 "retry.exhausted should exist"

  local attempts
  attempts="$(sql_scalar "$db" "SELECT COALESCE(MAX(attempts),0) FROM inbox;")"
  assert_ge "$attempts" 3 "attempts should reach max_retries+1"
}

run_case_wall_time_limit() {
  echo "[e2e-m3] case: max_wall_time -> control.limit_reached"
  local db="$TMP_DIR/wall.db"
  RUN_LOG="$TMP_DIR/wall.log"
  run_worker_for 4 \
    env \
      AUTONOUS_DB_PATH="$db" \
      AUTONOUS_MODEL_PROVIDER=dummy \
      AUTONOUS_COMMANDER=dummy \
      AUTONOUS_DUMMY_COMMANDER_SCRIPT='msg:wall-case,ok' \
      AUTONOUS_DUMMY_COMMANDER_SEND_SCRIPT='ok' \
      AUTONOUS_DUMMY_PROVIDER_SCRIPT='sleep:1500' \
      AUTONOUS_CONTROL_MAX_TURNS=1 \
      AUTONOUS_CONTROL_MAX_WALL_TIME_SECONDS=1 \
      AUTONOUS_CONTROL_MAX_RETRIES=1 \
      TG_DROP_PENDING=false \
      TG_TIMEOUT=0 \
      TG_SLEEP_SECONDS=0 \
      WORKER_SYSTEM_PROMPT='test prompt' \
      "$WORKER_BIN"

  local count
  count="$(sql_scalar "$db" "SELECT COUNT(*) FROM events WHERE event_type='control.limit_reached' AND json_extract(payload,'$.limit_type')='max_wall_time_seconds';")"
  assert_ge "$count" 1 "wall time limit event should exist"
}

run_case_progress_stalled() {
  echo "[e2e-m3] case: progress.stalled"
  local db="$TMP_DIR/stalled.db"
  RUN_LOG="$TMP_DIR/stalled.log"
  run_worker_for 8 \
    env \
      AUTONOUS_DB_PATH="$db" \
      AUTONOUS_MODEL_PROVIDER=dummy \
      AUTONOUS_COMMANDER=dummy \
      AUTONOUS_DUMMY_COMMANDER_SCRIPT='msg:stalled-case,ok' \
      AUTONOUS_DUMMY_COMMANDER_SEND_SCRIPT='ok' \
      AUTONOUS_DUMMY_PROVIDER_SCRIPT='err:provider_api' \
      AUTONOUS_CONTROL_MAX_TURNS=1 \
      AUTONOUS_CONTROL_MAX_WALL_TIME_SECONDS=120 \
      AUTONOUS_CONTROL_MAX_RETRIES=5 \
      TG_DROP_PENDING=false \
      TG_TIMEOUT=0 \
      TG_SLEEP_SECONDS=0 \
      WORKER_SYSTEM_PROMPT='test prompt' \
      "$WORKER_BIN"

  local stalled
  stalled="$(sql_scalar "$db" "SELECT COUNT(*) FROM events WHERE event_type='progress.stalled';")"
  assert_ge "$stalled" 1 "progress.stalled should exist"
}

run_case_circuit_poll_failures() {
  echo "[e2e-m3] case: circuit opened/half_open/closed from commander failures"
  local db="$TMP_DIR/circuit.db"
  RUN_LOG="$TMP_DIR/circuit.log"
  run_worker_for 38 \
    env \
      AUTONOUS_DB_PATH="$db" \
      AUTONOUS_MODEL_PROVIDER=dummy \
      AUTONOUS_COMMANDER=dummy \
      AUTONOUS_DUMMY_COMMANDER_SCRIPT='err:command_source_api,err:command_source_api,err:command_source_api,err:command_source_api,err:command_source_api,ok' \
      AUTONOUS_DUMMY_COMMANDER_SEND_SCRIPT='ok' \
      AUTONOUS_DUMMY_PROVIDER_SCRIPT='ok' \
      AUTONOUS_CONTROL_MAX_TURNS=1 \
      AUTONOUS_CONTROL_MAX_WALL_TIME_SECONDS=120 \
      AUTONOUS_CONTROL_MAX_RETRIES=2 \
      TG_DROP_PENDING=false \
      TG_TIMEOUT=0 \
      TG_SLEEP_SECONDS=0 \
      WORKER_SYSTEM_PROMPT='test prompt' \
      "$WORKER_BIN"

  local opened half_open closed
  opened="$(sql_scalar "$db" "SELECT COUNT(*) FROM events WHERE event_type='circuit.opened';")"
  half_open="$(sql_scalar "$db" "SELECT COUNT(*) FROM events WHERE event_type='circuit.half_open';")"
  closed="$(sql_scalar "$db" "SELECT COUNT(*) FROM events WHERE event_type='circuit.closed';")"
  assert_ge "$opened" 1 "circuit.opened should exist"
  assert_ge "$half_open" 1 "circuit.half_open should exist"
  assert_ge "$closed" 1 "circuit.closed should exist"
}

main() {
  build_worker
  TMP_DIR="$(mktemp -d)"
  trap 'rm -rf "$TMP_DIR"' EXIT

  run_case_retry_exhausted
  run_case_wall_time_limit
  run_case_progress_stalled
  run_case_circuit_poll_failures

  echo "[e2e-m3] all dummy failure-injection cases passed"
}

main "$@"
