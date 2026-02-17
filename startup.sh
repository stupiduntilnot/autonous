#!/usr/bin/env bash
set -euo pipefail

WORKSPACE_DIR="${WORKSPACE_DIR:-/workspace}"
SUPERVISOR_BIN="${SUPERVISOR_BIN:-${WORKSPACE_DIR}/bin/supervisor}"
WORKER_BIN="${WORKER_BIN:-${WORKSPACE_DIR}/bin/worker}"

export WORKER_BIN

mkdir -p /state "${WORKSPACE_DIR}/bin"

required_cmds=(bash ls sed cat head tee rg fd)
for cmd in "${required_cmds[@]}"; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing required command: $cmd" >&2
    exit 1
  fi
done

echo "building Go binaries..."
cd "${WORKSPACE_DIR}"
CGO_ENABLED=1 go build -o "${SUPERVISOR_BIN}" ./cmd/supervisor
CGO_ENABLED=1 go build -o "${WORKER_BIN}" ./cmd/worker

echo "startup launching supervisor"
exec "${SUPERVISOR_BIN}"
