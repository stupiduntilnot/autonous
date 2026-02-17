#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SEND_SCRIPT="$ROOT_DIR/scripts/send_test_message.sh"

CONTAINER="${CONTAINER:-autonous-agent}"
DB_IN_CONTAINER="${DB_IN_CONTAINER:-/state/agent.db}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-180}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-2}"
KEEP_ARTIFACTS="${KEEP_ARTIFACTS:-0}"
RESET_DB_BEFORE_RUN="${RESET_DB_BEFORE_RUN:-1}"
MAX_ROUND2_ATTEMPTS="${MAX_ROUND2_ATTEMPTS:-3}"

if [[ ! -x "$SEND_SCRIPT" ]]; then
  echo "[e2e-m4-multi][FAIL] missing send script: $SEND_SCRIPT"
  exit 1
fi

if ! docker ps --format '{{.Names}}' | grep -qx "$CONTAINER"; then
  echo "[e2e-m4-multi][FAIL] container not running: $CONTAINER"
  exit 1
fi

db_query() {
  local sql="$1"
  docker exec "$CONTAINER" sqlite3 "$DB_IN_CONTAINER" "$sql"
}

dump_debug() {
  local marker="$1"
  local root_path="$2"
  echo "[e2e-m4-multi][DEBUG] marker=$marker root=$root_path"
  echo "[e2e-m4-multi][DEBUG] inbox:"
  db_query "SELECT id,status,substr(text,1,180),substr(COALESCE(error,''),1,160) FROM inbox ORDER BY id DESC LIMIT 8;" || true
  echo "[e2e-m4-multi][DEBUG] history:"
  db_query "SELECT id,role,substr(text,1,180) FROM history ORDER BY id DESC LIMIT 12;" || true
  echo "[e2e-m4-multi][DEBUG] events:"
  db_query "SELECT id,event_type,substr(payload,1,220) FROM events ORDER BY id DESC LIMIT 40;" || true
  echo "[e2e-m4-multi][DEBUG] files:"
  docker exec "$CONTAINER" sh -lc "ls -la '$root_path' '$root_path/round1' '$root_path/round2' 2>/dev/null || true" || true
}

extract_first_number() {
  local s="$1"
  printf '%s' "$s" | grep -Eo '[0-9]+' | head -n1 || true
}

wait_task_done() {
  local marker="$1"
  local waited=0
  local row status

  while (( waited < TIMEOUT_SECONDS )); do
    row="$(db_query "SELECT id||'|'||status||'|'||COALESCE(error,'') FROM inbox WHERE text LIKE '%${marker}%' ORDER BY id DESC LIMIT 1;")"
    if [[ -n "$row" ]]; then
      status="$(printf '%s' "$row" | cut -d'|' -f2)"
      if [[ "$status" == "done" ]]; then
        printf '%s\n' "$row"
        return 0
      fi
      if [[ "$status" == "failed" || "$status" == "error" ]]; then
        echo "[e2e-m4-multi][FAIL] task status=$status marker=$marker row=$row"
        return 1
      fi
    fi
    sleep "$POLL_INTERVAL_SECONDS"
    waited=$((waited + POLL_INTERVAL_SECONDS))
  done

  echo "[e2e-m4-multi][FAIL] timeout waiting task marker=$marker"
  return 1
}

tool_count_for_task() {
  local task_id="$1"
  local event_type="$2"
  local tool_name="$3"
  db_query "WITH RECURSIVE root(id) AS (
    SELECT COALESCE(MAX(id),0) FROM events
      WHERE event_type='agent.started' AND payload LIKE '%\"task_id\":${task_id},%'
  ), t(id) AS (
    SELECT id FROM root
    UNION
    SELECT e.id FROM events e JOIN t ON e.parent_id=t.id
  )
  SELECT COUNT(*) FROM events
    WHERE id IN (SELECT id FROM t)
      AND event_type='${event_type}'
      AND payload LIKE '%\"tool_name\":\"${tool_name}\"%';"
}

