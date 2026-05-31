#!/usr/bin/env bash
# CI entrypoint for changed-code package tests.
# Resolves the GitHub event range, then runs tests for changed Go packages,
# direct reverse dependencies, and the frontend package when touched.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

ZERO_SHA="0000000000000000000000000000000000000000"
GO_PACKAGE_PATTERNS=(./cmd/... ./internal/... ./pkg/... ./scripts/...)
GO_LIST_ERR=$(mktemp -t ci-changed-golist.XXXXXX)
GO_PKG_CANDIDATES=()
HAS_FRONTEND_CODE_CHANGES=0
HAS_GUARDED_CHANGES=0

cleanup() {
  rm -f "$GO_LIST_ERR"
}
trap cleanup EXIT

run_without_git_env() (
  unset $(git rev-parse --local-env-vars)
  unset GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT
  local name
  while IFS='=' read -r name _; do
    case "$name" in
      GIT_CONFIG_KEY_*|GIT_CONFIG_VALUE_*) unset "$name" ;;
    esac
  done < <(env)
  "$@"
)

usage() {
  cat >&2 <<'USAGE'
usage:
  scripts/ci_changed_tests.sh
  scripts/ci_changed_tests.sh --range <rev-range>

When no --range is provided, GitHub Actions must pass:
  pull_request: GITHUB_EVENT_NAME, GITHUB_BASE_SHA, GITHUB_HEAD_SHA
  push:         GITHUB_EVENT_NAME, GITHUB_EVENT_BEFORE, GITHUB_SHA
USAGE
}

