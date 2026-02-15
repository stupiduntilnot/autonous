#!/usr/bin/env bash
set -euo pipefail

WORKSPACE_DIR="${WORKSPACE_DIR:-/workspace}"
MANIFEST_PATH="${MANIFEST_PATH:-${WORKSPACE_DIR}/Cargo.toml}"
AGENT_BIN="${AGENT_BIN:-${WORKSPACE_DIR}/target/release/autonous}"
RESTART_DELAY_SECONDS="${RESTART_DELAY_SECONDS:-1}"

if [[ ! -f "${MANIFEST_PATH}" ]]; then
  echo "Cargo manifest not found at ${MANIFEST_PATH}" >&2
  exit 1
fi

mkdir -p /state

if [[ ! -x "${AGENT_BIN}" ]]; then
  echo "Building Rust agent binary..."
  cargo build --release --manifest-path "${MANIFEST_PATH}"
fi

echo "startup supervisor running"

while true; do
  "${AGENT_BIN}"
  status=$?
  echo "agent exited with code ${status}; restarting in ${RESTART_DELAY_SECONDS}s"
  sleep "${RESTART_DELAY_SECONDS}"
done
