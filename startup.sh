#!/usr/bin/env bash
set -euo pipefail

WORKSPACE_DIR="${WORKSPACE_DIR:-/workspace}"
SUPERVISOR_BIN="${SUPERVISOR_BIN:-${WORKSPACE_DIR}/bin/supervisor}"
UPDATE_ARTIFACT_ROOT="${AUTONOUS_UPDATE_ARTIFACT_ROOT:-/state/artifacts}"
ACTIVE_WORKER_BIN="${AUTONOUS_UPDATE_ACTIVE_BIN:-/state/bin/worker.current}"
BOOTSTRAP_WORKER_BIN="${UPDATE_ARTIFACT_ROOT}/bootstrap/worker"
WORKER_BIN="${WORKER_BIN:-${ACTIVE_WORKER_BIN}}"

export WORKER_BIN
export AUTONOUS_UPDATE_ACTIVE_BIN="${ACTIVE_WORKER_BIN}"
export AUTONOUS_UPDATE_ARTIFACT_ROOT="${UPDATE_ARTIFACT_ROOT}"

mkdir -p /state "${WORKSPACE_DIR}/bin" "${UPDATE_ARTIFACT_ROOT}/bootstrap" "$(dirname "${ACTIVE_WORKER_BIN}")"

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
CGO_ENABLED=1 go build -o "${BOOTSTRAP_WORKER_BIN}" ./cmd/worker
ln -sf "${BOOTSTRAP_WORKER_BIN}" "${ACTIVE_WORKER_BIN}"

echo "startup launching supervisor"
exec "${SUPERVISOR_BIN}"
