#!/usr/bin/env bash
# P25-HIGH-02p: append one rollout observation row from a generated report.
# This helper turns the P25-HIGH-02h report output into an auditable daily row
# update. It fails closed on duplicate dates, TODO rows, no-sample rows marked as
# continue, and incomplete PASS claims. It does not enable default policy.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
OBSERVATION_FILE="${SKILL_PD_OBSERVATION_FILE:-${SCRIPT_DIR}/skill-progressive-disclosure-rollout-observation.md}"
ROLLOUT_REPORT_SCRIPT="${SKILL_PD_ROLLOUT_REPORT_SCRIPT:-${SCRIPT_DIR}/skill-progressive-disclosure-rollout-report.sh}"
ROLLOUT_REPORT_FILE="${SKILL_PD_ROLLOUT_REPORT_FILE:-}"
DRY_RUN="${SKILL_PD_APPEND_DRY_RUN:-false}"
REQUIRE_REAL_SAMPLE="${SKILL_PD_APPEND_REQUIRE_REAL_SAMPLE:-false}"

fail() {
  echo "P25-HIGH-02p rollout append failed: $*" >&2
  exit 1
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    fail "missing required command: $1"
  fi
}

require_cmd python3
[[ -f "${OBSERVATION_FILE}" ]] || fail "observation file not found: ${OBSERVATION_FILE}"

report_tmp="$(mktemp)"
trap 'rm -f "${report_tmp}"' EXIT

if [[ -n "${ROLLOUT_REPORT_FILE}" ]]; then
  [[ -f "${ROLLOUT_REPORT_FILE}" ]] || fail "rollout report file not found: ${ROLLOUT_REPORT_FILE}"
  cp "${ROLLOUT_REPORT_FILE}" "${report_tmp}"
else
  [[ -x "${ROLLOUT_REPORT_SCRIPT}" ]] || fail "rollout report script is not executable: ${ROLLOUT_REPORT_SCRIPT}"
  "${ROLLOUT_REPORT_SCRIPT}" >"${report_tmp}"
fi

python3 - "${OBSERVATION_FILE}" "${report_tmp}" "${DRY_RUN}" "${REQUIRE_REAL_SAMPLE}" <<'PY_SCRIPT'
import re
import sys
from pathlib import Path

observation_path = Path(sys.argv[1])
report_path = Path(sys.argv[2])
dry_run = sys.argv[3].lower() == "true"
require_real_sample = sys.argv[4].lower() == "true"

report_lines = report_path.read_text(encoding="utf-8", errors="replace").splitlines()

def cells_for(line: str):
    stripped = line.strip()
    if not stripped.startswith("|"):
        return None
    cells = [c.strip() for c in stripped.strip("|").split("|")]
    if len(cells) < 16:
        return None
    return cells

def clean(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == "`" and value[-1] == "`":
        value = value[1:-1]
    return value.strip()

row_line = ""
row_cells = None
for line in report_lines:
    cells = cells_for(line)
    if not cells:
        continue
    date = clean(cells[0])
    if re.fullmatch(r"\d{4}-\d{2}-\d{2}", date):
        row_line = line.strip()
        row_cells = cells
        break

if row_cells is None:
    raise SystemExit("report does not contain a daily observation row")

obs_text = observation_path.read_text(encoding="utf-8", errors="replace")
if "## Daily observation row template" not in obs_text:
    raise SystemExit("observation file missing daily row template section")
summary_marker = "\n## 30-day summary template"
if summary_marker not in obs_text:
    raise SystemExit("observation file missing 30-day summary template marker")

row_date = clean(row_cells[0])
total = int(float(clean(row_cells[4])))
manual_smoke = clean(row_cells[10])
prom_smoke = clean(row_cells[11])
rollback_drill = clean(row_cells[12])
decision = clean(row_cells[14])
notes = clean(row_cells[15])

if "TODO(" in row_line:
    raise SystemExit(f"{row_date}: row still contains TODO marker; do not append incomplete evidence")
if total <= 0:
    if require_real_sample:
        raise SystemExit(f"{row_date}: total host tool calls is 0 but SKILL_PD_APPEND_REQUIRE_REAL_SAMPLE=true")
    if "no samples" not in manual_smoke.lower() and "no samples" not in notes.lower():
        raise SystemExit(f"{row_date}: total host tool calls is 0 without no-sample marker")
    if decision != "hold":
        raise SystemExit(f"{row_date}: no-sample row decision is {decision!r}, want hold")
if decision == "continue" and (manual_smoke != "PASS" or prom_smoke != "PASS" or rollback_drill != "PASS"):
    raise SystemExit(
        f"{row_date}: decision=continue requires manual/prometheus/rollback PASS; "
        f"got {manual_smoke!r}/{prom_smoke!r}/{rollback_drill!r}"
    )

for existing in obs_text.splitlines():
    cells = cells_for(existing)
    if not cells:
        continue
    existing_date = clean(cells[0])
    if existing_date == row_date:
        raise SystemExit(f"{row_date}: observation row already exists")

insert_at = obs_text.index(summary_marker)
new_text = obs_text[:insert_at].rstrip() + "\n" + row_line + "\n" + obs_text[insert_at:]
if not dry_run:
    observation_path.write_text(new_text, encoding="utf-8")

print("P25-HIGH-02p rollout observation append passed.")
print(f"Observation file: {observation_path}")
print(f"Date: {row_date}")
print(f"Total host tool calls: {total}")
print(f"Decision: {decision}")
print(f"Dry run: {str(dry_run).lower()}")
print("This helper does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE and does not delete overrideSkillsToSummary.")
PY_SCRIPT
