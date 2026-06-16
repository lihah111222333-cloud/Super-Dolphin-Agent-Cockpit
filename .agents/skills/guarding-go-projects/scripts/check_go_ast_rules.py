#!/usr/bin/env python3
"""Check Go source for architecture violations that imports alone cannot express."""

from __future__ import annotations

import re
import os
import sys
from pathlib import Path


SKIP_DIRS = {".agents", ".git", "vendor", "node_modules", "third_party"}
GENERATED_RE = re.compile(r"Code generated .* DO NOT EDIT")
COMMAND_DIRS = {"scripts"}

DOMAIN_CALLS = [
    ("time.Now(", "domain must receive time through app ports, not call time.Now"),
    ("os.Getenv(", "domain must not read environment variables"),
    ("rand.", "domain must receive randomness through app ports"),
    ("log.", "domain must not log directly"),
    ("slog.", "domain must not log directly"),
    ("fmt.Print", "domain must not write to stdout/stderr"),
]

APP_CALLS = [
    ("sql.Open(", "app must use repository ports, not open databases directly"),
    ("http.Get(", "app must use outbound ports, not call HTTP directly"),
    ("http.Post(", "app must use outbound ports, not call HTTP directly"),
    ("gorm.Open(", "app must use repository ports, not open ORM connections directly"),
    ("os.Getenv(", "app must receive config from bootstrap/platform"),
]

DIRECT_OUTPUT_CALLS = [
    ("fmt.Print", "use structured logger at a boundary instead of fmt.Print*"),
    ("log.Print", "use platform logging abstraction instead of log.Print*"),
    ("log.Fatal", "return errors or exit only from command/bootstrap code"),
    ("slog.", "use internal/platform/logging abstraction instead of direct slog calls"),
]

PROCESS_EXIT_CALLS = [
    ("panic(", "panic is only allowed in tests, command/bootstrap startup, or documented impossible invariants"),
    ("os.Exit(", "os.Exit is only allowed in command/bootstrap code"),
]

ERROR_PATTERNS = [
    (
        re.compile(r"errors\.New\s*\(\s*fmt\.Sprintf\s*\("),
        "use fmt.Errorf instead of errors.New(fmt.Sprintf(...))",
    ),
    (
        re.compile(r"fmt\.Errorf\s*\([^)]*%v[^)]*,\s*err\s*\)"),
        "wrap errors with %w instead of formatting err with %v",
    ),
]


def default_baseline_path() -> Path:
    return Path(__file__).resolve().parents[1] / "baselines" / f"{Path(__file__).stem}.txt"


def load_baseline() -> set[str]:
    raw = os.environ.get("GO_GUARD_AST_BASELINE") or os.environ.get("GO_GUARD_BASELINE")
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


def is_generated(path: Path) -> bool:
    head = path.read_text(encoding="utf-8", errors="ignore")[:2048]
    return bool(GENERATED_RE.search(head))


def go_files(root: Path) -> list[Path]:
    files: list[Path] = []
    for path in root.rglob("*.go"):
        rel_parts = path.relative_to(root).parts
        if any(part in SKIP_DIRS for part in rel_parts):
            continue
        if path.is_file() and not is_generated(path):
            files.append(path)
    return sorted(files)


def layer(path: Path, root: Path) -> str:
    parts = path.relative_to(root).parts
    if parts and parts[0] == "cmd":
        return "cmd"
    if parts and parts[0] in COMMAND_DIRS:
        return "cmd"
    if len(parts) >= 3 and parts[0] == "docs" and parts[1] == "security" and parts[2] == "internal":
        return "cmd"
    if len(parts) >= 3 and parts[0] == "internal":
        if parts[1] == "bootstrap":
            return "bootstrap"
        if parts[1] == "platform":
            return "platform"
        if parts[2] == "domain":
            return "domain"
        if parts[2] == "app":
            return "app"
        if parts[2] == "adapter":
            return "adapter"
    return "other"


def adapter_kind(path: Path, root: Path) -> str | None:
    parts = path.relative_to(root).parts
    if len(parts) >= 4 and parts[0] == "internal" and parts[2] == "adapter":
        return parts[3]
    return None


def is_test_file(path: Path) -> bool:
    return path.name.endswith("_test.go")


def is_logging_platform(path: Path, root: Path) -> bool:
    parts = path.relative_to(root).parts
    if len(parts) >= 3 and parts[0] == "internal" and parts[1] == "platform" and parts[2] in {"logging", "log"}:
        return True
    return len(parts) >= 2 and parts[0] == "pkg" and parts[1] == "logger"


