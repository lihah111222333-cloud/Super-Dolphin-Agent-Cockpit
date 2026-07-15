#!/usr/bin/env python3
"""Validate current canonical repository skills without locking prose layout."""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SKILLS = ROOT / ".agents/skills"
REQUIRED = {"MCP协议", "MCP服务器构建", "产品评分", "代码审查维度", "全量项目地图生成", "前端", "后端", "字段守卫", "注释规范"}
FACTS = {
    "MCP协议": ["internal/mcpserver/common", "stdio MCP", "legacy HTTP", "fail-fast"],
    "MCP服务器构建": ["MCP协议", "唯一事实源"],
    "前端": ["frontend-app", "cmd/agent-terminal/web-dist", "npm run lint", "npm test", "npm run build"],
    "后端": ["internal/app/modules.go", "internal/platform/db/module.go", "internal/store/module.go", "SQLite", "sqlc"],
    "字段守卫": ["dynamically_enumerated_producer_fields", "missing", "stale", "fail-first", "Evidence/Owner"],
    "全量项目地图生成": ["scripts/generate_ai_project_map.mjs", "make project-map-refresh", "make project-map-check"],
    "注释规范": ["internal/archtest/guardlib.go", "archguard:ignore func_comment", "make guard"],
}
FORBIDDEN = ["mcp-go-agent-orchestration", "go-agent-orchestration", "github.com/mark3labs", "goimports -w ."]
FORBIDDEN_SKILL_TREE_TOKENS = FORBIDDEN + ["~/.claude/skills/design/scripts"]
MIRROR_IGNORED_REL_FILES = {"references/ui-styling/scripts/.coverage"}
PLACEHOLDER_VALUES = {"", "11", "todo", "tbd", "placeholder"}
BACKEND_REQUIRED_TRIGGER_WORDS = {
    "Go后端",
    "golang",
    "backend",
    "fx.Module",
    "sqlc",
    "jrpc2",
    "RunGroup",
    "internal/module",
    "internal/store",
    "internal/platform/runner",
    "cmd/mcp-",
}
BACKEND_LOW_SIGNAL_TRIGGER_WORDS = {"go", "mcp"}
BACKEND_REQUIRED_CONTENT = {
    "SKILL.md": ("internal/app/storeadapter", "internal/platform/runner", "./scripts/test_with_guard.sh"),
    "project_structure.md": (
        "internal/module/<name>/",
        "internal/store/<name>/",
        "internal/app/storeadapter/<name>",
    ),
    "concurrency_basics.md": ("internal/app.BindRuntime", 'group:"runners"'),
    "error_handling.md": ("module/service", "internal/platform/rpc", "errors.Is", "errors.As"),
    "code_organization.md": ("workspace creation failed", 'return "", err', "ClientConfig"),
    "testing_pitfalls.md": ("./scripts/test_with_guard.sh", "make test"),
    "effective_go_rules.md": ("go.mod",),
}
BACKEND_FORBIDDEN_CONTENT = {
    "project_structure.md": ("├── store.go         # 数据库访问层封装",),
    "concurrency_basics.md": (
        "_ = platformrunner.RunGroup",
        "context.WithCancel(context.Background())",
    ),
    "error_handling.md": ("**L2 业务层** | 带有自定义 Code 的 `*jrpc2.Error`",),
    "code_organization.md": ("Client{timeout: 30 * time.Second}", "return res, err"),
    "testing_pitfalls.md": ("go test -race ./...",),
}



def skill_dirs(base: Path) -> list[Path]:
    return sorted(p for p in base.iterdir() if p.is_dir() and (p / "SKILL.md").is_file()) if base.exists() else []


def rel_files(base: Path) -> set[str]:
    return {
        str(path.relative_to(base))
        for path in base.rglob("*")
        if path.is_file() and str(path.relative_to(base)) not in MIRROR_IGNORED_REL_FILES
    }


def normalize_mirror_bytes(data: bytes) -> bytes:
    lines = [line.rstrip(b" \t") for line in data.replace(b"\r\n", b"\n").split(b"\n")]
    while lines and not lines[-1]:
        lines.pop()
    return b"\n".join(lines)


def mirror_bytes_equal(canonical: bytes, mirror: bytes) -> bool:
    return canonical == mirror or normalize_mirror_bytes(canonical) == normalize_mirror_bytes(mirror)


def parse(text: str) -> tuple[dict[str, str], str]:
    lines = text.splitlines()
    if not lines or lines[0].strip() != "---":
        return {}, text.strip()
    try:
        end = next(i for i, line in enumerate(lines[1:], 1) if line.strip() == "---")
    except StopIteration:
        return {}, text.strip()
    fields = {}
    for line in lines[1:end]:
        if ":" in line:
            key, value = line.split(":", 1)
            fields[key.strip()] = value.strip().strip("\"'")
    return fields, "\n".join(lines[end + 1:]).strip()


