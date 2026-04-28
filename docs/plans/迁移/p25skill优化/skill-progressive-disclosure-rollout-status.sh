#!/usr/bin/env bash
# P25-HIGH-02q: summarize rollout observation progress and print next actions.
# This status helper is intentionally read-only. It tells the operator what to do
# next before Phase 3, but it does not enable default policy, edit evidence, or
# merge branches.

set -euo pipefail

OBSERVATION_FILE="${SKILL_PD_OBSERVATION_FILE:-docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-observation.md}"
REQUIRED_SAMPLE_DAYS="${SKILL_PD_REQUIRED_SAMPLE_DAYS:-30}"
MAX_NON_OK_RATE="${SKILL_PD_MAX_NON_OK_RATE:-0.05}"

if [[ ! -f "${OBSERVATION_FILE}" ]]; then
  echo "rollout status failed: observation file not found: ${OBSERVATION_FILE}" >&2
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
for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
    stripped = line.strip()
    if not stripped.startswith("|"):
        continue
    cells = [c.strip() for c in stripped.strip("|").split("|")]
    if len(cells) < 16:
        continue
    date = cells[0].strip().strip("`")
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

sample_days = 0
no_sample_days = 0
total_calls = 0
non_ok_calls = 0
rollback_drill_pass = False
last_sample_date = "none"
blockers = []

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
        if decision != "hold":
            blockers.append(f"{date}: no-sample row decision is {decision!r}, want hold")
        continue

    sample_days += 1
    last_sample_date = date
    total_calls += total
    row_non_ok = max(error + cwd_missing + approval_required, max(total - ok, 0))
    non_ok_calls += row_non_ok
    row_rate = row_non_ok / total if total else 0.0

    if manual_smoke != "PASS":
        blockers.append(f"{date}: manual smoke is {manual_smoke!r}, want PASS")
    if prometheus_smoke != "PASS":
        blockers.append(f"{date}: production Prometheus smoke is {prometheus_smoke!r}, want PASS")
    if rollback_drill == "PASS":
        rollback_drill_pass = True
    if rollback_drill == "FAIL":
        blockers.append(f"{date}: rollback drill failed")
    if decision != "continue":
        blockers.append(f"{date}: decision is {decision!r}, want continue")
    if row_rate > max_non_ok_rate:
        blockers.append(f"{date}: non-ok rate {row_rate:.4f} exceeds threshold {max_non_ok_rate:.4f}")
    if cwd_missing > 0 and not accepted(notes):
        blockers.append(f"{date}: cwd_missing={cwd_missing} without accepted incident note")
    if approval_required > 0 and not accepted(notes):
        blockers.append(f"{date}: approval_required={approval_required} without accepted incident note")
    if enrich_failure > 0 and "protocol-drift fix merged" not in notes.lower() and not accepted(notes):
        blockers.append(f"{date}: enrich_failure={enrich_failure} without protocol-drift fix note")
    if rollback_trigger != "none" and not accepted(notes):
        blockers.append(f"{date}: rollback trigger {rollback_trigger!r} without accepted incident note")

remaining_sample_days = max(required_sample_days - sample_days, 0)
overall_rate = (non_ok_calls / total_calls) if total_calls else 0.0
if sample_days > 0 and overall_rate > max_non_ok_rate:
    blockers.append(f"overall non-ok rate {overall_rate:.4f} exceeds threshold {max_non_ok_rate:.4f}")
if sample_days >= required_sample_days and not rollback_drill_pass:
    blockers.append("required sample days reached but no sampled row has rollback drill PASS")

print("P25-HIGH-02q rollout status:")
print(f"Observation file: {path}")
print(f"sample_days={sample_days}")
print(f"required_sample_days={required_sample_days}")
print(f"remaining_sample_days={remaining_sample_days}")
print(f"no_sample_days={no_sample_days}")
print(f"total_host_tool_calls={total_calls}")
print(f"non_ok_calls={non_ok_calls}")
print(f"non_ok_rate={overall_rate:.4f}")
print(f"rollback_drill_pass={str(rollback_drill_pass).lower()}")
print(f"last_sample_date={last_sample_date}")
print(f"blocker_count={len(blockers)}")
if blockers:
    print("Blockers:")
    for blocker in blockers:
        print(f"- {blocker}")

actions = []
if sample_days == 0:
    actions.append("Apply Prometheus scrape/rule config and run production smoke against real traffic.")
    actions.append("Generate a rollout report, then append it with skill-progressive-disclosure-rollout-append.sh.")
elif remaining_sample_days > 0:
    actions.append(f"Continue daily production smoke/report/append until {remaining_sample_days} more real sample day(s) are collected.")
    actions.append("Keep decision=hold for no-sample rows; no-sample rows do not count toward the gate.")
else:
    actions.append("Run skill-progressive-disclosure-rollout-gate.sh against the observation file.")
    actions.append("Collect production-smoke and authenticated Claude CLI E2E evidence.")
    actions.append("Run skill-progressive-disclosure-phase3-evidence-collect.sh to build the Phase 3 evidence bundle.")
if not rollback_drill_pass:
    actions.append("Schedule and record at least one rollback drill PASS before Phase 3 preflight.")
if blockers:
    actions.append("Resolve blockers above before any Phase 3 default-policy PR.")

print("Next phase actions:")
for idx, action in enumerate(actions, 1):
    print(f"{idx}. {action}")
print("This status helper does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE, delete overrideSkillsToSummary, merge branches, or mark rollout complete.")
PY_SCRIPT
