#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"

fail() {
  echo "guard_env_test: $*" >&2
  exit 1
}

assert_contains() {
  local file="$1"
  local needle="$2"
  if ! grep -Fq "$needle" "$file"; then
    echo "expected to find: $needle" >&2
    echo "--- stderr ---" >&2
    cat "$file" >&2
    fail "missing expected text"
  fi
}

assert_not_contains() {
  local file="$1"
  local needle="$2"
  if grep -Fq "$needle" "$file"; then
    echo "unexpected text: $needle" >&2
    echo "--- stderr ---" >&2
    cat "$file" >&2
    fail "found unexpected text"
  fi
}

test_resolve_real_go_from_real_go_bin() {
  local tmpdir real_go resolved
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' RETURN
  real_go="$tmpdir/real-go"
  printf '#!/usr/bin/env bash\nexit 0\n' > "$real_go"
  chmod +x "$real_go"

  resolved="$(REAL_GO_BIN="$real_go" "$BASH" -c 'source scripts/real_go_resolver.sh; resolve_real_go')"
  [[ "$resolved" -ef "$real_go" ]] || fail "resolve_real_go returned $resolved, want $real_go"
}

test_repo_wrapper_is_not_real_go() {
  local stderr status
  stderr="$(mktemp)"
  trap 'rm -f "$stderr"' RETURN

  set +e
  PATH="$ROOT_DIR/scripts" "$BASH" "$ROOT_DIR/scripts/test_with_guard.sh" --guard-only 2>"$stderr"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "repo go wrapper scenario unexpectedly succeeded"
  assert_contains "$stderr" "REAL_GO_BIN"
  assert_not_contains "$stderr" "command not found"
}

test_real_go_bin_rejects_wrapper() {
  local stderr status
  stderr="$(mktemp)"
  trap 'rm -f "$stderr"' RETURN

  set +e
  REAL_GO_BIN="$ROOT_DIR/scripts/go" "$BASH" -c 'source scripts/real_go_resolver.sh; resolve_real_go' 2>"$stderr" >/dev/null
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "REAL_GO_BIN wrapper scenario unexpectedly succeeded"
  assert_contains "$stderr" "REAL_GO_BIN"
  assert_contains "$stderr" "wrapper"
}

test_real_go_bin_rejects_normalized_wrapper() {
  local stderr status
  stderr="$(mktemp)"
  trap 'rm -f "$stderr"' RETURN

  set +e
  REAL_GO_BIN="$ROOT_DIR/scripts/../scripts/go" "$BASH" -c 'source scripts/real_go_resolver.sh; resolve_real_go' 2>"$stderr" >/dev/null
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "normalized REAL_GO_BIN wrapper scenario unexpectedly succeeded"
  assert_contains "$stderr" "REAL_GO_BIN"
  assert_contains "$stderr" "wrapper"
}

test_real_go_bin_rejects_symlink_wrapper() {
  local tmpdir stderr status
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' RETURN
  ln -s "$ROOT_DIR/scripts/go" "$tmpdir/fake-go"
  stderr="$tmpdir/stderr"

  set +e
  REAL_GO_BIN="$tmpdir/fake-go" "$BASH" -c 'source scripts/real_go_resolver.sh; resolve_real_go' 2>"$stderr" >/dev/null
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "symlink REAL_GO_BIN wrapper scenario unexpectedly succeeded"
  assert_contains "$stderr" "REAL_GO_BIN"
  assert_contains "$stderr" "wrapper"
}

test_global_go_wrapper_missing_fails_fast() {
  local tmpdir stderr status
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' RETURN
  stderr="$tmpdir/stderr"

  set +e
  GLOBAL_GO_WRAPPER="$tmpdir/missing-go-wrapper" "$BASH" -c 'source scripts/real_go_resolver.sh; resolve_real_go' 2>"$stderr" >/dev/null
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "missing GLOBAL_GO_WRAPPER unexpectedly succeeded"
  assert_contains "$stderr" "GLOBAL_GO_WRAPPER"
}

test_go_wrapper_allow_raw_uses_shared_resolver() {
  local tmpdir stderr status
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' RETURN
  stderr="$tmpdir/stderr"

  set +e
  PATH="$ROOT_DIR/scripts" GO_GUARD_ALLOW_RAW=1 "$BASH" "$ROOT_DIR/scripts/go" env 2>"$stderr" >/dev/null
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "go wrapper allow raw unexpectedly succeeded without real go"
  assert_contains "$stderr" "REAL_GO_BIN"
  assert_not_contains "$stderr" "command not found"
}

test_missing_go_fails_fast() {
  local tmpdir stderr status
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' RETURN
  printf '#!/usr/bin/env bash\necho wrapper should not run >&2\nexit 127\n' > "$tmpdir/go"
  chmod +x "$tmpdir/go"
  stderr="$tmpdir/stderr"

  set +e
  PATH="$tmpdir" GLOBAL_GO_WRAPPER="$tmpdir/go" "$BASH" "$ROOT_DIR/scripts/test_with_guard.sh" --guard-only 2>"$stderr"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "missing go scenario unexpectedly succeeded"
  assert_contains "$stderr" "REAL_GO_BIN"
  assert_contains "$stderr" "absolute path"
  assert_not_contains "$stderr" "command not found"
  assert_not_contains "$stderr" "wrapper should not run"
}

test_activate_guard_env_missing_go_returns() {
  local tmpdir stderr status
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' RETURN
  printf '#!/usr/bin/env bash\nexit 127\n' > "$tmpdir/go"
  chmod +x "$tmpdir/go"
  stderr="$tmpdir/stderr"

  set +e
  PATH="$tmpdir" GLOBAL_GO_WRAPPER="$tmpdir/go" "$BASH" -c 'source scripts/activate_guard_env.sh' 2>"$stderr"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "activate_guard_env unexpectedly succeeded without real go"
  assert_contains "$stderr" "REAL_GO_BIN"
  assert_not_contains "$stderr" "command not found"
}

cd "$ROOT_DIR"
test_resolve_real_go_from_real_go_bin
test_repo_wrapper_is_not_real_go
test_real_go_bin_rejects_wrapper
test_real_go_bin_rejects_normalized_wrapper
test_real_go_bin_rejects_symlink_wrapper
test_global_go_wrapper_missing_fails_fast
test_go_wrapper_allow_raw_uses_shared_resolver
test_missing_go_fails_fast
test_activate_guard_env_missing_go_returns
echo "guard_env_test: ok"