fail() {
  echo "FAIL: ci changed tests: $*" >&2
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

resolve_range() {
  case "${1:-}" in
    "")
      if [ "$#" -ne 0 ]; then
        usage
        exit 2
      fi
      resolve_github_range
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

go_pkg_candidate_contains() {
  local needle existing
  needle=$1
  if [ ${#GO_PKG_CANDIDATES[@]} -eq 0 ]; then
    return 1
  fi
  for existing in "${GO_PKG_CANDIDATES[@]}"; do
    if [ "$existing" = "$needle" ]; then
      return 0
    fi
  done
  return 1
}

is_go_path() {
  case "$1" in
    *.go) return 0 ;;
    *) return 1 ;;
  esac
}

pkg_for_path() {
  local dir
  dir=$(dirname "$1")
  if [ "$dir" = "." ]; then
    printf '.'
  else
    printf './%s' "$dir"
  fi
}

add_go_pkg_for_path() {
  local pkg
  is_go_path "$1" || return 0
  pkg=$(pkg_for_path "$1")
  if ! go_pkg_candidate_contains "$pkg"; then
    GO_PKG_CANDIDATES+=("$pkg")
  fi
}

add_go_pkg() {
  if ! go_pkg_candidate_contains "$1"; then
    GO_PKG_CANDIDATES+=("$1")
  fi
}

is_frontend_code_path() {
  case "$1" in
    cmd/agent-terminal/frontend/node_modules/*|\
    cmd/agent-terminal/frontend/.vite-cache/*|\
    cmd/agent-terminal/frontend/.build-cache/*|\
    cmd/agent-terminal/frontend/dist/*|\
    cmd/agent-terminal/frontend/playwright-report/*|\
    cmd/agent-terminal/frontend/test-results/*|\
    cmd/agent-terminal/frontend/review/*|\
    cmd/agent-terminal/frontend/full_test_output.txt|\
    cmd/agent-terminal/frontend/.DS_Store)
      return 1
      ;;
    cmd/agent-terminal/frontend/*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

record_changed_path() {
  local path="$1"
  case "$path" in
    go.mod|go.sum)
      HAS_GUARDED_CHANGES=1
      add_go_pkg ./cmd/...
      add_go_pkg ./internal/...
      add_go_pkg ./pkg/...
      add_go_pkg ./scripts/...
      ;;
    .githooks/*|.github/workflows/ci.yml|scripts/*.sh)
      HAS_GUARDED_CHANGES=1
      add_go_pkg ./scripts
      ;;
    cmd/*|internal/*|pkg/*|scripts/*.go)
      HAS_GUARDED_CHANGES=1
      add_go_pkg_for_path "$path"
      ;;
  esac
  if is_frontend_code_path "$path"; then
    HAS_GUARDED_CHANGES=1
    HAS_FRONTEND_CODE_CHANGES=1
  fi
}

collect_changed_paths_for_range() {
  local range="$1"
  local status old_path new_path path
  while IFS= read -r -d '' status; do
    case "$status" in
      R*|C*)
        IFS= read -r -d '' old_path || fail "parse changed rename/copy old path"
        IFS= read -r -d '' new_path || fail "parse changed rename/copy new path"
        record_changed_path "$old_path"
        record_changed_path "$new_path"
        ;;
      *)
        IFS= read -r -d '' path || fail "parse changed path"
        record_changed_path "$path"
        ;;
    esac
  done < <(git diff --name-status -z --diff-filter=ACMRD "$range")
}

is_excluded_go_pkg() {
  case "$1" in
    ./internal/provider/dreamexec|./internal/provider/dreamexec/*) return 0 ;;
    *) return 1 ;;
  esac
}

resolve_go_pkgs() {
  local p pkg_dir
  GO_PKGS=()
  if [ ${#GO_PKG_CANDIDATES[@]} -eq 0 ]; then
    return 0
  fi
  for p in "${GO_PKG_CANDIDATES[@]}"; do
    if is_excluded_go_pkg "$p"; then
      continue
    fi
    pkg_dir=${p#./}
    if [ "$p" = "." ]; then
      pkg_dir="."
    fi
    case "$p" in
      *...) ;;
      *)
        if [ ! -d "$pkg_dir" ]; then
          continue
        fi
        ;;
    esac
    if run_without_git_env go list "$p" >/dev/null 2>"$GO_LIST_ERR"; then
      GO_PKGS+=("$p")
      continue
    fi
    if grep -q "no Go files" "$GO_LIST_ERR"; then
      continue
    fi
    echo "❌ go list failed: $p" >&2
    cat "$GO_LIST_ERR" >&2
    exit 1
  done
}

reverse_dependency_packages() {
  if [ ${#GO_PKGS[@]} -eq 0 ]; then
    return 0
  fi

  local changed_imports module_path
  changed_imports="$(run_without_git_env go list -e -f '{{.ImportPath}}' "${GO_PKGS[@]}" 2>/dev/null | sed '/^$/d')"
  if [ -z "$changed_imports" ]; then
    return 0
  fi
  module_path="$(run_without_git_env go list -m -f '{{.Path}}')"

  run_without_git_env go list -e \
    -f '{{$p := .ImportPath}}{{range .Imports}}{{$p}}{{"\t"}}{{.}}{{"\n"}}{{end}}{{range .TestImports}}{{$p}}{{"\t"}}{{.}}{{"\n"}}{{end}}{{range .XTestImports}}{{$p}}{{"\t"}}{{.}}{{"\n"}}{{end}}' \
    "${GO_PACKAGE_PATTERNS[@]}" 2>/dev/null \
    | awk -F'\t' '
        BEGIN {
          while ((getline line < ARGV[1]) > 0) if (line != "") target[line] = 1
          close(ARGV[1])
          ARGV[1] = ""
        }
        $2 in target { print $1 }
      ' <(printf '%s\n' "$changed_imports") \
    | sort -u \
    | sed "s|^${module_path}/|./|" \
    | while IFS= read -r pkg; do
        [ -n "$pkg" ] || continue
        if ! is_excluded_go_pkg "$pkg"; then
          printf '%s\n' "$pkg"
        fi
      done
}

run_go_package_tests() {
  resolve_go_pkgs
  if [ ${#GO_PKGS[@]} -eq 0 ]; then
    if [ ${#GO_PKG_CANDIDATES[@]} -gt 0 ]; then
      echo "[ci-changed-tests] go affected package tests... (skipped: no remaining Go package)"
    fi
    return 0
  fi

  local all_pkg_lines pkg
  all_pkg_lines="$({ printf '%s\n' "${GO_PKGS[@]}"; reverse_dependency_packages; } | sed '/^$/d' | sort -u)"
  ALL_GO_PKGS=()
  while IFS= read -r pkg; do
    [ -n "$pkg" ] || continue
    ALL_GO_PKGS+=("$pkg")
  done <<< "$all_pkg_lines"

  echo "[ci-changed-tests] go affected package tests: ${ALL_GO_PKGS[*]}"
  run_without_git_env ./scripts/test_with_guard.sh "${ALL_GO_PKGS[@]}" -count=1 -timeout 180s
}

run_frontend_package_tests() {
  if [ "$HAS_FRONTEND_CODE_CHANGES" -ne 1 ]; then
    return 0
  fi

  echo "[ci-changed-tests] frontend codebase guard"
  (
    cd cmd/agent-terminal/frontend
    node scripts/size-guard.cjs
  )

  echo "[ci-changed-tests] frontend package tests"
  (
    cd cmd/agent-terminal/frontend
    npx vitest run
  )
}

RANGE="$(resolve_range "$@")"
if ! git rev-list --reverse "$RANGE" >/dev/null; then
  fail "invalid commit range: $RANGE"
fi

echo "[ci-changed-tests] changed range: $RANGE"
collect_changed_paths_for_range "$RANGE"

if [ "$HAS_GUARDED_CHANGES" -ne 1 ]; then
  echo "[ci-changed-tests] no guarded code changes; skipping package tests"
  exit 0
fi

run_go_package_tests
run_frontend_package_tests

echo "✅ ci changed tests OK"