def load_skills(failures: list[str]) -> dict[str, str]:
    result = {}
    for directory in skill_dirs(SKILLS):
        text = (directory / "SKILL.md").read_text(encoding="utf-8")
        fields, body = parse(text)
        name, description = fields.get("name", ""), fields.get("description", "")
        if not name or name != directory.name:
            failures.append(f"{directory}/SKILL.md: invalid or mismatched name")
        if len(description) < 8 or len(body) < 20:
            failures.append(f"{directory}/SKILL.md: incomplete description or body")
        result[name] = text
    missing = sorted(REQUIRED - result.keys())
    if missing:
        failures.append(f"missing canonical skills: {missing}")
    return result


def check_skill_package_schema(failures: list[str], base: Path | None = None) -> None:
    root = base if base is not None else SKILLS
    for directory in skill_dirs(root):
        text = (directory / "SKILL.md").read_text(encoding="utf-8")
        fields, body = parse(text)
        name = fields.get("name", "").strip().casefold()
        description = fields.get("description", "").strip()
        if name in PLACEHOLDER_VALUES:
            failures.append(f"{directory}/SKILL.md: missing or placeholder frontmatter name")
        if description.casefold() in PLACEHOLDER_VALUES or len(description) < 8:
            failures.append(f"{directory}/SKILL.md: missing or placeholder frontmatter description")
        if body.casefold() in PLACEHOLDER_VALUES or len(body) < 20:
            failures.append(f"{directory}/SKILL.md: missing or placeholder skill body")


def check_forbidden_skill_tree_tokens(failures: list[str], base: Path | None = None) -> None:
    root = base if base is not None else SKILLS
    for path in root.rglob("*"):
        if not path.is_file():
            continue
        try:
            text = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        for token in FORBIDDEN_SKILL_TREE_TOKENS:
            if token in text:
                failures.append(f"{path}: forbidden stale skill token {token!r}")


def require_review_facts(failures: list[str], label: str, text: str, facts: tuple[str, ...]) -> None:
    missing = [fact for fact in facts if fact not in text]
    if missing:
        failures.append(f"代码审查维度: {label} missing {missing}")


def check_review_skill(failures: list[str], review: str) -> None:
    rows = [
        (match.group(1), line)
        for line in review.splitlines()
        if (match := re.match(r"^\|\s*D(\d{2})\s+[^|]*\|[^|]*\|[^|]*\|\s*$", line))
    ]
    expected = [f"{index:02d}" for index in range(1, 20)]
    row_ids = [row_id for row_id, _ in rows]
    if row_ids != expected:
        failures.append(f"代码审查维度: expected one ordered D01-D19 matrix, got {row_ids}")

    row_by_id = dict(rows)
    require_review_facts(
        failures,
        "D01 canonical boundary routing",
        row_by_id.get("01", ""),
        ("DefaultBackendBoundaryRegistry()", "codemap 13"),
    )
    require_review_facts(
        failures,
        "D08 repository navigation",
        row_by_id.get("08", ""),
        ("codemap 07A", "codemap 07B", "codemap 09", "codemap 11", "codemap 12", "Dream", "命中"),
    )
    require_review_facts(
        failures,
        "D17-D19 boundaries",
        review,
        ("D17 的生产字段", "D18 回答", "D19 回答", "canonical", ".agents/skills"),
    )
    require_review_facts(
        failures,
        "D01-D19 coverage ledger",
        review,
        ("D01-D19", "coverage ledger", "Applied", "N/A + reason"),
    )
    require_review_facts(
        failures,
        "multi-lane evidence ledger",
        review,
        ("lane", "review object", "lane PASS", "repo PASS"),
    )
    require_review_facts(
        failures,
        "fix workflow",
        review,
        (
            "docs/契约/fix-workflow-convention.md",
            "Repro -> Root Cause -> RED -> Fix -> GREEN -> Guard -> Residual Retest -> Report",
        ),
    )
    require_review_facts(
        failures,
        "authoritative gate routing",
        review,
        (".githooks/pre-commit", ".githooks/pre-push", ".githooks/README.md", "scripts/ai_maintenance_gates.sh"),
    )
    if "静态命令清单" not in review and "static command list" not in review:
        failures.append("代码审查维度: authoritative gate routing missing static command list prohibition")
    require_review_facts(
        failures,
        "review object binding",
        review,
        ("worktree", "staged tree", "commit", "push range"),
    )
    require_review_facts(
        failures,
        "output schema",
        review,
        (
            "priority | dimension | coverage | reachability",
            "file:line_start-line_end",
            "violated_contract",
            "bug_locking_test",
            "gate",
        ),
    )
def parse_json_list_field(failures: list[str], path: Path, key: str) -> list[str]:
    fields, _ = parse(path.read_text(encoding="utf-8"))
    raw = fields.get(key, "")
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as exc:
        failures.append(f"{path}: {key} must be a JSON string list: {exc}")
        return []
    if not isinstance(value, list) or not all(isinstance(item, str) and item.strip() for item in value):
        failures.append(f"{path}: {key} must contain only non-empty strings")
        return []
    return [item.strip() for item in value]


