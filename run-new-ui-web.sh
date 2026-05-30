#!/usr/bin/env bash
# run-new-ui-web.sh — start the refactored React web frontend against a running backend bridge.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
FRONTEND_DIR="$PROJECT_DIR/frontend"
NPM_REGISTRY="${NPM_REGISTRY:-https://registry.npmmirror.com}"

if [ -f "$PROJECT_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$PROJECT_DIR/.env"
  set +a
fi

WEB_HOST="${WEB_HOST:-127.0.0.1}"
WEB_PORT="${WEB_PORT:-5178}"
SUPER_DOLPHIN_HTTP_ADDR="${SUPER_DOLPHIN_HTTP_ADDR:-127.0.0.1:4511}"
export SUPER_DOLPHIN_HTTP_ADDR

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

require_backend_bridge() {
  local host="${SUPER_DOLPHIN_HTTP_ADDR%:*}"
  local port="${SUPER_DOLPHIN_HTTP_ADDR##*:}"
  if ! lsof -ti ":$port" >/dev/null 2>&1; then
    echo "❌ backend bridge is not listening at $SUPER_DOLPHIN_HTTP_ADDR"
    echo "   Start the original client with ./run-debug.sh, or override SUPER_DOLPHIN_HTTP_ADDR."
    exit 1
  fi
  if ! curl -fsS "http://$host:$port" >/dev/null 2>&1; then
    echo "❌ backend bridge did not respond at http://$host:$port"
    exit 1
  fi
}

fail_if_web_port_busy() {
  if lsof -ti ":$WEB_PORT" >/dev/null 2>&1; then
    echo "❌ web port $WEB_PORT is already in use:"
    lsof -nP -iTCP:"$WEB_PORT" -sTCP:LISTEN || true
    exit 1
  fi
}

cleanup() {
  if [ -n "${VITE_PID:-}" ] && kill -0 "$VITE_PID" 2>/dev/null; then
    echo "  → stopping new UI web vite (PID: $VITE_PID)"
    kill "$VITE_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

require_backend_bridge
fail_if_web_port_busy
ensure_node_deps "$FRONTEND_DIR"

echo "┌─────────────────────────────────────────┐"
echo "│  Super Agent new UI web                 │"
echo "└─────────────────────────────────────────┘"
echo "  web:     http://$WEB_HOST:$WEB_PORT/"
echo "  bridge:  $SUPER_DOLPHIN_HTTP_ADDR"

(cd "$FRONTEND_DIR" && npm run dev -- --host "$WEB_HOST" --port "$WEB_PORT" --strictPort) &
VITE_PID=$!
wait "$VITE_PID"
