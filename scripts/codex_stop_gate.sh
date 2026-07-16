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
  if ! ${printed}; then
    printf 'none\n'
  fi
  exit 0
fi

log "Super-Dolphin Codex Stop gate"
log "Changed files:"
printf '%s\n' "${CHANGED_FILES}" >>"${LOG_FILE}" 2>/dev/null || true

FAILURES=()

go_package_filter_failed=false
if [[ -n "${GO_PKGS}" ]]; then
  if ! GO_PKGS="$(filter_active_go_packages "${GO_PKGS}")"; then
    go_package_filter_failed=true
    GO_PKGS=""
    FAILURES+=("changed Go package tests")
  fi
fi
if [[ -n "${GO_PKGS}" ]]; then
  go_args=()
  while IFS= read -r pkg; do
    [[ -z "${pkg}" ]] && continue
    go_args+=("${pkg}")
  done <<< "${GO_PKGS}"
  if ! run_cmd "changed Go package tests" ./scripts/test_with_guard.sh "${go_args[@]}" -count=1; then
    FAILURES+=("changed Go package tests")
  fi
elif ${HAS_GO_GUARD} && ! ${go_package_filter_failed}; then
  if ! run_cmd "Go code guard" ./scripts/test_with_guard.sh --guard-only; then
    FAILURES+=("Go code guard")
  fi
fi

if [[ -n "${FRONTEND_PROJECTS}" ]]; then
  while IFS= read -r project; do
    [[ -z "${project}" ]] && continue
    if [[ ! -f "${project}/package.json" ]]; then
      log "missing package.json: ${project}/package.json"
      FAILURES+=("frontend project tests")
      continue
    fi
    if ! run_cmd "frontend size guard (${project})" run_frontend_size_guard "${project}"; then
      FAILURES+=("frontend size guard (${project})")
    fi
    if ! run_cmd "frontend vitest (${project})" run_frontend_vitest "${project}"; then
      FAILURES+=("frontend vitest (${project})")
    fi
  done <<< "${FRONTEND_PROJECTS}"
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
