#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET="${TARGET:-agent-terminal}" # agent-terminal | app-server

normalize_target() {
  local value="$1"
  case "${value}" in
    agent-terminal|app-server) echo "${value}" ;;
    appserver) echo "app-server" ;;
    terminal|ui) echo "agent-terminal" ;;
    *)
      echo "unsupported TARGET: ${value}" >&2
      echo "supported: agent-terminal | app-server" >&2
      exit 1
      ;;
  esac
}

TARGET="$(normalize_target "${TARGET}")"
COVERMODE="${COVERMODE:-atomic}"

target_package() {
  case "${TARGET}" in
    agent-terminal) echo "./cmd/agent-terminal/..." ;;
    app-server) echo "./cmd/app-server" ;;
  esac
}

default_bin_path() {
  case "${TARGET}" in
    agent-terminal) echo "${ROOT_DIR}/bin/agent-terminal.cover" ;;
    app-server) echo "${ROOT_DIR}/bin/app-server.cover" ;;
  esac
}

default_cover_dir() {
  case "${TARGET}" in
    agent-terminal) echo "${ROOT_DIR}/.tmp/ui-cover" ;;
    app-server) echo "${ROOT_DIR}/.tmp/app-cover" ;;
  esac
}

default_profile_path() {
  case "${TARGET}" in
    agent-terminal) echo "${ROOT_DIR}/.tmp/ui-cover.out" ;;
    app-server) echo "${ROOT_DIR}/.tmp/app-cover.out" ;;
  esac
}

default_summary_path() {
  case "${TARGET}" in
    agent-terminal) echo "${ROOT_DIR}/.tmp/ui-cover-summary.txt" ;;
    app-server) echo "${ROOT_DIR}/.tmp/app-cover-summary.txt" ;;
  esac
}

default_triggered_path() {
  case "${TARGET}" in
    agent-terminal) echo "${ROOT_DIR}/.tmp/ui-triggered.txt" ;;
    app-server) echo "${ROOT_DIR}/.tmp/app-triggered.txt" ;;
  esac
}

default_untriggered_path() {
  case "${TARGET}" in
    agent-terminal) echo "${ROOT_DIR}/.tmp/ui-untriggered.txt" ;;
    app-server) echo "${ROOT_DIR}/.tmp/app-untriggered.txt" ;;
  esac
}

default_project_summary_path() {
  case "${TARGET}" in
    agent-terminal) echo "${ROOT_DIR}/.tmp/ui-cover-summary.project.txt" ;;
    app-server) echo "${ROOT_DIR}/.tmp/app-cover-summary.project.txt" ;;
  esac
}

BIN_PATH="${BIN_PATH:-$(default_bin_path)}"
COVER_DIR="${COVER_DIR:-$(default_cover_dir)}"
PROFILE_PATH="${PROFILE_PATH:-$(default_profile_path)}"
SUMMARY_PATH="${SUMMARY_PATH:-$(default_summary_path)}"
PROJECT_SUMMARY_PATH="${PROJECT_SUMMARY_PATH:-$(default_project_summary_path)}"
TRIGGERED_PATH="${TRIGGERED_PATH:-$(default_triggered_path)}"
UNTRIGGERED_PATH="${UNTRIGGERED_PATH:-$(default_untriggered_path)}"

usage() {
  cat <<EOF
Usage:
  TARGET=agent-terminal scripts/ui-coverage.sh build
  TARGET=agent-terminal scripts/ui-coverage.sh run --debug
  TARGET=agent-terminal scripts/ui-coverage.sh report

  TARGET=app-server scripts/ui-coverage.sh build
  TARGET=app-server scripts/ui-coverage.sh run --listen ws://127.0.0.1:4500
  TARGET=app-server scripts/ui-coverage.sh report

Environment overrides:
  TARGET               agent-terminal | app-server
  COVERMODE            set | count | atomic (default: atomic)
  BIN_PATH             instrumented binary path
  COVER_DIR            GOCOVERDIR output directory
  PROFILE_PATH         go tool covdata text profile output
  SUMMARY_PATH         go tool cover -func summary output (all instrumented funcs)
  PROJECT_SUMMARY_PATH project-only filtered summary output
  TRIGGERED_PATH       project functions with coverage > 0%
  UNTRIGGERED_PATH     project functions with coverage = 0%

Workflow:
  1) build: compile instrumented binary and reset coverage directory
  2) run: start binary, manually execute real business flow, then exit process
  3) report: generate project-level triggered / untriggered function lists

Notes:
  - "untriggered (=0%)" means NOT reached in this sampled flow, not safe-to-delete by itself.
  - Run multiple business scenarios before deciding deletion.
EOF
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing command: $1" >&2
    exit 1
  fi
}

trim_spaces() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  echo "${value}"
}

