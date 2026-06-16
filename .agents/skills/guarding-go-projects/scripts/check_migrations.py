#!/usr/bin/env python3
"""Check SQL migration naming, reversibility markers, and destructive DDL."""

from __future__ import annotations

import os
import re
import sys
from collections import defaultdict
from pathlib import Path


ALLOW_DESTRUCTIVE = "guard:allow-destructive-migration"
MIGRATION_DIR_NAMES = {"migrations", "migration", "db/migrations", "database/migrations"}
PAIR_RE = re.compile(r"^(?P<version>\d{3,})[_-][a-z0-9][a-z0-9_-]*\.(?P<direction>up|down)\.sql$")
GOOSE_RE = re.compile(r"^(?P<version>\d{3,})[_-][a-z0-9][a-z0-9_-]*\.sql$")
DESTRUCTIVE_RE = re.compile(
    r"(?is)\b(drop\s+(?:table|database|schema|column|index)|truncate\s+table|alter\s+table\s+\S+\s+drop\s+column)\b"
)


def default_baseline_path() -> Path:
    return Path(__file__).resolve().parents[1] / "baselines" / f"{Path(__file__).stem}.txt"


def load_baseline() -> set[str]:
    raw = os.environ.get("GO_GUARD_MIGRATIONS_BASELINE") or os.environ.get("GO_GUARD_BASELINE")
    path = Path(raw).resolve() if raw else default_baseline_path()
    if not path.exists():
        return set()
    entries: set[str] = set()
    for line in path.read_text(encoding="utf-8", errors="ignore").splitlines():
        item = line.strip()
        if not item or item.startswith("#"):
            continue
        if item.startswith("- "):
            item = item[2:].strip()
        entries.add(item)
    return entries


def apply_baseline(findings: list[str]) -> list[str]:
    baseline = load_baseline()
    if not baseline:
        return findings
    return [item for item in findings if item not in baseline]


def migration_dirs(root: Path) -> list[Path]:
    dirs: list[Path] = []
    for name in MIGRATION_DIR_NAMES:
        path = root / name
        if path.exists() and path.is_dir():
            dirs.append(path)
    return sorted(set(dirs))


def check_pair_style(files: list[Path], root: Path) -> list[str]:
    findings: list[str] = []
    groups: dict[str, set[str]] = defaultdict(set)
    for path in files:
        match = PAIR_RE.match(path.name)
        if match:
            groups[match.group("version")].add(match.group("direction"))
            continue
        if GOOSE_RE.match(path.name):
            continue
        findings.append(f"{path.relative_to(root)}: migration filename must be NNN_name.up.sql/down.sql or NNN_name.sql")

    for version, directions in sorted(groups.items()):
        if directions != {"up", "down"}:
            findings.append(f"migration version {version}: expected both .up.sql and .down.sql files")
    return findings


def check_goose_markers(path: Path, root: Path) -> list[str]:
    text = path.read_text(encoding="utf-8", errors="ignore")
    if PAIR_RE.match(path.name):
        return []
    if not GOOSE_RE.match(path.name):
        return []
    findings: list[str] = []
    if "-- +goose Up" not in text:
        findings.append(f"{path.relative_to(root)}: missing '-- +goose Up' marker")
    if "-- +goose Down" not in text:
        findings.append(f"{path.relative_to(root)}: missing '-- +goose Down' marker")
    return findings


def check_destructive(path: Path, root: Path) -> list[str]:
    text = path.read_text(encoding="utf-8", errors="ignore")
    if ALLOW_DESTRUCTIVE in text:
        return []
    if DESTRUCTIVE_RE.search(text):
        return [f"{path.relative_to(root)}: destructive DDL requires {ALLOW_DESTRUCTIVE!r} and review"]
    return []


def main(argv: list[str]) -> int:
    root = Path(argv[1] if len(argv) > 1 else ".").resolve()
    dirs = migration_dirs(root)
    if not dirs:
        print("SKIP: no migrations directory found")
        return 0

    findings: list[str] = []
    for directory in dirs:
        files = sorted(path for path in directory.rglob("*.sql") if path.is_file())
        findings.extend(check_pair_style(files, root))
        for path in files:
            findings.extend(check_goose_markers(path, root))
            if path.name.endswith(".up.sql") or GOOSE_RE.match(path.name):
                findings.extend(check_destructive(path, root))

    findings = apply_baseline(findings)

    if findings:
        print("Migration guard findings:")
        for finding in findings:
            print(f"- {finding}")
        return 1

    print("Migration guard passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
