#!/usr/bin/env bash
# P25-HIGH-02i: 30-day rollout gate verifier for skill progressive-disclosure.
# Reads rollout observation markdown rows and fails closed unless sampled days,
# smoke results, rollback drill, and metric thresholds are ready for Phase 3.
# Locked columns: Total host tool calls, Manual smoke result,
# Production Prometheus smoke result, Rollback drill result.

set -euo pipefail

OBSERVATION_FILE="${SKILL_PD_OBSERVATION_FILE:-docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-observation.md}"
REQUIRED_SAMPLE_DAYS="${SKILL_PD_REQUIRED_SAMPLE_DAYS:-30}"
MAX_NON_OK_RATE="${SKILL_PD_MAX_NON_OK_RATE:-0.05}"

if [[ ! -f "${OBSERVATION_FILE}" ]]; then
  echo "observation file not found: ${OBSERVATION_FILE}" >&2
  exit 2
fi

python3 - "${OBSERVATION_FILE}" "${REQUIRED_SAMPLE_DAYS}" "${MAX_NON_OK_RATE}" <<'PY_SCRIPT'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
required_sample_days = int(sys.argv[2])
max_non_ok_rate = float(sys.argv[3])

rows = []
for line in path.read_text(encoding="utf-8").splitlines():
    stripped = line.strip()
    if not stripped.startswith("|"):
        continue
    cells = [c.strip() for c in stripped.strip("|").split("|")]
    if len(cells) < 16:
        continue
    date = cells[0]
    if date in {"Date", "YYYY-MM-DD"} or set(date) <= {"-", ":"}:
        continue
    if not re.fullmatch(r"\d{4}-\d{2}-\d{2}", date):
        continue
    rows.append(cells)

def clean(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == "`" and value[-1] == "`":
        value = value[1:-1]
    return value.strip()

def as_int(value: str) -> int:
    value = clean(value)
    try:
        return int(float(value))
    except ValueError as exc:
        raise SystemExit(f"invalid integer cell {value!r} in {path}") from exc

def accepted(notes: str) -> bool:
    lowered = notes.lower()
    return "accepted incident" in lowered or "waived" in lowered

failures = []
sample_days = 0
no_sample_days = 0
total_calls = 0
non_ok_calls = 0
rollback_drill_pass = False

for cells in rows:
    date = clean(cells[0])
    total = as_int(cells[4])
    ok = as_int(cells[5])
    error = as_int(cells[6])
    cwd_missing = as_int(cells[7])
    approval_required = as_int(cells[8])
    enrich_failure = as_int(cells[9])
    manual_smoke = clean(cells[10])
    prometheus_smoke = clean(cells[11])
    rollback_drill = clean(cells[12])
    rollback_trigger = clean(cells[13])
    decision = clean(cells[14])
    notes = clean(cells[15])

    if total <= 0:
        no_sample_days += 1
        continue

    sample_days += 1
    total_calls += total
    explicit_non_ok = error + cwd_missing + approval_required
    inferred_non_ok = max(total - ok, 0)
    row_non_ok = max(explicit_non_ok, inferred_non_ok)
    non_ok_calls += row_non_ok
    row_rate = row_non_ok / total if total else 0.0

    if row_rate > max_non_ok_rate:
        failures.append(f"{date}: non-ok rate {row_rate:.4f} exceeds threshold {max_non_ok_rate:.4f}")
    if cwd_missing > 0 and not accepted(notes):
        failures.append(f"{date}: cwd_missing={cwd_missing} without accepted incident note")
    if approval_required > 0 and not accepted(notes):
        failures.append(f"{date}: approval_required={approval_required} without accepted incident note")
    if enrich_failure > 0 and "protocol-drift fix merged" not in notes.lower() and not accepted(notes):
        failures.append(f"{date}: enrich_failure={enrich_failure} without protocol-drift fix note")
    if manual_smoke != "PASS":
        failures.append(f"{date}: manual smoke is {manual_smoke!r}, want PASS")
    if prometheus_smoke != "PASS":
        failures.append(f"{date}: production Prometheus smoke is {prometheus_smoke!r}, want PASS")
    if rollback_drill == "FAIL":
        failures.append(f"{date}: rollback drill failed")
    if rollback_drill == "PASS":
        rollback_drill_pass = True
    if rollback_trigger != "none" and not accepted(notes):
        failures.append(f"{date}: rollback trigger {rollback_trigger!r} without accepted incident note")
    if decision != "continue":
        failures.append(f"{date}: decision is {decision!r}, want continue")

if sample_days < required_sample_days:
    failures.append(f"sample days {sample_days} < required {required_sample_days}; no-sample days={no_sample_days}")
if sample_days > 0 and total_calls <= 0:
    failures.append("sample rows exist but total host tool calls is 0")
if not rollback_drill_pass:
    failures.append("no sampled row has Rollback drill result PASS")

overall_rate = (non_ok_calls / total_calls) if total_calls else 0.0
if overall_rate > max_non_ok_rate:
    failures.append(f"overall non-ok rate {overall_rate:.4f} exceeds threshold {max_non_ok_rate:.4f}")

if failures:
    print("P25-HIGH-02i rollout gate failed:")
    for failure in failures:
        print(f"- {failure}")
    raise SystemExit(1)

print(
    "P25-HIGH-02i rollout gate passed: "
    f"sample_days={sample_days} total_host_tool_calls={total_calls} "
    f"non_ok_calls={non_ok_calls} non_ok_rate={overall_rate:.4f} "
    f"required_sample_days={required_sample_days} rollback_drill_pass=true"
)
PY_SCRIPT
