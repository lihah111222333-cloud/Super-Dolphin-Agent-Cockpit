#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"
source "$ROOT_DIR/scripts/real_go_resolver.sh"

usage() {
  cat <<'USAGE'
Usage:
  scripts/test_with_guard.sh [go test args...]
  scripts/test_with_guard.sh <file.go> [more.go...]
  scripts/test_with_guard.sh --quick-guard [go test args...]
  scripts/test_with_guard.sh --guard-only
  scripts/test_with_guard.sh --archtest-only
  scripts/test_with_guard.sh --with-race <race-package...> -- <go-test-args...>
  scripts/test_with_guard.sh --race-only <race-package...>
  scripts/test_with_guard.sh --help

Examples:
  scripts/test_with_guard.sh internal/app/app.go
  scripts/test_with_guard.sh ./internal/provider/claudecli/... -count=1
  scripts/test_with_guard.sh -run TestFoo ./internal/module/thread/...
  scripts/test_with_guard.sh --guard-only
  scripts/test_with_guard.sh --with-race ./internal/platform/db/sqlite -- ./internal/app -count=1
USAGE
}

QUICK_ARCHTEST_SKIP='^(TestCodeSizeGuard|TestPrioritySSAGuardsUseUnifiedFreezeBaseline|TestPrioritySSALoaderExtractionPreservesCandidates|TestWideOrchestrationLoaderExtractionPreservesCandidates)$'
QUICK_ARCHTEST_RUN='^(TestDependencyDirection|TestValidateDefaultBackendBoundaryGovernance|TestBackendBoundaryRuleFactsHaveOneSource)$'

run_guard() {
  local real_go="$1"
  local mode="${2:-full}"
  (
    cd "$ROOT_DIR"
    ./scripts/forbid_raw_go_test.sh
    "$real_go" run ./scripts/code_size_guard.go
    if [ "$mode" = "quick" ]; then
      "$real_go" test ./internal/archtest -run "$QUICK_ARCHTEST_RUN" -count=1
    else
      "$real_go" test ./internal/archtest -count=1
    fi
  )
}

run_archtest_only() {
  local real_go="$1"
  (
    cd "$ROOT_DIR"
    "$real_go" test ./internal/archtest -skip "$QUICK_ARCHTEST_SKIP" -count=1
  )
}

run_go_test() {
  local real_go="$1"
  shift
  (
    cd "$ROOT_DIR"
    "$real_go" test "$@"
  )
}

collect_copylocks_packages() {
  local arg
  for arg in "$@"; do
    case "$arg" in
      ./internal/provider|./internal/provider/*|\
      ./internal/platform|./internal/platform/*|\
      ./internal/module/thread|./internal/module/thread/*)
        printf '%s\n' "$arg"
        ;;
    esac
  done
}

run_copylocks_guard() {
  local real_go="$1"
  shift
  local -a packages=()
  local package
  while IFS= read -r package; do
    [ -n "$package" ] && packages+=("$package")
  done < <(collect_copylocks_packages "$@")
  if [ ${#packages[@]} -eq 0 ]; then
    echo "[test-with-guard] copylocks skip: no affected registered package"
    return 0
  fi
  (
    cd "$ROOT_DIR"
    "$real_go" vet -copylocks "${packages[@]}"
  )
}

run_with_race() {
  local real_go="$1"
  local guard_mode="$2"
  shift 2
  local -a race_packages=()
  while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do
    race_packages+=("$1")
    shift
  done
  if [ ${#race_packages[@]} -eq 0 ] || [ "$#" -eq 0 ] || [ "$1" != "--" ]; then
    usage
    return 1
  fi
  shift
  if [ "$#" -eq 0 ]; then
    usage
    return 1
  fi
  run_guard "$real_go" "$guard_mode"
  run_copylocks_guard "$real_go" "$@"
  run_go_test "$real_go" "$@"
  run_go_test "$real_go" "${race_packages[@]}" -race -short -count=1
}

run_race_only() {
  local real_go="$1"
  shift
  if [ "$#" -eq 0 ]; then
    usage
    return 1
  fi
  run_go_test "$real_go" "$@" -race -short -count=1
}

all_args_are_go_files() {
  local arg
  for arg in "$@"; do
    [[ "$arg" == *.go ]] || return 1
  done
  return 0
}

run_single_file_guard() {
  local real_go="$1"
  shift
  local -a go_files=()
  local arg
  for arg in "$@"; do
    case "$arg" in
      /*) go_files+=("$arg") ;;
      [A-Za-z]:/*|[A-Za-z]:\\*) go_files+=("$arg") ;;
      *) go_files+=("$PWD/$arg") ;;
    esac
  done

  (
    cd "$ROOT_DIR"
    local stderr status
    stderr="$(mktemp)"
    set +e
    "$real_go" run ./scripts/code_size_guard.go -- "${go_files[@]}" 2>"$stderr"
    status=$?
    set -e
    if [ "$status" -ne 0 ]; then
      grep -v -E '^exit status [0-9]+$' "$stderr" >&2 || true
    fi
    rm -f "$stderr"
    exit "$status"
  )
}

main() {
  if [ "$#" -eq 0 ]; then
    usage
    exit 1
  fi

  local guard_mode=full
  if [ "$1" = "--quick-guard" ]; then
    guard_mode=quick
    shift
    if [ "$#" -eq 0 ]; then
      usage
      exit 1
    fi
  fi

  case "$1" in
    --help|-h)
      usage
      ;;
    --guard-only)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      run_guard "$real_go" "$guard_mode"
      ;;
    --archtest-only)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      run_archtest_only "$real_go"
      ;;
    --with-race)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      run_with_race "$real_go" "$guard_mode" "$@"
      ;;
    --race-only)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      run_race_only "$real_go" "$@"
      ;;
    --)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      if [ "$#" -eq 0 ]; then
        usage
        exit 1
      fi
      run_guard "$real_go" "$guard_mode"
      run_copylocks_guard "$real_go" "$@"
      run_go_test "$real_go" "$@"
      ;;
    *)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      if all_args_are_go_files "$@"; then
        run_single_file_guard "$real_go" "$@"
        return
      fi
      run_guard "$real_go" "$guard_mode"
      run_copylocks_guard "$real_go" "$@"
      run_go_test "$real_go" "$@"
      ;;
  esac
}

main "$@"
