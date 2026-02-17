#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
WORKER_BIN="${WORKER_BIN:-$ROOT_DIR/bin/worker}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

DB="$TMP_DIR/write-e2e.db"
LOG="$TMP_DIR/worker.log"
WS="$TMP_DIR/ws"
mkdir -p "$WS"

json1='{"tool_calls":[{"name":"write","arguments":{"path":"out.txt","content":"hello-from-write\n","append":false}}],"final_answer":""}'
json2='{"tool_calls":[],"final_answer":"write done"}'

b64_1="$(printf "%s" "$json1" | base64 | tr -d '\n')"
b64_2="$(printf "%s" "$json2" | base64 | tr -d '\n')"

echo "[e2e-m4-write] building worker..."
(cd "$ROOT_DIR" && CGO_ENABLED=1 go build -o "$WORKER_BIN" ./cmd/worker)

echo "[e2e-m4-write] running worker with dummy provider..."
env \
  AUTONOUS_DB_PATH="$DB" \
  WORKSPACE_DIR="$WS" \
  AUTONOUS_MODEL_PROVIDER=dummy \
  AUTONOUS_COMMANDER=dummy \
  AUTONOUS_DUMMY_COMMANDER_SCRIPT='msg:write-e2e,ok' \
  AUTONOUS_DUMMY_COMMANDER_SEND_SCRIPT='ok' \
  AUTONOUS_DUMMY_PROVIDER_SCRIPT="msgb64:$b64_1,msgb64:$b64_2" \
  AUTONOUS_CONTROL_MAX_TURNS=2 \
  AUTONOUS_CONTROL_MAX_WALL_TIME_SECONDS=120 \
  AUTONOUS_CONTROL_MAX_RETRIES=1 \
  AUTONOUS_TOOL_ALLOWED_ROOTS="$WS" \
  TG_DROP_PENDING=false \
  TG_TIMEOUT=0 \
  TG_SLEEP_SECONDS=0 \
  "$WORKER_BIN" >"$LOG" 2>&1 &
pid=$!

sleep 6
kill "$pid" 2>/dev/null || true
wait "$pid" 2>/dev/null || true

tool_done="$(sqlite3 "$DB" "SELECT COUNT(*) FROM events WHERE event_type='tool_call.completed' AND payload LIKE '%\"tool_name\":\"write\"%';")"
reply_done="$(sqlite3 "$DB" "SELECT COUNT(*) FROM history WHERE role='assistant' AND text='write done';")"
status="$(sqlite3 "$DB" "SELECT status FROM inbox ORDER BY id DESC LIMIT 1;")"
content="$(cat "$WS/out.txt" 2>/dev/null || true)"

if [[ "$tool_done" -lt 1 ]]; then
  echo "[e2e-m4-write][FAIL] missing tool_call.completed for write"
  cat "$LOG"
  exit 1
fi
if [[ "$reply_done" -lt 1 ]]; then
  echo "[e2e-m4-write][FAIL] missing final assistant reply"
  cat "$LOG"
  exit 1
fi
if [[ "$status" != "done" ]]; then
  echo "[e2e-m4-write][FAIL] inbox latest status=$status"
  cat "$LOG"
  exit 1
fi
if [[ "$content" != "hello-from-write" ]]; then
  echo "[e2e-m4-write][FAIL] unexpected written content: $content"
  cat "$LOG"
  exit 1
fi

echo "[e2e-m4-write] passed"
