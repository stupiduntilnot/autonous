#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${TELEGRAM_BOT_TOKEN:-}" && -f "${HOME}/.env" ]]; then
  # shellcheck disable=SC1090
  source "${HOME}/.env"
fi

if [[ -z "${TELEGRAM_BOT_TOKEN:-}" ]]; then
  echo "TELEGRAM_BOT_TOKEN is not set." >&2
  exit 1
fi

API_BASE="https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}"
OFFSET="${TG_OFFSET_START:-0}"
TIMEOUT="${TG_TIMEOUT:-30}"
SLEEP_SECONDS="${TG_SLEEP_SECONDS:-1}"
DROP_PENDING="${TG_DROP_PENDING:-1}"
USE_CODEX="${USE_CODEX:-1}"

send_text() {
  local chat_id="$1"
  local text="$2"
  local limited
  limited="$(printf '%s' "${text}" | python3 -c 'import sys; s=sys.stdin.read(); print(s[:3900])')"
  python3 - "$API_BASE" "$chat_id" "$limited" <<'PY' | curl -sS -X POST -H "Content-Type: application/json" -d @- "${API_BASE}/sendMessage" >/dev/null
import json
import sys
api_base, chat_id, text = sys.argv[1], sys.argv[2], sys.argv[3]
print(json.dumps({"chat_id": chat_id, "text": text}))
PY
}

run_agent() {
  local prompt="$1"

  if [[ "${USE_CODEX}" == "1" ]]; then
    if ! command -v codex >/dev/null 2>&1; then
      echo "Agent error: codex CLI not found on PATH."
      return 0
    fi

    local out_file
    local err_file
    out_file="$(mktemp)"
    err_file="$(mktemp)"
    if codex exec --skip-git-repo-check --output-last-message "${out_file}" "${prompt}" >"${err_file}" 2>&1; then
      if [[ -s "${out_file}" ]]; then
        cat "${out_file}"
      else
        echo "Agent error: Codex returned empty output."
      fi
      rm -f "${out_file}"
      rm -f "${err_file}"
      return 0
    fi

    tail_err="$(tail -n 1 "${err_file}" 2>/dev/null || true)"
    rm -f "${out_file}"
    rm -f "${err_file}"
    if [[ -n "${tail_err}" ]]; then
      echo "Agent error: Codex failed (${tail_err})."
    else
      echo "Agent error: Codex failed."
    fi
    return 0
  fi

  # Local smoke testing mode when Codex is intentionally disabled.
  echo "Echo: ${prompt}"
}

bootstrap_offset() {
  local resp
  resp="$(curl -sS "${API_BASE}/getUpdates")"
  OFFSET="$(printf '%s' "${resp}" | python3 -c '
import json, sys
data = json.load(sys.stdin)
items = data.get("result", [])
if not items:
    print(0)
else:
    print(items[-1].get("update_id", 0) + 1)
')"
}

if [[ "${DROP_PENDING}" == "1" ]]; then
  bootstrap_offset
fi

echo "startup loop running (Ctrl+C to stop)"

while true; do
  resp="$(curl -sS "${API_BASE}/getUpdates" \
    --get \
    --data-urlencode "timeout=${TIMEOUT}" \
    --data-urlencode "offset=${OFFSET}")"

  if [[ "$(printf '%s' "${resp}" | python3 -c 'import sys, json; print("true" if json.load(sys.stdin).get("ok") else "false")')" != "true" ]]; then
    echo "Telegram API error: ${resp}" >&2
    sleep "${SLEEP_SECONDS}"
    continue
  fi

  updates_tsv="$(printf '%s' "${resp}" | python3 -c '
import json, sys
data = json.load(sys.stdin)
for u in data.get("result", []):
    update_id = u.get("update_id")
    msg = u.get("message") or {}
    chat_id = (msg.get("chat") or {}).get("id")
    text = msg.get("text")
    if update_id is None:
        continue
    if text is None:
        text = ""
    if "\t" in text or "\n" in text:
        text = text.replace("\t", " ").replace("\n", " ")
    cid = "" if chat_id is None else str(chat_id)
    print(f"{update_id}\t{cid}\t{text}")
')"

  while IFS=$'\t' read -r update_id chat_id text; do
    [[ -z "${update_id}" ]] && continue
    OFFSET="$((update_id + 1))"
    [[ -z "${chat_id}" || -z "${text}" ]] && continue

    echo "recv chat_id=${chat_id} text=${text}"
    reply="$(run_agent "${text}")"
    echo "send chat_id=${chat_id} text=${reply}"
    send_text "${chat_id}" "${reply}"
  done <<< "${updates_tsv}"

  sleep "${SLEEP_SECONDS}"
done
