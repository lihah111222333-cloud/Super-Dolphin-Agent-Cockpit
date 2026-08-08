#!/usr/bin/env bash
# CI entrypoint for commit-history guards.
# 解析显式 ECI range 或本地 merge-base range，然后运行共享的 commit guard。
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

usage() {
  cat >&2 <<'USAGE'
usage:
  scripts/ci_commit_guard.sh
  scripts/ci_commit_guard.sh --range <rev-range>

Alibaba Cloud ECI runs must provide an explicit range with --range.

For local runs without --range, the guard uses:
  merge-base(${SUPER_DOLPHIN_CI_COMMIT_GUARD_BASE_REF:-origin/main}, HEAD)..HEAD
USAGE
}

fail() {
  echo "FAIL: ci commit guard: $*" >&2
  exit 2
}

require_commit() {
  local label="$1"
  local sha="$2"

  if [ -z "$sha" ]; then
    fail "$label is required"
  fi
  if ! git rev-parse --verify --quiet "${sha}^{commit}" >/dev/null; then
    fail "$label is not available in this checkout: $sha (use fetch-depth: 0)"
  fi
}

resolve_local_range() {
  local base_ref="${SUPER_DOLPHIN_CI_COMMIT_GUARD_BASE_REF:-origin/main}"
  local base_sha
  local head_sha

  if ! git rev-parse --verify --quiet "${base_ref}^{commit}" >/dev/null; then
    fail "local base ref is unavailable: $base_ref; pass --range explicitly"
  fi
  if ! base_sha="$(git merge-base "$base_ref" HEAD)"; then
    fail "cannot compute merge-base for $base_ref and HEAD; pass --range explicitly"
  fi
  head_sha="$(git rev-parse HEAD)"
  require_commit "local base ref $base_ref" "$base_sha"
  require_commit "HEAD" "$head_sha"
  printf '%s..%s\n' "$base_sha" "$head_sha"
}

resolve_range() {
  case "${1:-}" in
    "")
      if [ "$#" -ne 0 ]; then
        usage
        exit 2
      fi
      resolve_local_range
      ;;
    --range)
      if [ "$#" -ne 2 ] || [ -z "${2:-}" ]; then
        usage
        exit 2
      fi
      printf '%s\n' "$2"
      ;;
    *)
      usage
      exit 2
      ;;
  esac
}

RANGE="$(resolve_range "$@")"

if ! git rev-list --reverse "$RANGE" >/dev/null; then
  fail "invalid commit range: $RANGE"
fi

echo "[ci-commit-guard] Chinese commit message guard: $RANGE"
./scripts/guard_commit_titles.sh --range "$RANGE"

echo "[ci-commit-guard] fix-test guard: $RANGE"
./scripts/guard_fix_commits_have_tests.sh --range "$RANGE"
