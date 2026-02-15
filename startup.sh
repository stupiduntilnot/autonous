#!/usr/bin/env bash
set -euo pipefail

WORKSPACE_DIR="${WORKSPACE_DIR:-/workspace}"
MANIFEST_PATH="${MANIFEST_PATH:-${WORKSPACE_DIR}/Cargo.toml}"
SUPERVISOR_BIN="${SUPERVISOR_BIN:-${WORKSPACE_DIR}/target/release/autonous-supervisor}"
WORKER_BIN="${WORKER_BIN:-${WORKSPACE_DIR}/target/release/autonous-worker}"

if [[ ! -f "${MANIFEST_PATH}" ]]; then
  echo "Cargo manifest not found at ${MANIFEST_PATH}" >&2
  exit 1
fi

mkdir -p /state

if [[ ! -x "${SUPERVISOR_BIN}" || ! -x "${WORKER_BIN}" ]]; then
  echo "Building Rust supervisor/worker binaries..."
  cargo build --release --manifest-path "${MANIFEST_PATH}" --bin autonous-supervisor --bin autonous-worker
fi

echo "startup launching supervisor"
exec "${SUPERVISOR_BIN}"