def check_backend_skill_contract(failures: list[str], root: Path | None = None) -> None:
    repo = (root if root is not None else ROOT).resolve()
    backend = repo / ".agents" / "skills" / "后端"
    main = backend / "SKILL.md"
    if not main.is_file():
        failures.append(f"{main}: missing canonical backend skill")
        return

    triggers = parse_json_list_field(failures, main, "trigger_words")
    normalized = {item.casefold(): item for item in triggers}
    for word in sorted(BACKEND_REQUIRED_TRIGGER_WORDS):
        if word.casefold() not in normalized:
            failures.append(f"{main}: missing high-signal backend trigger {word!r}")
    for word in sorted(BACKEND_LOW_SIGNAL_TRIGGER_WORDS):
        if word.casefold() in normalized:
            failures.append(f"{main}: low-signal backend trigger {word!r} is forbidden")

    for relative, facts in BACKEND_REQUIRED_CONTENT.items():
        path = backend / relative
        if not path.is_file():
            failures.append(f"{path}: missing backend skill contract file")
            continue
        text = path.read_text(encoding="utf-8")
        for fact in facts:
            if fact not in text:
                failures.append(f"{path}: missing backend contract fact {fact!r}")
        for token in BACKEND_FORBIDDEN_CONTENT.get(relative, ()):
            if token in text:
                failures.append(f"{path}: forbidden backend skill pattern {token!r}")

    effective = backend / "effective_go_rules.md"
    if effective.is_file() and re.search(r"\bGo\s+1\.\d+(?:\.\d+)?\+?", effective.read_text(encoding="utf-8")):
        failures.append(f"{effective}: Go version must come from go.mod, not skill prose")
    go_mod = repo / "go.mod"
    if not go_mod.is_file() or not re.search(r"(?m)^go\s+\d+\.\d+(?:\.\d+)?\s*$", go_mod.read_text(encoding="utf-8")):
        failures.append(f"{go_mod}: missing parseable Go version source")

    rungroup = repo / "docs" / "契约" / "rungroup-convention.md"
    if not rungroup.is_file():
        failures.append(f"{rungroup}: missing RunGroup contract")
        return
    contract = rungroup.read_text(encoding="utf-8")
    for fact in ("internal/platform/runner", "internal/app.BindRuntime", 'group:"runners"'):
        if fact not in contract:
            failures.append(f"{rungroup}: missing RunGroup contract fact {fact!r}")
    if "github.com/oklog/run" in contract:
        failures.append(f"{rungroup}: forbidden RunGroup contract pattern 'github.com/oklog/run'")


def check_skills(failures: list[str], skills: dict[str, str]) -> None:
    for name, facts in FACTS.items():
        for fact in facts:
            if fact not in skills.get(name, ""):
                failures.append(f".agents/skills/{name}/SKILL.md: missing contract fact {fact!r}")
    check_review_skill(failures, skills.get("代码审查维度", ""))
    compat = skills.get("MCP服务器构建", "")
    for duplicate in ("internal/mcpserver/common", "cmd/mcp-orch", "legacy HTTP", "task_create_dag"):
        if duplicate in compat:
            failures.append(f"MCP服务器构建: compatibility entry duplicates {duplicate!r}")
    for directory in skill_dirs(SKILLS):
        for path in directory.rglob("*"):
            if not path.is_file() or path.suffix.lower() not in {".md", ".txt", ".json", ".go", ".js", ".mjs", ".py", ".ts", ".tsx"}:
                continue
            try:
                text = path.read_text(encoding="utf-8")
            except UnicodeDecodeError:
                continue
            for token in FORBIDDEN:
                if token in text:
                    failures.append(f"{path.relative_to(ROOT)}: forbidden stale token {token!r}")


def check_policy(failures: list[str]) -> None:
    path = SKILLS / ".super-dolphin-skill-policy.json"
    if not path.exists():
        return
    for entry in json.loads(path.read_text(encoding="utf-8")).get("keep_selected", []):
        selected = entry.get("selected_source_id", "")
        if selected.startswith("project/") and not (SKILLS / selected.split("/", 1)[1] / "SKILL.md").is_file():
            failures.append(f"skill policy selects missing project skill: {selected}")


def main() -> int:
    failures: list[str] = []
    skills = load_skills(failures)
    check_skill_package_schema(failures)
    check_forbidden_skill_tree_tokens(failures)
    check_skills(failures, skills)
    check_backend_skill_contract(failures)
    check_policy(failures)
    for path, facts in {"AGENTS.md": ["自动加载仅限四个技能", "使用 `字段守卫`"], "docs/doc/codemap/07-module-read.md": ["<cwd>/.agents/skills", "provider mirror 是生成物，不是 canonical 真值"]}.items():
        text = (ROOT / path).read_text(encoding="utf-8")
        for fact in facts:
            if fact not in text:
                failures.append(f"{path}: missing contract fact {fact!r}")
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    print("super-agent-v3 skill adaptation checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
