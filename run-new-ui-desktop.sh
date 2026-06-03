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
  local peer_dir="${GO_AGENT_PEER_BIN_DIR:-$PROJECT_DIR}"
  local missing=0
  for bin in mcp-orch mcp-lsp; do
    if [ ! -x "$peer_dir/$bin" ]; then
      missing=1
      break
    fi
  done
  if [ "$missing" = "0" ]; then
    return 0
  fi
  rebuild_peer_binaries
}

rebuild_peer_binaries() {
  local peer_dir="${GO_AGENT_PEER_BIN_DIR:-$PROJECT_DIR}"
  mkdir -p "$peer_dir"
  echo "  → building peer binaries for new UI desktop: $peer_dir"
  (cd "$PROJECT_DIR" && go build -o "$peer_dir/mcp-orch" ./cmd/mcp-orch/ && go build -o "$peer_dir/mcp-lsp" ./cmd/mcp-lsp/)
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

wait_for_port_free() {
  local addr="$1"
  local label="$2"
  local port="${addr##*:}"
  for _ in $(seq 1 50); do
    if ! lsof -tiTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  echo "❌ timed out waiting for $label port $port to be free" >&2
  lsof -nP -iTCP:"$port" -sTCP:LISTEN >&2 || true
  exit 1
}

process_exited() {
  local pid="$1"
  local stat
  if ! kill -0 "$pid" 2>/dev/null; then
    return 0
  fi
  stat="$(ps -p "$pid" -o stat= 2>/dev/null || true)"
  case "$stat" in
    *Z*) return 0 ;;
  esac
  return 1
}

child_pids_of() {
  local pid="$1"
  if [ -z "$pid" ]; then
    return 0
  fi
  if command -v pgrep >/dev/null 2>&1; then
    pgrep -P "$pid" 2>/dev/null || true
    return 0
  fi
  ps -eo pid=,ppid= 2>/dev/null | awk -v parent="$pid" '$2 == parent { print $1 }'
}

process_workdir() {
  local pid="$1"
  lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p' | head -n 1
}

stop_process_tree() {
  local label="$1"
  local pid="$2"
  local child
  if [ -z "$pid" ]; then
    return 0
  fi
  for child in $(child_pids_of "$pid"); do
    stop_process_tree "$label" "$child"
  done
  if ! kill -0 "$pid" 2>/dev/null; then
    return 0
  fi
  echo "  → stopping $label (PID: $pid)"
  kill "$pid" 2>/dev/null || true
  for _ in $(seq 1 20); do
    if process_exited "$pid"; then
      return 0
    fi
    sleep 0.1
  done
  kill -KILL "$pid" 2>/dev/null || true
}

stop_stale_vite_for_port() {
  local port="$1"
  local pid cwd command_line
  for pid in $(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true); do
    cwd="$(process_workdir "$pid")"
    command_line="$(ps -p "$pid" -o command= 2>/dev/null || true)"
    case "$command_line" in
      *frontend-app/node_modules/.bin/vite*)
        if [ "$cwd" = "$FRONTEND_APP_DIR" ]; then
          stop_process_tree "stale frontend-app vite on port $port" "$pid"
        fi
        ;;
    esac
  done
}

print_backend_log_tail() {
  if [ -f "$SUPER_DOLPHIN_BACKEND_LOG" ]; then
    echo "  → backend log tail: $SUPER_DOLPHIN_BACKEND_LOG" >&2
    tail -80 "$SUPER_DOLPHIN_BACKEND_LOG" >&2 || true
  fi
}

print_frontend_log_tail() {
  if [ -f "$SUPER_DOLPHIN_FRONTEND_LOG" ]; then
    echo "  → frontend log tail: $SUPER_DOLPHIN_FRONTEND_LOG" >&2
    tail -80 "$SUPER_DOLPHIN_FRONTEND_LOG" >&2 || true
  fi
}

