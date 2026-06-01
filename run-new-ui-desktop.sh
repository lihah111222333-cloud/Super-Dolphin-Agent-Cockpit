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

postgres_platform_id() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
  esac
  echo "$os-$arch"
}

postgres_runtime_root_from_bin_dir() {
  local bin_dir="$1"
  case "$bin_dir" in
    */usr/lib/postgresql/*/bin)
      echo "${bin_dir%%/usr/lib/postgresql/*/bin}"
      ;;
    */bin)
      echo "${bin_dir%/bin}"
      ;;
    *)
      echo ""
      ;;
  esac
}

prepend_library_path_dir() {
  local dir="$1"
  [ -d "$dir" ] || return 0
  case ":${LD_LIBRARY_PATH:-}:" in
    *":$dir:"*) return 0 ;;
  esac
  export LD_LIBRARY_PATH="$dir${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
}

configure_postgres_library_path() {
  local bin_dir="$1"
  local root version_dir
  root="$(postgres_runtime_root_from_bin_dir "$bin_dir")"
  version_dir="$(dirname "$bin_dir")"
  prepend_library_path_dir "$version_dir/lib"
  if [ -n "$root" ]; then
    prepend_library_path_dir "$root/lib"
    prepend_library_path_dir "$root/usr/lib"
    prepend_library_path_dir "$root/usr/lib/x86_64-linux-gnu"
    prepend_library_path_dir "$root/usr/lib/aarch64-linux-gnu"
  fi
}

resolve_postgres_bin_dir() {
  local platform bin_dir pg_ctl_path
  platform="$(postgres_platform_id)"
  local candidates=()
  if [ -n "${SUPER_DOLPHIN_POSTGRES_BIN_DIR:-}" ]; then
    candidates+=("$SUPER_DOLPHIN_POSTGRES_BIN_DIR")
  fi
  if [ -n "${SUPER_DOLPHIN_POSTGRES_DIST:-}" ]; then
    candidates+=("$SUPER_DOLPHIN_POSTGRES_DIST/bin")
  fi
  candidates+=(
    "$PROJECT_DIR/third_party/postgres/$platform/bin"
    "$PROJECT_DIR/.build-cache/postgres/16.14/$platform/bin"
  )
  if [ -n "${HOME:-}" ]; then
    candidates+=("$HOME/.cache/super-dolphin-toolchain/postgresql-14-root/usr/lib/postgresql/14/bin")
  fi
  if pg_ctl_path="$(command -v pg_ctl 2>/dev/null)"; then
    candidates+=("$(dirname "$pg_ctl_path")")
  fi

  for bin_dir in "${candidates[@]}"; do
    if [ -x "$bin_dir/postgres" ] && [ -x "$bin_dir/initdb" ] && [ -x "$bin_dir/pg_ctl" ] && [ -x "$bin_dir/pg_config" ]; then
      echo "$bin_dir"
      return 0
    fi
  done

  echo "❌ missing PostgreSQL runtime; set SUPER_DOLPHIN_POSTGRES_DIST or SUPER_DOLPHIN_POSTGRES_BIN_DIR" >&2
  return 1
}

