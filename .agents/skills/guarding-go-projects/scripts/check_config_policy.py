#!/usr/bin/env python3
"""Check committed configuration files for unsafe secret handling."""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path


ALLOWED_ENV_EXAMPLES = {
    ".env.example",
    ".env.sample",
    ".env.template",
}

CONFIG_SUFFIXES = {
    ".env",
    ".yaml",
    ".yml",
    ".json",
    ".toml",
}

SENSITIVE_KEY_RE = re.compile(
    r"(?i)(secret|token|password|passwd|credential|private[_-]?key|api[_-]?key|client[_-]?secret|authorization|cookie)"
)

ASSIGNMENT_RE = re.compile(
    r"""
    ^\s*
    (?P<key>[A-Za-z0-9_.-]*(?:secret|token|password|passwd|credential|private[_-]?key|api[_-]?key|client[_-]?secret|authorization|cookie)[A-Za-z0-9_.-]*)
    \s*(?::|=)\s*
    (?P<value>.+?)\s*
    (?:\#.*)?$
    """,
    re.IGNORECASE | re.VERBOSE,
)

PLACEHOLDER_RE = re.compile(
    r"(?i)^(?:|null|nil|none|changeme|change-me|example|sample|dummy|placeholder|test|"
    r"your[_-]?.*|xxx+|\$\{[A-Z0-9_:-]+\}|<[^>]+>|\\\"<[^>]+>\\\"|'?<[^>]+>'?)$"
)


def git_files(root: Path) -> set[Path]:
    try:
        tracked = subprocess.check_output(
            ["git", "-C", str(root), "ls-files"],
            text=True,
            stderr=subprocess.DEVNULL,
        )
        staged = subprocess.check_output(
            ["git", "-C", str(root), "diff", "--cached", "--name-only"],
            text=True,
            stderr=subprocess.DEVNULL,
        )
    except (OSError, subprocess.CalledProcessError):
        return {path.relative_to(root) for path in root.rglob("*") if path.is_file()}

    files = {Path(line) for line in (tracked + "\n" + staged).splitlines() if line.strip()}
    return files


def is_env_file(path: Path) -> bool:
    name = path.name
    return name == ".env" or name.startswith(".env.")


def allowed_env_example(path: Path) -> bool:
    return path.name in ALLOWED_ENV_EXAMPLES or path.name.endswith(".example")


def is_config_file(path: Path) -> bool:
    lower_parts = [part.lower() for part in path.parts]
    name = path.name.lower()
    if is_env_file(path):
        return True
    if path.suffix.lower() not in CONFIG_SUFFIXES:
        return False
    return (
        "configs" in lower_parts
        or "config" in lower_parts
        or "configuration" in lower_parts
        or "secrets" in lower_parts
        or "secret" in lower_parts
        or "config" in name
        or "secret" in name
    )


def normalize_value(raw: str) -> str:
    value = raw.strip().strip(",")
    if (value.startswith('"') and value.endswith('"')) or (value.startswith("'") and value.endswith("'")):
        value = value[1:-1]
    return value.strip()


def is_placeholder(value: str) -> bool:
    normalized = normalize_value(value)
    if PLACEHOLDER_RE.match(normalized):
        return True
    if normalized.startswith("${") and normalized.endswith("}"):
        return True
    return False


def scan_config_file(path: Path, root: Path) -> list[str]:
    rel = path.as_posix()
    abs_path = root / path
    try:
        lines = abs_path.read_text(encoding="utf-8", errors="ignore").splitlines()
    except OSError as exc:
        return [f"{rel}: failed to read file: {exc}"]

    findings: list[str] = []
    for line_no, line in enumerate(lines, start=1):
        if "guard:allow-config-secret" in line:
            continue
        stripped = line.strip()
        if not stripped or stripped.startswith("#") or stripped.startswith("//"):
            continue

        match = ASSIGNMENT_RE.match(line)
        if not match:
            continue
        key = match.group("key")
        value = normalize_value(match.group("value"))
        if not SENSITIVE_KEY_RE.search(key):
            continue
        if is_placeholder(value):
            continue
        findings.append(f"{rel}:{line_no}: sensitive config key {key!r} must use a placeholder or secret reference")
    return findings


def check_gitignore(root: Path) -> list[str]:
    gitignore = root / ".gitignore"
    if not gitignore.exists():
        return [".gitignore: missing .env ignore rules"]
    text = gitignore.read_text(encoding="utf-8", errors="ignore")
    required = [".env", ".env.*", "!.env.example"]
    missing = [item for item in required if item not in text]
    if missing:
        return [f".gitignore: missing config secret ignore rule(s): {', '.join(missing)}"]
    return []


def main(argv: list[str]) -> int:
    root = Path(argv[1] if len(argv) > 1 else ".").resolve()
    findings: list[str] = []
    findings.extend(check_gitignore(root))

    files = git_files(root)
    if any(is_config_file(path) for path in files) and not (root / ".env.example").exists():
        findings.append(".env.example: required when repository contains configuration files")

    for path in sorted(files):
        if is_env_file(path) and not allowed_env_example(path):
            findings.append(f"{path.as_posix()}: local env files must not be tracked or staged")
            continue
        if is_config_file(path) and (root / path).is_file():
            findings.extend(scan_config_file(path, root))

    if findings:
        print("Configuration policy findings:")
        for finding in findings:
            print(f"- {finding}")
        print("Use 'guard:allow-config-secret' only for documented false positives.")
        return 1

    print("Configuration policy passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
