#!/usr/bin/env bash
set -euo pipefail

# Load TELEGRAM_BOT_TOKEN from ~/.env if not already exported.
if [[ -z "${TELEGRAM_BOT_TOKEN:-}" && -f "${HOME}/.env" ]]; then
  # shellcheck disable=SC1090
  source "${HOME}/.env"
fi

if [[ -z "${TELEGRAM_BOT_TOKEN:-}" ]]; then
  echo "TELEGRAM_BOT_TOKEN is not set. Put it in ~/.env or export it first." >&2
  exit 1
fi

API_BASE="https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}"
OFFSET=0
TIMEOUT="${TG_TIMEOUT:-30}"
SLEEP_SECONDS="${TG_SLEEP_SECONDS:-1}"

echo "Polling Telegram updates... (Ctrl+C to stop)"

while true; do
  RESP="$(curl -sS "${API_BASE}/getUpdates" \
    --get \
    --data-urlencode "timeout=${TIMEOUT}" \
    --data-urlencode "offset=${OFFSET}")"

  if [[ "$(printf '%s' "${RESP}" | python3 -c 'import sys, json; print("true" if json.load(sys.stdin).get("ok") else "false")')" != "true" ]]; then
    echo "API error: ${RESP}" >&2
    sleep "${SLEEP_SECONDS}"
    continue
  fi

  # Print each update as one line and advance offset.
  while IFS= read -r line; do
    [[ -z "${line}" ]] && continue
    echo "${line}"
    update_id="$(printf '%s' "${line}" | python3 -c 'import sys, json; print(json.load(sys.stdin)["update_id"])')"
    OFFSET="$((update_id + 1))"
  done < <(
    printf '%s' "${RESP}" | python3 -c '
import json, sys
data = json.load(sys.stdin)
for u in data.get("result", []):
    msg = u.get("message", {})
    chat = msg.get("chat", {})
    out = {
        "update_id": u.get("update_id"),
        "chat_id": chat.get("id"),
        "from_id": (msg.get("from") or {}).get("id"),
        "text": msg.get("text"),
        "date": msg.get("date"),
    }
    print(json.dumps(out, ensure_ascii=False))
'
  )

  sleep "${SLEEP_SECONDS}"
done
