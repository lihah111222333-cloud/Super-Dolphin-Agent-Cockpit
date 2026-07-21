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

# These assertions match literal shell source; dollar expressions must reach grep unchanged.
# shellcheck disable=SC2016
{
assert_contains "$DESKTOP_SCRIPT" 'APP_COMMIT="$(git -C "$PROJECT_DIR" rev-parse --verify HEAD 2>/dev/null)"'
assert_contains "$DESKTOP_SCRIPT" 'SCHEMA_BUILD_IDENTITY_LDFLAG="-X github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema.buildAppCommit=$APP_COMMIT"'
assert_contains "$DESKTOP_SCRIPT" 'go run -ldflags "$SCHEMA_BUILD_IDENTITY_LDFLAG" ./cmd/agent-terminal'
assert_contains "$DESKTOP_SCRIPT" 'PROJECT_WORKTREE_ID="$(printf '\''%s'\'' "$PROJECT_DIR" | git -C "$PROJECT_DIR" hash-object --stdin)"'
assert_contains "$DESKTOP_SCRIPT" 'SUPER_DOLPHIN_HOME="${SUPER_DOLPHIN_HOME:-/tmp/sd-new-ui-${USER:-user}/worktree-$PROJECT_WORKTREE_ID}"'
assert_contains "$DESKTOP_SCRIPT" 'make APP_COMMIT="$APP_COMMIT" build-peer-binaries'
}
assert_contains "$DESKTOP_SCRIPT" 'schema_helper_package_current'
assert_contains "$DESKTOP_SCRIPT" "npm run dev"

echo "✅ new UI desktop hot reload script contract ok"
