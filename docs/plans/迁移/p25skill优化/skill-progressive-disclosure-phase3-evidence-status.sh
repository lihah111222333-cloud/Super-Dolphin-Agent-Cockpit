#!/usr/bin/env bash
# P25-HIGH-02u: read-only Phase 3 evidence readiness status helper.
# It summarizes rollout gate readiness plus production-smoke / authenticated
# Claude CLI E2E evidence presence before the collector is run. It does not
# write evidence, enable defaults, delete overrides, or merge branches.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
OBSERVATION_FILE="${SKILL_PD_OBSERVATION_FILE:-${SCRIPT_DIR}/skill-progressive-disclosure-rollout-observation.md}"
PRODUCTION_SMOKE_EVIDENCE="${SKILL_PD_PRODUCTION_SMOKE_EVIDENCE:-}"
CLAUDECLI_E2E_EVIDENCE="${SKILL_PD_CLAUDECLI_E2E_EVIDENCE:-}"
REQUIRED_SAMPLE_DAYS="${SKILL_PD_REQUIRED_SAMPLE_DAYS:-30}"
MAX_NON_OK_RATE="${SKILL_PD_MAX_NON_OK_RATE:-0.05}"
FAIL_ON_BLOCKERS="${SKILL_PD_PHASE3_STATUS_FAIL_ON_BLOCKERS:-false}"

python3 - "${OBSERVATION_FILE}" "${PRODUCTION_SMOKE_EVIDENCE}" "${CLAUDECLI_E2E_EVIDENCE}" "${REQUIRED_SAMPLE_DAYS}" "${MAX_NON_OK_RATE}" "${FAIL_ON_BLOCKERS}" <<'PY_SCRIPT'
import re
import sys
from pathlib import Path

observation_path = Path(sys.argv[1])
production_path_arg = sys.argv[2]
claude_path_arg = sys.argv[3]
required_sample_days = int(sys.argv[4])
max_non_ok_rate = float(sys.argv[5])
fail_on_blockers = sys.argv[6].lower() == "true"

blockers = []
observation_blockers = []


def add_blocker(message: str, *, observation: bool = False) -> None:
    blockers.append(message)
    if observation:
        observation_blockers.append(message)


