#!/usr/bin/env python3
"""Check Go source size budgets that are not covered by gofmt or go test."""

from __future__ import annotations

import os
import sys
from collections import defaultdict
from pathlib import Path


SKIP_DIRS = {".agents", ".git", ".idea", "vendor", "node_modules", "third_party"}


def default_baseline_path() -> Path:
    return Path(__file__).resolve().parents[1] / "baselines" / f"{Path(__file__).stem}.txt"


def load_baseline() -> set[str]:
    raw = os.environ.get("GO_GUARD_SIZE_BASELINE") or os.environ.get("GO_GUARD_BASELINE")
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


def apply_baseline(violations: list[str]) -> list[str]:
    baseline = load_baseline()
    if not baseline:
        return violations
    return [item for item in violations if item not in baseline]


def env_int(name: str, default: int) -> int:
    raw = os.environ.get(name)
    if raw is None or raw == "":
        return default
    try:
        value = int(raw)
    except ValueError:
        raise SystemExit(f"ERROR: {name} must be an integer, got {raw!r}")
    return value


def is_generated(path: Path) -> bool:
    try:
        head = path.read_text(encoding="utf-8", errors="ignore").splitlines()[:20]
    except OSError:
        return False
    joined = "\n".join(head)
    return "Code generated" in joined and "DO NOT EDIT" in joined


def go_files(root: Path) -> list[Path]:
    files: list[Path] = []
    for path in root.rglob("*.go"):
        rel_parts = path.relative_to(root).parts
        if any(part in SKIP_DIRS for part in rel_parts):
            continue
        if is_generated(path):
            continue
        files.append(path)
    return sorted(files)


def line_count(path: Path) -> int:
    try:
        return len(path.read_text(encoding="utf-8", errors="ignore").splitlines())
    except OSError as exc:
        raise SystemExit(f"ERROR: failed to read {path}: {exc}")


def main(argv: list[str]) -> int:
    root = Path(argv[1] if len(argv) > 1 else ".").resolve()
    if not (root / "go.mod").exists():
        print(f"SKIP: no go.mod found under {root}")
        return 0

    max_file_lines = env_int("GO_GUARD_MAX_FILE_LINES", 400)
    max_test_file_lines = env_int("GO_GUARD_MAX_TEST_FILE_LINES", 700)
    max_package_files = env_int("GO_GUARD_MAX_PACKAGE_GO_FILES", 30)

    violations: list[str] = []
    package_files: dict[Path, list[Path]] = defaultdict(list)

    for path in go_files(root):
        rel = path.relative_to(root).as_posix()
        lines = line_count(path)
        limit = max_test_file_lines if path.name.endswith("_test.go") else max_file_lines
        if limit > 0 and lines > limit:
            violations.append(f"{rel}: {lines} lines exceeds limit {limit}")
        package_files[path.parent].append(path)

    if max_package_files > 0:
        for package_dir, files in sorted(package_files.items()):
            if len(files) > max_package_files:
                rel = package_dir.relative_to(root).as_posix()
                violations.append(f"{rel}: {len(files)} Go files exceeds package limit {max_package_files}")

    violations = apply_baseline(violations)

    if violations:
        print("Go source size violations:")
        for item in violations:
            print(f"- {item}")
        return 1

    print("Go source size check passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
