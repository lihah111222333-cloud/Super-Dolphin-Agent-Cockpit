#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"
source "$ROOT_DIR/scripts/real_go_resolver.sh"
source "$ROOT_DIR/scripts/local_go_cache.sh"

usage() {
  cat <<'USAGE'
Usage:
  scripts/test_with_guard.sh [go test args...]
  scripts/test_with_guard.sh <file.go> [more.go...]
  scripts/test_with_guard.sh --host-test <light|medium> [go test args...]
  scripts/test_with_guard.sh --quick-guard [go test args...]
  scripts/test_with_guard.sh --guard-only
  scripts/test_with_guard.sh --archtest-only
  scripts/test_with_guard.sh --canonical-backend <package-pattern...>
  scripts/test_with_guard.sh --with-race <race-package...> -- <go-test-args...>
  scripts/test_with_guard.sh --race-only <race-package...>
  scripts/test_with_guard.sh --ci-guard
  scripts/test_with_guard.sh --ci-guard-source
  scripts/test_with_guard.sh --ci-copylocks <provider|platform|thread>
  scripts/test_with_guard.sh --ci-nested-module <exact-tracked-module>
  scripts/test_with_guard.sh --ci-package <exact-package>
  scripts/test_with_guard.sh --ci-package-test <exact-package> <exact-test>
  scripts/test_with_guard.sh --ci-compile-package <exact-package>
  scripts/test_with_guard.sh --ci-package-benchmark <exact-package> <exact-benchmark>
  scripts/test_with_guard.sh --ci-race-guard
  scripts/test_with_guard.sh --ci-race-package <exact-package>
  scripts/test_with_guard.sh --ci-race-package-test <exact-package> <exact-test>
  scripts/test_with_guard.sh --help

Examples:
  scripts/test_with_guard.sh internal/app/app.go
  scripts/test_with_guard.sh --host-test light ./cmd/mcp-lsp/internal/hiddenexec -run '^TestName$' -timeout=120s -count=1
  scripts/test_with_guard.sh --host-test medium -tags=e2e ./cmd/mcp-lsp -run '^TestName_E2E$' -timeout=600s -count=1
  scripts/test_with_guard.sh ./internal/provider/claudecli/... -count=1
  scripts/test_with_guard.sh -run TestFoo ./internal/module/thread/...
  scripts/test_with_guard.sh --guard-only
  scripts/test_with_guard.sh --canonical-backend ./...
  scripts/test_with_guard.sh --with-race ./internal/platform/db/sqlite -- ./internal/app -count=1
USAGE
}

QUICK_ARCHTEST_SKIP='^(TestCodeSizeGuard/(size_and_freeze|repository_rules)|TestPrioritySSALoaderExtractionPreservesCandidates|TestWideOrchestrationLoaderExtractionPreservesCandidates)$'
QUICK_ARCHTEST_RUN='^(TestDependencyDirection|TestValidateDefaultBackendBoundaryGovernance|TestBackendBoundaryRuleFactsHaveOneSource)$'
MCP_LSP_RESOURCE_COHORT_E2E_RUN='^TestMcpLSPBinary(LinkedWorktreesResourceCohortRecycleAndRecover|ResourceCohortMalformedReportQuarantine)_E2E$'

run_guard() {
  local real_go="$1"
  local mode="${2:-full}"
  (
    cd "$ROOT_DIR"
    ./scripts/forbid_raw_go_test.sh
    if [ "$mode" = "quick" ]; then
      "$real_go" run ./scripts/code_size_guard.go
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

host_test_duration_seconds() {
  local value="$1" number
  case "$value" in
    *s) number="${value%s}" ;;
    *m) number="${value%m}"; [[ "$number" =~ ^[0-9]+$ ]] || return 1; printf '%s\n' "$((number * 60))"; return 0 ;;
    *) return 1 ;;
  esac
  [[ "$number" =~ ^[0-9]+$ ]] || return 1
  printf '%s\n' "$number"
}