load_env_defaults() {
  local env_file
  for env_file in "${ROOT_DIR}/.env" "${ROOT_DIR}/../.env"; do
    if [[ ! -f "${env_file}" ]]; then
      continue
    fi

    while IFS= read -r raw_line || [[ -n "${raw_line}" ]]; do
      local line key value
      line="${raw_line%$'\r'}"
      if [[ -z "${line}" || "${line}" =~ ^[[:space:]]*# ]]; then
        continue
      fi
      if [[ "${line}" != *"="* ]]; then
        continue
      fi
      key="$(trim_spaces "${line%%=*}")"
      value="$(trim_spaces "${line#*=}")"
      if [[ -z "${key}" ]]; then
        continue
      fi
      if [[ "${value}" =~ ^\".*\"$ ]]; then
        value="${value:1:${#value}-2}"
      elif [[ "${value}" =~ ^\'.*\'$ ]]; then
        value="${value:1:${#value}-2}"
      fi
      if [[ -z "${!key+x}" ]]; then
        export "${key}=${value}"
      fi
    done < "${env_file}"

    echo "loaded env defaults from: ${env_file}"
    return 0
  done
}

module_path() {
  (
    cd "${ROOT_DIR}"
    go list -m -f '{{.Path}}'
  )
}

run_covdata() {
  if go tool | grep -qx 'covdata'; then
    go tool covdata "$@"
    return
  fi
  if [[ -d /usr/local/go/src/cmd/covdata ]]; then
    go run /usr/local/go/src/cmd/covdata "$@"
    return
  fi
  echo "covdata tool unavailable. please install a Go toolchain with cmd/covdata." >&2
  exit 1
}

build_binary() {
  require_cmd go
  mkdir -p "$(dirname "${BIN_PATH}")" "${COVER_DIR}" "$(dirname "${PROFILE_PATH}")"
  rm -rf "${COVER_DIR}"
  mkdir -p "${COVER_DIR}"
  (
    cd "${ROOT_DIR}"
    go build -cover -covermode="${COVERMODE}" -coverpkg=./... -o "${BIN_PATH}" "$(target_package)"
  )
  cat <<EOF
built (${TARGET}): ${BIN_PATH}
covermode: ${COVERMODE}
cover dir reset: ${COVER_DIR}

next:
  TARGET=${TARGET} scripts/ui-coverage.sh run $( [[ "${TARGET}" == "agent-terminal" ]] && echo "--debug" || echo "--listen ws://127.0.0.1:4500" )
EOF
}

run_binary() {
  if [[ ! -x "${BIN_PATH}" ]]; then
    echo "instrumented binary not found: ${BIN_PATH}" >&2
    echo "run: TARGET=${TARGET} scripts/ui-coverage.sh build" >&2
    exit 1
  fi
  mkdir -p "${COVER_DIR}"
  load_env_defaults
  echo "target: ${TARGET}"
  echo "GOCOVERDIR=${COVER_DIR}"
  echo "starting: ${BIN_PATH} $*"
  (
    cd "${ROOT_DIR}"
    GOCOVERDIR="${COVER_DIR}" "${BIN_PATH}" "$@"
  )
}

render_report() {
  require_cmd go
  mkdir -p "$(dirname "${PROFILE_PATH}")"
  if [[ ! -d "${COVER_DIR}" ]]; then
    echo "cover dir not found: ${COVER_DIR}" >&2
    exit 1
  fi
  if [[ -z "$(ls -A "${COVER_DIR}" 2>/dev/null)" ]]; then
    echo "cover dir is empty: ${COVER_DIR}" >&2
    echo "run sampled business flow first: TARGET=${TARGET} scripts/ui-coverage.sh run ..." >&2
    exit 1
  fi

  local counter_count
  counter_count="$(find "${COVER_DIR}" -maxdepth 1 -type f -name 'covcounters.*' | wc -l | tr -d ' ')"
  if [[ "${counter_count}" == "0" ]]; then
    echo "no coverage counters found in: ${COVER_DIR}" >&2
    echo "tip: close the app gracefully, then rerun report." >&2
    echo "tip: if this persists, ensure the binary flushes runtime coverage counters on shutdown." >&2
    exit 1
  fi

  local mod
  mod="$(module_path)"

  (
    cd "${ROOT_DIR}"
    run_covdata textfmt -i="${COVER_DIR}" -o="${PROFILE_PATH}"
    go tool cover -func="${PROFILE_PATH}" > "${SUMMARY_PATH}"
  )

  awk -v prefix="${mod}/" '$1 ~ ("^"prefix) { print }' "${SUMMARY_PATH}" > "${PROJECT_SUMMARY_PATH}"

  awk '
    /total:/ { next }
    {
      pct = $NF
      gsub(/%/, "", pct)
      if ((pct + 0) > 0) {
        print $0
      }
    }
  ' "${PROJECT_SUMMARY_PATH}" | sort > "${TRIGGERED_PATH}"

  awk '
    /total:/ { next }
    {
      pct = $NF
      gsub(/%/, "", pct)
      if ((pct + 0) == 0) {
        print $0
      }
    }
  ' "${PROJECT_SUMMARY_PATH}" | sort > "${UNTRIGGERED_PATH}"

  local triggered_count untriggered_count total_line
  triggered_count="$(wc -l < "${TRIGGERED_PATH}" | tr -d ' ')"
  untriggered_count="$(wc -l < "${UNTRIGGERED_PATH}" | tr -d ' ')"
  total_line="$(tail -n 1 "${SUMMARY_PATH}")"

  cat <<EOF
target: ${TARGET}
summary(all): ${SUMMARY_PATH}
summary(project): ${PROJECT_SUMMARY_PATH}
triggered(project, >0%): ${TRIGGERED_PATH} (${triggered_count})
untriggered(project, =0%): ${UNTRIGGERED_PATH} (${untriggered_count})
${total_line}

top untriggered project functions (first 30):
EOF
  head -n 30 "${UNTRIGGERED_PATH}" || true
}

main() {
  local subcommand="${1:-}"
  if [[ -z "${subcommand}" ]]; then
    usage
    exit 1
  fi
  shift || true

  case "${subcommand}" in
    build)
      build_binary
      ;;
    run)
      run_binary "$@"
      ;;
    report)
      render_report
      ;;
    help|-h|--help)
      usage
      ;;
    *)
      usage
      exit 1
      ;;
  esac
}

main "$@"
