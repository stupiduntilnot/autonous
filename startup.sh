#!/usr/bin/env bash
set -euo pipefail

WORKSPACE_DIR="${WORKSPACE_DIR:-/workspace}"
SUPERVISOR_BIN="${SUPERVISOR_BIN:-${WORKSPACE_DIR}/bin/supervisor}"
WORKER_BIN="${WORKER_BIN:-${WORKSPACE_DIR}/bin/worker}"

export WORKER_BIN

mkdir -p /state

if [[ ! -x "${SUPERVISOR_BIN}" || ! -x "${WORKER_BIN}" ]]; then
  echo "Go binaries not found; building..."
  cd "${WORKSPACE_DIR}"
  CGO_ENABLED=1 go build -o "${SUPERVISOR_BIN}" ./cmd/supervisor
  CGO_ENABLED=1 go build -o "${WORKER_BIN}" ./cmd/worker
fi

echo "startup launching supervisor"
exec "${SUPERVISOR_BIN}"