resolve_postgres_share_dir() {
  local bin_dir="$1"
  local root sharedir candidate
  if [ -n "${SUPER_DOLPHIN_POSTGRES_SHARE_DIR:-}" ]; then
    if [ -f "$SUPER_DOLPHIN_POSTGRES_SHARE_DIR/postgres.bki" ]; then
      echo "$SUPER_DOLPHIN_POSTGRES_SHARE_DIR"
      return 0
    fi
    echo "❌ SUPER_DOLPHIN_POSTGRES_SHARE_DIR missing postgres.bki: $SUPER_DOLPHIN_POSTGRES_SHARE_DIR" >&2
    return 1
  fi

  root="$(postgres_runtime_root_from_bin_dir "$bin_dir")"
  local candidates=()
  if sharedir="$("$bin_dir/pg_config" --sharedir 2>/dev/null)"; then
    candidates+=("$sharedir")
    if [ -n "$root" ] && [[ "$sharedir" = /* ]]; then
      candidates+=("$root$sharedir")
    fi
  fi
  candidates+=(
    "$root/share"
    "$root/share/postgresql@16"
    "$root/share/postgresql"
    "$root/usr/share/postgresql/16"
    "$root/usr/share/postgresql/14"
    "$(dirname "$bin_dir")/share"
    "$(dirname "$(dirname "$bin_dir")")/share/postgresql"
  )

  for candidate in "${candidates[@]}"; do
    if [ -f "$candidate/postgres.bki" ]; then
      echo "$candidate"
      return 0
    fi
  done

  echo "❌ missing PostgreSQL share dir with postgres.bki; set SUPER_DOLPHIN_POSTGRES_SHARE_DIR" >&2
  return 1
}

postgres_is_local_database_url() {
  local url="$1"
  local rest authority host port normalized_host
  POSTGRES_URL_HOST=""
  POSTGRES_URL_PORT=""
  case "$url" in
    postgres://*) rest="${url#postgres://}" ;;
    postgresql://*) rest="${url#postgresql://}" ;;
    *) return 1 ;;
  esac
  rest="${rest#*@}"
  authority="${rest%%/*}"
  host="${authority%%:*}"
  port="${authority#*:}"
  if [ "$port" = "$authority" ] || [ -z "$port" ]; then
    port="5432"
  fi
  normalized_host="$(printf '%s' "$host" | tr '[:upper:]' '[:lower:]')"
  case "$normalized_host" in
    localhost|127.0.0.1)
      POSTGRES_URL_HOST="$host"
      POSTGRES_URL_PORT="$port"
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

configure_dev_postgres_runtime() {
  export SUPER_DOLPHIN_PROCESS_ROLE="${SUPER_DOLPHIN_PROCESS_ROLE:-desktop}"

  local database_url="${DATABASE_URL:-${POSTGRES_CONNECTION_STRING:-}}"
  if [ -n "$database_url" ] && ! postgres_is_local_database_url "$database_url"; then
    return 0
  fi

  local bin_dir share_dir
  bin_dir="$(resolve_postgres_bin_dir)"
  export SUPER_DOLPHIN_POSTGRES_BIN_DIR="${SUPER_DOLPHIN_POSTGRES_BIN_DIR:-$bin_dir}"
  configure_postgres_library_path "$SUPER_DOLPHIN_POSTGRES_BIN_DIR"
  share_dir="$(resolve_postgres_share_dir "$SUPER_DOLPHIN_POSTGRES_BIN_DIR")"
  export SUPER_DOLPHIN_POSTGRES_SHARE_DIR="${SUPER_DOLPHIN_POSTGRES_SHARE_DIR:-$share_dir}"

  if [ -z "$database_url" ]; then
    export SUPER_DOLPHIN_EMBEDDED_POSTGRES="${SUPER_DOLPHIN_EMBEDDED_POSTGRES:-true}"
    echo "  → embedded PostgreSQL enabled for dev runtime"
  fi
}

ensure_local_postgres() {
  local database_url="${DATABASE_URL:-${POSTGRES_CONNECTION_STRING:-}}"
  if [ -z "$database_url" ]; then
    return 0
  fi
  if ! postgres_is_local_database_url "$database_url"; then
    return 0
  fi

  local host="$POSTGRES_URL_HOST"
  local port="$POSTGRES_URL_PORT"
  if lsof -tiTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "  → local PostgreSQL already listening on $host:$port"
    return 0
  fi
  if [ ! -f "$SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR/PG_VERSION" ]; then
    echo "❌ DATABASE_URL points to local PostgreSQL ($host:$port), but data dir is not initialized: $SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR" >&2
    echo "   Start PostgreSQL manually, set DATABASE_URL to a reachable database, or initialize the local data dir." >&2
    exit 1
  fi

  mkdir -p "$SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR" "$(dirname "$SUPER_DOLPHIN_LOCAL_POSTGRES_LOG")"
  chmod 700 "$SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR"
  echo "  → starting local PostgreSQL: $host:$port"
  "$SUPER_DOLPHIN_POSTGRES_BIN_DIR/pg_ctl" -D "$SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR" \
    -l "$SUPER_DOLPHIN_LOCAL_POSTGRES_LOG" \
    -o "-h $host -p $port -k $SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR" \
    -w -t 30 start
  LOCAL_POSTGRES_STARTED="1"
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
  if [ "${LOCAL_POSTGRES_STARTED:-}" = "1" ]; then
    echo "  → stopping local PostgreSQL"
    "$SUPER_DOLPHIN_POSTGRES_BIN_DIR/pg_ctl" -D "$SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR" -w -t 30 stop -m fast >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

SUPER_DOLPHIN_HTTP_ADDR="${SUPER_DOLPHIN_HTTP_ADDR:-127.0.0.1:4512}"
GO_AGENT_CTL_RPC_ADDR="${GO_AGENT_CTL_RPC_ADDR:-127.0.0.1:8092}"
VITE_DEV_URL="${VITE_DEV_URL:-http://127.0.0.1:5175}"
FRONTEND_DEVSERVER_URL="${FRONTEND_DEVSERVER_URL:-$VITE_DEV_URL}"
VITE_DEV_HOST="${VITE_DEV_URL#http://}"
VITE_DEV_HOST="${VITE_DEV_HOST#https://}"
VITE_DEV_HOST="${VITE_DEV_HOST%%/*}"
VITE_DEV_PORT="${VITE_DEV_HOST##*:}"
VITE_DEV_HOST="${VITE_DEV_HOST%:*}"
if [ -z "$VITE_DEV_HOST" ] || [ -z "$VITE_DEV_PORT" ] || [ "$VITE_DEV_HOST" = "$VITE_DEV_PORT" ]; then
  echo "❌ VITE_DEV_URL must include host and port, got: $VITE_DEV_URL" >&2
  exit 1
fi
GO_AGENT_PEER_BIN_DIR="${GO_AGENT_PEER_BIN_DIR:-$PROJECT_DIR}"
SUPER_DOLPHIN_RUNTIME_MODE="${SUPER_DOLPHIN_RUNTIME_MODE:-dev}"
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="${SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR:-$PROJECT_DIR}"
SUPER_DOLPHIN_DEV_ENTRYPOINT="${SUPER_DOLPHIN_DEV_ENTRYPOINT:-run-new-ui-desktop.sh}"
SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR="${SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR:-$PROJECT_DIR/.tmp/pgdata}"
SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR="${SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR:-$PROJECT_DIR/.tmp/pgsocket}"
SUPER_DOLPHIN_LOCAL_POSTGRES_LOG="${SUPER_DOLPHIN_LOCAL_POSTGRES_LOG:-$PROJECT_DIR/.tmp/postgres.log}"
export SUPER_DOLPHIN_HTTP_ADDR GO_AGENT_CTL_RPC_ADDR VITE_DEV_URL FRONTEND_DEVSERVER_URL GO_AGENT_PEER_BIN_DIR
export SUPER_DOLPHIN_RUNTIME_MODE SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR SUPER_DOLPHIN_DEV_ENTRYPOINT
export SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR SUPER_DOLPHIN_LOCAL_POSTGRES_LOG
export LOG_LEVEL="${LOG_LEVEL:-debug}"
export ENABLE_MEMORY_SYSTEM="${ENABLE_MEMORY_SYSTEM:-1}"
export ENABLE_MEMORY_TOOLS="${ENABLE_MEMORY_TOOLS:-1}"
export MULTI_AGENT_MEMORY_FEATURE_TEAMMEM="${MULTI_AGENT_MEMORY_FEATURE_TEAMMEM:-1}"
export CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME="${CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME:-1}"

ensure_dev_control_session_token
configure_dev_postgres_runtime
fail_if_port_busy "$SUPER_DOLPHIN_HTTP_ADDR"
fail_if_port_busy "$GO_AGENT_CTL_RPC_ADDR"
ensure_local_postgres
ensure_node_deps "$FRONTEND_APP_DIR"
ensure_peer_binaries

echo "┌─────────────────────────────────────────┐"
echo "│  Super Agent new UI desktop             │"
echo "└─────────────────────────────────────────┘"
echo "  frontend-app: $VITE_DEV_URL"
echo "  bridge:       $SUPER_DOLPHIN_HTTP_ADDR"
echo "  control rpc:  $GO_AGENT_CTL_RPC_ADDR"
echo "  peer bin dir: $GO_AGENT_PEER_BIN_DIR"
echo "  runtime:      $SUPER_DOLPHIN_RUNTIME_MODE ($SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR)"

(cd "$FRONTEND_APP_DIR" && npm run dev -- --host "$VITE_DEV_HOST" --port "$VITE_DEV_PORT" --strictPort) &
VITE_PID=$!
wait_for_http "$VITE_DEV_URL" "frontend-app vite"

(cd "$PROJECT_DIR" && go run ./cmd/agent-terminal) &
DESKTOP_PID=$!
wait "$DESKTOP_PID"
