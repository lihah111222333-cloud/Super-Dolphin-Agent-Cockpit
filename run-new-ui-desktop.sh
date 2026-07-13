#!/usr/bin/env bash
# run-new-ui-desktop.sh — start the refactored frontend-app in a desktop Wails shell.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
FRONTEND_APP_DIR="$PROJECT_DIR/frontend-app"
NPM_REGISTRY="${NPM_REGISTRY:-https://registry.npmmirror.com}"

if [ -f "$PROJECT_DIR/.env" ]; then
  echo "❌ .env is not supported because shell-sourcing it can execute commands." >&2
  echo "   Remove .env, run ./run-new-ui-desktop.sh --help, and export required variables explicitly." >&2
  exit 1
fi

if [ "${1:-}" = "--help" ]; then
  echo "Usage: export NAME=value; ./run-new-ui-desktop.sh"
  echo "Environment configuration must be supplied explicitly; .env files are rejected."
  exit 0
elif [ "$#" -ne 0 ]; then
  echo "❌ unsupported argument: $1 (use --help)" >&2
  exit 1
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

ensure_dev_codex_cli() {
  local candidate="${SUPER_DOLPHIN_DEV_CODEX_CLI:-}"
  local candidate_dir

  if [ -n "$candidate" ] && [ "${candidate#/}" = "$candidate" ]; then
    candidate="$(command -v "$candidate" 2>/dev/null || true)"
  fi
  if [ -z "$candidate" ]; then
    candidate="$(command -v codex 2>/dev/null || true)"
  fi
  if [ -z "$candidate" ] && [ "$(uname -s)" = "Darwin" ] && [ -x "/Applications/ChatGPT.app/Contents/Resources/codex" ]; then
    candidate="/Applications/ChatGPT.app/Contents/Resources/codex"
  fi
  if [ -z "$candidate" ] || [ ! -x "$candidate" ]; then
    echo "❌ Codex CLI is required; set SUPER_DOLPHIN_DEV_CODEX_CLI to an executable path" >&2
    exit 1
  fi
  if ! "$candidate" app-server --help >/dev/null 2>&1; then
    echo "❌ Codex CLI does not support app-server: $candidate" >&2
    exit 1
  fi

  candidate_dir="$(cd "$(dirname "$candidate")" && pwd)"
  case ":$PATH:" in
    *":$candidate_dir:"*) ;;
    *) PATH="$candidate_dir:$PATH" ;;
  esac
  SUPER_DOLPHIN_DEV_CODEX_CLI="$candidate"
  export PATH SUPER_DOLPHIN_DEV_CODEX_CLI
}

ensure_node_deps() {
  local dir="$1"
  if [ ! -f "$dir/package.json" ]; then
    echo "❌ missing package.json: $dir"
    exit 1
  fi
  cd "$dir"
  local package_manager="${SUPER_DOLPHIN_FRONTEND_PACKAGE_MANAGER:-}"
  if [ -z "$package_manager" ]; then
    if [ -f pnpm-lock.yaml ]; then
      package_manager="pnpm"
    elif command -v npm >/dev/null 2>&1 && ! npm_command_is_pnpm_shim; then
      package_manager="npm"
    elif command -v pnpm >/dev/null 2>&1; then
      package_manager="pnpm"
    else
      echo "❌ no supported frontend package manager found (need npm or pnpm)" >&2
      exit 1
    fi
  fi

  if [ ! -d node_modules ] || [ ! -x node_modules/.bin/vite ]; then
    install_node_deps "$package_manager" "$dir"
  elif [ -f package-lock.json ] && [ package-lock.json -nt node_modules ]; then
    install_node_deps "$package_manager" "package-lock changed"
  elif [ -f pnpm-lock.yaml ] && [ pnpm-lock.yaml -nt node_modules ]; then
    install_node_deps "$package_manager" "pnpm-lock changed"
  elif [ package.json -nt node_modules ]; then
    install_node_deps "$package_manager" "package.json changed"
  else
    echo "  → dependencies unchanged"
  fi
}