def clean(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == "`" and value[-1] == "`":
        value = value[1:-1]
    return value.strip()


def as_int(value: str, label: str) -> int:
    value = clean(value)
    try:
        return int(float(value))
    except ValueError as exc:
        raise SystemExit(f"invalid integer cell {label}={value!r} in {observation_path}") from exc


def accepted(notes: str) -> bool:
    lowered = notes.lower()
    return "accepted incident" in lowered or "waived" in lowered


sample_days = 0
no_sample_days = 0
total_calls = 0
non_ok_calls = 0
rollback_drill_pass = False
last_sample_date = "none"
rows = []

if not observation_path.is_file():
    add_blocker(f"rollout observation file not found: {observation_path}", observation=True)
else:
    for line in observation_path.read_text(encoding="utf-8", errors="replace").splitlines():
        stripped = line.strip()
        if not stripped.startswith("|"):
            continue
        cells = [c.strip() for c in stripped.strip("|").split("|")]
        if len(cells) < 16:
            continue
        date = clean(cells[0])
        if date in {"Date", "YYYY-MM-DD"} or set(date) <= {"-", ":"}:
            continue
        if not re.fullmatch(r"\d{4}-\d{2}-\d{2}", date):
            continue
        rows.append(cells)

    if not rows:
        add_blocker("rollout observation has no dated rows", observation=True)

for cells in rows:
    date = clean(cells[0])
    total = as_int(cells[4], "Total host tool calls")
    ok = as_int(cells[5], "ok")
    error = as_int(cells[6], "error")
    cwd_missing = as_int(cells[7], "cwd_missing")
    approval_required = as_int(cells[8], "approval_required")
    enrich_failure = as_int(cells[9], "enrich_failure")
    manual_smoke = clean(cells[10])
    prometheus_smoke = clean(cells[11])
    rollback_drill = clean(cells[12])
    rollback_trigger = clean(cells[13])
    decision = clean(cells[14])
    notes = clean(cells[15])

    if total <= 0:
        no_sample_days += 1
        if decision != "hold":
            add_blocker(f"{date}: no-sample row decision is {decision!r}, want hold", observation=True)
        continue

    sample_days += 1
    last_sample_date = date
    total_calls += total
    row_non_ok = max(error + cwd_missing + approval_required, max(total - ok, 0))
    non_ok_calls += row_non_ok
    row_rate = row_non_ok / total if total else 0.0

    if manual_smoke != "PASS":
        add_blocker(f"{date}: manual smoke is {manual_smoke!r}, want PASS", observation=True)
    if prometheus_smoke != "PASS":
        add_blocker(f"{date}: production Prometheus smoke is {prometheus_smoke!r}, want PASS", observation=True)
    if rollback_drill == "PASS":
        rollback_drill_pass = True
    if rollback_drill == "FAIL":
        add_blocker(f"{date}: rollback drill failed", observation=True)
    if decision != "continue":
        add_blocker(f"{date}: decision is {decision!r}, want continue", observation=True)
    if row_rate > max_non_ok_rate:
        add_blocker(f"{date}: non-ok rate {row_rate:.4f} exceeds threshold {max_non_ok_rate:.4f}", observation=True)
    if cwd_missing > 0 and not accepted(notes):
        add_blocker(f"{date}: cwd_missing={cwd_missing} without accepted incident note", observation=True)
    if approval_required > 0 and not accepted(notes):
        add_blocker(f"{date}: approval_required={approval_required} without accepted incident note", observation=True)
    if enrich_failure > 0 and "protocol-drift fix merged" not in notes.lower() and not accepted(notes):
        add_blocker(f"{date}: enrich_failure={enrich_failure} without protocol-drift fix note", observation=True)
    if rollback_trigger != "none" and not accepted(notes):
        add_blocker(f"{date}: rollback trigger {rollback_trigger!r} without accepted incident note", observation=True)

remaining_sample_days = max(required_sample_days - sample_days, 0)
overall_rate = (non_ok_calls / total_calls) if total_calls else 0.0
if sample_days < required_sample_days:
    add_blocker(f"sample days {sample_days} < required {required_sample_days}", observation=True)
if sample_days > 0 and overall_rate > max_non_ok_rate:
    add_blocker(f"overall non-ok rate {overall_rate:.4f} exceeds threshold {max_non_ok_rate:.4f}", observation=True)
if sample_days >= required_sample_days and not rollback_drill_pass:
    add_blocker("required sample days reached but no sampled row has rollback drill PASS", observation=True)
if total_calls <= 0:
    add_blocker("total host tool calls across sampled rows must be positive", observation=True)


def read_evidence(path_arg: str, label: str):
    if not path_arg:
        add_blocker(f"{label} evidence path is required")
        return None
    path = Path(path_arg)
    if not path.is_file():
        add_blocker(f"{label} evidence file not found: {path}")
        return None
    return path.read_text(encoding="utf-8", errors="replace")


def require_contains(body: str, needle: str, label: str) -> None:
    if needle not in body:
        add_blocker(f"{label} evidence missing token: {needle}")


def require_not_contains(body: str, needle: str, label: str) -> None:
    if needle in body:
        add_blocker(f"{label} evidence must not contain token: {needle}")


def require_positive_field(body: str, field: str, label: str) -> None:
    match = re.search(rf"(?im)^\s*{re.escape(field)}\s*:\s*([0-9]+(?:\.[0-9]+)?)\s*$", body)
    if not match or float(match.group(1)) <= 0:
        add_blocker(f"{label} evidence field must be positive: {field}")

before_production_blockers = len(blockers)
production_body = read_evidence(production_path_arg, "production smoke")
if production_body is not None:
    require_contains(production_body, "Evidence type: production-smoke", "production smoke")
    require_contains(production_body, "P25-HIGH-02g smoke passed.", "production smoke")
    require_contains(production_body, "real traffic is non-zero", "production smoke")
    require_positive_field(production_body, "Total host tool calls", "production smoke")
    require_not_contains(production_body, "TODO", "production smoke")
production_ready = len(blockers) == before_production_blockers

before_claude_blockers = len(blockers)
claude_body = read_evidence(claude_path_arg, "authenticated Claude CLI E2E")
if claude_body is not None:
    require_contains(claude_body, "Evidence type: authenticated-claudecli-e2e", "authenticated Claude CLI E2E")
    require_contains(claude_body, "Authenticated environment: true", "authenticated Claude CLI E2E")
    require_contains(claude_body, "TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E", "authenticated Claude CLI E2E")
    require_contains(claude_body, "PASS", "authenticated Claude CLI E2E")
    require_not_contains(claude_body, "SKIP", "authenticated Claude CLI E2E")
    require_not_contains(claude_body, "TODO", "authenticated Claude CLI E2E")
claude_ready = len(blockers) == before_claude_blockers

rollout_gate_ready = len(observation_blockers) == 0
phase3_collect_ready = rollout_gate_ready and production_ready and claude_ready and not blockers

print("P25-HIGH-02u phase3 evidence status:")
print(f"Observation file: {observation_path}")
print(f"sample_days={sample_days}")
print(f"required_sample_days={required_sample_days}")
print(f"remaining_sample_days={remaining_sample_days}")
print(f"no_sample_days={no_sample_days}")
print(f"total_host_tool_calls={total_calls}")
print(f"non_ok_rate={overall_rate:.4f}")
print(f"rollback_drill_pass={str(rollback_drill_pass).lower()}")
print(f"last_sample_date={last_sample_date}")
print(f"rollout_gate_ready={str(rollout_gate_ready).lower()}")
print(f"production_smoke_evidence_ready={str(production_ready).lower()}")
print(f"claudecli_e2e_evidence_ready={str(claude_ready).lower()}")
print(f"phase3_collect_ready={str(phase3_collect_ready).lower()}")
print(f"blocker_count={len(blockers)}")
if blockers:
    print("Blockers:")
    for blocker in blockers:
        print(f"- {blocker}")

print("Next phase actions:")
actions = []
if not rollout_gate_ready:
    if remaining_sample_days > 0:
        actions.append("Continue daily production smoke/report/append until the required real sample days are collected.")
    actions.append("Run skill-progressive-disclosure-rollout-status.sh and resolve rollout blockers before Phase 3 collect.")
if not production_ready:
    actions.append("Generate production-smoke evidence with skill-progressive-disclosure-production-smoke-evidence-generate.sh from a real PASS rollout report.")
if not claude_ready:
    actions.append("Generate authenticated Claude CLI E2E evidence with skill-progressive-disclosure-claudecli-e2e-evidence-generate.sh from a real authenticated PASS output.")
if phase3_collect_ready:
    actions.append("Run skill-progressive-disclosure-phase3-evidence-collect.sh to create the canonical Phase 3 evidence bundle.")
    actions.append("After collector/preflight pass, open a separate Phase 3 policy PR for default formalization if owner approves.")
else:
    actions.append("Do not open a Phase 3 default-policy PR until blocker_count=0 and phase3_collect_ready=true.")
for idx, action in enumerate(actions, 1):
    print(f"{idx}. {action}")
print("This status helper is read-only and does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE, delete overrideSkillsToSummary, merge branches, or mark rollout complete.")

if fail_on_blockers and blockers:
    raise SystemExit(1)
PY_SCRIPT