def allows_direct_process_control(path: Path, root: Path) -> bool:
    current_layer = layer(path, root)
    return is_test_file(path) or current_layer in {"cmd", "bootstrap"} or is_logging_platform(path, root)


def allows_direct_logging(path: Path, root: Path) -> bool:
    current_layer = layer(path, root)
    return is_test_file(path) or current_layer in {"cmd", "bootstrap"} or is_logging_platform(path, root)


def line_for_offset(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


def check_tokens(path: Path, root: Path, tokens: list[tuple[str, str]]) -> list[str]:
    rel = path.relative_to(root).as_posix()
    text = path.read_text(encoding="utf-8", errors="ignore")
    findings: list[str] = []
    for token, reason in tokens:
        start = 0
        while True:
            index = text.find(token, start)
            if index == -1:
                break
            findings.append(f"{rel}:{line_for_offset(text, index)}: {reason}")
            start = index + len(token)
    return findings


def check_regexes(path: Path, root: Path, patterns: list[tuple[re.Pattern[str], str]]) -> list[str]:
    rel = path.relative_to(root).as_posix()
    text = path.read_text(encoding="utf-8", errors="ignore")
    findings: list[str] = []
    for pattern, reason in patterns:
        for match in pattern.finditer(text):
            findings.append(f"{rel}:{line_for_offset(text, match.start())}: {reason}")
    return findings


def check_err_swallowing(path: Path, root: Path) -> list[str]:
    rel = path.relative_to(root).as_posix()
    lines = path.read_text(encoding="utf-8", errors="ignore").splitlines()
    findings: list[str] = []
    for index, line in enumerate(lines):
        if "if err != nil" not in line:
            continue
        block_lines: list[str] = []
        depth = 0
        seen_body = False
        for current in range(index, len(lines)):
            block_line = lines[current]
            block_lines.append(block_line)
            depth += block_line.count("{")
            if "{" in block_line:
                seen_body = True
            depth -= block_line.count("}")
            if seen_body and depth <= 0:
                break
        block = "\n".join(block_lines)
        for return_match in re.finditer(r"\breturn\s+([^\n}]*)", block):
            return_expr = return_match.group(1)
            if "nil" in return_expr and "err" not in return_expr:
                findings.append(f"{rel}:{index + 1}: do not swallow err with return nil")
                break
        if re.search(r"\b(?:log\.|slog\.)\w+\s*\(", block) and re.search(r"\breturn\b[^\n]*\berr\b", block):
            findings.append(f"{rel}:{index + 1}: do not log and return the same error from internal code")
    return findings


def check_repository_orchestration(path: Path, root: Path) -> list[str]:
    if adapter_kind(path, root) not in {"postgres", "mysql", "sqlite", "redis"}:
        return []
    text = path.read_text(encoding="utf-8", errors="ignore")
    rel = path.relative_to(root).as_posix()
    findings: list[str] = []
    for match in re.finditer(r"\b(?:New|Create|Build)[A-Z][A-Za-z0-9]*(?:Service|UseCase|Handler)\s*\(", text):
        findings.append(f"{rel}:{line_for_offset(text, match.start())}: repository adapters must not assemble services or handlers")
    return findings


def main(argv: list[str]) -> int:
    root = Path(argv[1] if len(argv) > 1 else ".").resolve()
    if not (root / "go.mod").exists():
        print(f"SKIP: no go.mod found under {root}")
        return 0

    findings: list[str] = []
    for path in go_files(root):
        current_layer = layer(path, root)
        if current_layer == "domain":
            findings.extend(check_tokens(path, root, DOMAIN_CALLS))
        elif current_layer == "app":
            findings.extend(check_tokens(path, root, APP_CALLS))
        if not allows_direct_logging(path, root):
            findings.extend(check_tokens(path, root, DIRECT_OUTPUT_CALLS))
        if not allows_direct_process_control(path, root):
            findings.extend(check_tokens(path, root, PROCESS_EXIT_CALLS))
        findings.extend(check_regexes(path, root, ERROR_PATTERNS))
        if current_layer not in {"cmd", "bootstrap"} and not is_test_file(path):
            findings.extend(check_err_swallowing(path, root))
        findings.extend(check_repository_orchestration(path, root))

    findings = apply_baseline(findings)

    if findings:
        print("Go AST architecture findings:")
        for finding in findings:
            print(f"- {finding}")
        return 1

    print("Go AST architecture check passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