validate_host_test_args() {
  local load_class="$1"
  shift
  case "$load_class" in
    light|medium) ;;
    *) echo "host test class must be light or medium" >&2; return 2 ;;
  esac
  if [[ -n "${GOFLAGS:-}" ]]; then
    echo "host test rejects inherited GOFLAGS; pass the classified -tags flag explicitly" >&2
    return 2
  fi

  local package_name="" test_selector="" timeout_value="" tags_value="" count_value="" verbose_seen=0
  local arg value
  while [[ $# -gt 0 ]]; do
    arg="$1"
    case "$arg" in
      -run|-timeout|-tags|-count)
        [[ $# -ge 2 ]] || { echo "host test flag $arg requires one value" >&2; return 2; }
        value="$2"
        shift 2
        ;;
      -run=*|-timeout=*|-tags=*|-count=*)
        value="${arg#*=}"
        arg="${arg%%=*}"
        shift
        ;;
      -v)
        [[ "$verbose_seen" -eq 0 ]] || { echo "host test received duplicate -v" >&2; return 2; }
        verbose_seen=1
        shift
        continue
        ;;
      -*)
        echo "host test rejects unclassified flag $arg; use ECI for heavy or unknown workloads" >&2
        return 2
        ;;
      *)
        [[ -z "$package_name" ]] || { echo "host test requires exactly one package" >&2; return 2; }
        package_name="$arg"
        shift
        continue
        ;;
    esac
    case "$arg" in
      -run)
        [[ -z "$test_selector" ]] || { echo "host test received duplicate -run" >&2; return 2; }
        test_selector="$value"
        ;;
      -timeout)
        [[ -z "$timeout_value" ]] || { echo "host test received duplicate -timeout" >&2; return 2; }
        timeout_value="$value"
        ;;
      -tags)
        [[ -z "$tags_value" ]] || { echo "host test received duplicate -tags" >&2; return 2; }
        tags_value="$value"
        ;;
      -count)
        [[ -z "$count_value" ]] || { echo "host test received duplicate -count" >&2; return 2; }
        count_value="$value"
        ;;
    esac
  done

  if [[ -z "$package_name" ]] || ! canonical_backend_target_allowed "$package_name" || [[ "$package_name" == *...* ]]; then
    echo "host test requires one exact ./package target without recursive ellipsis" >&2
    return 2
  fi
  if [[ ! "$test_selector" =~ ^\^Test[[:alnum:]_]+\$$ ]]; then
    echo "host test requires one exact top-level selector such as ^TestName$" >&2
    return 2
  fi
  if [[ "$count_value" != "1" ]]; then
    echo "host test requires -count=1" >&2
    return 2
  fi
  local timeout_seconds
  if ! timeout_seconds="$(host_test_duration_seconds "$timeout_value")" || [[ "$timeout_seconds" -le 0 ]]; then
    echo "host test requires a positive integral seconds/minutes -timeout" >&2
    return 2
  fi
  if [[ "$load_class" == "light" ]]; then
    if [[ -n "$tags_value" ]] || [[ "$timeout_seconds" -gt 120 ]]; then
      echo "light host test rejects build tags and requires timeout <= 120s" >&2
      return 2
    fi
  elif [[ -n "$tags_value" && "$tags_value" != "e2e" ]] || [[ "$timeout_seconds" -gt 600 ]]; then
    echo "medium host test accepts only optional -tags=e2e and requires timeout <= 600s" >&2
    return 2
  fi
}

