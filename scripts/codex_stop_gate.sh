#!/usr/bin/env bash
# Codex Stop hook gate for Super-Dolphin.
# Runs scoped checks for changed Go/frontend code and validates hook changes.

set -u

PRINT_PLAN=false
if [[ "${1:-}" == "--print-plan" ]]; then
  PRINT_PLAN=true
fi

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
if [[ -z "${REPO_ROOT}" ]]; then
  printf '{"decision":"block","reason":"Codex Stop gate failed: not inside a git repository."}\n'
  exit 0
fi

cd "${REPO_ROOT}" || {
  printf '{"decision":"block","reason":"Codex Stop gate failed: cannot enter repository root."}\n'
  exit 0
}

GIT_BIN="${CODEX_STOP_GATE_GIT_BIN:-git}"
if [[ -z "${CODEX_STOP_GATE_GIT_BIN:-}" && "${REPO_ROOT}" == /mnt/* ]] && command -v git.exe >/dev/null 2>&1; then
  GIT_BIN="git.exe"
fi

GO_BIN="${CODEX_STOP_GATE_GO_BIN:-go}"
if [[ -z "${CODEX_STOP_GATE_GO_BIN:-}" ]] && ! command -v "${GO_BIN}" >/dev/null 2>&1 && command -v go.exe >/dev/null 2>&1; then
  GO_BIN="$(command -v go.exe)"
fi
if [[ -z "${REAL_GO_BIN:-}" && "${GO_BIN}" == *go.exe ]]; then
  export REAL_GO_BIN="${GO_BIN}"
fi

NODE_BIN="${CODEX_STOP_GATE_NODE_BIN:-node}"
if [[ -z "${CODEX_STOP_GATE_NODE_BIN:-}" ]] && ! command -v "${NODE_BIN}" >/dev/null 2>&1 && command -v node.exe >/dev/null 2>&1; then
  NODE_BIN="$(command -v node.exe)"
fi

NPX_BIN="${CODEX_STOP_GATE_NPX_BIN:-npx}"
if [[ -z "${CODEX_STOP_GATE_NPX_BIN:-}" ]] && ! command -v "${NPX_BIN}" >/dev/null 2>&1; then
  if command -v npx >/dev/null 2>&1; then
    NPX_BIN="$(command -v npx)"
  elif command -v npx.cmd >/dev/null 2>&1; then
    NPX_BIN="$(command -v npx.cmd)"
  fi
fi

HOOK_INPUT=""
if [[ ! -t 0 ]]; then
  if IFS= read -r -t 1 first_line; then
    HOOK_INPUT="${first_line}"
    while IFS= read -r -t 1 next_line; do
      HOOK_INPUT="${HOOK_INPUT}"$'\n'"${next_line}"
    done
  fi
fi

LOG_DIR="${CODEX_STOP_GATE_LOG_DIR:-${REPO_ROOT}/.codex/logs}"
mkdir -p "${LOG_DIR}" 2>/dev/null || true
LOG_FILE="${LOG_DIR}/stop-gate-$(date +%Y%m%d%H%M%S).log"

GO_PACKAGE_PATTERNS=("./cmd/..." "./internal/..." "./pkg/..." "./scripts/...")
FRONTEND_PROJECT="frontend-app"

log() {
  printf '%s\n' "$*" >&2
  printf '%s\n' "$*" >>"${LOG_FILE}" 2>/dev/null || true
}

json_escape() {
  local text="$1"
  text="${text//\\/\\\\}"
  text="${text//\"/\\\"}"
  text="${text//$'\n'/\\n}"
  printf '%s' "${text}"
}

emit_continue() {
  printf '{"continue":true}\n'
}

emit_block() {
  local reason="$1"
  printf '{"decision":"block","reason":"%s"}\n' "$(json_escape "${reason}")"
}

hook_stop_active() {
  case "${HOOK_INPUT}" in
    *'"stop_hook_active":true'*|*'"stop_hook_active": true'*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

if ! ${PRINT_PLAN} && hook_stop_active; then
  log "stop_hook_active=true; skipping Super-Dolphin Stop gate to avoid hook recursion"
  emit_continue
  exit 0
fi

STOP_GATE_LOCK_DIR="${CODEX_STOP_GATE_LOCK_DIR:-${REPO_ROOT}/.codex/stop-gate.lock}"
ACTIVE_STAGE_PIDS=()

terminate_process_tree() {
  local parent="$1"
  if ! kill -STOP "${parent}" 2>/dev/null; then
    return
  fi
  local child
  while IFS= read -r child; do
    [[ -z "${child}" ]] && continue
    terminate_process_tree "${child}"
  done < <(pgrep -P "${parent}" 2>/dev/null || true)
  kill -TERM "${parent}" 2>/dev/null || true
  kill -CONT "${parent}" 2>/dev/null || true
}

release_gate_lock() {
  if ((BASH_SUBSHELL != 0)); then
    return
  fi
  local owner_pid=""
  if [[ -r "${STOP_GATE_LOCK_DIR}/pid" ]]; then
    IFS= read -r owner_pid <"${STOP_GATE_LOCK_DIR}/pid" || true
  fi
  if [[ "${owner_pid}" == "$$" ]]; then
    rm -f -- "${STOP_GATE_LOCK_DIR}/pid"
    rmdir -- "${STOP_GATE_LOCK_DIR}" 2>/dev/null || true
  fi
}

release_gate_resources() {
  if ((BASH_SUBSHELL != 0)); then
    return
  fi
  local pid
  if [[ "${#ACTIVE_STAGE_PIDS[@]}" -gt 0 ]]; then
    for pid in "${ACTIVE_STAGE_PIDS[@]}"; do
      if kill -0 "${pid}" 2>/dev/null; then
        terminate_process_tree "${pid}"
      fi
    done
  fi
  release_gate_lock
}

record_gate_lock_owner() {
  if ! printf '%s\n' "$$" >"${STOP_GATE_LOCK_DIR}/pid"; then
    rmdir -- "${STOP_GATE_LOCK_DIR}" 2>/dev/null || true
    emit_block "Super-Dolphin Codex Stop gate blocked: cannot record lock ownership at ${STOP_GATE_LOCK_DIR}."
    return 1
  fi
  trap release_gate_resources EXIT
}

acquire_gate_lock() {
  if mkdir "${STOP_GATE_LOCK_DIR}" 2>/dev/null; then
    record_gate_lock_owner
    return
  fi

  local owner_pid=""
  if [[ -r "${STOP_GATE_LOCK_DIR}/pid" ]]; then
    IFS= read -r owner_pid <"${STOP_GATE_LOCK_DIR}/pid" || true
  fi
  if [[ "${owner_pid}" =~ ^[0-9]+$ ]] && kill -0 "${owner_pid}" 2>/dev/null; then
    emit_block "Super-Dolphin Codex Stop gate blocked: another gate is active for this worktree (pid ${owner_pid})."
    return 1
  fi
  if [[ ! "${owner_pid}" =~ ^[0-9]+$ ]]; then
    emit_block "Super-Dolphin Codex Stop gate blocked: stale or invalid lock at ${STOP_GATE_LOCK_DIR}."
    return 1
  fi

  local reclaim_dir="${STOP_GATE_LOCK_DIR}.reclaim"
  if ! mkdir "${reclaim_dir}" 2>/dev/null; then
    emit_block "Super-Dolphin Codex Stop gate blocked: dead-owner lock recovery is already active for ${STOP_GATE_LOCK_DIR}."
    return 1
  fi

  local confirmed_owner=""
  if [[ -r "${STOP_GATE_LOCK_DIR}/pid" ]]; then
    IFS= read -r confirmed_owner <"${STOP_GATE_LOCK_DIR}/pid" || true
  fi
  if [[ "${confirmed_owner}" != "${owner_pid}" ]] || kill -0 "${confirmed_owner}" 2>/dev/null; then
    rmdir -- "${reclaim_dir}" 2>/dev/null || true
    emit_block "Super-Dolphin Codex Stop gate blocked: lock ownership changed while reclaiming ${STOP_GATE_LOCK_DIR}."
    return 1
  fi

  rm -f -- "${STOP_GATE_LOCK_DIR}/pid"
  if ! rmdir -- "${STOP_GATE_LOCK_DIR}" 2>/dev/null || ! mkdir "${STOP_GATE_LOCK_DIR}" 2>/dev/null; then
    rmdir -- "${reclaim_dir}" 2>/dev/null || true
    emit_block "Super-Dolphin Codex Stop gate blocked: cannot reclaim dead-owner lock at ${STOP_GATE_LOCK_DIR}."
    return 1
  fi
  if ! record_gate_lock_owner; then
    rmdir -- "${reclaim_dir}" 2>/dev/null || true
    return 1
  fi
  rmdir -- "${reclaim_dir}" 2>/dev/null || true
}

if ! ${PRINT_PLAN} && ! acquire_gate_lock; then
  exit 0
fi

changed_files() {
  if [[ -n "${CODEX_STOP_GATE_CHANGED_FILES_FILE:-}" ]]; then
    sed '/^[[:space:]]*$/d' "${CODEX_STOP_GATE_CHANGED_FILES_FILE}"
    return
  fi

  {
    "${GIT_BIN}" diff --name-only --diff-filter=ACMRD HEAD -- 2>/dev/null || true
    "${GIT_BIN}" ls-files --others --exclude-standard 2>/dev/null || true
  } | sed '/^[[:space:]]*$/d' | sort -u
}

is_frontend_code_path() {
  case "$1" in
    "${FRONTEND_PROJECT}"/node_modules/*|\
    "${FRONTEND_PROJECT}"/.vite-cache/*|\
    "${FRONTEND_PROJECT}"/.build-cache/*|\
    "${FRONTEND_PROJECT}"/dist/*|\
    "${FRONTEND_PROJECT}"/playwright-report/*|\
    "${FRONTEND_PROJECT}"/test-results/*|\
    "${FRONTEND_PROJECT}"/review/*|\
    "${FRONTEND_PROJECT}"/full_test_output.txt|\
    "${FRONTEND_PROJECT}"/.DS_Store)
      return 1
      ;;
    "${FRONTEND_PROJECT}"/*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

is_root_go_change() {
  case "$1" in
    go.mod|go.sum)
      return 0
      ;;
    third_party/*)
      return 1
      ;;
    *.go)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

is_hook_change() {
  case "$1" in
    .codex/config.toml|\
    .codex/hooks.json|\
    .codex/.gitignore|\
    scripts/codex_stop_gate.sh|\
    scripts/tests/test_codex_stop_gate_plan.sh)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

changed_go_packages() {
  while IFS= read -r file; do
    [[ -z "${file}" ]] && continue
    case "${file}" in
      go.mod|go.sum)
        printf '%s\n' "${GO_PACKAGE_PATTERNS[@]}"
        ;;
      third_party/*)
        ;;
      *.go)
        local dir
        dir="$(dirname "${file}")"
        if [[ "${dir}" == "." ]]; then
          printf './\n'
        else
          printf './%s\n' "${dir}"
        fi
        ;;
    esac
  done | sort -u
}

filter_active_go_packages() {
  local packages="$1"
  local filtered=""
  local pkg
  local output
  local status
  local has_active
  local line

  while IFS= read -r pkg; do
    [[ -z "${pkg}" ]] && continue
    output="$("${GO_BIN}" list -e -f '{{if .Error}}__CODEX_STOP_GATE_GO_LIST_ERROR__ {{.Error}}{{else if or .GoFiles .TestGoFiles}}{{.ImportPath}}{{else}}__CODEX_STOP_GATE_GO_LIST_EMPTY__{{end}}' "${pkg}" 2>>"${LOG_FILE}")"
    status=$?
    if ((status != 0)); then
      log "go list failed for ${pkg} (exit ${status}); blocking instead of skipping changed Go package tests"
      return 1
    fi
    if [[ -z "${output}" ]]; then
      log "go list produced empty output for ${pkg}; blocking instead of skipping changed Go package tests"
      return 1
    fi

    has_active=false
    while IFS= read -r line; do
      [[ -z "${line}" ]] && continue
      case "${line}" in
        __CODEX_STOP_GATE_GO_LIST_ERROR__*)
          log "go list could not resolve ${pkg}: ${line#__CODEX_STOP_GATE_GO_LIST_ERROR__ }"
          return 1
          ;;
        __CODEX_STOP_GATE_GO_LIST_EMPTY__)
          ;;
        *)
          has_active=true
          ;;
      esac
    done <<< "${output}"

    if ${has_active}; then
      filtered="${filtered}${pkg}"$'\n'
    else
      log "skip ${pkg} (no active Go files for current build tags)"
    fi
  done <<< "${packages}"

  printf '%s' "${filtered}" | sed '/^[[:space:]]*$/d'
}

changed_frontend_projects() {
  while IFS= read -r file; do
    [[ -z "${file}" ]] && continue
    if is_frontend_code_path "${file}"; then
      printf '%s\n' "${FRONTEND_PROJECT}"
    fi
  done | sort -u
}

run_cmd() {
  local label="$1"
  shift

  log "Step: ${label}"
  log "Command: $*"
  if "$@" >>"${LOG_FILE}" 2>&1; then
    log "PASS: ${label}"
    return 0
  fi

  log "FAIL: ${label}"
  log "Last output for ${label}:"
  tail -n 80 "${LOG_FILE}" >&2 2>/dev/null || true
  return 1
}

run_frontend_size_guard() {
  local project="$1"
  if [[ -f "${project}/scripts/size-guard.cjs" ]]; then
    (cd "${project}" && "${NODE_BIN}" scripts/size-guard.cjs)
    return
  fi
  (cd "${project}" && npm run lint)
}

run_frontend_vitest() {
  local project="$1"
  (cd "${project}" && "${NPX_BIN}" vitest run)
}

CHANGED_FILES="$(changed_files)"

if [[ -z "${CHANGED_FILES}" ]]; then
  if ${PRINT_PLAN}; then
    printf 'none\n'
    exit 0
  fi
  emit_continue
  exit 0
fi

CAPCONTRACT_PATH_RULES_HELPER="scripts/capcontract/path_rules.sh"
if [[ ! -r "${CAPCONTRACT_PATH_RULES_HELPER}" ]]; then
  log "capcontract path rules: missing helper ${CAPCONTRACT_PATH_RULES_HELPER}"
  if ${PRINT_PLAN}; then
    exit 1
  fi
  emit_block "Codex Stop gate failed: capcontract path rules helper is missing."
  exit 0
fi
# shellcheck source=scripts/capcontract/path_rules.sh
if ! source "${CAPCONTRACT_PATH_RULES_HELPER}"; then
  log "capcontract path rules: failed to source ${CAPCONTRACT_PATH_RULES_HELPER}"
  if ${PRINT_PLAN}; then
    exit 1
  fi
  emit_block "Codex Stop gate failed: capcontract path rules helper could not be loaded."
  exit 0
fi
if ! load_capcontract_path_rules "${GO_BIN}"; then
  log "capcontract path rules: failed to load generator-derived rules"
  if ${PRINT_PLAN}; then
    exit 1
  fi
  emit_block "Codex Stop gate failed: capcontract path rules could not be generated."
  exit 0
fi

HAS_GO_GUARD=false
HAS_FRONTEND_GUARD=false
HAS_HOOK_TEST=false
HAS_CAPCONTRACT_GUARD=false

while IFS= read -r file; do
  [[ -z "${file}" ]] && continue
  if is_root_go_change "${file}"; then
    HAS_GO_GUARD=true
  fi
  if is_frontend_code_path "${file}"; then
    HAS_FRONTEND_GUARD=true
  fi
  if is_hook_change "${file}"; then
    HAS_HOOK_TEST=true
  fi
  capcontract_match_status=0
  is_capcontract_change "${file}" || capcontract_match_status=$?
  if [[ "${capcontract_match_status}" -eq 0 ]]; then
    HAS_CAPCONTRACT_GUARD=true
  elif [[ "${capcontract_match_status}" -gt 1 ]]; then
    log "capcontract path rules: failed to classify changed path ${file}"
    if ${PRINT_PLAN}; then
      exit 1
    fi
    emit_block "Codex Stop gate failed: a changed path could not be classified for the capability contract."
    exit 0
  fi
done <<< "${CHANGED_FILES}"

GO_PKGS="$(changed_go_packages <<< "${CHANGED_FILES}")"
FRONTEND_PROJECTS="$(changed_frontend_projects <<< "${CHANGED_FILES}")"

if ${PRINT_PLAN}; then
  printed=false
  while IFS= read -r pkg; do
    [[ -z "${pkg}" ]] && continue
    printf 'go_pkg %s\n' "${pkg}"
    printed=true
  done <<< "${GO_PKGS}"
  while IFS= read -r project; do
    [[ -z "${project}" ]] && continue
    printf 'frontend_project %s\n' "${project}"
    printed=true
  done <<< "${FRONTEND_PROJECTS}"
  if ${HAS_GO_GUARD}; then
    printf 'guard go\n'
    printed=true
  fi
  if ${HAS_FRONTEND_GUARD}; then
    printf 'guard frontend\n'
    printed=true
  fi
  if ${HAS_HOOK_TEST}; then
    printf 'hook_test scripts/tests/test_codex_stop_gate_plan.sh\n'
    printed=true
  fi
  if ${HAS_CAPCONTRACT_GUARD}; then
    printf 'capcontract_check make capcontract-check\n'
    printed=true
  fi
  if { [[ -n "${GO_PKGS}" ]] || ${HAS_GO_GUARD}; } && [[ -n "${FRONTEND_PROJECTS}" ]]; then
    printf 'execution parallel go frontend\n'
    printed=true
  fi
  if ! ${printed}; then
    printf 'none\n'
  fi
  exit 0
fi

log "Super-Dolphin Codex Stop gate"
log "Changed files:"
printf '%s\n' "${CHANGED_FILES}" >>"${LOG_FILE}" 2>/dev/null || true

FAILURES=()

run_go_stage() {
  if [[ -n "${GO_PKGS}" ]]; then
    if ! GO_PKGS="$(filter_active_go_packages "${GO_PKGS}")"; then
      return 1
    fi
  fi
  if [[ -n "${GO_PKGS}" ]]; then
    local go_args=()
    local pkg
    while IFS= read -r pkg; do
      [[ -z "${pkg}" ]] && continue
      go_args+=("${pkg}")
    done <<< "${GO_PKGS}"
    run_cmd "changed Go package tests" ./scripts/test_with_guard.sh "${go_args[@]}" -count=1
    return
  fi
  if ${HAS_GO_GUARD}; then
    run_cmd "Go code guard" ./scripts/test_with_guard.sh --guard-only
  fi
}

run_frontend_stage() {
  local status=0
  local project
  while IFS= read -r project; do
    [[ -z "${project}" ]] && continue
    if [[ ! -f "${project}/package.json" ]]; then
      log "missing package.json: ${project}/package.json"
      status=$((status | 4))
      continue
    fi
    if ! run_cmd "frontend size guard (${project})" run_frontend_size_guard "${project}"; then
      status=$((status | 1))
    fi
    if ! run_cmd "frontend vitest (${project})" run_frontend_vitest "${project}"; then
      status=$((status | 2))
    fi
  done <<< "${FRONTEND_PROJECTS}"
  return "${status}"
}

go_stage_pid=""
frontend_stage_pid=""
if [[ -n "${GO_PKGS}" ]] || ${HAS_GO_GUARD}; then
  run_go_stage &
  go_stage_pid=$!
  ACTIVE_STAGE_PIDS+=("${go_stage_pid}")
fi
if [[ -n "${FRONTEND_PROJECTS}" ]]; then
  run_frontend_stage &
  frontend_stage_pid=$!
  ACTIVE_STAGE_PIDS+=("${frontend_stage_pid}")
fi

go_stage_status=0
frontend_stage_status=0
if [[ -n "${go_stage_pid}" ]]; then
  if wait "${go_stage_pid}"; then
    go_stage_status=0
  else
    go_stage_status=$?
  fi
fi
if [[ -n "${frontend_stage_pid}" ]]; then
  if wait "${frontend_stage_pid}"; then
    frontend_stage_status=0
  else
    frontend_stage_status=$?
  fi
fi
ACTIVE_STAGE_PIDS=()

if ((go_stage_status != 0)); then
  if [[ -n "${GO_PKGS}" ]]; then
    FAILURES+=("changed Go package tests")
  else
    FAILURES+=("Go code guard")
  fi
fi
if ((frontend_stage_status & 4)); then
  FAILURES+=("frontend project tests")
fi
if ((frontend_stage_status & 1)); then
  FAILURES+=("frontend size guard (${FRONTEND_PROJECT})")
fi
if ((frontend_stage_status & 2)); then
  FAILURES+=("frontend vitest (${FRONTEND_PROJECT})")
fi

if ${HAS_HOOK_TEST}; then
  if ! run_cmd "Codex Stop gate plan tests" bash scripts/tests/test_codex_stop_gate_plan.sh; then
    FAILURES+=("Codex Stop gate plan tests")
  fi
fi

if ${HAS_CAPCONTRACT_GUARD}; then
  if ! run_cmd "capability contract check" make capcontract-check; then
    FAILURES+=("capability contract check")
  fi
fi

if [[ "${#FAILURES[@]}" -gt 0 ]]; then
  reason="Super-Dolphin Codex Stop gate failed: ${FAILURES[*]}. Fix the failing scoped tests or guards, then stop again. Full log: ${LOG_FILE}"
  emit_block "${reason}"
  exit 0
fi

emit_continue
