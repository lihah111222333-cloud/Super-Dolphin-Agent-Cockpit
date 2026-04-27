#!/usr/bin/env bash
# P25-HIGH-02l: Phase 3 evidence bundle verifier for skill progressive-disclosure.
# Validates that all Phase 3 evidence files were collected into one bundle
# directory before provider default policy formalization or override deletion.

set -euo pipefail

BUNDLE_DIR="${SKILL_PD_EVIDENCE_BUNDLE_DIR:-}"
WRITE_MANIFEST="${SKILL_PD_EVIDENCE_BUNDLE_WRITE_MANIFEST:-false}"

fail() {
  echo "P25-HIGH-02l evidence bundle failed: $*" >&2
  exit 1
}

[[ -n "${BUNDLE_DIR}" ]] || fail "SKILL_PD_EVIDENCE_BUNDLE_DIR is required"
[[ -d "${BUNDLE_DIR}" ]] || fail "evidence bundle dir not found: ${BUNDLE_DIR}"

PRODUCTION_SMOKE_EVIDENCE="${BUNDLE_DIR}/production-smoke-evidence.md"
CLAUDECLI_E2E_EVIDENCE="${BUNDLE_DIR}/claudecli-e2e-evidence.md"
ROLLOUT_OBSERVATION="${BUNDLE_DIR}/rollout-observation.md"
ROLLOUT_GATE_OUTPUT="${BUNDLE_DIR}/rollout-gate-output.txt"
PHASE3_PREFLIGHT_OUTPUT="${BUNDLE_DIR}/phase3-preflight-output.txt"
MANIFEST_PATH="${BUNDLE_DIR}/manifest.md"

require_file() {
  local path="$1"
  local label="$2"
  [[ -f "${path}" ]] || fail "missing ${label}: ${path}"
}

require_contains() {
  local path="$1"
  local needle="$2"
  local label="$3"
  require_file "${path}" "${label}"
  if ! python3 - "${path}" "${needle}" <<'PY_SCRIPT'
import sys
from pathlib import Path
path = Path(sys.argv[1])
needle = sys.argv[2]
body = path.read_text(encoding="utf-8", errors="replace")
raise SystemExit(0 if needle in body else 1)
PY_SCRIPT
  then
    fail "${label} missing token: ${needle}"
  fi
}

require_not_contains() {
  local path="$1"
  local needle="$2"
  local label="$3"
  require_file "${path}" "${label}"
  if ! python3 - "${path}" "${needle}" <<'PY_SCRIPT'
import sys
from pathlib import Path
path = Path(sys.argv[1])
needle = sys.argv[2]
body = path.read_text(encoding="utf-8", errors="replace")
raise SystemExit(1 if needle in body else 0)
PY_SCRIPT
  then
    fail "${label} must not contain token: ${needle}"
  fi
}

require_positive_field() {
  local path="$1"
  local field="$2"
  local label="$3"
  require_file "${path}" "${label}"
  if ! python3 - "${path}" "${field}" <<'PY_SCRIPT'
import re
import sys
from pathlib import Path
path = Path(sys.argv[1])
field = sys.argv[2]
body = path.read_text(encoding="utf-8", errors="replace")
match = re.search(rf"(?im)^\s*{re.escape(field)}\s*:\s*([0-9]+(?:\.[0-9]+)?)\s*$", body)
if not match:
    raise SystemExit(1)
raise SystemExit(0 if float(match.group(1)) > 0 else 1)
PY_SCRIPT
  then
    fail "${label} field must be positive: ${field}"
  fi
}

require_contains "${PRODUCTION_SMOKE_EVIDENCE}" "Evidence type: production-smoke" "production smoke evidence"
require_contains "${PRODUCTION_SMOKE_EVIDENCE}" "P25-HIGH-02g smoke passed." "production smoke evidence"
require_contains "${PRODUCTION_SMOKE_EVIDENCE}" "real traffic is non-zero" "production smoke evidence"
require_positive_field "${PRODUCTION_SMOKE_EVIDENCE}" "Total host tool calls" "production smoke evidence"

require_contains "${CLAUDECLI_E2E_EVIDENCE}" "Evidence type: authenticated-claudecli-e2e" "authenticated Claude CLI E2E evidence"
require_contains "${CLAUDECLI_E2E_EVIDENCE}" "Authenticated environment: true" "authenticated Claude CLI E2E evidence"
require_contains "${CLAUDECLI_E2E_EVIDENCE}" "TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E" "authenticated Claude CLI E2E evidence"
require_contains "${CLAUDECLI_E2E_EVIDENCE}" "PASS" "authenticated Claude CLI E2E evidence"
require_not_contains "${CLAUDECLI_E2E_EVIDENCE}" "SKIP" "authenticated Claude CLI E2E evidence"

require_contains "${ROLLOUT_OBSERVATION}" "| Date | Version / commit | Switch state | Window | Total host tool calls |" "rollout observation"
require_contains "${ROLLOUT_OBSERVATION}" "Production Prometheus smoke result" "rollout observation"
require_contains "${ROLLOUT_OBSERVATION}" "Rollback drill result" "rollout observation"

require_contains "${ROLLOUT_GATE_OUTPUT}" "P25-HIGH-02i rollout gate passed" "rollout gate output"
require_contains "${ROLLOUT_GATE_OUTPUT}" "sample_days=" "rollout gate output"
require_contains "${ROLLOUT_GATE_OUTPUT}" "rollback_drill_pass=true" "rollout gate output"

require_contains "${PHASE3_PREFLIGHT_OUTPUT}" "P25-HIGH-02j phase3 preflight passed." "phase3 preflight output"
require_contains "${PHASE3_PREFLIGHT_OUTPUT}" "30-day rollout gate verifier passed with real samples." "phase3 preflight output"
require_contains "${PHASE3_PREFLIGHT_OUTPUT}" "does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE" "phase3 preflight output"

if [[ "${WRITE_MANIFEST}" == "true" ]]; then
  python3 - "${MANIFEST_PATH}" "${BUNDLE_DIR}" <<'PY_SCRIPT'
import datetime as _dt
import sys
from pathlib import Path
manifest = Path(sys.argv[1])
bundle = Path(sys.argv[2])
files = [
    "production-smoke-evidence.md",
    "claudecli-e2e-evidence.md",
    "rollout-observation.md",
    "rollout-gate-output.txt",
    "phase3-preflight-output.txt",
]
lines = [
    "# Skill Progressive Disclosure Phase 3 Evidence Bundle",
    "",
    "Evidence type: phase3-evidence-bundle",
    f"Generated at: {_dt.datetime.utcnow().isoformat(timespec='seconds')}Z",
    "",
    "## Files",
    "",
]
for name in files:
    lines.append(f"- `{name}`")
lines.extend([
    "",
    "## Gate",
    "",
    "P25-HIGH-02l evidence bundle passed.",
    "This manifest is only an evidence index; it does not enable defaults or delete override code.",
    "",
])
manifest.write_text("\n".join(lines), encoding="utf-8")
PY_SCRIPT
fi

cat <<'EOF'
P25-HIGH-02l evidence bundle passed.
Required files verified:
- production-smoke-evidence.md
- claudecli-e2e-evidence.md
- rollout-observation.md
- rollout-gate-output.txt
- phase3-preflight-output.txt
This check does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE and does not delete overrideSkillsToSummary.
EOF