npm_command_is_pnpm_shim() {
  local npm_path
  local user_agent
  npm_path="$(command -v npm 2>/dev/null || true)"
  if [ -n "$npm_path" ] && sed -n '1,24p' "$npm_path" 2>/dev/null | grep -q 'PNPM='; then
    return 0
  fi
  user_agent="$(npm config get user-agent 2>/dev/null || true)"
  case "$user_agent" in
    pnpm/*) return 0 ;;
  esac
  return 1
}

install_node_deps() {
  local package_manager="$1"
  local reason="$2"
  case "$package_manager" in
    npm)
      if npm_command_is_pnpm_shim; then
        echo "❌ npm resolves to pnpm shim; set SUPER_DOLPHIN_FRONTEND_PACKAGE_MANAGER=pnpm or fix PATH" >&2
        exit 1
      fi
      if [ -f package-lock.json ]; then
        echo "  → npm ci ($reason)"
        npm ci --registry="$NPM_REGISTRY"
      else
        echo "  → npm install ($reason)"
        npm install --registry="$NPM_REGISTRY"
      fi
      ;;
    pnpm)
      if [ -f pnpm-lock.yaml ]; then
        echo "  → pnpm install --frozen-lockfile ($reason)"
        pnpm install --frozen-lockfile --registry="$NPM_REGISTRY"
      else
        echo "  → pnpm install --no-frozen-lockfile --lockfile=false ($reason)"
        pnpm install --no-frozen-lockfile --lockfile=false --registry="$NPM_REGISTRY"
      fi
      ;;
    *)
      echo "❌ unsupported SUPER_DOLPHIN_FRONTEND_PACKAGE_MANAGER: $package_manager" >&2
      exit 1
      ;;
  esac
}

peer_binary_stale() {
  local bin="$1"
  shift
  local rel path
  if [ ! -x "$bin" ]; then
    return 0
  fi
  for rel in "$@"; do
    path="$PROJECT_DIR/$rel"
    if [ ! -e "$path" ]; then
      continue
    fi
    if find "$path" -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' -o -name '*.yaml' -o -name '*.yml' \) -newer "$bin" -print -quit | grep -q .; then
      return 0
    fi
  done
  return 1
}

ensure_peer_binaries() {
  local peer_dir="${GO_AGENT_PEER_BIN_DIR:-$PROJECT_DIR}"
  local needs_rebuild=0
  for bin in mcp-orch mcp-lsp; do
    if [ ! -x "$peer_dir/$bin" ]; then
      needs_rebuild=1
      break
    fi
  done
  if [ "$needs_rebuild" = "0" ] && peer_binary_stale "$peer_dir/mcp-orch" cmd/mcp-orch internal pkg go.mod go.sum; then
    needs_rebuild=1
  fi
  if [ "$needs_rebuild" = "0" ] && peer_binary_stale "$peer_dir/mcp-lsp" cmd/mcp-lsp internal pkg go.mod go.sum; then
    needs_rebuild=1
  fi
  if [ "$needs_rebuild" = "0" ]; then
    return 0
  fi
  rebuild_peer_binaries
}

require_positive_integer() {
  local name="$1"
  local value="$2"
  case "$value" in
    ''|*[!0-9]*)
      echo "❌ $name must be a positive integer, got: $value" >&2
      exit 1
      ;;
  esac
  if [ "$value" -le 0 ]; then
    echo "❌ $name must be a positive integer, got: $value" >&2
    exit 1
  fi
}

require_positive_number() {
  local name="$1"
  local value="$2"
  if ! awk -v value="$value" 'BEGIN { if (value ~ /^[0-9]+([.][0-9]+)?$/ && value + 0 > 0) exit 0; exit 1 }'; then
    echo "❌ $name must be a positive number, got: $value" >&2
    exit 1
  fi
}

validate_vite_dev_url() {
  local raw="$1"
  local authority rest
  case "$raw" in
    http://*) rest="${raw#http://}" ;;
    https://*) rest="${raw#https://}" ;;
    *)
      echo "❌ VITE_DEV_URL must use loopback http/https with host and port, got: $raw" >&2
      exit 1
      ;;
  esac
  authority="${rest%%/*}"
  authority="${authority%%\?*}"
  authority="${authority%%#*}"
  if [ -z "$authority" ] || [ "$authority" != "${authority#*@}" ]; then
    echo "❌ VITE_DEV_URL must use loopback http/https with host and port, got: $raw" >&2
    exit 1
  fi
  case "$authority" in
    \[*\]:*)
      VITE_DEV_HOST="${authority%%]*}"
      VITE_DEV_HOST="${VITE_DEV_HOST#[}"
      VITE_DEV_PORT="${authority##*:}"
      ;;
    *:*)
      VITE_DEV_HOST="${authority%:*}"
      VITE_DEV_PORT="${authority##*:}"
      ;;
    *)
      echo "❌ VITE_DEV_URL must use loopback http/https with host and port, got: $raw" >&2
      exit 1
      ;;
  esac
  case "$VITE_DEV_PORT" in
    ''|*[!0-9]*)
      echo "❌ VITE_DEV_URL must use loopback http/https with host and port, got: $raw" >&2
      exit 1
      ;;
  esac
  if [ "$VITE_DEV_PORT" -le 0 ]; then
    echo "❌ VITE_DEV_URL must use loopback http/https with host and port, got: $raw" >&2
    exit 1
  fi
  case "$VITE_DEV_HOST" in
    localhost|127.0.0.1|::1)
      ;;
    *)
      echo "❌ VITE_DEV_URL must use loopback http/https with host and port, got: $raw" >&2
      exit 1
      ;;
  esac
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
  local attempts="$3"
  for _ in $(seq 1 "$attempts"); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      echo "  → $label ready: $url"
      return 0
    fi
    # 误判防护：wait_for_http/process_exited/print_*_log_tail 防止前端 readiness 失败静默超时。
    if [ "$label" = "frontend-app vite" ]; then
      if [ -n "${VITE_PID:-}" ] && process_exited "$VITE_PID"; then
        echo "❌ $label exited before readiness: $url" >&2
        capture_wait_status "$VITE_PID"
        print_frontend_log_tail
        exit 1
      fi
      if [ -n "${DESKTOP_PID:-}" ] && process_exited "$DESKTOP_PID"; then
        echo "❌ desktop backend exited before $label readiness: $url" >&2
        capture_wait_status "$DESKTOP_PID"
        print_backend_log_tail
        exit 1
      fi
    fi
    sleep "$SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS"
  done
  echo "❌ timed out waiting for $label: $url" >&2
  if [ "$label" = "frontend-app vite" ]; then
    print_frontend_log_tail
  fi
  exit 1
}

fail_if_port_busy() {
  local addr="$1"
  local port="${addr##*:}"
  # 误判防护：fail_if_port_busy 让非 stale 端口占用 fail-fast，避免连到旧进程。
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
  # 误判防护：wait_for_port_free 是后端重启前的端口释放守卫。
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
  # 误判防护：process_exited 同时识别普通退出和 zombie 进程。
  if ! kill -0 "$pid" 2>/dev/null; then
    return 0
  fi
  stat="$(ps -p "$pid" -o stat= 2>/dev/null || true)"
  case "$stat" in
    *Z*) return 0 ;;
  esac
  return 1
}

capture_wait_status() {
  local pid="$1"
  WAIT_STATUS=0
  set +e
  wait "$pid"
  WAIT_STATUS="$?"
  set -e
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
  # 误判防护：stop_process_tree 递归停止子进程，cleanup trap 依赖该守卫防泄漏。
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
        # 误判防护：stop_stale_vite_for_port 只清理 cwd 为 frontend-app 的同端口 Vite。
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
    if grep -q "ENOSPC: System limit for number of file watchers reached" "$SUPER_DOLPHIN_FRONTEND_LOG"; then
      echo "  → frontend watcher limit hit; default polling should avoid this, or increase fs.inotify.max_user_watches for native watching" >&2
    fi
  fi
}

wait_for_backend() {
  local url="http://${SUPER_DOLPHIN_HTTP_ADDR}/metrics"
  for _ in $(seq 1 "$SUPER_DOLPHIN_BACKEND_READY_ATTEMPTS"); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      echo "  → desktop backend ready: $url"
      return 0
    fi
    # 误判防护：wait_for_backend/process_exited/print_backend_log_tail 防止后端失败静默等待。
    if [ -n "${DESKTOP_PID:-}" ] && process_exited "$DESKTOP_PID"; then
      echo "❌ desktop backend exited before readiness: $url" >&2
      capture_wait_status "$DESKTOP_PID"
      print_backend_log_tail
      exit 1
    fi
    sleep "$SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS"
  done
  echo "❌ timed out waiting for desktop backend: $url" >&2
  print_backend_log_tail
  exit 1
}

wait_for_any_process_exit() {
  local status
  while true; do
    if [ -n "${DESKTOP_PID:-}" ] && process_exited "$DESKTOP_PID"; then
      capture_wait_status "$DESKTOP_PID"
      status="$WAIT_STATUS"
      if [ "$status" -ne 0 ]; then
        print_backend_log_tail
      fi
      return "$status"
    fi
    if [ -n "${VITE_PID:-}" ] && process_exited "$VITE_PID"; then
      capture_wait_status "$VITE_PID"
      status="$WAIT_STATUS"
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

backend_hot_reload_config_error() {
  echo "❌ $1" >&2
  exit 1
}

validate_backend_hot_positive_integer() {
  local name="$1"
  local value="$2"
  local max="$3"
  if [ -z "$value" ]; then
    backend_hot_reload_config_error "$name must be a positive integer"
  fi
  if ! [[ "$value" =~ ^[0-9]+$ ]]; then
    backend_hot_reload_config_error "$name must be a positive integer, got: $value"
  fi
  if ! awk -v value="$value" -v max="$max" 'BEGIN { exit (value >= 1 && value <= max ? 0 : 1) }'; then
    backend_hot_reload_config_error "$name must be between 1 and $max, got: $value"
  fi
}

validate_backend_hot_poll_interval() {
  local value="$1"
  local min="${SUPER_DOLPHIN_HOT_MIN_POLL_INTERVAL:-1}"
  if [ -z "$value" ]; then
    backend_hot_reload_config_error "SUPER_DOLPHIN_HOT_POLL_INTERVAL must be a numeric value at least ${min}s"
  fi
  if ! [[ "$value" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    backend_hot_reload_config_error "SUPER_DOLPHIN_HOT_POLL_INTERVAL must be a numeric value at least ${min}s, got: $value"
  fi
  if ! awk -v value="$value" -v min="$min" 'BEGIN { exit (value >= min ? 0 : 1) }'; then
    backend_hot_reload_config_error "SUPER_DOLPHIN_HOT_POLL_INTERVAL must be at least ${min}s, got: $value"
  fi
}

validate_backend_hot_watch_paths() {
  local paths="$1"
  local count=0
  local rel path
  if [ -z "$paths" ]; then
    backend_hot_reload_config_error "SUPER_DOLPHIN_HOT_WATCH_PATHS must not be empty"
  fi
  for rel in $paths; do
    count=$((count + 1))
    if [ "$count" -gt "$SUPER_DOLPHIN_HOT_MAX_WATCH_PATHS" ]; then
      backend_hot_reload_config_error "backend hot reload watch path limit exceeded ($count > $SUPER_DOLPHIN_HOT_MAX_WATCH_PATHS)"
    fi
    case "$rel" in
      /*)
        backend_hot_reload_config_error "SUPER_DOLPHIN_HOT_WATCH_PATHS entries must be repository-relative paths, got: $rel"
        ;;
      *//*)
        backend_hot_reload_config_error "SUPER_DOLPHIN_HOT_WATCH_PATHS entries must be repository-relative paths without empty segments, got: $rel"
        ;;
    esac
    case "/$rel/" in
      */../*|*/./*)
        backend_hot_reload_config_error "SUPER_DOLPHIN_HOT_WATCH_PATHS entries must be repository-relative paths without . or .. segments, got: $rel"
        ;;
    esac
    path="$PROJECT_DIR/$rel"
    if [ ! -e "$path" ]; then
      backend_hot_reload_config_error "backend hot reload watch path does not exist: $rel"
    fi
  done
  if [ "$count" -eq 0 ]; then
    backend_hot_reload_config_error "SUPER_DOLPHIN_HOT_WATCH_PATHS must include at least one path"
  fi
}

validate_backend_hot_reload_config() {
  local max_paths_limit="${SUPER_DOLPHIN_HOT_MAX_WATCH_PATHS_LIMIT:-64}"
  local max_files_limit="${SUPER_DOLPHIN_HOT_MAX_WATCH_FILES_LIMIT:-20000}"
  validate_backend_hot_positive_integer "SUPER_DOLPHIN_HOT_MAX_WATCH_PATHS" "$SUPER_DOLPHIN_HOT_MAX_WATCH_PATHS" "$max_paths_limit"
  validate_backend_hot_positive_integer "SUPER_DOLPHIN_HOT_MAX_WATCH_FILES" "$SUPER_DOLPHIN_HOT_MAX_WATCH_FILES" "$max_files_limit"
  validate_backend_hot_poll_interval "$SUPER_DOLPHIN_HOT_POLL_INTERVAL"
  validate_backend_hot_watch_paths "$SUPER_DOLPHIN_HOT_WATCH_PATHS"
}

parse_frontend_watch_bool() {
  local name="$1"
  local value="$2"
  case "$value" in
    1|true|TRUE|yes|YES|on|ON) printf '1\n' ;;
    0|false|FALSE|no|NO|off|OFF) printf '0\n' ;;
    "")
      echo "❌ $name must be a boolean (1/0, true/false, yes/no, on/off); got empty value" >&2
      return 1
      ;;
    *)
      echo "❌ $name must be a boolean (1/0, true/false, yes/no, on/off); got: $value" >&2
      return 1
      ;;
  esac
}

resolve_frontend_watch_polling() {
  local vite_set="0"
  local chokidar_set="0"
  local vite_polling=""
  local chokidar_polling=""
  if [ -n "${SUPER_DOLPHIN_VITE_USE_POLLING+x}" ]; then
    vite_set="1"
    vite_polling="$(parse_frontend_watch_bool "SUPER_DOLPHIN_VITE_USE_POLLING" "$SUPER_DOLPHIN_VITE_USE_POLLING")" || return 1
  fi
  if [ -n "${CHOKIDAR_USEPOLLING+x}" ]; then
    chokidar_set="1"
    chokidar_polling="$(parse_frontend_watch_bool "CHOKIDAR_USEPOLLING" "$CHOKIDAR_USEPOLLING")" || return 1
  fi
  if [ "$vite_set" = "1" ] && [ "$chokidar_set" = "1" ] && [ "$vite_polling" != "$chokidar_polling" ]; then
    echo "❌ conflicting frontend watch config: SUPER_DOLPHIN_VITE_USE_POLLING resolves to $vite_polling but CHOKIDAR_USEPOLLING resolves to $chokidar_polling" >&2
    return 1
  fi
  if [ "$vite_set" = "1" ]; then
    printf '%s\n' "$vite_polling"
    return 0
  fi
  if [ "$chokidar_set" = "1" ]; then
    printf '%s\n' "$chokidar_polling"
    return 0
  fi
  printf '1\n'
}

configure_frontend_watch_mode() {
  local polling
  polling="$(resolve_frontend_watch_polling)" || return 1
  SUPER_DOLPHIN_VITE_USE_POLLING="$polling"
  CHOKIDAR_USEPOLLING="$polling"
  export SUPER_DOLPHIN_VITE_USE_POLLING
  export CHOKIDAR_USEPOLLING
  if [ "$polling" = "1" ]; then
    FRONTEND_WATCH_MODE="polling (SUPER_DOLPHIN_VITE_USE_POLLING=$SUPER_DOLPHIN_VITE_USE_POLLING, CHOKIDAR_USEPOLLING=$CHOKIDAR_USEPOLLING)"
    return 0
  fi
  FRONTEND_WATCH_MODE="native fs events (SUPER_DOLPHIN_VITE_USE_POLLING=$SUPER_DOLPHIN_VITE_USE_POLLING, CHOKIDAR_USEPOLLING=$CHOKIDAR_USEPOLLING)"
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
}

stat_backend_watch_file() {
  local file="$1"
  stat -f '%m %z %N' "$file" 2>/dev/null || stat -c '%Y %s %n' "$file" 2>/dev/null || true
}

list_backend_watch_files() {
  local rel path
  for rel in $SUPER_DOLPHIN_HOT_WATCH_PATHS; do
    path="$PROJECT_DIR/$rel"
    if [ ! -e "$path" ]; then
      backend_hot_reload_config_error "backend hot reload watch path does not exist: $rel"
    fi
    if [ -f "$path" ]; then
      printf '%s\n' "$path"
      continue
    fi
    find "$path" \
      \( -path '*/.git' -o -path '*/.tmp' -o -path '*/.build-cache' -o -path '*/bin' -o -path '*/dist' -o -path '*/node_modules' -o -path '*/frontend-app/dist' -o -path '*/cmd/agent-terminal/frontend/dist' \) -prune -o \
      -type f \( -name '*.go' -o -name '*.sql' -o -name '*.yaml' -o -name '*.yml' -o -name '*.json' -o -name 'go.mod' -o -name 'go.sum' \) -print
  done
}

