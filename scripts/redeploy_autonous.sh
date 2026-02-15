#!/usr/bin/env bash
set -euo pipefail

CONTAINER_NAME="${CONTAINER_NAME:-autonous-agent}"
IMAGE_NAME="${IMAGE_NAME:-autonous-agent:dev}"
ENV_FILE="${ENV_FILE:-$HOME/.env}"
WORKDIR="${WORKDIR:-$(cd "$(dirname "$0")/.." && pwd)}"
WORKER_SUICIDE_EVERY="-e"
TG_PENDING_WINDOW_SECONDS="${TG_PENDING_WINDOW_SECONDS:-60}"
WAIT_SECONDS="${WAIT_SECONDS:-30}"

log() {
  printf '[redeploy] %s\n' "$*"
}

wait_until_container_absent() {
  local name="$1"
  local max_wait="$2"
  local waited=0

  while docker ps -a --filter "name=^/${name}$" -q | grep -q .; do
    if (( waited >= max_wait )); then
      log "timeout waiting for container ${name} to disappear"
      return 1
    fi
    sleep 1
    waited=$((waited + 1))
  done
}

require_file() {
  local path="$1"
  if [[ ! -f "$path" ]]; then
    log "required file not found: $path"
    exit 1
  fi
}

require_file "$ENV_FILE"

old_tagged_id="$(docker image inspect -f '{{.Id}}' "$IMAGE_NAME" 2>/dev/null || true)"

if docker ps -a --filter "name=^/${CONTAINER_NAME}$" -q | grep -q .; then
  log "removing old container ${CONTAINER_NAME}"
  docker rm -f "$CONTAINER_NAME" >/dev/null
  wait_until_container_absent "$CONTAINER_NAME" "$WAIT_SECONDS"
fi

log "building image ${IMAGE_NAME} from ${WORKDIR}"
docker build -t "$IMAGE_NAME" "$WORKDIR"

new_tagged_id="$(docker image inspect -f '{{.Id}}' "$IMAGE_NAME")"
log "new image id: ${new_tagged_id}"

if [[ -n "$old_tagged_id" && "$old_tagged_id" != "$new_tagged_id" ]]; then
  if docker image inspect "$old_tagged_id" >/dev/null 2>&1; then
    log "removing old tagged image id ${old_tagged_id}"
    docker rmi "$old_tagged_id" >/dev/null 2>&1 || true
  fi
fi

# Clean dangling images created by retagging this repo.
log "cleaning dangling images for repository autonous-agent"
docker images --filter dangling=true --format '{{.Repository}} {{.ID}}' \
  | awk '$1=="<none>" {print $2}' \
  | xargs -r docker rmi >/dev/null 2>&1 || true

log "starting new container ${CONTAINER_NAME}"
docker run -d \
  --name "$CONTAINER_NAME" \
  --restart unless-stopped \
  --env-file "$ENV_FILE" \
  -e WORKER_SUICIDE_EVERY="$WORKER_SUICIDE_EVERY" \
  -e TG_PENDING_WINDOW_SECONDS="$TG_PENDING_WINDOW_SECONDS" \
  "$IMAGE_NAME" >/dev/null

log "container status"
docker ps --filter "name=^/${CONTAINER_NAME}$" --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'
log "done"
