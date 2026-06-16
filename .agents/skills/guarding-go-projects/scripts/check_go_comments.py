#!/usr/bin/env python3
"""Check required Go documentation comments."""

from __future__ import annotations

import os
import re
import sys
from collections import defaultdict
from pathlib import Path


SKIP_DIRS = {".agents", ".git", "vendor", "node_modules", "third_party"}
GENERATED_RE = re.compile(r"Code generated .* DO NOT EDIT")
PACKAGE_RE = re.compile(r"^\s*package\s+([a-zA-Z_][a-zA-Z0-9_]*)")
EXPORTED_DECL_RE = re.compile(
    r"^(?:func\s+(?:\([^)]*\)\s*)?(?P<func>[A-Z][A-Za-z0-9_]*)|"
    r"type\s+(?P<type>[A-Z][A-Za-z0-9_]*)|"
    r"(?:const|var)\s+(?P<value>[A-Z][A-Za-z0-9_]*))\b"
)
FUNC_RE = re.compile(r"^func\s+(?:\([^)]*\)\s*)?(?P<name>[a-zA-Z_][A-Za-z0-9_]*)\s*\(")
ALLOW_MARKER = "guard:allow-missing-comment"


def default_baseline_path() -> Path:
    return Path(__file__).resolve().parents[1] / "baselines" / f"{Path(__file__).stem}.txt"


def load_baseline() -> set[str]:
    raw = os.environ.get("GO_GUARD_COMMENTS_BASELINE") or os.environ.get("GO_GUARD_BASELINE")
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


def env_int(name: str, default: int) -> int:
    raw = os.environ.get(name)
    if raw is None or raw == "":
        return default
    try:
        return int(raw)
    except ValueError:
        raise SystemExit(f"ERROR: {name} must be an integer, got {raw!r}")


def is_generated(path: Path) -> bool:
    text = path.read_text(encoding="utf-8", errors="ignore")[:2048]
    return bool(GENERATED_RE.search(text))


def go_files(root: Path) -> list[Path]:
    files: list[Path] = []
    for path in root.rglob("*.go"):
        rel_parts = path.relative_to(root).parts
        if any(part in SKIP_DIRS for part in rel_parts):
            continue
        if path.is_file() and not is_generated(path):
            files.append(path)
    return sorted(files)


def package_name(path: Path) -> str | None:
    for line in path.read_text(encoding="utf-8", errors="ignore").splitlines():
        match = PACKAGE_RE.match(line)
        if match:
            return match.group(1)
    return None


def previous_comment(lines: list[str], index: int) -> str:
    comments: list[str] = []
    current = index - 1
    while current >= 0 and lines[current].strip() == "":
        current -= 1
    while current >= 0:
        stripped = lines[current].strip()
        if stripped.startswith("//"):
            comments.append(stripped[2:].strip())
            current -= 1
            continue
        if stripped.endswith("*/"):
            comments.append(stripped.strip("/* ").strip())
            break
        break
    return " ".join(reversed(comments)).strip()


def has_package_comment(path: Path, package: str) -> bool:
    lines = path.read_text(encoding="utf-8", errors="ignore").splitlines()
    for index, line in enumerate(lines):
        if PACKAGE_RE.match(line):
            comment = previous_comment(lines, index)
            if package == "main" and comment.startswith("Command "):
                return True
            return comment.startswith(f"Package {package} ")
    return False


def function_end(lines: list[str], start: int) -> int:
    depth = 0
    seen_body = False
    for index in range(start, len(lines)):
        line = lines[index]
        depth += line.count("{")
        if "{" in line:
            seen_body = True
        depth -= line.count("}")
        if seen_body and depth <= 0:
            return index
    return start


def check_file(path: Path, root: Path, long_func_lines: int) -> list[str]:
    rel = path.relative_to(root).as_posix()
    lines = path.read_text(encoding="utf-8", errors="ignore").splitlines()
    findings: list[str] = []

    for index, line in enumerate(lines):
        if ALLOW_MARKER in line:
            continue

        stripped = line.strip()
        exported = EXPORTED_DECL_RE.match(stripped)
        if exported:
            name = next(value for value in exported.groupdict().values() if value)
            comment = previous_comment(lines, index)
            if not comment.startswith(f"{name} "):
                findings.append(f"{rel}:{index + 1}: exported {name} must have a doc comment starting with '{name} '")

        func = FUNC_RE.match(stripped)
        if func:
            end = function_end(lines, index)
            length = end - index + 1
            if long_func_lines > 0 and length >= long_func_lines:
                comment = previous_comment(lines, index)
                if not comment:
                    name = func.group("name")
                    findings.append(f"{rel}:{index + 1}: long function {name} has {length} lines and needs an intent comment")

    return findings


def main(argv: list[str]) -> int:
    root = Path(argv[1] if len(argv) > 1 else ".").resolve()
    if not (root / "go.mod").exists():
        print(f"SKIP: no go.mod found under {root}")
        return 0

    long_func_lines = env_int("GO_GUARD_COMMENT_LONG_FUNC_LINES", 40)
    files = go_files(root)
    by_package: dict[Path, list[Path]] = defaultdict(list)
    findings: list[str] = []

    for path in files:
        by_package[path.parent].append(path)
        findings.extend(check_file(path, root, long_func_lines))

    for package_dir, package_files in sorted(by_package.items()):
        package = package_name(package_files[0])
        if not package or package.endswith("_test"):
            continue
        if not any(has_package_comment(path, package) for path in package_files):
            rel = package_dir.relative_to(root).as_posix()
            findings.append(f"{rel}: package {package} needs a package comment, preferably in doc.go")

    findings = apply_baseline(findings)

    if findings:
        print("Go comment guard findings:")
        for finding in findings:
            print(f"- {finding}")
        print(f"Use {ALLOW_MARKER!r} only for documented false positives.")
        return 1

    print("Go comment guard passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
