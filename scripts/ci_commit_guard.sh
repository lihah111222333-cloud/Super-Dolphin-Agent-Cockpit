#!/usr/bin/env bash
# CI entrypoint for commit-history guards.
# Resolves the GitHub event range, then runs the shared commit guards.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

ZERO_SHA="0000000000000000000000000000000000000000"

usage() {
  cat >&2 <<'USAGE'
usage:
  scripts/ci_commit_guard.sh
  scripts/ci_commit_guard.sh --range <rev-range>

When no --range is provided, GitHub Actions must pass:
  pull_request: GITHUB_EVENT_NAME, GITHUB_BASE_SHA, GITHUB_HEAD_SHA
  push:         GITHUB_EVENT_NAME, GITHUB_EVENT_BEFORE, GITHUB_SHA

For local runs without GitHub event variables, the guard uses:
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
  if [ "$sha" = "$ZERO_SHA" ]; then
    fail "$label is the zero SHA; provide an explicit base commit"
  fi
  if ! git rev-parse --verify --quiet "${sha}^{commit}" >/dev/null; then
    fail "$label is not available in this checkout: $sha (use fetch-depth: 0)"
  fi
}

resolve_github_range() {
  local event_name="${GITHUB_EVENT_NAME:-}"
  local base_sha
  local head_sha

  case "$event_name" in
    pull_request|pull_request_target)
      base_sha="${GITHUB_BASE_SHA:-}"
      head_sha="${GITHUB_HEAD_SHA:-}"
      require_commit "GITHUB_BASE_SHA" "$base_sha"
      require_commit "GITHUB_HEAD_SHA" "$head_sha"
      ;;
    push)
      base_sha="${GITHUB_EVENT_BEFORE:-}"
      head_sha="${GITHUB_SHA:-}"
      require_commit "GITHUB_EVENT_BEFORE" "$base_sha"
      require_commit "GITHUB_SHA" "$head_sha"
      ;;
    *)
      fail "unsupported GITHUB_EVENT_NAME=${event_name:-<empty>}; pass --range explicitly"
      ;;
  esac

  printf '%s..%s\n' "$base_sha" "$head_sha"
}

resolve_local_range() {
  local base_ref="${SUPER_DOLPHIN_CI_COMMIT_GUARD_BASE_REF:-origin/main}"
  local base_sha
  local head_sha

  if ! git rev-parse --verify --quiet "${base_ref}^{commit}" >/dev/null; then
    fail "unsupported GITHUB_EVENT_NAME=<empty> and local base ref is unavailable: $base_ref; pass --range explicitly"
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
      if [ -z "${GITHUB_EVENT_NAME:-}" ]; then
        resolve_local_range
      else
        resolve_github_range
      fi
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
