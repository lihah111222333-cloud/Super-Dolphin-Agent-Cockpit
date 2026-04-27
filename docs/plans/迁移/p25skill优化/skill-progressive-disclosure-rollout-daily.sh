#!/usr/bin/env bash
# P25-HIGH-02r: one-command daily rollout observation runner.
# Runs rollout report -> append -> status, preserving artifacts for audit. This
# helper does not enable default policy, delete overrides, merge branches, or mark
# rollout complete.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
OBS_DATE="${SKILL_PD_DATE:-$(date +%F)}"
OBSERVATION_FILE="${SKILL_PD_OBSERVATION_FILE:-${SCRIPT_DIR}/skill-progressive-disclosure-rollout-observation.md}"
ROLLOUT_REPORT_SCRIPT="${SKILL_PD_ROLLOUT_REPORT_SCRIPT:-${SCRIPT_DIR}/skill-progressive-disclosure-rollout-report.sh}"
ROLLOUT_APPEND_SCRIPT="${SKILL_PD_ROLLOUT_APPEND_SCRIPT:-${SCRIPT_DIR}/skill-progressive-disclosure-rollout-append.sh}"
ROLLOUT_STATUS_SCRIPT="${SKILL_PD_ROLLOUT_STATUS_SCRIPT:-${SCRIPT_DIR}/skill-progressive-disclosure-rollout-status.sh}"
OUTPUT_DIR="${SKILL_PD_DAILY_OUTPUT_DIR:-${TMPDIR:-/tmp}/p25-skill-rollout-daily-${OBS_DATE}}"
REPORT_OUT="${SKILL_PD_DAILY_REPORT_OUT:-${OUTPUT_DIR}/rollout-report-${OBS_DATE}.md}"
APPEND_OUT="${SKILL_PD_DAILY_APPEND_OUT:-${OUTPUT_DIR}/rollout-append-${OBS_DATE}.txt}"
STATUS_OUT="${SKILL_PD_DAILY_STATUS_OUT:-${OUTPUT_DIR}/rollout-status-${OBS_DATE}.txt}"
REQUIRE_REAL_SAMPLE="${SKILL_PD_DAILY_REQUIRE_REAL_SAMPLE:-false}"
APPEND_DRY_RUN="${SKILL_PD_DAILY_DRY_RUN:-false}"
RUN_STATUS="${SKILL_PD_DAILY_RUN_STATUS:-true}"

fail() {
  echo "P25-HIGH-02r rollout daily failed: $*" >&2
  exit 1
}

require_executable() {
  local path="$1"
  local label="$2"
  [[ -x "${path}" ]] || fail "${label} script is not executable: ${path}"
}

[[ -f "${OBSERVATION_FILE}" ]] || fail "observation file not found: ${OBSERVATION_FILE}"
require_executable "${ROLLOUT_REPORT_SCRIPT}" "rollout report"
require_executable "${ROLLOUT_APPEND_SCRIPT}" "rollout append"
require_executable "${ROLLOUT_STATUS_SCRIPT}" "rollout status"
mkdir -p "${OUTPUT_DIR}"

set +e
"${ROLLOUT_REPORT_SCRIPT}" >"${REPORT_OUT}" 2>&1
report_status=$?
set -e
if (( report_status != 0 )); then
  cat "${REPORT_OUT}" >&2 || true
  fail "rollout report failed; see ${REPORT_OUT}"
fi

set +e
env SKILL_PD_OBSERVATION_FILE="${OBSERVATION_FILE}" \
    SKILL_PD_ROLLOUT_REPORT_FILE="${REPORT_OUT}" \
    SKILL_PD_APPEND_REQUIRE_REAL_SAMPLE="${REQUIRE_REAL_SAMPLE}" \
    SKILL_PD_APPEND_DRY_RUN="${APPEND_DRY_RUN}" \
    "${ROLLOUT_APPEND_SCRIPT}" >"${APPEND_OUT}" 2>&1
append_status=$?
set -e
if (( append_status != 0 )); then
  cat "${APPEND_OUT}" >&2 || true
  fail "rollout append failed; see ${APPEND_OUT}; report preserved at ${REPORT_OUT}"
fi

if [[ "${RUN_STATUS}" == "true" ]]; then
  set +e
  env SKILL_PD_OBSERVATION_FILE="${OBSERVATION_FILE}" \
      SKILL_PD_REQUIRED_SAMPLE_DAYS="${SKILL_PD_REQUIRED_SAMPLE_DAYS:-30}" \
      SKILL_PD_MAX_NON_OK_RATE="${SKILL_PD_MAX_NON_OK_RATE:-0.05}" \
      "${ROLLOUT_STATUS_SCRIPT}" >"${STATUS_OUT}" 2>&1
  status_status=$?
  set -e
  if (( status_status != 0 )); then
    cat "${STATUS_OUT}" >&2 || true
    fail "rollout status failed; see ${STATUS_OUT}"
  fi
fi

cat <<EOF_SUMMARY
P25-HIGH-02r rollout daily passed.
Observation file: ${OBSERVATION_FILE}
Output dir: ${OUTPUT_DIR}
Report artifact: ${REPORT_OUT}
Append output: ${APPEND_OUT}
Status output: ${STATUS_OUT}
Append dry run: ${APPEND_DRY_RUN}
Require real sample: ${REQUIRE_REAL_SAMPLE}
EOF_SUMMARY

cat "${APPEND_OUT}"
if [[ "${RUN_STATUS}" == "true" ]]; then
  printf '\n'
  cat "${STATUS_OUT}"
fi
cat <<'EOF_FOOTER'
This daily helper does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE, delete overrideSkillsToSummary, merge branches, or mark rollout complete.
EOF_FOOTER
