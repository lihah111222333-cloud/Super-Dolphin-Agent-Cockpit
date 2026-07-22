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
  scripts/test_with_guard.sh --canonical-backend <package-pattern...>
  scripts/test_with_guard.sh --with-race <race-package...> -- <go-test-args...>
  scripts/test_with_guard.sh --race-only <race-package...>
  scripts/test_with_guard.sh --help

Examples:
  scripts/test_with_guard.sh internal/app/app.go
  scripts/test_with_guard.sh ./internal/provider/claudecli/... -count=1
  scripts/test_with_guard.sh -run TestFoo ./internal/module/thread/...
  scripts/test_with_guard.sh --guard-only
  scripts/test_with_guard.sh --canonical-backend ./cmd/... ./internal/... ./pkg/... ./scripts/...
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

production_docker_e2e_wrapper_timeout() {
  [[ "${SUPER_DOLPHIN_GATE_PRODUCTION_DOCKER_E2E:-}" == "1" ]] || return 1
  local arg expect_run_pattern=""
  for arg in "$@"; do
    if [[ -n "$expect_run_pattern" ]]; then
      case "$arg" in
        "^TestProductionProvisionBootstrapOwnerHookDockerE2E$") printf '15m\n'; return 0 ;;
        "^TestProductionProvisionBootstrapOwnerReleaseCLIDockerE2E$") printf '40m\n'; return 0 ;;
      esac
      expect_run_pattern=""
      continue
    fi
    case "$arg" in
      -run|-test.run) expect_run_pattern="1" ;;
      -run=*|-test.run=*)
        case "${arg#*=}" in
          "^TestProductionProvisionBootstrapOwnerHookDockerE2E$") printf '15m\n'; return 0 ;;
          "^TestProductionProvisionBootstrapOwnerReleaseCLIDockerE2E$") printf '40m\n'; return 0 ;;
        esac
        ;;
    esac
  done
  return 1
}

go_test_timeout_is_explicit() {
  local arg
  for arg in "$@"; do
    case "$arg" in
      -timeout|-test.timeout|-timeout=*|-test.timeout=*) return 0 ;;
    esac
  done
  return 1
}

run_go_test() {
  local real_go="$1"
  shift
  local -a test_args=("$@")
  local wrapper_timeout=""
  if wrapper_timeout="$(production_docker_e2e_wrapper_timeout "${test_args[@]}")"; then
    if go_test_timeout_is_explicit "${test_args[@]}"; then
      echo "production Docker E2E timeout is wrapper-owned; remove the explicit go test timeout" >&2
      return 2
    fi
    test_args+=("-timeout=$wrapper_timeout")
  fi
  (
    cd "$ROOT_DIR"
    "$real_go" test "${test_args[@]}"
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

run_nested_module_guard() {
  local real_go="$1"
  "$ROOT_DIR/scripts/check_nested_go_modules.sh" "$real_go"
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
  run_copylocks_guard "$real_go" ./internal/provider/... ./internal/platform/... ./internal/module/thread/...
  run_nested_module_guard "$real_go"
  run_go_test "$real_go" "$@"
  run_go_test "$real_go" "${race_packages[@]}" -race -short -count=1 -timeout=180s
}

canonical_backend_target_allowed() {
  local target="$1"
  if [[ -z "$target" || "$target" == *[[:space:]]* ]]; then
    return 1
  fi
  local component remainder="$target"
  while [[ "$remainder" == */* ]]; do
    component="${remainder%%/*}"
    remainder="${remainder#*/}"
    if [[ "$component" == *..* && "$component" != "..." ]]; then
      return 1
    fi
  done
  if [[ "$remainder" == *..* && "$remainder" != "..." ]]; then
    return 1
  fi
  case "$target" in
    ./cmd/*|./internal/*|./pkg/*|./scripts/*) return 0 ;;
    *) return 1 ;;
  esac
}

CANONICAL_BACKEND_PACKAGES=()

resolve_canonical_backend_packages() {
  local real_go="$1"
  shift
  if [ "$#" -eq 0 ]; then
    echo "canonical backend mode requires at least one package pattern" >&2
    return 2
  fi

  local target
  for target in "$@"; do
    if ! canonical_backend_target_allowed "$target"; then
      echo "canonical backend mode rejects non-backend package target: $target" >&2
      return 2
    fi
  done

  local listed
  if ! listed="$(cd "$ROOT_DIR" && "$real_go" list "$@")"; then
    echo "canonical backend mode failed to resolve package targets" >&2
    return 1
  fi
  if [ -z "$listed" ]; then
    echo "canonical backend mode resolved no packages" >&2
    return 1
  fi

  CANONICAL_BACKEND_PACKAGES=()
  local package
  while IFS= read -r package || [ -n "$package" ]; do
    if [[ -z "$package" || "$package" == *[[:space:]]* ]]; then
      echo "canonical backend mode received invalid package path: $package" >&2
      return 1
    fi
    # run_guard already exercised this exact package. Descendants remain required.
    if [[ "$package" == */internal/archtest ]]; then
      continue
    fi
    CANONICAL_BACKEND_PACKAGES+=("$package")
  done <<<"$listed"
}

run_canonical_backend() {
  local real_go="$1"
  local guard_mode="$2"
  shift 2
  resolve_canonical_backend_packages "$real_go" "$@"

  (
    run_guard "$real_go" "$guard_mode"
    run_copylocks_guard "$real_go" ./internal/provider/... ./internal/platform/... ./internal/module/thread/...
    run_nested_module_guard "$real_go"
  ) &
  local guard_pid=$!

  local test_pid=""
  if [ "${#CANONICAL_BACKEND_PACKAGES[@]}" -gt 0 ]; then
    run_go_test "$real_go" "${CANONICAL_BACKEND_PACKAGES[@]}" -count=1 -timeout=180s &
    test_pid=$!
  fi

  local guard_status=0 test_status=0
  if wait "$guard_pid"; then
    :
  else
    guard_status=$?
  fi
  if [ -n "$test_pid" ]; then
    if wait "$test_pid"; then
      :
    else
      test_status=$?
    fi
  fi
  if [ "$guard_status" -ne 0 ] || [ "$test_status" -ne 0 ]; then
    echo "canonical backend lanes failed: guard=$guard_status test=$test_status" >&2
    return 1
  fi
}

run_race_only() {
  local real_go="$1"
  shift
  if [ "$#" -eq 0 ]; then
    usage
    return 1
  fi
  run_guard "$real_go"
  run_copylocks_guard "$real_go" ./internal/provider/... ./internal/platform/... ./internal/module/thread/...
  run_nested_module_guard "$real_go"
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
    --canonical-backend)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      run_canonical_backend "$real_go" "$guard_mode" "$@"
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
      run_nested_module_guard "$real_go"
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
      run_nested_module_guard "$real_go"
      run_go_test "$real_go" "$@"
      ;;
  esac
}

main "$@"
