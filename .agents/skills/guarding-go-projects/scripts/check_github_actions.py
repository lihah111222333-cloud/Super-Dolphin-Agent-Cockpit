#!/usr/bin/env python3
"""Check GitHub Actions workflows for baseline security settings."""

from __future__ import annotations

import re
import sys
from pathlib import Path


ALLOWED_TOP_PERMISSIONS = {"contents": "read"}
UNPINNED_USES = re.compile(r"uses:\s*([^@\s]+)@([^\s#]+)")


def check_permissions(path: Path) -> list[str]:
    lines = path.read_text().splitlines()
    for index, line in enumerate(lines):
        if re.match(r"^permissions:\s*$", line):
            block = []
            for child in lines[index + 1 :]:
                if child and not child.startswith((" ", "\t")):
                    break
                if child.strip():
                    block.append(child.strip())
            if block == ["contents: read"]:
                return []
            return [f"{path}: top-level permissions must contain only 'contents: read'"]
        if re.match(r"^permissions:\s+read-all\s*$", line):
            return [f"{path}: use explicit 'contents: read' permissions, not read-all"]
    return [f"{path}: missing top-level permissions: contents: read"]


def check_uses_pinning(path: Path) -> list[str]:
    findings: list[str] = []
    for line_no, line in enumerate(path.read_text().splitlines(), start=1):
        match = UNPINNED_USES.search(line)
        if not match:
            continue
        ref = match.group(2).strip("'\"")
        if ref == "main" or ref == "master" or ref == "latest":
            findings.append(f"{path}:{line_no}: action ref {ref!r} is not pinned to a version")
    return findings


def main(argv: list[str]) -> int:
    root = Path(argv[1] if len(argv) > 1 else ".").resolve()
    workflow_dir = root / ".github" / "workflows"
    if not workflow_dir.exists():
        print("SKIP: no .github/workflows directory found")
        return 0

    findings: list[str] = []
    for path in sorted(workflow_dir.glob("*.y*ml")):
        rel = path.relative_to(root)
        findings.extend(check_permissions(rel))
        findings.extend(check_uses_pinning(rel))

    if findings:
        print("GitHub Actions guard findings:")
        for finding in findings:
            print(f"- {finding}")
        return 1

    print("GitHub Actions guard passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
