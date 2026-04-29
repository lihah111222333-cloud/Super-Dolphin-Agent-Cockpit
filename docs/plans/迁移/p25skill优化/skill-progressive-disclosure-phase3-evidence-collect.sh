#!/usr/bin/env bash
# P25-HIGH-02n: Phase 3 evidence bundle collector for skill progressive-disclosure.
# Copies reviewed evidence into the canonical bundle layout, runs rollout gate,
# Phase 3 preflight, and the bundle verifier, then writes a manifest by default.
# This script is intentionally fail-closed and does not enable default policy.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
BUNDLE_OUT_DIR="${SKILL_PD_BUNDLE_OUT_DIR:-}"
PRODUCTION_SMOKE_EVIDENCE="${SKILL_PD_PRODUCTION_SMOKE_EVIDENCE:-}"
CLAUDECLI_E2E_EVIDENCE="${SKILL_PD_CLAUDECLI_E2E_EVIDENCE:-}"
OBSERVATION_FILE="${SKILL_PD_OBSERVATION_FILE:-}"
ROLLOUT_GATE_SCRIPT="${SKILL_PD_ROLLOUT_GATE_SCRIPT:-${SCRIPT_DIR}/skill-progressive-disclosure-rollout-gate.sh}"
PHASE3_PREFLIGHT_SCRIPT="${SKILL_PD_PHASE3_PREFLIGHT_SCRIPT:-${SCRIPT_DIR}/skill-progressive-disclosure-phase3-preflight.sh}"
EVIDENCE_BUNDLE_SCRIPT="${SKILL_PD_EVIDENCE_BUNDLE_SCRIPT:-${SCRIPT_DIR}/skill-progressive-disclosure-phase3-evidence-bundle.sh}"
REQUIRED_SAMPLE_DAYS="${SKILL_PD_REQUIRED_SAMPLE_DAYS:-30}"
MAX_NON_OK_RATE="${SKILL_PD_MAX_NON_OK_RATE:-0.05}"
WRITE_MANIFEST="${SKILL_PD_EVIDENCE_BUNDLE_WRITE_MANIFEST:-true}"
RUN_LOCAL_GO_TESTS="${SKILL_PD_RUN_LOCAL_GO_TESTS:-false}"

fail() {
  echo "P25-HIGH-02n evidence collect failed: $*" >&2
  exit 1
}

require_executable() {
  local path="$1"
  local label="$2"
  [[ -x "${path}" ]] || fail "${label} script is not executable: ${path}"
}

copy_required() {
  local src="$1"
  local dest="$2"
  local label="$3"
  [[ -n "${src}" ]] || fail "${label} evidence path is required"
  [[ -f "${src}" ]] || fail "${label} evidence file not found: ${src}"
  python3 - "${src}" "${dest}" <<'PY_SCRIPT'
import shutil
import sys
from pathlib import Path

src = Path(sys.argv[1]).resolve()
dest = Path(sys.argv[2]).resolve()
dest.parent.mkdir(parents=True, exist_ok=True)
if src != dest:
    shutil.copyfile(src, dest)
PY_SCRIPT
}

run_and_capture() {
  local output_path="$1"
  local label="$2"
  shift 2
  local tmp_output
  tmp_output="$(mktemp)"
  if "$@" >"${tmp_output}" 2>&1; then
    mv "${tmp_output}" "${output_path}"
    return 0
  fi
  mv "${tmp_output}" "${output_path}"
  fail "${label} failed; see ${output_path}"
}

[[ -n "${BUNDLE_OUT_DIR}" ]] || fail "SKILL_PD_BUNDLE_OUT_DIR is required"
mkdir -p "${BUNDLE_OUT_DIR}"
BUNDLE_OUT_DIR="$(cd -- "${BUNDLE_OUT_DIR}" && pwd)"

require_executable "${ROLLOUT_GATE_SCRIPT}" "rollout gate"
require_executable "${PHASE3_PREFLIGHT_SCRIPT}" "Phase 3 preflight"
require_executable "${EVIDENCE_BUNDLE_SCRIPT}" "Phase 3 evidence bundle"