wait_for_backend() {
  local url="http://${SUPER_DOLPHIN_HTTP_ADDR}/metrics"
  for _ in $(seq 1 100); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      echo "  → desktop backend ready: $url"
      return 0
    fi
    if [ -n "${DESKTOP_PID:-}" ] && process_exited "$DESKTOP_PID"; then
      echo "❌ desktop backend exited before readiness: $url" >&2
      print_backend_log_tail
      wait "$DESKTOP_PID" || true
      exit 1
    fi
    sleep 0.2
  done
  echo "❌ timed out waiting for desktop backend: $url" >&2
  print_backend_log_tail
  exit 1
}

wait_for_any_process_exit() {
  while true; do
    if [ -n "${DESKTOP_PID:-}" ] && process_exited "$DESKTOP_PID"; then
      wait "$DESKTOP_PID"
      local status="$?"
      if [ "$status" -ne 0 ]; then
        print_backend_log_tail
      fi
      return "$status"
    fi
    if [ -n "${VITE_PID:-}" ] && process_exited "$VITE_PID"; then
      wait "$VITE_PID"
      local status="$?"
      if [ "$status" -ne 0 ]; then
        print_frontend_log_tail
      fi
      return "$status"
    fi
    sleep 0.5
  done
}

backend_hot_reload_enabled() {
  case "${SUPER_DOLPHIN_BACKEND_HOT_RELOAD:-0}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

start_desktop_backend() {
  if backend_hot_reload_enabled; then
    rebuild_peer_binaries
  fi
  {
    echo
    echo "===== desktop backend start $(date -u '+%Y-%m-%dT%H:%M:%SZ') ====="
  } >>"$SUPER_DOLPHIN_BACKEND_LOG"
  (cd "$PROJECT_DIR" && go run ./cmd/agent-terminal >>"$SUPER_DOLPHIN_BACKEND_LOG" 2>&1) &
  DESKTOP_PID=$!
  echo "  → desktop backend started (PID: $DESKTOP_PID)"
}

restart_desktop_backend() {
  echo "  → backend source changed; restarting desktop backend"
  if [ -n "${DESKTOP_PID:-}" ]; then
    stop_process_tree "new UI desktop backend" "$DESKTOP_PID"
    wait_for_port_free "$SUPER_DOLPHIN_HTTP_ADDR" "desktop backend"
  fi
  start_desktop_backend
  wait_for_backend
  seed_dev_preferences
}

stat_backend_watch_file() {
  local file="$1"
  stat -f '%m %z %N' "$file" 2>/dev/null || stat -c '%Y %s %n' "$file" 2>/dev/null || true
}

snapshot_backend_watch_state() {
  local rel path
  for rel in $SUPER_DOLPHIN_HOT_WATCH_PATHS; do
    path="$PROJECT_DIR/$rel"
    if [ ! -e "$path" ]; then
      continue
    fi
    if [ -f "$path" ]; then
      printf '%s\n' "$path"
      continue
    fi
    find "$path" \
      \( -path '*/.git' -o -path '*/.tmp' -o -path '*/.build-cache' -o -path '*/bin' -o -path '*/dist' -o -path '*/node_modules' -o -path '*/frontend-app/dist' -o -path '*/cmd/agent-terminal/frontend/dist' \) -prune -o \
      -type f \( -name '*.go' -o -name '*.sql' -o -name '*.yaml' -o -name '*.yml' -o -name '*.json' -o -name 'go.mod' -o -name 'go.sum' \) -print
  done | LC_ALL=C sort -u | while IFS= read -r file; do
    stat_backend_watch_file "$file"
  done
}

run_backend_hot_supervisor_loop() {
  local interval="$SUPER_DOLPHIN_HOT_POLL_INTERVAL"
  local previous current status
  previous="$(snapshot_backend_watch_state)"
  echo "  backend hot reload: enabled"
  echo "  backend watch paths: $SUPER_DOLPHIN_HOT_WATCH_PATHS"
  echo "  backend poll interval: ${interval}s"
  while true; do
    if [ -n "${VITE_PID:-}" ] && process_exited "$VITE_PID"; then
      wait "$VITE_PID"
      status="$?"
      if [ "$status" -ne 0 ]; then
        print_frontend_log_tail
      fi
      return "$status"
    fi
    if [ -n "${DESKTOP_PID:-}" ] && process_exited "$DESKTOP_PID"; then
      wait "$DESKTOP_PID"
      status="$?"
      if [ "$status" -ne 0 ]; then
        print_backend_log_tail
      fi
      return "$status"
    fi
    sleep "$interval"
    current="$(snapshot_backend_watch_state)"
    if [ "$current" != "$previous" ]; then
      previous="$current"
      restart_desktop_backend
      previous="$(snapshot_backend_watch_state)"
    fi
  done
}

seed_dev_preferences() {
  case "${SUPER_DOLPHIN_SEED_DEV_PREFERENCES:-1}" in
    0|false|FALSE|no|NO)
      echo "  → dev provider preference seed skipped"
      return 0
      ;;
  esac
  if [ "${DEV_LOCAL_POSTGRES_MANAGED:-}" != "1" ]; then
    return 0
  fi
  if [ -z "$SUPER_DOLPHIN_DEV_PROVIDER" ] || [ -z "$SUPER_DOLPHIN_DEV_CODEX_MODEL" ] || [ -z "$SUPER_DOLPHIN_DEV_CODEX_EFFORT" ] || [ -z "$SUPER_DOLPHIN_DEV_CODEX_HOME" ] || [ -z "$SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY" ] || [ -z "$SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER" ]; then
    echo "❌ dev provider preferences require non-empty SUPER_DOLPHIN_DEV_PROVIDER, SUPER_DOLPHIN_DEV_CODEX_MODEL, SUPER_DOLPHIN_DEV_CODEX_EFFORT, SUPER_DOLPHIN_DEV_CODEX_HOME, SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY, and SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER" >&2
    exit 1
  fi
  if [ "$SUPER_DOLPHIN_DEV_PROVIDER" != "codex" ]; then
    echo "❌ run-new-ui-desktop.sh only seeds codex dev provider preferences; got SUPER_DOLPHIN_DEV_PROVIDER=$SUPER_DOLPHIN_DEV_PROVIDER" >&2
    exit 1
  fi
  if [ ! -x "$SUPER_DOLPHIN_POSTGRES_BIN_DIR/psql" ]; then
    echo "❌ missing PostgreSQL psql binary: $SUPER_DOLPHIN_POSTGRES_BIN_DIR/psql" >&2
    exit 1
  fi

  "$SUPER_DOLPHIN_POSTGRES_BIN_DIR/psql" "$DATABASE_URL" \
    -v ON_ERROR_STOP=1 \
    -v active_provider="$SUPER_DOLPHIN_DEV_PROVIDER" \
    -v codex_model="$SUPER_DOLPHIN_DEV_CODEX_MODEL" \
    -v codex_effort="$SUPER_DOLPHIN_DEV_CODEX_EFFORT" \
    -v codex_home="$SUPER_DOLPHIN_DEV_CODEX_HOME" \
    -v codex_instance_key="$SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY" \
    -v codex_model_provider="$SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER" <<'SQL' >/dev/null
INSERT INTO ui_preferences (cwd, key, value)
VALUES
  ('', 'settings.provider.active', to_jsonb(:'active_provider'::text)),
  ('', 'settings.provider.codex.model', to_jsonb(:'codex_model'::text)),
  ('', 'settings.provider.codex.effort', to_jsonb(:'codex_effort'::text)),
  ('', 'settings.provider.codex.codexHome', to_jsonb(:'codex_home'::text)),
  ('', 'settings.provider.codex.codexInstanceKey', to_jsonb(:'codex_instance_key'::text)),
  ('', 'settings.provider.codex.codexModelProvider', to_jsonb(:'codex_model_provider'::text))
ON CONFLICT (cwd, key) DO NOTHING;
SQL
  echo "  → dev provider preferences ready"
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
    DATABASE_URL="${DATABASE_URL:-postgres://super_dolphin@127.0.0.1:${SUPER_DOLPHIN_LOCAL_POSTGRES_PORT}/super_dolphin?sslmode=disable}"
    export DATABASE_URL
    DEV_LOCAL_POSTGRES_MANAGED="1"
    echo "  → local PostgreSQL enabled for dev runtime"
  fi
}

