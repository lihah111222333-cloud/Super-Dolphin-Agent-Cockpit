#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AGENT_TERMINAL_CMD=(go run ./cmd/agent-terminal)
PIDS=()

if [[ -x "$ROOT_DIR/bin/agent-terminal" ]]; then
  AGENT_TERMINAL_CMD=("$ROOT_DIR/bin/agent-terminal")
fi

cleanup() {
  local pid
  for pid in "${PIDS[@]:-}"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
}

trap cleanup EXIT INT TERM

start_process() {
  local label="$1"
  shift
  echo "[dev-dual] starting ${label}: $*"
  (
    cd "$ROOT_DIR"
    "$@"
  ) &
  PIDS+=("$!")
}

port_in_use() {
  local port="$1"
  ss -ltn "sport = :${port}" | tail -n +2 | grep -q .
}

pick_port() {
  local start="$1"
  local port="$start"
  while port_in_use "$port"; do
    port=$((port + 1))
  done
  printf '%s\n' "$port"
}

start_desktop_if_free() {
  local label="$1"
  local ctl_port="$2"
  local asset_port="$3"
  shift 3

  if port_in_use "$ctl_port"; then
    echo "[dev-dual] ${label} skipped: 127.0.0.1:${ctl_port} is already in use"
    return 0
  fi
  if port_in_use "$asset_port"; then
    echo "[dev-dual] ${label} skipped: 127.0.0.1:${asset_port} is already in use"
    return 0
  fi

  start_process "$label" \
    env GO_AGENT_CTL_RPC_ADDR="127.0.0.1:${ctl_port}" GO_AGENT_HTTP_ASSET_ADDR="127.0.0.1:${asset_port}" "$@"
}

start_in_dir() {
  local label="$1"
  local dir="$2"
  shift 2
  echo "[dev-dual] starting ${label} in ${dir}: $*"
  (
    cd "$ROOT_DIR/$dir"
    "$@"
  ) &
  PIDS+=("$!")
}

wait_for_http() {
  local url="$1"
  local label="$2"
  local attempts=80
  local i
  for ((i = 1; i <= attempts; i += 1)); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      echo "[dev-dual] ${label} ready at ${url}"
      return 0
    fi
    sleep 0.25
  done
  echo "[dev-dual] ${label} did not become ready at ${url}" >&2
  return 1
}

if [[ ! -f "$ROOT_DIR/cmd/agent-terminal/frontend/dist/index.html" ]]; then
  echo "[dev-dual] cmd/agent-terminal/frontend/dist missing; building original embedded frontend first"
  (
    cd "$ROOT_DIR/cmd/agent-terminal/frontend"
    npm run build
  )
fi

FRONTEND_WEB_PORT="$(pick_port 5174)"
FRONTEND_APP_PORT="$(pick_port 5175)"
FRONTEND_APP_HTTP_PORT="$(pick_port 4512)"
FRONTEND_APP_CTL_PORT="$(pick_port 8091)"

if [[ "$FRONTEND_WEB_PORT" == "$FRONTEND_APP_PORT" ]]; then
  FRONTEND_APP_PORT="$(pick_port "$((FRONTEND_WEB_PORT + 1))")"
fi

start_in_dir "/frontend web client" "frontend" \
  env GO_AGENT_HTTP_ASSET_ADDR=127.0.0.1:4511 npm run dev -- --host 127.0.0.1 --port "$FRONTEND_WEB_PORT" --strictPort

start_desktop_if_free "original desktop client" 8090 4511 \
  "${AGENT_TERMINAL_CMD[@]}"

start_in_dir "frontend-app vite" "frontend-app" \
  env GO_AGENT_HTTP_ASSET_ADDR="127.0.0.1:${FRONTEND_APP_HTTP_PORT}" npm exec vite -- --host 127.0.0.1 --port "$FRONTEND_APP_PORT" --strictPort

wait_for_http "http://127.0.0.1:${FRONTEND_APP_PORT}/" "frontend-app vite"

start_desktop_if_free "frontend-app desktop client" "$FRONTEND_APP_CTL_PORT" "$FRONTEND_APP_HTTP_PORT" \
  VITE_DEV_URL="http://127.0.0.1:${FRONTEND_APP_PORT}" "${AGENT_TERMINAL_CMD[@]}"

echo "[dev-dual] /frontend web:       http://127.0.0.1:${FRONTEND_WEB_PORT}/"
echo "[dev-dual] frontend-app vite:   http://127.0.0.1:${FRONTEND_APP_PORT}/ (loaded by frontend-app desktop window)"
echo "[dev-dual] frontend-app bridge:  ws://127.0.0.1:${FRONTEND_APP_HTTP_PORT}/wails/ws"
echo "[dev-dual] frontend-app ctl:     127.0.0.1:${FRONTEND_APP_CTL_PORT}"
echo "[dev-dual] Press Ctrl+C to stop all child processes."

wait
