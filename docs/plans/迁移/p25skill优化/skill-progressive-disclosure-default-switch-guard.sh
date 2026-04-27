#!/usr/bin/env bash
# P25-HIGH-02m: PR-6 default-switch static guard for skill progressive-disclosure.
# Fails closed if this observability PR accidentally enables default discovery,
# deletes codexapp overrideSkillsToSummary, or removes Phase 3 evidence gates/helpers.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${SKILL_PD_REPO_ROOT:-$(cd -- "${SCRIPT_DIR}/../../../.." && pwd)}"

fail() {
  echo "P25-HIGH-02m default-switch guard failed: $*" >&2
  exit 1
}

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

require_count() {
  local path="$1"
  local needle="$2"
  local expected="$3"
  local label="$4"
  require_file "${path}" "${label}"
  if ! python3 - "${path}" "${needle}" "${expected}" <<'PY_SCRIPT'
import sys
from pathlib import Path
path = Path(sys.argv[1])
needle = sys.argv[2]
expected = int(sys.argv[3])
body = path.read_text(encoding="utf-8", errors="replace")
actual = body.count(needle)
if actual != expected:
    print(f"count={actual}, expected={expected}", file=sys.stderr)
    raise SystemExit(1)
PY_SCRIPT
  then
    fail "${label} expected ${expected} occurrence(s) of token: ${needle}"
  fi
}

PROMPT_CONFIG="${REPO_ROOT}/internal/module/prompt/config.go"
PROMPT_DISCOVERY_TEST="${REPO_ROOT}/internal/module/prompt/skill_catalog_fx_test.go"
CODEXAPP_OVERRIDE="${REPO_ROOT}/internal/provider/codexapp/skill_mode_override.go"
CODEXAPP_OVERRIDE_TEST="${REPO_ROOT}/internal/provider/codexapp/skill_mode_override_test.go"
CODEXAPP_SESSION_TURN="${REPO_ROOT}/internal/provider/codexapp/session_turn.go"
P25_DOC="${REPO_ROOT}/docs/plans/迁移/p25skill优化/p25skill优化.md"
PHASE3_PREFLIGHT="${REPO_ROOT}/docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-preflight.sh"
PHASE3_BUNDLE="${REPO_ROOT}/docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-bundle.sh"
PHASE3_COLLECTOR="${REPO_ROOT}/docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-collect.sh"

require_contains "${PROMPT_CONFIG}" "EnableSkillProgressiveDisclosure: parseBoolEnv(envEnableSkillProgressiveDisclosure, false)" "prompt config default-disabled policy"
require_not_contains "${PROMPT_CONFIG}" "EnableSkillProgressiveDisclosure: parseBoolEnv(envEnableSkillProgressiveDisclosure, true)" "prompt config default-disabled policy"
require_contains "${PROMPT_DISCOVERY_TEST}" "TestSkillProgressiveDisclosure_DefaultDisabled" "prompt default-disabled smoke test"

require_contains "${CODEXAPP_OVERRIDE}" "func overrideSkillsToSummary" "codexapp temporary Summary override"
require_contains "${CODEXAPP_OVERRIDE}" "Phase 2" "codexapp temporary Summary override"
require_contains "${CODEXAPP_OVERRIDE_TEST}" "TestOverrideSkillsToSummary_FlipsUnspecifiedToSummary" "codexapp override tests"
require_contains "${CODEXAPP_OVERRIDE_TEST}" "TestOverrideSkillsToSummary_PreservesExplicitFull" "codexapp override tests"
require_count "${CODEXAPP_SESSION_TURN}" "overrideSkillsToSummary(req.Skills)" 2 "codexapp override call sites"

require_contains "${PHASE3_PREFLIGHT}" "P25-HIGH-02j phase3 preflight passed." "Phase 3 preflight gate"
require_contains "${PHASE3_PREFLIGHT}" "does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE" "Phase 3 preflight gate"
require_contains "${PHASE3_BUNDLE}" "P25-HIGH-02l evidence bundle passed." "Phase 3 evidence bundle gate"
require_contains "${PHASE3_BUNDLE}" "does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE" "Phase 3 evidence bundle gate"
require_contains "${PHASE3_COLLECTOR}" "P25-HIGH-02n evidence collect passed." "Phase 3 evidence collector"
require_contains "${PHASE3_COLLECTOR}" "does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE" "Phase 3 evidence collector"
require_contains "${P25_DOC}" "PR-6 混入 Phase 3 default policy 删除 override" "P25 red-flag documentation"

cat <<'EOF'
P25-HIGH-02m default-switch guard passed.
Current PR remains observability-only:
- ENABLE_SKILL_PROGRESSIVE_DISCLOSURE default is still false.
- codexapp overrideSkillsToSummary and its two session_turn call sites still exist.
- Phase 3 preflight, evidence bundle, and evidence collector gates/helpers are present before any default policy / override deletion.
EOF
