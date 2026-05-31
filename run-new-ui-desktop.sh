#!/usr/bin/env bash
# run-new-ui-desktop.sh — start the refactored frontend-app in a desktop Wails shell.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
FRONTEND_APP_DIR="$PROJECT_DIR/frontend-app"
NPM_REGISTRY="${NPM_REGISTRY:-https://registry.npmmirror.com}"

if [ -f "$PROJECT_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$PROJECT_DIR/.env"
  set +a
fi

ensure_dev_control_session_token() {
  if [ -n "${GO_AGENT_CTL_SESSION_TOKEN:-}" ]; then
    return 0
  fi
  if [ -n "${GO_AGENT_MCP_SESSION_TOKEN:-}" ]; then
    export GO_AGENT_CTL_SESSION_TOKEN="$GO_AGENT_MCP_SESSION_TOKEN"
    return 0
  fi
  export GO_AGENT_CTL_SESSION_TOKEN="dev-new-ui-$(date +%s)-$$"
}

ensure_node_deps() {
  local dir="$1"
  if [ ! -f "$dir/package.json" ]; then
    echo "❌ missing package.json: $dir"
    exit 1
  fi
  cd "$dir"
  if [ ! -d node_modules ]; then
    echo "  → npm ci ($dir)"
    npm ci --registry="$NPM_REGISTRY"
  elif [ -f package-lock.json ] && [ package-lock.json -nt node_modules ]; then
    echo "  → npm ci (package-lock changed)"
    npm ci --registry="$NPM_REGISTRY"
  elif [ package.json -nt node_modules ]; then
    echo "  → npm install (package.json changed)"
    npm install --registry="$NPM_REGISTRY"
  else
    echo "  → dependencies unchanged"
  fi
}

ensure_peer_binaries() {
  local missing=0
  for bin in mcp-orch mcp-lsp; do
    if [ ! -x "$PROJECT_DIR/$bin" ]; then
      missing=1
      break
    fi
  done
  if [ "$missing" = "0" ]; then
    return 0
  fi
  echo "  → building peer binaries for new UI desktop"
  (cd "$PROJECT_DIR" && go build -o ./mcp-orch ./cmd/mcp-orch/ && go build -o ./mcp-lsp ./cmd/mcp-lsp/)
}

wait_for_http() {
  local url="$1"
  local label="$2"
  for _ in $(seq 1 50); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      echo "  → $label ready: $url"
      return 0
    fi
    sleep 0.2
  done
  echo "❌ timed out waiting for $label: $url"
  exit 1
}

fail_if_port_busy() {
  local addr="$1"
  local port="${addr##*:}"
  if lsof -tiTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "❌ port $port is already in use:"
    lsof -nP -iTCP:"$port" -sTCP:LISTEN || true
    exit 1
  fi
}

cleanup() {
  if [ -n "${VITE_PID:-}" ] && kill -0 "$VITE_PID" 2>/dev/null; then
    echo "  → stopping frontend-app vite (PID: $VITE_PID)"
    kill "$VITE_PID" 2>/dev/null || true
  fi
  if [ -n "${DESKTOP_PID:-}" ] && kill -0 "$DESKTOP_PID" 2>/dev/null; then
    echo "  → stopping new UI desktop backend (PID: $DESKTOP_PID)"
    kill "$DESKTOP_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

SUPER_DOLPHIN_HTTP_ADDR="${SUPER_DOLPHIN_HTTP_ADDR:-127.0.0.1:4512}"
GO_AGENT_CTL_RPC_ADDR="${GO_AGENT_CTL_RPC_ADDR:-127.0.0.1:8092}"
VITE_DEV_URL="${VITE_DEV_URL:-http://127.0.0.1:5175}"
FRONTEND_DEVSERVER_URL="${FRONTEND_DEVSERVER_URL:-$VITE_DEV_URL}"
GO_AGENT_PEER_BIN_DIR="${GO_AGENT_PEER_BIN_DIR:-$PROJECT_DIR}"
SUPER_DOLPHIN_RUNTIME_MODE="${SUPER_DOLPHIN_RUNTIME_MODE:-dev}"
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="${SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR:-$PROJECT_DIR}"
SUPER_DOLPHIN_DEV_ENTRYPOINT="${SUPER_DOLPHIN_DEV_ENTRYPOINT:-run-new-ui-desktop.sh}"
export SUPER_DOLPHIN_HTTP_ADDR GO_AGENT_CTL_RPC_ADDR VITE_DEV_URL FRONTEND_DEVSERVER_URL GO_AGENT_PEER_BIN_DIR
export SUPER_DOLPHIN_RUNTIME_MODE SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR SUPER_DOLPHIN_DEV_ENTRYPOINT
export LOG_LEVEL="${LOG_LEVEL:-debug}"
export ENABLE_MEMORY_SYSTEM="${ENABLE_MEMORY_SYSTEM:-1}"
export ENABLE_MEMORY_TOOLS="${ENABLE_MEMORY_TOOLS:-1}"
export MULTI_AGENT_MEMORY_FEATURE_TEAMMEM="${MULTI_AGENT_MEMORY_FEATURE_TEAMMEM:-1}"
export CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME="${CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME:-1}"

ensure_dev_control_session_token
fail_if_port_busy "$SUPER_DOLPHIN_HTTP_ADDR"
fail_if_port_busy "$GO_AGENT_CTL_RPC_ADDR"
ensure_node_deps "$FRONTEND_APP_DIR"
ensure_peer_binaries

echo "┌─────────────────────────────────────────┐"
echo "│  Super Agent new UI desktop             │"
echo "└─────────────────────────────────────────┘"
echo "  frontend-app: $VITE_DEV_URL"
echo "  bridge:       $SUPER_DOLPHIN_HTTP_ADDR"
echo "  control rpc:  $GO_AGENT_CTL_RPC_ADDR"
echo "  peer bin dir: $GO_AGENT_PEER_BIN_DIR"

(cd "$FRONTEND_APP_DIR" && npm run dev) &
VITE_PID=$!
wait_for_http "$VITE_DEV_URL" "frontend-app vite"

(cd "$PROJECT_DIR" && go run ./cmd/agent-terminal) &
DESKTOP_PID=$!
wait "$DESKTOP_PID"
