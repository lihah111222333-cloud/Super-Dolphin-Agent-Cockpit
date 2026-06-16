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
GOFMT_BIN="${CODEX_STOP_GATE_GOFMT_BIN:-gofmt}"
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
if ! ${PRINT_PLAN} && [[ ! -t 0 ]]; then
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

FULL_GO_GUARD="${CODEX_STOP_GATE_FULL_GO_GUARD:-${CODEX_STOP_GATE_FULL:-0}}"
FRONTEND_TESTS="${CODEX_STOP_GATE_FRONTEND_TESTS:-0}"
HOOK_TESTS="${CODEX_STOP_GATE_HOOK_TESTS:-1}"
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
    ${FRONTEND_PROJECT}/node_modules/*|\
    ${FRONTEND_PROJECT}/.vite-cache/*|\
    ${FRONTEND_PROJECT}/.build-cache/*|\
    ${FRONTEND_PROJECT}/dist/*|\
    ${FRONTEND_PROJECT}/playwright-report/*|\
    ${FRONTEND_PROJECT}/test-results/*|\
    ${FRONTEND_PROJECT}/review/*|\
    ${FRONTEND_PROJECT}/full_test_output.txt|\
    ${FRONTEND_PROJECT}/.DS_Store)
      return 1
      ;;
    ${FRONTEND_PROJECT}/*)
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

changed_go_files() {
  while IFS= read -r file; do
    [[ -z "${file}" ]] && continue
    case "${file}" in
      third_party/*)
        ;;
      *.go)
        printf '%s\n' "${file}"
        ;;
    esac
  done | sort -u
}

filter_active_go_packages() {
  local packages="$1"
  local filtered=""

  while IFS= read -r pkg; do
    [[ -z "${pkg}" ]] && continue
    if "${GO_BIN}" list -e -f '{{if or .GoFiles .TestGoFiles}}{{.ImportPath}}{{end}}' "${pkg}" 2>>"${LOG_FILE}" | grep -q .; then
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

run_go_gofmt() {
  local files="$1"
  local output
  output="$(mktemp)"

  while IFS= read -r file; do
    [[ -z "${file}" ]] && continue
    [[ -f "${file}" ]] || continue
    "${GOFMT_BIN}" -l "${file}" >>"${output}"
  done <<< "${files}"

  if [[ -s "${output}" ]]; then
    log "Go files need gofmt:"
    cat "${output}" >&2
    rm -f "${output}"
    return 1
  fi
  rm -f "${output}"
}

run_frontend_quick_guard() {
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

run_hook_syntax_checks() {
  [[ ! -f scripts/codex_stop_gate.sh ]] || bash -n scripts/codex_stop_gate.sh
  for hook in .githooks/*; do
    [[ -f "${hook}" ]] || continue
    bash -n "${hook}"
  done
}

run_hook_plan_tests() {
  if [[ ! -f scripts/tests/test_codex_stop_gate_plan.sh ]]; then
    log "skip Codex Stop gate plan tests: scripts/tests/test_codex_stop_gate_plan.sh not found"
    return 0
  fi
  bash scripts/tests/test_codex_stop_gate_plan.sh
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

HAS_GO_GUARD=false
HAS_FRONTEND_GUARD=false
HAS_HOOK_TEST=false

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
done <<< "${CHANGED_FILES}"

GO_PKGS="$(changed_go_packages <<< "${CHANGED_FILES}")"
GO_FILES="$(changed_go_files <<< "${CHANGED_FILES}")"
FRONTEND_PROJECTS="$(changed_frontend_projects <<< "${CHANGED_FILES}")"

if ${PRINT_PLAN}; then
  printed=false
  while IFS= read -r file; do
    [[ -z "${file}" ]] && continue
    printf 'gofmt %s\n' "${file}"
    printed=true
  done <<< "${GO_FILES}"
  while IFS= read -r pkg; do
    [[ -z "${pkg}" ]] && continue
    printf 'go_test %s\n' "${pkg}"
    printed=true
  done <<< "${GO_PKGS}"
  while IFS= read -r project; do
    [[ -z "${project}" ]] && continue
    printf 'frontend_lint %s\n' "${project}"
    if [[ "${FRONTEND_TESTS}" = "1" ]]; then
      printf 'frontend_test %s\n' "${project}"
    else
      printf 'frontend_test_skipped %s CODEX_STOP_GATE_FRONTEND_TESTS=0\n' "${project}"
    fi
    printed=true
  done <<< "${FRONTEND_PROJECTS}"
  if ${HAS_GO_GUARD}; then
    if [[ "${FULL_GO_GUARD}" = "1" ]]; then
      printf 'guard go full\n'
    else
      printf 'guard go full_skipped CODEX_STOP_GATE_FULL_GO_GUARD=0\n'
    fi
    printed=true
  fi
  if ${HAS_HOOK_TEST}; then
    printf 'hook_syntax bash -n\n'
    if [[ "${HOOK_TESTS}" = "1" && -f scripts/tests/test_codex_stop_gate_plan.sh ]]; then
      printf 'hook_test scripts/tests/test_codex_stop_gate_plan.sh\n'
    else
      printf 'hook_test_skipped scripts/tests/test_codex_stop_gate_plan.sh\n'
    fi
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

if [[ -n "${GO_FILES}" ]]; then
  if ! run_cmd "changed Go gofmt" run_go_gofmt "${GO_FILES}"; then
    FAILURES+=("changed Go gofmt")
  fi
fi

if [[ -n "${GO_PKGS}" ]]; then
  GO_PKGS="$(filter_active_go_packages "${GO_PKGS}")"
fi
if [[ -n "${GO_PKGS}" ]]; then
  go_args=()
  while IFS= read -r pkg; do
    [[ -z "${pkg}" ]] && continue
    go_args+=("${pkg}")
  done <<< "${GO_PKGS}"
  if ! run_cmd "changed Go package tests" "${GO_BIN}" test "${go_args[@]}" -count=1; then
    FAILURES+=("changed Go package tests")
  fi
fi
if [[ -n "${GO_PKGS}" ]] || ${HAS_GO_GUARD}; then
  if [[ "${FULL_GO_GUARD}" = "1" ]]; then
    if ! run_cmd "Go code guard" make guard-change; then
      FAILURES+=("Go code guard")
    fi
  else
    log "skip full Go code guard; set CODEX_STOP_GATE_FULL_GO_GUARD=1 to run make guard-change"
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
    if ! run_cmd "frontend quick guard (${project})" run_frontend_quick_guard "${project}"; then
      FAILURES+=("frontend quick guard (${project})")
    fi
    if [[ "${FRONTEND_TESTS}" = "1" ]]; then
      if ! run_cmd "frontend vitest (${project})" run_frontend_vitest "${project}"; then
        FAILURES+=("frontend vitest (${project})")
      fi
    else
      log "skip frontend vitest (${project}); set CODEX_STOP_GATE_FRONTEND_TESTS=1 to run it"
    fi
  done <<< "${FRONTEND_PROJECTS}"
fi

if ${HAS_HOOK_TEST}; then
  if ! run_cmd "hook shell syntax checks" run_hook_syntax_checks; then
    FAILURES+=("hook shell syntax checks")
  fi
  if [[ "${HOOK_TESTS}" = "1" ]]; then
    if ! run_cmd "Codex Stop gate plan tests" run_hook_plan_tests; then
      FAILURES+=("Codex Stop gate plan tests")
    fi
  else
    log "skip Codex Stop gate plan tests; CODEX_STOP_GATE_HOOK_TESTS=0"
  fi
fi

if [[ "${#FAILURES[@]}" -gt 0 ]]; then
  reason="Super-Dolphin Codex Stop gate failed: ${FAILURES[*]}. Fix the failing scoped tests or guards, then stop again. Full log: ${LOG_FILE}"
  emit_block "${reason}"
  exit 0
fi

emit_continue