CANONICAL_PRODUCTION_SMOKE="${BUNDLE_OUT_DIR}/production-smoke-evidence.md"
CANONICAL_CLAUDECLI_E2E="${BUNDLE_OUT_DIR}/claudecli-e2e-evidence.md"
CANONICAL_OBSERVATION="${BUNDLE_OUT_DIR}/rollout-observation.md"
ROLLOUT_GATE_OUTPUT="${BUNDLE_OUT_DIR}/rollout-gate-output.txt"
PHASE3_PREFLIGHT_OUTPUT="${BUNDLE_OUT_DIR}/phase3-preflight-output.txt"
EVIDENCE_BUNDLE_OUTPUT="${BUNDLE_OUT_DIR}/evidence-bundle-output.txt"
MANIFEST_PATH="${BUNDLE_OUT_DIR}/manifest.md"

# Avoid stale green evidence when reusing a bundle directory after a fail-closed run.
rm -f "${ROLLOUT_GATE_OUTPUT}" "${PHASE3_PREFLIGHT_OUTPUT}" "${EVIDENCE_BUNDLE_OUTPUT}" "${MANIFEST_PATH}"

copy_required "${PRODUCTION_SMOKE_EVIDENCE}" "${CANONICAL_PRODUCTION_SMOKE}" "production smoke"
copy_required "${CLAUDECLI_E2E_EVIDENCE}" "${CANONICAL_CLAUDECLI_E2E}" "authenticated Claude CLI E2E"
copy_required "${OBSERVATION_FILE}" "${CANONICAL_OBSERVATION}" "rollout observation"

run_and_capture "${ROLLOUT_GATE_OUTPUT}" "rollout gate" \
  env SKILL_PD_OBSERVATION_FILE="${CANONICAL_OBSERVATION}" \
      SKILL_PD_REQUIRED_SAMPLE_DAYS="${REQUIRED_SAMPLE_DAYS}" \
      SKILL_PD_MAX_NON_OK_RATE="${MAX_NON_OK_RATE}" \
      "${ROLLOUT_GATE_SCRIPT}"

run_and_capture "${PHASE3_PREFLIGHT_OUTPUT}" "Phase 3 preflight" \
  env SKILL_PD_ROLLOUT_GATE_SCRIPT="${ROLLOUT_GATE_SCRIPT}" \
      SKILL_PD_OBSERVATION_FILE="${CANONICAL_OBSERVATION}" \
      SKILL_PD_REQUIRED_SAMPLE_DAYS="${REQUIRED_SAMPLE_DAYS}" \
      SKILL_PD_MAX_NON_OK_RATE="${MAX_NON_OK_RATE}" \
      SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="${CANONICAL_PRODUCTION_SMOKE}" \
      SKILL_PD_CLAUDECLI_E2E_EVIDENCE="${CANONICAL_CLAUDECLI_E2E}" \
      SKILL_PD_RUN_LOCAL_GO_TESTS="${RUN_LOCAL_GO_TESTS}" \
      "${PHASE3_PREFLIGHT_SCRIPT}"

run_and_capture "${EVIDENCE_BUNDLE_OUTPUT}" "Phase 3 evidence bundle verifier" \
  env SKILL_PD_EVIDENCE_BUNDLE_DIR="${BUNDLE_OUT_DIR}" \
      SKILL_PD_EVIDENCE_BUNDLE_WRITE_MANIFEST="${WRITE_MANIFEST}" \
      "${EVIDENCE_BUNDLE_SCRIPT}"

cat "${EVIDENCE_BUNDLE_OUTPUT}"
cat <<EOF_SUMMARY
P25-HIGH-02n evidence collect passed.
Bundle dir: ${BUNDLE_OUT_DIR}
Generated files:
- production-smoke-evidence.md
- claudecli-e2e-evidence.md
- rollout-observation.md
- rollout-gate-output.txt
- phase3-preflight-output.txt
- evidence-bundle-output.txt
- manifest.md (when SKILL_PD_EVIDENCE_BUNDLE_WRITE_MANIFEST=true)
This collector does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE and does not delete overrideSkillsToSummary.
EOF_SUMMARY