enforce_backend_watch_file_limit() {
  local phase="$1"
  awk -v max="$SUPER_DOLPHIN_HOT_MAX_WATCH_FILES" -v phase="$phase" '
    {
      count += 1
      if (count > max) {
        printf "❌ backend hot reload %s file limit exceeded (%d > %d)\n", phase, count, max > "/dev/stderr"
        exit 1
      }
      print
    }
  '
}

snapshot_backend_watch_state() {
  local files
  files="$(list_backend_watch_files | enforce_backend_watch_file_limit "scan" | LC_ALL=C sort -u | enforce_backend_watch_file_limit "unique")"
  if [ -z "$files" ]; then
    return 0
  fi
  printf '%s\n' "$files" | while IFS= read -r file; do
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
      capture_wait_status "$VITE_PID"
      status="$WAIT_STATUS"
      if [ "$status" -ne 0 ]; then
        print_frontend_log_tail
      fi
      return "$status"
    fi
    if [ -n "${DESKTOP_PID:-}" ] && process_exited "$DESKTOP_PID"; then
      capture_wait_status "$DESKTOP_PID"
      status="$WAIT_STATUS"
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

ensure_sqlite_runtime() {
  export SUPER_DOLPHIN_PROCESS_ROLE="${SUPER_DOLPHIN_PROCESS_ROLE:-desktop}"
  SUPER_DOLPHIN_SQLITE_PATH="${SUPER_DOLPHIN_SQLITE_PATH:-$SUPER_DOLPHIN_HOME/super-dolphin.db}"
  if [ -z "${SUPER_DOLPHIN_SQLITE_PATH:-}" ]; then
    echo "SQLite database path is empty" >&2
    exit 1
  fi
  local sqlite_parent
  sqlite_parent="$(dirname "$SUPER_DOLPHIN_SQLITE_PATH")"
  if [ -z "$sqlite_parent" ] || [ "$sqlite_parent" = "." ]; then
    echo "SQLite database path must include a parent directory: $SUPER_DOLPHIN_SQLITE_PATH" >&2
    exit 1
  fi
  mkdir -p "$sqlite_parent"
  export SUPER_DOLPHIN_SQLITE_PATH
}

cleanup() {
  if [ "${CLEANUP_DONE:-}" = "1" ]; then
    return 0
  fi
  # 误判防护：cleanup 使用 CLEANUP_DONE 守卫，EXIT/INT/TERM/HUP trap 可重复触发也只清理一次。
  CLEANUP_DONE="1"
  if [ -n "${VITE_PID:-}" ]; then
    stop_process_tree "frontend-app vite" "$VITE_PID"
  fi
  if [ -n "${DESKTOP_PID:-}" ]; then
    stop_process_tree "new UI desktop backend" "$DESKTOP_PID"
  fi
}
trap cleanup EXIT
trap 'cleanup; exit 130' INT TERM HUP

SUPER_DOLPHIN_HTTP_ADDR="${SUPER_DOLPHIN_HTTP_ADDR:-127.0.0.1:4512}"
GO_AGENT_CTL_RPC_ADDR="${GO_AGENT_CTL_RPC_ADDR:-127.0.0.1:8092}"
VITE_DEV_URL="${VITE_DEV_URL:-http://127.0.0.1:5175}"
FRONTEND_DEVSERVER_URL="${FRONTEND_DEVSERVER_URL:-$VITE_DEV_URL}"
if [ "$FRONTEND_DEVSERVER_URL" != "$VITE_DEV_URL" ]; then
  echo "❌ FRONTEND_DEVSERVER_URL must match VITE_DEV_URL for Wails readiness, got FRONTEND_DEVSERVER_URL=$FRONTEND_DEVSERVER_URL VITE_DEV_URL=$VITE_DEV_URL" >&2
  exit 1
fi
validate_vite_dev_url "$VITE_DEV_URL"
SUPER_DOLPHIN_BACKEND_HOT_RELOAD="${SUPER_DOLPHIN_BACKEND_HOT_RELOAD:-0}"
SUPER_DOLPHIN_FRONTEND_READY_ATTEMPTS="${SUPER_DOLPHIN_FRONTEND_READY_ATTEMPTS:-300}"
SUPER_DOLPHIN_BACKEND_READY_ATTEMPTS="${SUPER_DOLPHIN_BACKEND_READY_ATTEMPTS:-300}"
SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS="${SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS:-0.2}"
require_positive_integer "SUPER_DOLPHIN_FRONTEND_READY_ATTEMPTS" "$SUPER_DOLPHIN_FRONTEND_READY_ATTEMPTS"
require_positive_integer "SUPER_DOLPHIN_BACKEND_READY_ATTEMPTS" "$SUPER_DOLPHIN_BACKEND_READY_ATTEMPTS"
require_positive_number "SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS" "$SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS"
SUPER_DOLPHIN_HOT_PEER_BIN_DIR="${SUPER_DOLPHIN_HOT_PEER_BIN_DIR:-$PROJECT_DIR/.tmp/run-new-ui-desktop/hot-peers}"
if backend_hot_reload_enabled; then
  GO_AGENT_PEER_BIN_DIR="${GO_AGENT_PEER_BIN_DIR:-$SUPER_DOLPHIN_HOT_PEER_BIN_DIR}"
else
  GO_AGENT_PEER_BIN_DIR="${GO_AGENT_PEER_BIN_DIR:-$PROJECT_DIR}"
fi
if [ -z "${SUPER_DOLPHIN_HOT_WATCH_PATHS+x}" ]; then
  SUPER_DOLPHIN_HOT_WATCH_PATHS="cmd internal pkg migrations sql go.mod go.sum sqlc.yaml"
fi
if [ -z "${SUPER_DOLPHIN_HOT_POLL_INTERVAL+x}" ]; then
  SUPER_DOLPHIN_HOT_POLL_INTERVAL="1"
fi
if [ -z "${SUPER_DOLPHIN_HOT_MAX_WATCH_PATHS+x}" ]; then
  SUPER_DOLPHIN_HOT_MAX_WATCH_PATHS="16"
fi
if [ -z "${SUPER_DOLPHIN_HOT_MAX_WATCH_FILES+x}" ]; then
  SUPER_DOLPHIN_HOT_MAX_WATCH_FILES="5000"
fi
SUPER_DOLPHIN_HOT_MIN_POLL_INTERVAL="1"
SUPER_DOLPHIN_HOT_MAX_WATCH_PATHS_LIMIT="64"
SUPER_DOLPHIN_HOT_MAX_WATCH_FILES_LIMIT="20000"
validate_backend_hot_reload_config
SUPER_DOLPHIN_RUNTIME_MODE="${SUPER_DOLPHIN_RUNTIME_MODE:-dev}"
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="${SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR:-$PROJECT_DIR}"
SUPER_DOLPHIN_DEV_ENTRYPOINT="${SUPER_DOLPHIN_DEV_ENTRYPOINT:-run-new-ui-desktop.sh}"
if [ -z "${SUPER_DOLPHIN_DEPENDENCY_PROFILE+x}" ]; then
  SUPER_DOLPHIN_DEPENDENCY_PROFILE="desktop_host"
fi
if [ -z "$SUPER_DOLPHIN_DEPENDENCY_PROFILE" ]; then
  echo "❌ SUPER_DOLPHIN_DEPENDENCY_PROFILE must not be empty" >&2
  exit 1
fi
SUPER_DOLPHIN_HOME="${SUPER_DOLPHIN_HOME:-/tmp/sd-new-ui-${USER:-user}/super-dolphin-home}"
SUPER_DOLPHIN_BACKEND_LOG="${SUPER_DOLPHIN_BACKEND_LOG:-$PROJECT_DIR/.tmp/run-new-ui-desktop/backend.log}"
SUPER_DOLPHIN_FRONTEND_LOG="${SUPER_DOLPHIN_FRONTEND_LOG:-$PROJECT_DIR/.tmp/run-new-ui-desktop/frontend.log}"
SUPER_DOLPHIN_DEV_PROVIDER="${SUPER_DOLPHIN_DEV_PROVIDER:-codex}"
SUPER_DOLPHIN_DEV_CODEX_MODEL="${SUPER_DOLPHIN_DEV_CODEX_MODEL:-gpt-5.5}"
SUPER_DOLPHIN_DEV_CODEX_EFFORT="${SUPER_DOLPHIN_DEV_CODEX_EFFORT:-xhigh}"
SUPER_DOLPHIN_DEV_CODEX_HOME="${SUPER_DOLPHIN_DEV_CODEX_HOME:-$HOME/.codex}"
SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY="${SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY:-default}"
SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER="${SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER:-openai}"
export SUPER_DOLPHIN_HTTP_ADDR GO_AGENT_CTL_RPC_ADDR VITE_DEV_URL FRONTEND_DEVSERVER_URL GO_AGENT_PEER_BIN_DIR
export SUPER_DOLPHIN_RUNTIME_MODE SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR SUPER_DOLPHIN_DEV_ENTRYPOINT
export SUPER_DOLPHIN_DEPENDENCY_PROFILE
export SUPER_DOLPHIN_HOME SUPER_DOLPHIN_BACKEND_HOT_RELOAD SUPER_DOLPHIN_HOT_WATCH_PATHS SUPER_DOLPHIN_HOT_POLL_INTERVAL
export LOG_LEVEL="${LOG_LEVEL:-debug}"
export ENABLE_MEMORY_SYSTEM="${ENABLE_MEMORY_SYSTEM:-1}"
export ENABLE_MEMORY_TOOLS="${ENABLE_MEMORY_TOOLS:-1}"
export MULTI_AGENT_MEMORY_FEATURE_TEAMMEM="${MULTI_AGENT_MEMORY_FEATURE_TEAMMEM:-1}"
export CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME="${CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME:-1}"

configure_frontend_watch_mode
mkdir -p "$(dirname "$SUPER_DOLPHIN_BACKEND_LOG")" "$(dirname "$SUPER_DOLPHIN_FRONTEND_LOG")" "$SUPER_DOLPHIN_HOME"
ensure_dev_control_session_token
ensure_sqlite_runtime
ensure_dev_codex_cli
stop_stale_vite_for_port "$VITE_DEV_PORT"
fail_if_port_busy "$VITE_DEV_HOST:$VITE_DEV_PORT"
fail_if_port_busy "$SUPER_DOLPHIN_HTTP_ADDR"
fail_if_port_busy "$GO_AGENT_CTL_RPC_ADDR"
ensure_node_deps "$FRONTEND_APP_DIR"
ensure_peer_binaries

echo "┌─────────────────────────────────────────┐"
echo "│  Super Agent new UI desktop             │"
echo "└─────────────────────────────────────────┘"
echo "  frontend-app: $FRONTEND_DEVSERVER_URL"
echo "  bridge:       $SUPER_DOLPHIN_HTTP_ADDR"
echo "  control rpc:  $GO_AGENT_CTL_RPC_ADDR"
echo "  peer bin dir: $GO_AGENT_PEER_BIN_DIR"
echo "  runtime:      $SUPER_DOLPHIN_RUNTIME_MODE ($SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR)"
echo "  home:         $SUPER_DOLPHIN_HOME"
echo "  sqlite:       $SUPER_DOLPHIN_SQLITE_PATH"
echo "  codex cli:    $SUPER_DOLPHIN_DEV_CODEX_CLI"
echo "  frontend watch: $FRONTEND_WATCH_MODE"
echo "  logs:         $SUPER_DOLPHIN_BACKEND_LOG"
if backend_hot_reload_enabled; then
  echo "  backend hot:  enabled"
fi

: >"$SUPER_DOLPHIN_BACKEND_LOG"
: >"$SUPER_DOLPHIN_FRONTEND_LOG"

(cd "$FRONTEND_APP_DIR" && npm run dev -- --host "$VITE_DEV_HOST" --port "$VITE_DEV_PORT" --strictPort >"$SUPER_DOLPHIN_FRONTEND_LOG" 2>&1) &
VITE_PID=$!
wait_for_http "$FRONTEND_DEVSERVER_URL" "frontend-app vite" "$SUPER_DOLPHIN_FRONTEND_READY_ATTEMPTS"

start_desktop_backend
wait_for_backend

if backend_hot_reload_enabled; then
  run_backend_hot_supervisor_loop
else
  wait_for_any_process_exit
fi
