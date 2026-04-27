#!/usr/bin/env bash
# P25-HIGH-02o: one-command PR-6 verification wrapper for skill progressive-disclosure.
# Runs script syntax checks, the default-switch guard, focused regression tests,
# and git diff whitespace checks. It does not enable default policy or delete overrides.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${SKILL_PD_REPO_ROOT:-$(cd -- "${SCRIPT_DIR}/../../../.." && pwd)}"
SKIP_GO_TESTS="${SKILL_PD_PR6_VERIFY_SKIP_GO_TESTS:-false}"
SKIP_GIT_DIFF_CHECK="${SKILL_PD_PR6_VERIFY_SKIP_GIT_DIFF_CHECK:-false}"

fail() {
  echo "P25-HIGH-02o PR-6 verification failed: $*" >&2
  exit 1
}

run_cmd() {
  echo "+ $*"
  "$@"
}

require_script() {
  local path="$1"
  [[ -f "${path}" ]] || fail "missing script: ${path}"
  [[ -x "${path}" ]] || fail "script is not executable: ${path}"
}

SCRIPTS=(
  "${SCRIPT_DIR}/skill-progressive-disclosure-rollout-smoke.sh"
  "${SCRIPT_DIR}/skill-progressive-disclosure-rollout-report.sh"
  "${SCRIPT_DIR}/skill-progressive-disclosure-rollout-gate.sh"
  "${SCRIPT_DIR}/skill-progressive-disclosure-phase3-preflight.sh"
  "${SCRIPT_DIR}/skill-progressive-disclosure-phase3-evidence-bundle.sh"
  "${SCRIPT_DIR}/skill-progressive-disclosure-phase3-evidence-collect.sh"
  "${SCRIPT_DIR}/skill-progressive-disclosure-default-switch-guard.sh"
)

for script in "${SCRIPTS[@]}"; do
  require_script "${script}"
  run_cmd bash -n "${script}"
done

run_cmd "${SCRIPT_DIR}/skill-progressive-disclosure-default-switch-guard.sh"

if [[ "${SKIP_GO_TESTS}" != "true" ]]; then
  (
    cd "${REPO_ROOT}"
    run_cmd go test ./pkg/skillmetrics ./internal/platform/metrics -count=1
    run_cmd go test ./internal/provider/codexapp -run 'Test(OverrideSkillsToSummary|BuildTurnStartParams|BuildTurnSteerParams)' -count=1
    run_cmd go test ./internal/platform/toolbridge -run 'Test.*(Observability|Metrics|ListToolsForCodex|CallHostTool)' -count=1
    run_cmd go test ./internal/module/prompt -run 'Test(SkillProgressiveDisclosure|SkillCatalogProvider)' -count=1
    run_cmd go test ./internal/module/turn -run TestApplyHydration_UntrustedSummary -count=1
    run_cmd go test ./internal/ui/wails -run TestHTTPAssetRoutesExposePrometheusMetricsEndpoint -count=1
  )
else
  echo "+ SKIP go test commands (SKILL_PD_PR6_VERIFY_SKIP_GO_TESTS=true)"
fi

if [[ "${SKIP_GIT_DIFF_CHECK}" != "true" ]]; then
  (
    cd "${REPO_ROOT}"
    run_cmd git diff --check
  )
else
  echo "+ SKIP git diff --check (SKILL_PD_PR6_VERIFY_SKIP_GIT_DIFF_CHECK=true)"
fi

cat <<'EOF_SUMMARY'
P25-HIGH-02o PR-6 verification passed.
Verified:
- rollout / smoke / report / gate / preflight / evidence bundle / evidence collect / default-switch scripts parse with bash -n and are executable.
- default-switch guard confirms ENABLE_SKILL_PROGRESSIVE_DISCLOSURE stays default false and overrideSkillsToSummary remains present.
- focused PR-6 regression tests passed unless explicitly skipped by SKILL_PD_PR6_VERIFY_SKIP_GO_TESTS=true.
- git diff --check passed unless explicitly skipped by SKILL_PD_PR6_VERIFY_SKIP_GIT_DIFF_CHECK=true.
This verifier does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE and does not delete overrideSkillsToSummary.
EOF_SUMMARY
