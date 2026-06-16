#!/usr/bin/env python3
"""Validate that Git commit subject and details are written in Chinese."""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path


HAN_RE = re.compile(r"[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]")
ENGLISH_CONVENTIONAL_PREFIX_RE = re.compile(
    r"^(?:build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test)(?:\([^)]+\))?!?:",
    re.IGNORECASE,
)
SCISSORS_RE = re.compile(r"^# ------------------------ >8 ------------------------")


def clean_message(raw: str) -> list[str]:
    lines: list[str] = []
    for line in raw.replace("\r\n", "\n").splitlines():
        if SCISSORS_RE.match(line):
            break
        if line.lstrip().startswith("#"):
            continue
        lines.append(line.rstrip())

    while lines and not lines[0].strip():
        lines.pop(0)
    while lines and not lines[-1].strip():
        lines.pop()
    return lines


def has_chinese(value: str) -> bool:
    return bool(HAN_RE.search(value))


def validate_message(raw: str, label: str) -> list[str]:
    lines = clean_message(raw)
    if not lines:
        return [f"{label}: 提交信息不能为空"]

    subject = lines[0].strip()
    body_lines = [line.strip() for line in lines[1:] if line.strip()]
    body = "\n".join(body_lines)
    findings: list[str] = []

    if not has_chinese(subject):
        findings.append(f"{label}: 提交主题必须包含中文")
    if ENGLISH_CONVENTIONAL_PREFIX_RE.match(subject):
        findings.append(f"{label}: 提交主题不能使用英文 Conventional Commit 前缀，请改为中文主题")
    if not body_lines:
        findings.append(f"{label}: 提交详情不能为空，必须用中文说明变更内容和原因")
    elif not has_chinese(body):
        findings.append(f"{label}: 提交详情必须包含中文")

    return findings


def read_commit_file(path: Path) -> str:
    try:
        return path.read_text(encoding="utf-8")
    except OSError as exc:
        raise SystemExit(f"读取提交信息失败: {path}: {exc}") from exc


def commit_hashes(rev_range: str) -> list[str]:
    try:
        out = subprocess.check_output(
            ["git", "log", "--format=%H", rev_range],
            text=True,
            stderr=subprocess.STDOUT,
        )
    except subprocess.CalledProcessError as exc:
        raise SystemExit(f"读取提交范围失败: {rev_range}\n{exc.output}") from exc
    return [line.strip() for line in out.splitlines() if line.strip()]


def commit_message(commit: str) -> str:
    try:
        return subprocess.check_output(
            ["git", "log", "-1", "--format=%B", commit],
            text=True,
            stderr=subprocess.STDOUT,
        )
    except subprocess.CalledProcessError as exc:
        raise SystemExit(f"读取提交信息失败: {commit}\n{exc.output}") from exc


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Validate Chinese Git commit subject and details")
    parser.add_argument("commit_msg_file", nargs="?", help="path to the commit message file from commit-msg hook")
    parser.add_argument("--range", dest="rev_range", help="git revision range to validate, for CI")
    args = parser.parse_args(argv[1:])

    findings: list[str] = []
    if args.rev_range:
        commits = commit_hashes(args.rev_range)
        if not commits:
            print(f"提交信息校验跳过：范围内没有提交 {args.rev_range}")
            return 0
        for commit in commits:
            label = commit[:12]
            findings.extend(validate_message(commit_message(commit), label))
    else:
        if not args.commit_msg_file:
            raise SystemExit("usage: check_commit_message.py <commit-msg-file> 或 --range <rev-range>")
        findings.extend(validate_message(read_commit_file(Path(args.commit_msg_file)), "当前提交"))

    if findings:
        print("提交信息校验失败：")
        for finding in findings:
            print(f"- {finding}")
        print("")
        print("要求：")
        print("- 第一行提交主题必须为中文主题，不能使用 feat:/fix:/chore: 等英文前缀。")
        print("- 第二段提交详情不能为空，必须用中文说明变更内容和原因。")
        print("")
        print("示例：")
        print("  git commit -m '守卫：强制提交信息使用中文' -m '新增 commit-msg 校验，确保主题和详情均为中文。'")
        return 1

    print("提交信息校验通过。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