tool_failed_then_continued() {
  local task_id="$1"
  db_query "WITH RECURSIVE root(id) AS (
    SELECT COALESCE(MAX(id),0) FROM events
      WHERE event_type='agent.started' AND payload LIKE '%\"task_id\":${task_id},%'
  ), t(id) AS (
    SELECT id FROM root
    UNION
    SELECT e.id FROM events e JOIN t ON e.parent_id=t.id
  )
  SELECT CASE WHEN EXISTS (
    SELECT 1 FROM events f
      WHERE f.id IN (SELECT id FROM t) AND f.event_type='tool_call.failed'
      AND EXISTS (
        SELECT 1 FROM events s
          WHERE s.id IN (SELECT id FROM t)
            AND s.event_type='tool_call.started'
            AND s.id > f.id
      )
  ) THEN 1 ELSE 0 END;"
}

trim_file_in_container() {
  local path="$1"
  docker exec "$CONTAINER" sh -lc "tr -d '\n\r ' < '$path'" || true
}

RUN_TAG="$(date +%Y%m%d-%H%M%S)-$RANDOM"
ROOT="$(docker exec "$CONTAINER" sh -lc "mkdir -p /workspace/e2e/multi-tools && mktemp -d /workspace/e2e/multi-tools/mt-${RUN_TAG}-XXXXXX")"
R1="$ROOT/round1"
R2="$ROOT/round2"

