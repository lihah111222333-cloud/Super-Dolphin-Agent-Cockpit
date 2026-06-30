#!/usr/bin/env python3
"""Validate the production risk remediation evidence ledger."""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path


ID_RE = re.compile(r"\bP[0-9]-[0-9]{2}\b")
TABLE_ID_RE = re.compile(r"^\|\s*(P[0-9]-[0-9]{2})\s*\|")
COMMIT_RE = re.compile(r"^[0-9a-f]{7,40}$")
PLACEHOLDER_COMMITS = {"local", "none", "n/a", "na", "pending", "tbd", "todo", "working-tree", "worktree"}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", required=True, type=Path)
    parser.add_argument("--evidence", required=True, type=Path)
    return parser.parse_args()


def read_text(path: Path) -> str:
    if not path.is_file():
        raise ValueError(f"missing file: {path}")
    return path.read_text(encoding="utf-8")


def queue_rows(plan_text: str) -> dict[str, str]:
    rows: dict[str, str] = {}
    for line in plan_text.splitlines():
        match = TABLE_ID_RE.match(line)
        if match:
            rows[match.group(1)] = line
    if not rows:
        raise ValueError("plan contains no queue table rows")
    return rows


def ids_from_category(plan_text: str, category: str) -> set[str]:
    for line in plan_text.splitlines():
        if line.startswith("| " + category + " |"):
            return set(ID_RE.findall(line))
    return set()


def classify_plan_ids(plan_text: str) -> tuple[set[str], dict[str, set[str]]]:
    rows = queue_rows(plan_text)
    guard_only = ids_from_category(plan_text, "Guard / test governance only")
    evidence_only = ids_from_category(plan_text, "Evidence-only")
    adjusted_readiness = {
        queue_id
        for queue_id, row in rows.items()
        if "ADJUSTED:" in row and queue_id not in guard_only
    }
    reserved = {
        "Adjusted Readiness Dispositions": adjusted_readiness,
        "Guard-Only Dispositions": guard_only,
        "Evidence-Only Dispositions": evidence_only,
    }
    active = set(rows) - set().union(*reserved.values())
    return active, reserved


def section_for_line(line: str, current: str) -> str:
    if line.startswith("## "):
        return line.strip("# ").strip()
    return current


def evidence_rows(evidence_text: str) -> dict[str, set[str]]:
    sections = {
        "Active Evidence": set(),
        "Adjusted Readiness Dispositions": set(),
        "Guard-Only Dispositions": set(),
        "Evidence-Only Dispositions": set(),
    }
    current = ""
    for line in evidence_text.splitlines():
        current = section_for_line(line, current)
        match = TABLE_ID_RE.match(line)
        if not match:
            continue
        if current not in sections:
            raise ValueError(f"ID row {match.group(1)} appears outside a declared evidence section: {current or '<none>'}")
        sections[current].add(match.group(1))
    return sections


def table_cells(line: str) -> list[str]:
    cells: list[str] = []
    current: list[str] = []
    in_code = False
    for char in line.strip():
        if char == "`":
            in_code = not in_code
            current.append(char)
            continue
        if char == "|" and not in_code:
            cells.append("".join(current))
            current = []
            continue
        current.append(char)
    cells.append("".join(current))
    if cells and cells[0].strip() == "":
        cells = cells[1:]
    if cells and cells[-1].strip() == "":
        cells = cells[:-1]
    return [cell.strip().strip("`") for cell in cells]


def active_commit_cells(evidence_text: str) -> dict[str, str]:
    commits: dict[str, str] = {}
    current = ""
    for line in evidence_text.splitlines():
        current = section_for_line(line, current)
        if current != "Active Evidence":
            continue
        match = TABLE_ID_RE.match(line)
        if not match:
            continue
        cells = table_cells(line)
        if len(cells) < 5:
            raise ValueError(f"active evidence row {match.group(1)} is missing a Commit cell")
        commits[match.group(1)] = cells[4]
    return commits


def format_ids(ids: set[str]) -> str:
    return ", ".join(sorted(ids)) if ids else "<none>"


def repo_root_for(path: Path) -> Path | None:
    result = subprocess.run(
        ["git", "-C", str(path.parent), "rev-parse", "--show-toplevel"],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
    )
    if result.returncode != 0:
        return None
    return Path(result.stdout.strip())


def commit_exists(repo_root: Path | None, commit: str) -> bool:
    if repo_root is None:
        return True
    result = subprocess.run(
        ["git", "-C", str(repo_root), "cat-file", "-e", f"{commit}^{{commit}}"],
        check=False,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    return result.returncode == 0


def validate_active_commits(evidence_text: str, repo_root: Path | None) -> list[str]:
    failures: list[str] = []
    for queue_id, commit in active_commit_cells(evidence_text).items():
        normalized = commit.lower().strip()
        if normalized in PLACEHOLDER_COMMITS:
            failures.append(f"{queue_id}: Commit must be a concrete git SHA, got {commit!r}")
            continue
        if not COMMIT_RE.fullmatch(normalized):
            failures.append(f"{queue_id}: Commit must be 7-40 lowercase hex characters, got {commit!r}")
            continue
        if not commit_exists(repo_root, normalized):
            failures.append(f"{queue_id}: Commit {commit!r} does not resolve in git")
    return failures


def validate(plan_text: str, evidence_text: str, repo_root: Path | None = None) -> list[str]:
    active, reserved = classify_plan_ids(plan_text)
    rows = evidence_rows(evidence_text)
    failures: list[str] = []

    missing_active = active - rows["Active Evidence"]
    extra_active = rows["Active Evidence"] - active
    if missing_active:
        failures.append(f"missing active evidence rows: {format_ids(missing_active)}")
    if extra_active:
        failures.append(f"extra active evidence rows: {format_ids(extra_active)}")
    failures.extend(validate_active_commits(evidence_text, repo_root))

    reserved_ids = set().union(*reserved.values())
    misplaced_reserved = rows["Active Evidence"] & reserved_ids
    if misplaced_reserved:
        failures.append(
            "reserved disposition IDs must not appear in active evidence: "
            + format_ids(misplaced_reserved)
        )

    for section, want in reserved.items():
        got = rows[section]
        missing = want - got
        extra = got - want
        if missing:
            failures.append(f"{section}: missing disposition rows: {format_ids(missing)}")
        if extra:
            failures.append(f"{section}: extra disposition rows: {format_ids(extra)}")

    return failures


def main() -> int:
    args = parse_args()
    try:
        failures = validate(read_text(args.plan), read_text(args.evidence), repo_root_for(args.plan))
    except ValueError as exc:
        print(f"FAIL: risk evidence validation: {exc}", file=sys.stderr)
        return 2
    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    print("risk evidence ledger matches plan queue IDs and dispositions")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