initialize_local_postgres_data_dir() {
  mkdir -p "$(dirname "$SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR")"
  echo "  → initializing local PostgreSQL data dir: $SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR"
  "$SUPER_DOLPHIN_POSTGRES_BIN_DIR/initdb" -D "$SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR" \
    -U super_dolphin \
    -L "$SUPER_DOLPHIN_POSTGRES_SHARE_DIR" \
    --locale=C \
    --auth=trust \
    --encoding=UTF8 >/dev/null
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
    if [ "${DEV_LOCAL_POSTGRES_MANAGED:-}" = "1" ]; then
      initialize_local_postgres_data_dir
    else
      echo "❌ DATABASE_URL points to local PostgreSQL ($host:$port), but data dir is not initialized: $SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR" >&2
      echo "   Start PostgreSQL manually, set DATABASE_URL to a reachable database, or initialize the local data dir." >&2
      exit 1
    fi
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
  if [ "${CLEANUP_DONE:-}" = "1" ]; then
    return 0
  fi
  CLEANUP_DONE="1"
  if [ -n "${VITE_PID:-}" ]; then
    stop_process_tree "frontend-app vite" "$VITE_PID"
  fi
  if [ -n "${DESKTOP_PID:-}" ]; then
    stop_process_tree "new UI desktop backend" "$DESKTOP_PID"
  fi
  if [ "${LOCAL_POSTGRES_STARTED:-}" = "1" ]; then
    echo "  → stopping local PostgreSQL"
    "$SUPER_DOLPHIN_POSTGRES_BIN_DIR/pg_ctl" -D "$SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR" -w -t 30 stop -m fast >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
trap 'cleanup; exit 130' INT TERM HUP

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
SUPER_DOLPHIN_BACKEND_HOT_RELOAD="${SUPER_DOLPHIN_BACKEND_HOT_RELOAD:-0}"
SUPER_DOLPHIN_HOT_PEER_BIN_DIR="${SUPER_DOLPHIN_HOT_PEER_BIN_DIR:-$PROJECT_DIR/.tmp/run-new-ui-desktop-hot/peers}"
if backend_hot_reload_enabled; then
  GO_AGENT_PEER_BIN_DIR="${GO_AGENT_PEER_BIN_DIR:-$SUPER_DOLPHIN_HOT_PEER_BIN_DIR}"
else
  GO_AGENT_PEER_BIN_DIR="${GO_AGENT_PEER_BIN_DIR:-$PROJECT_DIR}"
fi
SUPER_DOLPHIN_HOT_WATCH_PATHS="${SUPER_DOLPHIN_HOT_WATCH_PATHS:-cmd internal pkg migrations sql go.mod go.sum sqlc.yaml}"
SUPER_DOLPHIN_HOT_POLL_INTERVAL="${SUPER_DOLPHIN_HOT_POLL_INTERVAL:-1}"
SUPER_DOLPHIN_RUNTIME_MODE="${SUPER_DOLPHIN_RUNTIME_MODE:-dev}"
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="${SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR:-$PROJECT_DIR}"
SUPER_DOLPHIN_DEV_ENTRYPOINT="${SUPER_DOLPHIN_DEV_ENTRYPOINT:-run-new-ui-desktop.sh}"
SUPER_DOLPHIN_HOME="${SUPER_DOLPHIN_HOME:-/tmp/sd-new-ui-${USER:-user}/super-dolphin-home}"
SUPER_DOLPHIN_BACKEND_LOG="${SUPER_DOLPHIN_BACKEND_LOG:-$PROJECT_DIR/.tmp/run-new-ui-desktop/backend.log}"
SUPER_DOLPHIN_FRONTEND_LOG="${SUPER_DOLPHIN_FRONTEND_LOG:-$PROJECT_DIR/.tmp/run-new-ui-desktop/frontend.log}"
SUPER_DOLPHIN_DEV_PROVIDER="${SUPER_DOLPHIN_DEV_PROVIDER:-codex}"
SUPER_DOLPHIN_DEV_CODEX_MODEL="${SUPER_DOLPHIN_DEV_CODEX_MODEL:-gpt-5.5}"
SUPER_DOLPHIN_DEV_CODEX_EFFORT="${SUPER_DOLPHIN_DEV_CODEX_EFFORT:-xhigh}"
SUPER_DOLPHIN_DEV_CODEX_HOME="${SUPER_DOLPHIN_DEV_CODEX_HOME:-$HOME/.codex}"
SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY="${SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY:-default}"
SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER="${SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER:-openai}"
SUPER_DOLPHIN_LOCAL_POSTGRES_PORT="${SUPER_DOLPHIN_LOCAL_POSTGRES_PORT:-55433}"
SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR="${SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR:-$PROJECT_DIR/.tmp/pgdata}"
SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR="${SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR:-$PROJECT_DIR/.tmp/pgsocket}"
SUPER_DOLPHIN_LOCAL_POSTGRES_LOG="${SUPER_DOLPHIN_LOCAL_POSTGRES_LOG:-$PROJECT_DIR/.tmp/postgres.log}"
export SUPER_DOLPHIN_HTTP_ADDR GO_AGENT_CTL_RPC_ADDR VITE_DEV_URL FRONTEND_DEVSERVER_URL GO_AGENT_PEER_BIN_DIR
export SUPER_DOLPHIN_RUNTIME_MODE SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR SUPER_DOLPHIN_DEV_ENTRYPOINT
export SUPER_DOLPHIN_HOME SUPER_DOLPHIN_BACKEND_HOT_RELOAD SUPER_DOLPHIN_HOT_WATCH_PATHS SUPER_DOLPHIN_HOT_POLL_INTERVAL
export SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR SUPER_DOLPHIN_LOCAL_POSTGRES_LOG
export LOG_LEVEL="${LOG_LEVEL:-debug}"
export ENABLE_MEMORY_SYSTEM="${ENABLE_MEMORY_SYSTEM:-1}"
export ENABLE_MEMORY_TOOLS="${ENABLE_MEMORY_TOOLS:-1}"
export MULTI_AGENT_MEMORY_FEATURE_TEAMMEM="${MULTI_AGENT_MEMORY_FEATURE_TEAMMEM:-1}"
export CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME="${CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME:-1}"

mkdir -p "$(dirname "$SUPER_DOLPHIN_BACKEND_LOG")" "$(dirname "$SUPER_DOLPHIN_FRONTEND_LOG")" "$SUPER_DOLPHIN_HOME"
ensure_dev_control_session_token
configure_dev_postgres_runtime
stop_stale_vite_for_port "$VITE_DEV_PORT"
fail_if_port_busy "$VITE_DEV_HOST:$VITE_DEV_PORT"
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
echo "  home:         $SUPER_DOLPHIN_HOME"
echo "  logs:         $SUPER_DOLPHIN_BACKEND_LOG"
if backend_hot_reload_enabled; then
  echo "  backend hot:  enabled"
fi

: >"$SUPER_DOLPHIN_BACKEND_LOG"
start_desktop_backend
wait_for_backend
seed_dev_preferences

(cd "$FRONTEND_APP_DIR" && npm run dev -- --host "$VITE_DEV_HOST" --port "$VITE_DEV_PORT" --strictPort >"$SUPER_DOLPHIN_FRONTEND_LOG" 2>&1) &
VITE_PID=$!
wait_for_http "$VITE_DEV_URL" "frontend-app vite"

if backend_hot_reload_enabled; then
  run_backend_hot_supervisor_loop
else
  wait_for_any_process_exit
fi
