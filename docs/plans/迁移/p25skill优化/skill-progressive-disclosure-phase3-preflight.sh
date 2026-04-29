#!/usr/bin/env bash
# P25-HIGH-02j: Phase 3 default-policy preflight gate for skill progressive-disclosure.
# This script is intentionally fail-closed: it does not enable defaults by itself,
# and it requires rollout gate + production smoke + authenticated Claude CLI E2E
# evidence before a PR may formalize provider default policy or delete override code.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../../../.." && pwd)"
ROLLOUT_GATE_SCRIPT="${SKILL_PD_ROLLOUT_GATE_SCRIPT:-${SCRIPT_DIR}/skill-progressive-disclosure-rollout-gate.sh}"
OBSERVATION_FILE="${SKILL_PD_OBSERVATION_FILE:-${SCRIPT_DIR}/skill-progressive-disclosure-rollout-observation.md}"
REQUIRED_SAMPLE_DAYS="${SKILL_PD_REQUIRED_SAMPLE_DAYS:-30}"
MAX_NON_OK_RATE="${SKILL_PD_MAX_NON_OK_RATE:-0.05}"
PRODUCTION_SMOKE_EVIDENCE="${SKILL_PD_PRODUCTION_SMOKE_EVIDENCE:-}"
CLAUDECLI_E2E_EVIDENCE="${SKILL_PD_CLAUDECLI_E2E_EVIDENCE:-}"
RUN_LOCAL_GO_TESTS="${SKILL_PD_RUN_LOCAL_GO_TESTS:-false}"

fail() {
  echo "P25-HIGH-02j phase3 preflight failed: $*" >&2
  exit 1
}

require_file_contains() {
  local path="$1"
  local needle="$2"
  local label="$3"
  [[ -n "${path}" ]] || fail "${label} evidence path is required"
  [[ -f "${path}" ]] || fail "${label} evidence file not found: ${path}"
  if ! python3 - "${path}" "${needle}" <<'PY_SCRIPT'
import sys
from pathlib import Path
path = Path(sys.argv[1])
needle = sys.argv[2]
body = path.read_text(encoding="utf-8", errors="replace")
raise SystemExit(0 if needle in body else 1)
PY_SCRIPT
  then
    fail "${label} evidence missing token: ${needle}"
  fi
}

require_file_not_contains() {
  local path="$1"
  local needle="$2"
  local label="$3"
  [[ -n "${path}" ]] || fail "${label} evidence path is required"
  [[ -f "${path}" ]] || fail "${label} evidence file not found: ${path}"
  if ! python3 - "${path}" "${needle}" <<'PY_SCRIPT'
import sys
from pathlib import Path
path = Path(sys.argv[1])
needle = sys.argv[2]
body = path.read_text(encoding="utf-8", errors="replace")
raise SystemExit(1 if needle in body else 0)
PY_SCRIPT
  then
    fail "${label} evidence must not contain token: ${needle}"
  fi
}

require_positive_field() {
  local path="$1"
  local field="$2"
  local label="$3"
  [[ -n "${path}" ]] || fail "${label} evidence path is required"
  [[ -f "${path}" ]] || fail "${label} evidence file not found: ${path}"
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
    fail "${label} evidence field must be positive: ${field}"
  fi
}

[[ -x "${ROLLOUT_GATE_SCRIPT}" ]] || fail "rollout gate script is not executable: ${ROLLOUT_GATE_SCRIPT}"
[[ -f "${OBSERVATION_FILE}" ]] || fail "observation file not found: ${OBSERVATION_FILE}"

require_file_contains "${PRODUCTION_SMOKE_EVIDENCE}" "Evidence type: production-smoke" "production smoke"
require_file_contains "${PRODUCTION_SMOKE_EVIDENCE}" "P25-HIGH-02g smoke passed." "production smoke"
require_file_contains "${PRODUCTION_SMOKE_EVIDENCE}" "real traffic is non-zero" "production smoke"
require_positive_field "${PRODUCTION_SMOKE_EVIDENCE}" "Total host tool calls" "production smoke"
require_file_contains "${CLAUDECLI_E2E_EVIDENCE}" "Evidence type: authenticated-claudecli-e2e" "authenticated Claude CLI E2E"
require_file_contains "${CLAUDECLI_E2E_EVIDENCE}" "Authenticated environment: true" "authenticated Claude CLI E2E"
require_file_contains "${CLAUDECLI_E2E_EVIDENCE}" "TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E" "authenticated Claude CLI E2E"
require_file_contains "${CLAUDECLI_E2E_EVIDENCE}" "PASS" "authenticated Claude CLI E2E"
require_file_not_contains "${CLAUDECLI_E2E_EVIDENCE}" "SKIP" "authenticated Claude CLI E2E"

SKILL_PD_OBSERVATION_FILE="${OBSERVATION_FILE}" \
SKILL_PD_REQUIRED_SAMPLE_DAYS="${REQUIRED_SAMPLE_DAYS}" \
SKILL_PD_MAX_NON_OK_RATE="${MAX_NON_OK_RATE}" \
  "${ROLLOUT_GATE_SCRIPT}"

if [[ "${RUN_LOCAL_GO_TESTS}" == "true" ]]; then
  (
    cd "${REPO_ROOT}"
    go test ./internal/provider/codexapp -run 'Test(OverrideSkillsToSummary|BuildTurnStartParams|BuildTurnSteerParams)' -count=1
    go test ./internal/module/prompt -run 'Test(SkillProgressiveDisclosure|SkillCatalogProvider)' -count=1
    go test ./internal/platform/toolbridge -run 'Test.*(Observability|Metrics|ListToolsForCodex|CallHostTool)' -count=1
  )
fi

cat <<'EOF'
P25-HIGH-02j phase3 preflight passed.
Evidence gates satisfied:
- 30-day rollout gate verifier passed with real samples.
- Production smoke evidence is attached, typed as production-smoke, and has positive Total host tool calls.
- Authenticated Claude CLI E2E evidence is typed, authenticated, non-SKIP, and contains PASS for TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E.
This script does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE and does not delete overrideSkillsToSummary.
EOF