cleanup() {
  if [[ "$KEEP_ARTIFACTS" == "1" ]]; then
    echo "[e2e-m4-multi] keep artifacts: $ROOT"
    return
  fi
  docker exec "$CONTAINER" rm -rf "$ROOT" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "[e2e-m4-multi] run_tag=$RUN_TAG"
echo "[e2e-m4-multi] root=$ROOT"

if [[ "$RESET_DB_BEFORE_RUN" == "1" ]]; then
  echo "[e2e-m4-multi] resetting db tables before run..."
  db_query "DELETE FROM events;"
  db_query "DELETE FROM inbox;"
  db_query "DELETE FROM history;"
fi

echo "[e2e-m4-multi] prepare round1 dataset..."
docker exec "$CONTAINER" mkdir -p "$R1/nested"
docker exec "$CONTAINER" sh -lc "cat > '$R1/a.txt' <<'EOF'
hello 12 world
EOF"
docker exec "$CONTAINER" sh -lc "cat > '$R1/b.txt' <<'EOF'
foo 8 bar 30
EOF"
docker exec "$CONTAINER" sh -lc "cat > '$R1/c.txt' <<'EOF'
only letters here
EOF"
docker exec "$CONTAINER" sh -lc "cat > '$R1/nested/d.md' <<'EOF'
7 and 3
EOF"

hist_before_r1="$(db_query "SELECT COALESCE(MAX(id),0) FROM history;")"
MARKER_R1="E2E-M4-MULTI-${RUN_TAG}-R1"
MSG1="${MARKER_R1}: scan ${R1}, find all numbers in files, sum them, and write result to ${R1}/sum.txt. You must use find, read, and write tools, and you must produce tool calls before any final answer. Do not use bash tool. For each read call, include arguments.limit=200 (or other positive integer). For write call, include arguments.content as plain digits only. Never write placeholders like <sum_result>. Do not inspect ${R1}/sum.txt before writing it. Reply only the number."
echo "[e2e-m4-multi] round1 send: $MSG1"
"$SEND_SCRIPT" "$MSG1"

if ! row_r1="$(wait_task_done "$MARKER_R1")"; then
  dump_debug "$MARKER_R1" "$ROOT"
  exit 1
fi
task1_id="$(printf '%s' "$row_r1" | cut -d'|' -f1)"
sum1="$(trim_file_in_container "$R1/sum.txt")"
reply1="$(db_query "SELECT text FROM history WHERE role='assistant' AND id>${hist_before_r1} ORDER BY id DESC LIMIT 1;")"
reply1_num="$(extract_first_number "$reply1")"

if [[ "$sum1" != "60" ]]; then
  echo "[e2e-m4-multi][FAIL] round1 sum file mismatch: got=$sum1 want=60"
  dump_debug "$MARKER_R1" "$ROOT"
  exit 1
fi
if [[ "$reply1_num" != "60" ]]; then
  echo "[e2e-m4-multi][FAIL] round1 assistant reply mismatch: got=$reply1 want contains 60"
  dump_debug "$MARKER_R1" "$ROOT"
  exit 1
fi

r1_find_done="$(tool_count_for_task "$task1_id" "tool_call.completed" "find")"
r1_read_done="$(tool_count_for_task "$task1_id" "tool_call.completed" "read")"
r1_write_done="$(tool_count_for_task "$task1_id" "tool_call.completed" "write")"
r1_bash_done="$(tool_count_for_task "$task1_id" "tool_call.completed" "bash")"

if [[ "$r1_find_done" -lt 1 || "$r1_read_done" -lt 1 || "$r1_write_done" -lt 1 ]]; then
  echo "[e2e-m4-multi][FAIL] round1 missing expected tools: find=$r1_find_done read=$r1_read_done write=$r1_write_done"
  dump_debug "$MARKER_R1" "$ROOT"
  exit 1
fi
if [[ "$r1_bash_done" -gt 0 ]]; then
  echo "[e2e-m4-multi][WARN] round1 used bash unexpectedly: bash.completed=$r1_bash_done"
fi
r1_failed_then_continued="$(tool_failed_then_continued "$task1_id")"
if [[ "$r1_failed_then_continued" -eq 1 ]]; then
  echo "[e2e-m4-multi] round1 observed tool failure then continued execution"
fi

echo "[e2e-m4-multi] prepare round2 dataset..."
docker exec "$CONTAINER" mkdir -p "$R2"
docker exec "$CONTAINER" sh -lc "cat > '$R2/e.txt' <<'EOF'
1
2
3
EOF"

round2_ok=0
r2_bash_done=0
sum2=""
reply2_num=""
for ((attempt=1; attempt<=MAX_ROUND2_ATTEMPTS; attempt++)); do
  hist_before_r2="$(db_query "SELECT COALESCE(MAX(id),0) FROM history;")"
  MARKER_R2="E2E-M4-MULTI-${RUN_TAG}-R2-A${attempt}"
  MSG2="${MARKER_R2}: use ${R2}/e.txt and ${R1}/sum.txt, you must use bash tool to compute total sum, and you must produce tool calls before final answer. Then write result to ${R2}/sum.txt using plain digits only (no placeholders), and reply only the number."
  if (( attempt > 1 )); then
    MSG2="${MSG2} Previous attempt did not persist 66 to file. Ensure ${R2}/sum.txt contains exactly 66."
  fi
  echo "[e2e-m4-multi] round2 send (attempt ${attempt}): $MSG2"
  "$SEND_SCRIPT" "$MSG2"

  if ! row_r2="$(wait_task_done "$MARKER_R2")"; then
    dump_debug "$MARKER_R2" "$ROOT"
    exit 1
  fi
  task2_id="$(printf '%s' "$row_r2" | cut -d'|' -f1)"
  sum2="$(trim_file_in_container "$R2/sum.txt")"
  reply2="$(db_query "SELECT text FROM history WHERE role='assistant' AND id>${hist_before_r2} ORDER BY id DESC LIMIT 1;")"
  reply2_num="$(extract_first_number "$reply2")"
  r2_bash_done="$(tool_count_for_task "$task2_id" "tool_call.completed" "bash")"

  if [[ "$sum2" == "66" && "$reply2_num" == "66" && "$r2_bash_done" -ge 1 ]]; then
    round2_ok=1
    break
  fi
  echo "[e2e-m4-multi][WARN] round2 attempt ${attempt} not converged: sum_file=${sum2} reply=${reply2} bash.completed=${r2_bash_done}"
done

if [[ "$round2_ok" -ne 1 ]]; then
  echo "[e2e-m4-multi][FAIL] round2 did not converge within ${MAX_ROUND2_ATTEMPTS} attempts: sum_file=${sum2} reply_num=${reply2_num} bash.completed=${r2_bash_done}"
  dump_debug "${MARKER_R2}" "$ROOT"
  exit 1
fi

echo "[e2e-m4-multi] round1: sum=$sum1 find=$r1_find_done read=$r1_read_done write=$r1_write_done bash=$r1_bash_done"
echo "[e2e-m4-multi] round2: sum=$sum2 bash=$r2_bash_done"
echo "[e2e-m4-multi] passed"
