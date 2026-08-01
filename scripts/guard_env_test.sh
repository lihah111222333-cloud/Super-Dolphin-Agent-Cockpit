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

write_fake_go() {
  local file="$1" version="$2"
  printf '#!/usr/bin/env bash\nif [[ "$1" == "version" ]]; then printf "go version go%s test/arch\\n"; exit 0; fi\nexit 0\n' "$version" > "$file"
  chmod +x "$file"
}

test_resolve_real_go_from_real_go_bin() {
  local tmpdir real_go resolved
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' RETURN
  real_go="$tmpdir/real-go"
  write_fake_go "$real_go" "1.26.5"

  resolved="$(REAL_GO_BIN="$real_go" "$BASH" -c 'source scripts/real_go_resolver.sh; resolve_real_go')"
  [[ "$resolved" -ef "$real_go" ]] || fail "resolve_real_go returned $resolved, want $real_go"
}

test_resolve_real_go_prefers_matching_goroot_over_newer_path() {
  local tmpdir matching newer resolved
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' RETURN
  mkdir -p "$tmpdir/go-root/bin" "$tmpdir/path"
  matching="$tmpdir/go-root/bin/go"
  newer="$tmpdir/path/go"
  write_fake_go "$matching" "1.26.5"
  write_fake_go "$newer" "1.26.5"

  resolved="$(GOROOT="$tmpdir/go-root" PATH="$tmpdir/path:$PATH" "$BASH" -c 'source scripts/real_go_resolver.sh; resolve_real_go')"
  [[ "$resolved" -ef "$matching" ]] || fail "resolve_real_go returned $resolved, want matching GOROOT binary"
}

test_real_go_bin_rejects_wrong_repo_version() {
  local tmpdir wrong stderr status
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' RETURN
  wrong="$tmpdir/go"
  stderr="$tmpdir/stderr"
  write_fake_go "$wrong" "1.26.5"

  set +e
  REAL_GO_BIN="$wrong" "$BASH" -c 'source scripts/real_go_resolver.sh; resolve_real_go' 2>"$stderr" >/dev/null
  status=$?
  set -e
  [[ "$status" -ne 0 ]] || fail "wrong-version REAL_GO_BIN unexpectedly succeeded"
  assert_contains "$stderr" "go1.26.5"
}

test_remote_worker_uses_manifest_verified_goroot_binary() {
  local tmpdir worker_go resolved
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' RETURN
  mkdir -p "$tmpdir/go-root/bin"
  worker_go="$tmpdir/go-root/bin/go"
  printf '#!/usr/bin/env bash\nexit 99\n' > "$worker_go"
  chmod +x "$worker_go"

  resolved="$(GOROOT="$tmpdir/go-root" SUPER_DOLPHIN_TEST_BACKEND=remote-worker "$BASH" -c 'source scripts/real_go_resolver.sh; resolve_real_go')"
  [[ "$resolved" -ef "$worker_go" ]] || fail "remote worker returned $resolved, want manifest-verified GOROOT binary"
}

test_repo_wrapper_is_not_real_go() {
  local stderr status
  stderr="$(mktemp)"
  trap 'rm -f "$stderr"' RETURN

  set +e
  GOROOT= PATH="$ROOT_DIR/scripts" SUPER_DOLPHIN_TEST_BACKEND=remote-worker "$BASH" "$ROOT_DIR/scripts/test_with_guard.sh" --guard-only 2>"$stderr"
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
  GOROOT= PATH="$ROOT_DIR/scripts" GO_GUARD_ALLOW_RAW=1 "$BASH" "$ROOT_DIR/scripts/go" env 2>"$stderr" >/dev/null
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
  GOROOT= PATH="$tmpdir" GLOBAL_GO_WRAPPER="$tmpdir/go" SUPER_DOLPHIN_TEST_BACKEND=remote-worker "$BASH" "$ROOT_DIR/scripts/test_with_guard.sh" --guard-only 2>"$stderr"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "missing go scenario unexpectedly succeeded"
  assert_contains "$stderr" "REAL_GO_BIN"
  assert_contains "$stderr" "REAL_GO_BIN=/absolute/path/to/go"
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
  GOROOT= PATH="$tmpdir" GLOBAL_GO_WRAPPER="$tmpdir/go" "$BASH" -c 'source scripts/activate_guard_env.sh' 2>"$stderr"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "activate_guard_env unexpectedly succeeded without real go"
  assert_contains "$stderr" "REAL_GO_BIN"
  assert_not_contains "$stderr" "command not found"
}

cd "$ROOT_DIR"
test_resolve_real_go_from_real_go_bin
test_resolve_real_go_prefers_matching_goroot_over_newer_path
test_real_go_bin_rejects_wrong_repo_version
test_remote_worker_uses_manifest_verified_goroot_binary
test_repo_wrapper_is_not_real_go
test_real_go_bin_rejects_wrapper
test_real_go_bin_rejects_normalized_wrapper
test_real_go_bin_rejects_symlink_wrapper
test_global_go_wrapper_missing_fails_fast
test_go_wrapper_allow_raw_uses_shared_resolver
test_missing_go_fails_fast
test_activate_guard_env_missing_go_returns
echo "guard_env_test: ok"
