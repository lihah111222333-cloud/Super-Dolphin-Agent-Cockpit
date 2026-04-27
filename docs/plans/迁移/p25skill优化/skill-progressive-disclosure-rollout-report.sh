#!/usr/bin/env bash
# P25-HIGH-02h: rollout observation report generator for skill progressive-disclosure.
# Queries Prometheus for the daily counters, optionally runs the production smoke,
# and prints a markdown row ready for skill-progressive-disclosure-rollout-observation.md.

set -euo pipefail

PROMETHEUS_URL="${PROMETHEUS_URL:-http://127.0.0.1:9090}"
QUERY_WINDOW="${SKILL_PD_QUERY_WINDOW:-24h}"
OBS_DATE="${SKILL_PD_DATE:-$(date +%F)}"
SWITCH_STATE="${SKILL_PD_SWITCH_STATE:-false}"
VERSION="${SKILL_PD_VERSION:-}"
MANUAL_SMOKE_RESULT="${SKILL_PD_MANUAL_SMOKE_RESULT:-}"
PROM_SMOKE_RESULT="${SKILL_PD_PROMETHEUS_SMOKE_RESULT:-}"
ROLLBACK_DRILL_RESULT="${SKILL_PD_ROLLBACK_DRILL_RESULT:-SKIP(no release window)}"
ROLLBACK_TRIGGER="${SKILL_PD_ROLLBACK_TRIGGER:-none}"
DECISION="${SKILL_PD_DECISION:-hold}"
NOTES="${SKILL_PD_NOTES:-}"
RUN_ROLLOUT_SMOKE="${SKILL_PD_RUN_ROLLOUT_SMOKE:-false}"

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROLLOUT_SMOKE_SCRIPT="${SKILL_PD_ROLLOUT_SMOKE_SCRIPT:-${SCRIPT_DIR}/skill-progressive-disclosure-rollout-smoke.sh}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 2
  fi
}

resolve_version() {
  if [[ -n "${VERSION}" ]]; then
    return
  fi
  if command -v git >/dev/null 2>&1 && git -C "${SCRIPT_DIR}" rev-parse --show-toplevel >/dev/null 2>&1; then
    VERSION="$(git -C "${SCRIPT_DIR}" rev-parse --short HEAD)"
  else
    VERSION="unknown"
  fi
}

run_query() {
  local query="$1"
  curl --fail --silent --show-error --get --data-urlencode "query=${query}" --max-time 10 "${PROMETHEUS_URL%/}/api/v1/query"
}

extract_value() {
  local payload="$1"
  python3 - "${payload}" <<'PY'
import json
import sys

payload = json.loads(sys.argv[1])
if payload.get("status") != "success":
    raise SystemExit(f"prometheus query failed: {payload}")
result = payload.get("data", {}).get("result", [])
if not result:
    print("0")
    raise SystemExit(0)
value = result[0].get("value", [0, "0"])
raw = value[1] if len(value) > 1 else "0"
try:
    numeric = float(raw)
except ValueError:
    print(raw)
    raise SystemExit(0)
rounded = round(numeric)
if abs(numeric - rounded) < 1e-9:
    print(int(rounded))
else:
    print(f"{numeric:.3f}")
PY
}

query_value() {
  local query="$1"
  local body
  body="$(run_query "$query")"
  extract_value "${body}"
}

join_notes() {
  python3 - "$@" <<'PY'
import sys
parts = [p for p in sys.argv[1:] if p]
print("; ".join(parts))
PY
}

require_cmd curl
require_cmd python3
resolve_version

smoke_output=""
if [[ "${RUN_ROLLOUT_SMOKE}" == "true" ]]; then
  if [[ ! -x "${ROLLOUT_SMOKE_SCRIPT}" ]]; then
    echo "rollout smoke script is not executable: ${ROLLOUT_SMOKE_SCRIPT}" >&2
    exit 2
  fi
  set +e
  smoke_output="$(${ROLLOUT_SMOKE_SCRIPT} 2>&1)"
  smoke_status=$?
  set -e
  if (( smoke_status == 0 )); then
    PROM_SMOKE_RESULT="${PROM_SMOKE_RESULT:-PASS}"
  else
    PROM_SMOKE_RESULT="${PROM_SMOKE_RESULT:-FAIL}"
    DECISION="hold"
  fi
fi

total_calls="$(query_value "round(sum(increase(host_tool_calls_total[${QUERY_WINDOW}])))")"
ok_calls="$(query_value "round(sum(increase(host_tool_calls_total{outcome=\"ok\"}[${QUERY_WINDOW}])))")"
error_calls="$(query_value "round(sum(increase(host_tool_calls_total{outcome=\"error\"}[${QUERY_WINDOW}])))")"
cwd_missing_calls="$(query_value "round(sum(increase(host_tool_calls_total{outcome=\"cwd_missing\"}[${QUERY_WINDOW}])))")"
approval_required_calls="$(query_value "round(sum(increase(host_tool_calls_total{outcome=\"approval_required\"}[${QUERY_WINDOW}])))")"
enrich_failures="$(query_value "round(sum(increase(enrich_failures_total[${QUERY_WINDOW}])))")"
artifact_approval_miss="$(query_value "round(sum(increase(skill_artifact_approval_miss_total[${QUERY_WINDOW}])))")"

if [[ -z "${MANUAL_SMOKE_RESULT}" ]]; then
  if [[ "${total_calls}" == "0" ]]; then
    MANUAL_SMOKE_RESULT="SKIP(no samples)"
  else
    MANUAL_SMOKE_RESULT="TODO(run manual smoke)"
  fi
fi
if [[ -z "${PROM_SMOKE_RESULT}" ]]; then
  PROM_SMOKE_RESULT="SKIP(not applied)"
fi

no_sample_note=""
if [[ "${total_calls}" == "0" ]]; then
  DECISION="hold"
  no_sample_note="无样本 / no samples; gate remains open"
fi
notes="$(join_notes "${no_sample_note}" "artifact_approval_miss=${artifact_approval_miss}" "${NOTES}")"

cat <<'REPORT_EOF'
# P25-HIGH-02h rollout observation report
REPORT_EOF
printf '\n- Prometheus URL: %s\n' "${PROMETHEUS_URL}"
printf -- '- Query window: %s\n' "${QUERY_WINDOW}"
printf -- '- Rollback trigger: %s\n' "${ROLLBACK_TRIGGER}"
printf '%s\n\n' '- Paste the markdown row below into skill-progressive-disclosure-rollout-observation.md.'
printf '%s\n' '| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |'
printf '%s\n' '|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|'
printf '| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | `%s` | `%s` | `%s` | %s | %s | %s |\n' \
  "${OBS_DATE}" "${VERSION}" "${SWITCH_STATE}" "${QUERY_WINDOW}" "${total_calls}" "${ok_calls}" "${error_calls}" "${cwd_missing_calls}" "${approval_required_calls}" "${enrich_failures}" "${MANUAL_SMOKE_RESULT}" "${PROM_SMOKE_RESULT}" "${ROLLBACK_DRILL_RESULT}" "${ROLLBACK_TRIGGER}" "${DECISION}" "${notes}"
printf '\nArtifact approval cache miss: %s\n' "${artifact_approval_miss}"

if [[ -n "${smoke_output}" ]]; then
  cat <<'SMOKE_EOF'

## Production smoke output

```
SMOKE_EOF
  printf '%s\n' "${smoke_output}"
  printf '%s\n' '```'
fi