host_test_resource_snapshot() {
  local logical_cpus="" one_minute_load="" memory_free_percent=""
  if command -v sysctl >/dev/null 2>&1; then
    logical_cpus="$(sysctl -n hw.logicalcpu 2>/dev/null || true)"
    one_minute_load="$(sysctl -n vm.loadavg 2>/dev/null | awk '{print $2}' || true)"
  fi
  if [[ -z "$logical_cpus" ]] && command -v getconf >/dev/null 2>&1; then
    logical_cpus="$(getconf _NPROCESSORS_ONLN 2>/dev/null || true)"
  fi
  if [[ -z "$one_minute_load" && -r /proc/loadavg ]]; then
    read -r one_minute_load _ </proc/loadavg
  fi
  if command -v memory_pressure >/dev/null 2>&1; then
    memory_free_percent="$(memory_pressure -Q 2>/dev/null | awk -F': ' '/free percentage/ {gsub(/%/, "", $2); print $2; exit}' || true)"
  elif [[ -r /proc/meminfo ]]; then
    memory_free_percent="$(awk '/MemTotal:/ {total=$2} /MemAvailable:/ {available=$2} END {if (total > 0) printf "%.0f", available*100/total}' /proc/meminfo)"
  fi
  if [[ ! "$logical_cpus" =~ ^[0-9]+$ ]] || [[ "$logical_cpus" -le 0 ]] ||
    [[ ! "$one_minute_load" =~ ^[0-9]+([.][0-9]+)?$ ]] ||
    [[ ! "$memory_free_percent" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    echo "host load evidence is unavailable; route this test to ECI" >&2
    return 2
  fi
  awk -v load="$one_minute_load" -v cpus="$logical_cpus" -v memory="$memory_free_percent" '
    BEGIN {
      ratio = load / cpus
      tier = (ratio <= 0.50 && memory >= 25) ? "low" : ((ratio <= 0.80 && memory >= 15) ? "medium" : "high")
      printf "%s %.4f %d %.1f\n", tier, ratio, cpus, memory
    }
  '
}

admit_host_test_load() {
  local load_class="$1" snapshot tier ratio logical_cpus memory_free_percent
  if ! snapshot="$(host_test_resource_snapshot)"; then
    return 2
  fi
  read -r tier ratio logical_cpus memory_free_percent <<<"$snapshot"
  if [[ "$tier" == "high" ]]; then
    echo "host test admission rejected: class=$load_class host_tier=$tier load_per_cpu=$ratio memory_free_percent=$memory_free_percent; use ECI" >&2
    return 2
  fi
  printf '[test-with-guard] host admission class=%s host_tier=%s load_per_cpu=%s logical_cpus=%s memory_free_percent=%s\n' \
    "$load_class" "$tier" "$ratio" "$logical_cpus" "$memory_free_percent"
}

run_host_test() {
  local real_go="$1" load_class="$2" output_file status=0 cache_values local_cache_root local_temp_root local_cache_identity
  shift 2
  validate_host_test_args "$load_class" "$@"
  admit_host_test_load "$load_class"
  echo "[test-with-guard] LOCAL_NON_AUTHORITATIVE class=$load_class; authoritative PASS still requires ECI"
  run_guard "$real_go" quick
  admit_host_test_load "$load_class"
  run_copylocks_guard "$real_go" "$@"
  local gomaxprocs=2 build_parallelism=1
  if [[ "$load_class" == "medium" ]]; then
    gomaxprocs=4
    build_parallelism=2
  fi
  cache_values="$(local_go_cache_prepare "$ROOT_DIR" "$real_go")"
  local_cache_root="${cache_values%%$'\n'*}"
  cache_values="${cache_values#*$'\n'}"
  local_temp_root="${cache_values%%$'\n'*}"
  local_cache_identity="${cache_values#*$'\n'}"
  if [[ -z "$local_cache_root" || -z "$local_temp_root" || ! "$local_cache_identity" =~ ^[0-9a-f]{64}$ ]]; then
    echo "local Go cache preparation returned an invalid identity" >&2
    return 2
  fi
  output_file="$(mktemp -t super-agent-host-test.XXXXXX)"
  (
    export GOMAXPROCS="$gomaxprocs"
    export GOCACHE="$local_cache_root"
    export GOTMPDIR="$local_temp_root"
    export GOTOOLCHAIN=local
    run_go_test "$real_go" -p="$build_parallelism" "$@"
  ) 2>&1 | tee "$output_file" || status=$?
  local_go_cache_cleanup_temp "$local_temp_root"
  if [[ "$status" -ne 0 ]]; then
    rm -f -- "$output_file"
    return "$status"
  fi
  if grep -Fq '[no tests to run]' "$output_file"; then
    rm -f -- "$output_file"
    echo "host test selected no tests; check the exact selector and required build tags" >&2
    return 2
  fi
  rm -f -- "$output_file"
  echo "[test-with-guard] LOCAL_NON_AUTHORITATIVE PASS class=$load_class"
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
  if [[ -z "$target" || "$target" != ./* || "$target" == *[[:space:]]* || "$target" == *\\* ]]; then
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
  return 0
}

CANONICAL_BACKEND_PACKAGES=()

# REMOTE_WORKLOAD_FINGERPRINT_CANONICAL_BEGIN
canonical_backend_includes_mcp_lsp() {
  local package
  for package in ${CANONICAL_BACKEND_PACKAGES[@]+"${CANONICAL_BACKEND_PACKAGES[@]}"}; do
    case "$package" in
      */cmd/mcp-lsp) return 0 ;;
    esac
  done
  return 1
}

run_mcp_lsp_resource_cohort_e2e() {
  local real_go="$1"
  if ! canonical_backend_includes_mcp_lsp; then
    echo "[test-with-guard] mcp-lsp resource cohort E2E skip: package not selected"
    return 0
  fi
  run_go_test "$real_go" \
    -tags=e2e ./cmd/mcp-lsp \
    -run "$MCP_LSP_RESOURCE_COHORT_E2E_RUN" \
    -v -timeout=240s -count=1
}

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
  run_mcp_lsp_resource_cohort_e2e "$real_go"
}
# REMOTE_WORKLOAD_FINGERPRINT_CANONICAL_END

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

run_ci_package() {
  local real_go="$1"
  local race_mode="$2"
  shift 2
  if { [ "$#" -ne 1 ] && [ "$#" -ne 2 ]; } || ! canonical_backend_target_allowed "$1" || [[ "$1" == *...* ]]; then
    echo "CI package mode requires one exact canonical backend package and optional exact test" >&2
    return 2
  fi
  local package_name="$1"
  local test_name="${2:-}"
  if [ -n "$test_name" ] && { [[ ! "$test_name" =~ ^(Test|Fuzz|Example)[[:alnum:]_]*$ ]] || [[ "$test_name" == */* ]]; }; then
    echo "CI package test mode requires one exact top-level Go test name" >&2
    return 2
  fi
  local listed
  if ! listed="$(cd "$ROOT_DIR" && "$real_go" list "$package_name")" || [ "$listed" != "${listed//$'\n'/}" ] || [ -z "$listed" ]; then
    echo "CI package mode failed to resolve exactly one package" >&2
    return 2
  fi
  run_copylocks_guard "$real_go" "$package_name"
  local -a test_args=("$package_name" -json)
  if [ -n "$test_name" ]; then
    test_args+=(-run "^${test_name}$")
  fi
  if [ "$race_mode" = "race" ]; then
    run_go_test "$real_go" "${test_args[@]}" -race -short -count=1 -timeout=0
  else
    run_go_test "$real_go" "${test_args[@]}" -count=1 -timeout=0
  fi
}

run_ci_package_benchmark() {
  local real_go="$1"
  shift
  if [ "$#" -ne 2 ] || ! canonical_backend_target_allowed "$1" || [[ "$1" == *...* ]]; then
    echo "CI benchmark mode requires one exact canonical backend package and one exact benchmark" >&2
    return 2
  fi
  local package_name="$1"
  local benchmark_name="$2"
  if [[ ! "$benchmark_name" =~ ^Benchmark[[:alnum:]_]*$ ]]; then
    echo "CI benchmark mode requires one exact top-level Go benchmark name" >&2
    return 2
  fi
  local listed
  if ! listed="$(cd "$ROOT_DIR" && "$real_go" list "$package_name")" || [ "$listed" != "${listed//$'\n'/}" ] || [ -z "$listed" ]; then
    echo "CI benchmark mode failed to resolve exactly one package" >&2
    return 2
  fi
  run_copylocks_guard "$real_go" "$package_name"
  run_go_test "$real_go" "$package_name" -json -run '^$' -bench "^${benchmark_name}$" -count=1 -timeout=0
}

run_ci_compile_package() {
  local real_go="$1"
  shift
  if [ "$#" -ne 1 ] || ! canonical_backend_target_allowed "$1" || [[ "$1" == *...* ]]; then
    echo "CI compile mode requires one exact canonical backend package" >&2
    return 2
  fi
  local package_name="$1" listed output compile_status=0
  if ! listed="$(cd "$ROOT_DIR" && "$real_go" list "$package_name")" || [ "$listed" != "${listed//$'\n'/}" ] || [ -z "$listed" ]; then
    echo "CI compile mode failed to resolve exactly one package" >&2
    return 2
  fi
  output="$(mktemp "${TMPDIR:-/tmp}/super-dolphin-ci-compile.XXXXXX")"
  if (cd "$ROOT_DIR" && "$real_go" test -c -o "$output" "$package_name"); then
    :
  else
    compile_status=$?
  fi
  rm -f -- "$output"
  return "$compile_status"
}

run_ci_guard() {
  local real_go="$1"
  local guard_mode="$2"
  run_guard "$real_go" "$guard_mode"
  run_copylocks_guard "$real_go" ./internal/provider/... ./internal/platform/... ./internal/module/thread/...
  run_nested_module_guard "$real_go"
}

run_ci_copylocks_guard() {
  local real_go="$1"
  local target="$2"
  case "$target" in
    provider) run_copylocks_guard "$real_go" ./internal/provider/... ;;
    platform) run_copylocks_guard "$real_go" ./internal/platform/... ;;
    thread) run_copylocks_guard "$real_go" ./internal/module/thread/... ;;
    *) echo "CI copylocks mode requires provider, platform, or thread" >&2; return 2 ;;
  esac
}

run_ci_nested_module_guard() {
  local real_go="$1"
  local module_dir="$2"
  case "$module_dir" in
    build/gate/runtime-proxy|build/gate/runtime-tools|third_party/kelindar-event) ;;
    *) echo "CI nested module mode received an unsupported module" >&2; return 2 ;;
  esac
  "$ROOT_DIR/scripts/check_nested_go_modules.sh" "$real_go" "$module_dir"
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

  local guard_mode=full host_test_class=""
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      --quick-guard)
        [[ "$guard_mode" == "full" ]] || { echo "duplicate --quick-guard" >&2; exit 2; }
        guard_mode=quick
        shift
        ;;
      --host-test)
        [[ -z "$host_test_class" ]] || { echo "duplicate --host-test" >&2; exit 2; }
        [[ "$#" -ge 2 ]] || { usage; exit 2; }
        host_test_class="$2"
        shift 2
        ;;
      *) break ;;
    esac
  done
  if [[ "$#" -eq 0 ]]; then
    usage
    exit 1
  fi

  if [ "$1" != "--help" ] && [ "$1" != "-h" ] && ! all_args_are_go_files "$@"; then
    if [[ -n "$host_test_class" ]]; then
      require_remote_test_execution "host-$host_test_class"
    else
      require_remote_test_execution
    fi
  fi

  if [[ -n "$host_test_class" ]]; then
    [[ "$guard_mode" == "full" ]] || { echo "host test selects its own quick guard; omit --quick-guard" >&2; exit 2; }
    local real_go
    if ! real_go="$(resolve_real_go)"; then
      exit 1
    fi
    run_host_test "$real_go" "$host_test_class" "$@"
    return
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
    --ci-guard)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      [ "$#" -eq 0 ] || { usage; exit 1; }
      run_ci_guard "$real_go" quick
      ;;
    --ci-guard-source)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      [ "$#" -eq 0 ] || { usage; exit 1; }
      run_guard "$real_go" quick
      ;;
    --ci-copylocks)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      [ "$#" -eq 1 ] || { usage; exit 1; }
      run_ci_copylocks_guard "$real_go" "$1"
      ;;
    --ci-nested-module)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      [ "$#" -eq 1 ] || { usage; exit 1; }
      run_ci_nested_module_guard "$real_go" "$1"
      ;;
    --ci-package)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      run_ci_package "$real_go" normal "$@"
      ;;
    --ci-package-test)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      [ "$#" -eq 2 ] || { usage; exit 1; }
      run_ci_package "$real_go" normal "$@"
      ;;
    --ci-compile-package)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      run_ci_compile_package "$real_go" "$@"
      ;;
    --ci-package-benchmark)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      [ "$#" -eq 2 ] || { usage; exit 1; }
      run_ci_package_benchmark "$real_go" "$@"
      ;;
    --ci-race-guard)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      [ "$#" -eq 0 ] || { usage; exit 1; }
      run_ci_guard "$real_go" full
      ;;
    --ci-race-package)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      run_ci_package "$real_go" race "$@"
      ;;
    --ci-race-package-test)
      local real_go
      if ! real_go="$(resolve_real_go)"; then
        exit 1
      fi
      shift
      [ "$#" -eq 2 ] || { usage; exit 1; }
      run_ci_package "$real_go" race "$@"
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
