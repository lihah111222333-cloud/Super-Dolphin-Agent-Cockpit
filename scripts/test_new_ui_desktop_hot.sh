#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
DESKTOP_SCRIPT="$ROOT_DIR/run-new-ui-desktop.sh"

fail() {
  echo "❌ $*" >&2
  exit 1
}

assert_file() {
  local path="$1"
  [ -f "$path" ] || fail "missing file: $path"
}

assert_executable() {
  local path="$1"
  [ -x "$path" ] || fail "file is not executable: $path"
}

assert_contains() {
  local path="$1"
  local needle="$2"
  if ! grep -Fq "$needle" "$path"; then
    fail "$path does not contain required text: $needle"
  fi
}

assert_not_contains() {
  local path="$1"
  local needle="$2"
  if grep -Fq "$needle" "$path"; then
    fail "$path contains retired text: $needle"
  fi
}

assert_file "$DESKTOP_SCRIPT"
assert_file "$ROOT_DIR/internal/platform/db/sqlite/migrations/001_baseline.sql"

bash -n "$DESKTOP_SCRIPT"

assert_contains "$DESKTOP_SCRIPT" "SUPER_DOLPHIN_BACKEND_HOT_RELOAD=\"\${SUPER_DOLPHIN_BACKEND_HOT_RELOAD:-0}\""
assert_contains "$DESKTOP_SCRIPT" "backend_hot_reload_enabled"
assert_contains "$DESKTOP_SCRIPT" "snapshot_backend_watch_state"
assert_contains "$DESKTOP_SCRIPT" "restart_desktop_backend"
assert_contains "$DESKTOP_SCRIPT" "run_backend_hot_supervisor_loop"
assert_contains "$DESKTOP_SCRIPT" "SUPER_DOLPHIN_HOT_WATCH_PATHS"
assert_contains "$DESKTOP_SCRIPT" 'SUPER_DOLPHIN_HOT_WATCH_PATHS="cmd internal pkg sql go.mod go.sum sqlc.yaml"'
assert_not_contains "$DESKTOP_SCRIPT" 'SUPER_DOLPHIN_HOT_WATCH_PATHS="cmd internal pkg migrations sql go.mod go.sum sqlc.yaml"'
assert_contains "$DESKTOP_SCRIPT" ".tmp/run-new-ui-desktop/hot-peers"
assert_contains "$DESKTOP_SCRIPT" "go run ./cmd/agent-terminal"
assert_contains "$DESKTOP_SCRIPT" "npm run dev"

echo "✅ new UI desktop hot reload script contract ok"
