#!/usr/bin/env bash
set -euo pipefail

# Load environment variables from ~/.env
set -a
source ~/.env
set +a

export BOT_USERNAME="${BOT_USERNAME:-autonous_bot}"
export SESSION_FILE="${SESSION_FILE:-$HOME/.telethon_test_session}"

if [[ $# -lt 1 ]]; then
  echo "Usage: $0 <message>" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
exec python3 "$SCRIPT_DIR/tg_send.py" "$1"
