#!/usr/bin/env bash
# P25-HIGH-02g: production apply smoke for skill progressive-disclosure rollout.
# This script verifies that the already-created metrics endpoint, Prometheus
# scrape target, alert rules, and Alertmanager route are actually live before the
# 30-day observation window starts.

set -euo pipefail

METRICS_URL="${SUPER_DOLPHIN_METRICS_URL:-http://127.0.0.1:4511/metrics}"
PROMETHEUS_URL="${PROMETHEUS_URL:-http://127.0.0.1:9090}"
ALERTMANAGER_URL="${ALERTMANAGER_URL:-http://127.0.0.1:9093}"
JOB_NAME="${SKILL_PD_PROMETHEUS_JOB:-super-dolphin-skill-progressive-disclosure}"
REQUIRED_ALERTS=(
  SkillHostToolHighErrorRate
  SkillHostToolCWDMissing
  SkillHostToolApprovalRequiredStuck
  SkillToolEnrichFailures
)

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 2
  fi
}

fetch() {
  local url="$1"
  curl --fail --silent --show-error --max-time 5 "$url"
}

assert_contains() {
  local haystack="$1"
  local needle="$2"
  local label="$3"
  if [[ "$haystack" != *"$needle"* ]]; then
    echo "${label}: missing ${needle}" >&2
    exit 1
  fi
}

require_cmd curl

echo "[1/4] Checking Super-Dolphin metrics endpoint: ${METRICS_URL}"
metrics_body="$(fetch "${METRICS_URL}")"
assert_contains "${metrics_body}" "host_tool_calls_total" "metrics endpoint"
assert_contains "${metrics_body}" "enrich_failures_total" "metrics endpoint"

echo "[2/4] Checking Prometheus target is UP: ${PROMETHEUS_URL} job=${JOB_NAME}"
targets_body="$(fetch "${PROMETHEUS_URL%/}/api/v1/targets?state=active")"
assert_contains "${targets_body}" "\"job\":\"${JOB_NAME}\"" "Prometheus targets"
assert_contains "${targets_body}" "\"health\":\"up\"" "Prometheus targets"

echo "[3/4] Checking Prometheus alert rules are loaded"
rules_body="$(fetch "${PROMETHEUS_URL%/}/api/v1/rules")"
for alert_name in "${REQUIRED_ALERTS[@]}"; do
  assert_contains "${rules_body}" "${alert_name}" "Prometheus rules"
done

echo "[4/4] Checking Alertmanager readiness: ${ALERTMANAGER_URL}"
if ! curl --fail --silent --show-error --max-time 5 "${ALERTMANAGER_URL%/}/-/ready" >/dev/null; then
  echo "Alertmanager /-/ready failed; trying /api/v2/status" >&2
  fetch "${ALERTMANAGER_URL%/}/api/v2/status" >/dev/null
fi

cat <<'EOF'
P25-HIGH-02g smoke passed.
You may start or continue the 30-day rollout observation window only if this
result is attached to the observation row and real traffic is non-zero.
EOF
