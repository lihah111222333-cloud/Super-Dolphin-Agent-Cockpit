#!/usr/bin/env python3
"""Scan repository text files for high-risk secrets."""

from __future__ import annotations

import math
import re
import sys
from pathlib import Path


SKIP_DIRS = {
    ".git",
    ".idea",
    ".venv",
    "node_modules",
    "vendor",
    "dist",
    "build",
    "target",
    ".next",
}

SKIP_SUFFIXES = {
    ".png",
    ".jpg",
    ".jpeg",
    ".gif",
    ".webp",
    ".pdf",
    ".zip",
    ".gz",
    ".tar",
    ".tgz",
    ".ico",
    ".woff",
    ".woff2",
    ".ttf",
    ".lock",
    ".sum",
}

ALLOW_MARKER = "guard:allow-secret"

SECRET_PATTERNS = [
    ("private key", re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----")),  # guard:allow-secret
    ("github token", re.compile(r"\bgh[pousr]_[A-Za-z0-9_]{30,}\b")),
    ("openai token", re.compile(r"\bsk-[A-Za-z0-9_-]{20,}\b")),
    ("anthropic token", re.compile(r"\bsk-ant-[A-Za-z0-9_-]{20,}\b")),
    ("slack token", re.compile(r"\bxox[baprs]-[A-Za-z0-9-]{20,}\b")),
    ("aws access key", re.compile(r"\bAKIA[0-9A-Z]{16}\b")),
    ("jwt", re.compile(r"\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b")),
    (
        "credential assignment",
        re.compile(
            r"(?i)\b(?:password|passwd|secret|token|api[_-]?key|private[_-]?key|client[_-]?secret)\b"
            r"\s*[:=]\s*['\"]?([^'\"\s#]{12,})"
        ),
    ),
    (
        "url credential",
        re.compile(r"\b(?:postgres|mysql|mongodb|redis|amqp|http|https)://[^/\s:@]+:[^@\s/]{8,}@"),
    ),
]

PLACEHOLDER_RE = re.compile(
    r"(?i)^(?:changeme|change-me|example|sample|dummy|placeholder|test|secret|password|token|"
    r"your[_-]?.*|xxx+|\$\{[^}]+\}|<[^>]+>)$"
)


def is_binary(path: Path) -> bool:
    try:
        chunk = path.read_bytes()[:4096]
    except OSError:
        return True
    return b"\0" in chunk


def entropy(value: str) -> float:
    if not value:
        return 0.0
    counts = {char: value.count(char) for char in set(value)}
    length = len(value)
    return -sum((count / length) * math.log2(count / length) for count in counts.values())


def should_scan(path: Path, root: Path) -> bool:
    rel_parts = path.relative_to(root).parts
    if any(part in SKIP_DIRS for part in rel_parts):
        return False
    if path.suffix.lower() in SKIP_SUFFIXES:
        return False
    return path.is_file() and not is_binary(path)


def is_placeholder(value: str) -> bool:
    normalized = value.strip().strip("'\"")
    return bool(PLACEHOLDER_RE.match(normalized))


def scan_file(path: Path, root: Path) -> list[str]:
    rel = path.relative_to(root).as_posix()
    try:
        lines = path.read_text(encoding="utf-8", errors="ignore").splitlines()
    except OSError as exc:
        return [f"{rel}: failed to read file: {exc}"]

    findings: list[str] = []
    for line_no, line in enumerate(lines, start=1):
        if ALLOW_MARKER in line:
            continue
        for label, pattern in SECRET_PATTERNS:
            match = pattern.search(line)
            if not match:
                continue
            value = match.group(1) if match.groups() else match.group(0)
            if is_placeholder(value):
                continue
            if label == "credential assignment" and entropy(value) < 3.0 and len(value) < 24:
                continue
            findings.append(f"{rel}:{line_no}: possible {label}")
            break
    return findings


def main(argv: list[str]) -> int:
    root = Path(argv[1] if len(argv) > 1 else ".").resolve()
    findings: list[str] = []
    for path in sorted(root.rglob("*")):
        if should_scan(path, root):
            findings.extend(scan_file(path, root))

    if findings:
        print("Secret scan findings:")
        for finding in findings:
            print(f"- {finding}")
        print(f"Use {ALLOW_MARKER!r} only for documented false positives.")
        return 1

    print("Secret scan passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
