#!/usr/bin/env python3
"""Validate repo-local skill adaptation and provider mirror hygiene."""

from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]

MIRROR_IGNORED_REL_FILES = {
    "references/ui-styling/scripts/.coverage",
}


REQUIRED = {
    "CLAUDE.md": [
        "子代理不强制绑定 `mcp-go-agent-orchestration`",
        "原生子代理/多代理能力",
        "可选使用 `task_create_dag`",
    ],
    ".agents/skills/使用超能力/references/codex-tools.md": [
        "子代理生命周期不绑定 mcp-orch",
        "Codex 多代理是本仓库允许的正常派发路径",
        "不要伪造 DAG/node/run 证据",
    ],
    ".agents/skills/子代理驱动开发/SKILL.md": [
        "子代理生命周期不强制绑定 mcp-orch",
        "缺少 mcp-go-agent-orchestration 工具不是阻断条件",
        "未使用 mcp-orch 时伪造 DAG/node/run 证据",
    ],
    ".agents/skills/调度并行代理/SKILL.md": [
        "并行代理不强制绑定 mcp-orch",
        "缺少 mcp-go-agent-orchestration 工具不是阻断条件",
        "不需要伪造 DAG/node 证据",
    ],
    ".agents/skills/并行代理调度/SKILL.md": [
        "平台原生并行代理可直接使用",
        "不要因为没有 mcp-go-agent-orchestration 就阻断派发",
    ],
    ".agents/skills/子代理开发/SKILL.md": [
        "平台原生子代理直接派发",
        "不要把生命周期强制绑定到 mcp-orch",
        "没有 mcp-go-agent-orchestration 工具不是阻断条件",
    ],
    ".agents/skills/请求代码审查/SKILL.md": [
        "可选创建审查 DAG/node",
        "没有 mcp-go-agent-orchestration 工具不是阻断条件",
    ],
    ".agents/skills/MCP协议/SKILL.md": [
        "internal/mcpserver/common",
        "stdio MCP",
        "legacy HTTP",
        "cmd/mcp-orch/sql/queries/task_dag",
    ],
    ".agents/skills/MCP服务器构建/SKILL.md": [
        "internal/mcpserver/common",
        "stdio MCP",
        "legacy HTTP",
    ],
    ".agents/skills/前端/SKILL.md": [
        "frontend-app",
        "cmd/agent-terminal/web-dist",
        "React/Vite",
    ],
    ".agents/skills/UI设计/SKILL.md": [
        "frontend-app",
        "不默认引入 Tailwind",
        "前端",
    ],
    ".agents/skills/TailwindCSS样式规范/SKILL.md": [
        "仅当用户明确要求",
        "super-agent-v3 默认不引入 Tailwind",
    ],
    ".agents/skills/ui-ux-design/SKILL.md": [
        "super-agent-v3 路由约束",
        "前端",
        "只有显式要求或依赖已存在时",
    ],
    ".agents/skills/测试规范/SKILL.md": [
        "./scripts/test_with_guard.sh",
        "make guard",
        "python3 scripts/validate_super_agent_skills.py",
        "canonical 与 mirror 一致",
    ],
    ".agents/skills/测试驱动开发/SKILL.md": [
        "./scripts/test_with_guard.sh path/to/file.go",
        "cd frontend-app && npm test",
    ],
    ".agents/skills/注释规范/SKILL.md": [
        "函数级中文注释",
        "internal/archtest/guardlib.go",
    ],
    ".agents/skills/代码审查维度/SKILL.md": [
        "super-agent-v3",
        "## 详细模式",
        "## 18 维详细审查表",
        "| D18 | DRY",
        "## 维度参考要求",
        "| D01 | 当前改动所属 codemap 分卷或 README 架构图",
        "| D18 | 重复规则或重复实现出现的位置",
        "## D01 类型分类",
        "## D04 典型症状/判定场景",
        "## D18 类型分类",
        "## D18 DRY 要求",
        "## D18 典型症状/判定场景",
        "## 使用方式",
        "mcp-orch 是可选编排面",
        "不得把子代理生命周期强制绑定到 DAG",
        "frontend-app",
        "SQLite/sqlc",
        "无效豁免",
        "pre-push",
        "validate_super_agent_skills.py",
        "canonical `.agents/skills`",
    ],
    ".agents/skills/后端/project_structure.md": [
        "internal/platform/db",
        "internal/app/modules.go",
        "store.Module 是明确的聚合例外",
        "modernc.org/sqlite",
        "DAG 编排由独立 `mcp-orch` MCP server 承担",
        "cmd/mcp-orch/sql/queries",
    ],
    ".agents/skills/后端/SKILL.md": [
        "internal/app/modules.go",
        "internal/platform/db/module.go",
        "internal/store/module.go",
        "SQLite schema",
        "provider-native mirror",
    ],
    ".agents/skills/后端/testing_pitfalls.md": [
        'sql.Open("sqlite", ":memory:")',
        "t.Fatalf",
    ],
    "docs/契约/sqlc-convention.md": [
        'engine: "sqlite"',
        "internal/platform/db/sqlite/migrations",
        "modernc.org/sqlite",
        "cmd/mcp-orch/sql/queries",
    ],
    ".agents/skills/refactoring-guardrails/SKILL.md": [
        "禁止静默兜底",
        "return Output{}, fmt.Errorf",
        "./scripts/test_with_guard.sh",
    ],
    ".agents/skills/Git原子提交规范/SKILL.md": [
        "codex/",
        "git status --short",
        "git diff --cached --check",
    ],
    ".agents/skills/Git工作树/SKILL.md": [
        ".worktrees/",
        "codex/",
        "使用git工作区",
    ],
    ".agents/skills/使用git工作区/SKILL.md": [
        'git worktree add "$path" -b "$branch" "$base_branch"',
        "不要把 `git worktree add ...` 拼成字符串变量再执行",
        '如果必须动态组装命令，使用 shell 数组并以 `"${cmd[@]}"` 执行',
    ],
    ".agents/skills/结束开发分支/SKILL.md": [
        "./scripts/test_with_guard.sh",
        "frontend-app",
        "git status --short",
    ],
    ".agents/skills/完成开发分支/SKILL.md": [
        "结束开发分支",
        "frontend-app",
        "git status --short",
    ],
    ".agents/skills/编写技能/SKILL.md": [
        ".agents/skills",
        "历史 `.agent/skills`",
        "不要强制绑定 `mcp-orch`",
        "可选编排路径",
    ],
    ".agents/skills/架构设计/SKILL.md": [
        "frontend-app",
        "SQLite + sqlc",
        "internal/provider",
        "docs/契约/README.md",
        "docs/架构/README.md",
        "docs/契约/onion-architecture-convention.md",
        "docs/架构/skeleton-fx.md",
        "源码、测试和 LSP 证据仍是事实来源",
    ],
    ".agents/skills/日志与错误处理/SKILL.md": [
        "stdio MCP",
        "fail-fast",
        "./scripts/test_with_guard.sh",
    ],
    ".agents/skills/安全工程师/SKILL.md": [
        "sqlc",
        "MCP tool",
        "fail-fast",
    ],
    ".agents/skills/全量项目地图生成/SKILL.md": [
        "docs/doc/codemap",
        "make codemap-check",
        "frontend-app",
    ],
}


FORBIDDEN = {
    "AGENTS.md": [
        "task_start_node",
        "Sub-agents MUST use",
    ],
    "CLAUDE.md": [
        "任务生命周期管理与协同",
        "必须使用 `mcp-go-agent-orchestration`",
        "task_start_node",
    ],
    ".agents/skills/super-dolphin-workflow/SKILL.md": [
        "/Users/ai/Desktop/Super-Dolphin",
    ],
    ".agents/skills/MCP协议/SKILL.md": [
        "go-agent-orchestration",
        "核心配置",
        "github.com/mark3labs",
        "、`sql/queries/task_dag*`",
    ],
    ".agents/skills/使用git工作区/SKILL.md": [
        "添加到 .gitignore + 提交该变更",
    ],
    ".agents/skills/使用超能力/references/codex-tools.md": [
        "task_start_node",
        "只有在用户仍要求",
        "子代理强制编排",
        "所有子代理工作必须先进入 mcp-orch",
        "不要启动子代理",
        "不能替代 mcp-orch DAG 状态",
        "除非用户明确要求绕过本仓库编排规则",
    ],
    ".agents/skills/子代理驱动开发/SKILL.md": [
        "task_start_node",
        "降级为 Codex 多代理 fallback",
        "强制前置",
        "生命周期必须先进入 mcp-orch",
        "不要启动子代理",
    ],
    ".agents/skills/调度并行代理/SKILL.md": [
        "task_start_node",
        "Codex 多代理 fallback，则用多个",
        "强制要求",
        "并行代理必须通过 mcp-orch",
        "不要启动子代理",
        "不要把它当成本仓库的子代理执行路径",
    ],
    ".agents/skills/并行代理调度/SKILL.md": [
        "task_start_node",
        "Codex 多代理 fallback",
        "先用 `task_create_dag` 建 DAG",
        "只能改为单代理只读分析或等待工具可用",
    ],
    ".agents/skills/子代理开发/SKILL.md": [
        "task_start_node",
        "Codex fallback",
        "都必须是 mcp-orch",
        "不要启动子代理",
    ],
    ".agents/skills/请求代码审查/SKILL.md": [
        "task_start_node",
        "再使用 Codex 多代理 fallback",
        "优先创建审查 DAG/node，而不是裸派发后台任务",
        "不要启动子代理",
    ],
    ".agents/skills/编写技能/SKILL.md": [
        "task_start_node",
        "工具缺失时说明 fallback",
        "涉及子代理时必须写入",
        "先 mcp-orch DAG/run/node",
        "工具缺失时停止",
    ],
    ".agents/skills/执行计划/SKILL.md": [
        "task_start_node",
        "任何子代理执行都必须先进入 mcp-orch",
    ],
    ".agents/skills/编写计划/SKILL.md": [
        "task_start_node",
        "subagents must be represented by mcp-orch",
    ],
    "docs/契约/sqlc-convention.md": [
        'engine: "postgresql"',
        "PostgreSQL 继续作为唯一主库",
        "pgxpool",
        "CopyFrom",
        "sql_package: \"pgx",
    ],
    ".agents/skills/后端/project_structure.md": [
        "database/        # 数据库连接池",
        "pgxpool",
    ],
    ".agents/skills/后端/testing_pitfalls.md": [
        'sql.Open("sqlite3"',
        "db, _ :=",
    ],
}


NO_TASK_TOOL_TEMPLATES = [
    ".agents/skills/子代理驱动开发/implementer-prompt.md",
    ".agents/skills/子代理驱动开发/spec-reviewer-prompt.md",
    ".agents/skills/子代理驱动开发/code-quality-reviewer-prompt.md",
    ".agents/skills/编写计划/plan-document-reviewer-prompt.md",
    ".agents/skills/头脑风暴/spec-document-reviewer-prompt.md",
]

STALE_TOKEN_PATHS = [
    ".agents/skills/代码审查维度",
    ".agents/skills/全量项目地图生成",
    ".agents/skills/后端",
    ".agents/skills/日志与错误处理",
    ".agents/skills/架构设计",
    ".agents/skills/测试规范",
    ".agents/skills/测试驱动开发",
    ".agents/skills/完成前验证",
    ".agents/skills/接收代码审查",
    ".agents/skills/请求代码审查",
    ".agents/skills/系统化调试",
    ".agents/skills/编写技能",
    ".agents/skills/refactoring-guardrails",
    "docs/契约/sqlc-convention.md",
]

STALE_TOKENS = [
    "wjboot",
    "WJBoot",
    "go -C backend",
    "./cmd/code_guard",
    "commit_gate",
    "GORM 数据库",
    "MySQL",
    "量化交易",
    "回测",
    "pgxpool",
    'engine: "postgresql"',
]

FORBIDDEN_MIRROR_SKILLS = {
    "Docker容器化部署",
    "GORM数据库操作",
    "MySQL高可用运维",
    "Python量化机器学习",
    "Swagger文档规范",
    "WebSocket实时通信",
    "gRPC服务设计",
    "go汇编优化-x86",
    "产品经理",
    "任务编排",
    "子代理业务分析",
    "性能优化实践",
    "技能查找",
    "数据采集开发",
    "文档索引导航",
    "系统性调试",
    "运维工程师",
    "量化架构",
    "量化架构设计",
    "量化策略算法开发",
    "需求澄清提问",
    "验收官",
    "token",
}


def read(path: str) -> str:
    full_path = ROOT / path
    if not full_path.exists():
        raise AssertionError(f"missing {path}")
    return full_path.read_text(encoding="utf-8")


def text_files(path: Path) -> list[Path]:
    if path.is_file():
        return [path]
    return sorted(
        p for p in path.rglob("*")
        if p.is_file() and p.suffix.lower() in {".md", ".txt", ".ts", ".tsx", ".go", ".py", ".json"}
    )


def skill_dirs(base: Path) -> list[Path]:
    if not base.exists():
        return []
    return sorted(p for p in base.iterdir() if p.is_dir() and (p / "SKILL.md").is_file())


def rel_files(base: Path) -> set[str]:
    files: set[str] = set()
    for p in base.rglob("*"):
        if not p.is_file():
            continue
        rel = str(p.relative_to(base))
        if rel in MIRROR_IGNORED_REL_FILES:
            continue
        files.add(rel)
    return files


def normalize_mirror_bytes(data: bytes) -> bytes:
    """Normalize provider mirror whitespace that should not change skill meaning."""
    lines = data.replace(b"\r\n", b"\n").split(b"\n")
    lines = [line.rstrip(b" \t") for line in lines]
    while lines and not lines[-1]:
        lines.pop()
    return b"\n".join(lines)


def mirror_bytes_equal(canonical: bytes, mirror: bytes) -> bool:
    """Compare generated mirrors while tolerating checkout-safe whitespace cleanup."""
    if canonical == mirror:
        return True
    return normalize_mirror_bytes(canonical) == normalize_mirror_bytes(mirror)


def check_mirror(failures: list[str], mirror_rel: str, *, required: bool) -> None:
    canonical_root = ROOT / ".agents/skills"
    mirror_root = ROOT / mirror_rel
    if not mirror_root.exists():
        if required:
            failures.append(f"{mirror_rel}: missing provider mirror")
        return
    if mirror_root.is_symlink():
        failures.append(f"{mirror_rel}: provider mirror must not be a symlink")
        return

    canonical_names = {p.name for p in skill_dirs(canonical_root)}
    mirror_names = {p.name for p in skill_dirs(mirror_root)}
    extra = sorted(mirror_names - canonical_names)
    missing = sorted(canonical_names - mirror_names)
    if extra:
        failures.append(f"{mirror_rel}: extra non-canonical skills {extra}")
    if missing:
        failures.append(f"{mirror_rel}: missing canonical skills {missing}")

    forbidden = sorted(mirror_names & FORBIDDEN_MIRROR_SKILLS)
    if forbidden:
        failures.append(f"{mirror_rel}: forbidden old/global skills {forbidden}")

    for name in sorted(canonical_names & mirror_names):
        canonical_dir = canonical_root / name
        mirror_dir = mirror_root / name
        canonical_files = rel_files(canonical_dir)
        mirror_files = rel_files(mirror_dir)
        if canonical_files != mirror_files:
            failures.append(f"{mirror_rel}/{name}: file set differs from canonical")
            continue
        for rel in sorted(canonical_files):
            if not mirror_bytes_equal((canonical_dir / rel).read_bytes(), (mirror_dir / rel).read_bytes()):
                failures.append(f"{mirror_rel}/{name}/{rel}: differs from canonical")


def check_policy_hashes(failures: list[str]) -> None:
    policy_path = ROOT / ".agents/skills/.super-dolphin-skill-policy.json"
    if not policy_path.exists():
        return
    policy = json.loads(policy_path.read_text(encoding="utf-8"))
    for entry in policy.get("keep_selected", []):
        selected = entry.get("selected_source_id", "")
        if selected.startswith("project/"):
            name = selected.split("/", 1)[1]
            skill = ROOT / ".agents/skills" / name / "SKILL.md"
            if not skill.exists():
                failures.append(f"policy selected project skill missing: {name}")
                continue
            digest = hashlib.sha256(skill.read_bytes()).hexdigest()
            if entry.get("selected_content_hash") != digest:
                failures.append(f"policy selected_content_hash mismatch: {name}")
        for source in entry.get("sources", []):
            canonical = source.get("canonical_id", "")
            if not canonical.startswith("project/"):
                continue
            name = canonical.split("/", 1)[1]
            skill = ROOT / ".agents/skills" / name / "SKILL.md"
            if not skill.exists():
                failures.append(f"policy source project skill missing: {name}")
                continue
            digest = hashlib.sha256(skill.read_bytes()).hexdigest()
            if source.get("content_hash") != digest:
                failures.append(f"policy source hash mismatch: {name}")


def check_review_dimension_sections(failures: list[str]) -> None:
    rel_path = ".agents/skills/代码审查维度/SKILL.md"
    text = read(rel_path)
    for i in range(1, 19):
        dim = f"D{i:02d}"
        for needle in (
            f"| {dim} |",
            f"## {dim} 类型分类",
            f"## {dim} 典型症状/判定场景",
        ):
            if needle not in text:
                failures.append(f"{rel_path}: missing {needle!r}")
    if "## D18 DRY 要求" not in text:
        failures.append(f"{rel_path}: missing '## D18 DRY 要求'")
    for needle in (
        "同一规则、字段清单",
        "DRY 简化必须保留 D01 架构边界、D02 fail-fast、D10 安全边界和 D17 字段守卫",
        "若重复代码承载不同 provider 语义",
    ):
        if needle not in text:
            failures.append(f"{rel_path}: missing D18 DRY anchor {needle!r}")

    start_marker = "## 维度参考要求"
    end_marker = "## D01 类型分类"
    try:
        start = text.index(start_marker)
        end = text.index(end_marker, start)
    except ValueError:
        failures.append(f"{rel_path}: missing review dimension reference section")
        return

    reference_section = text[start:end]
    for i in range(1, 19):
        dim = f"D{i:02d}"
        matching_rows = [
            line for line in reference_section.splitlines()
            if line.startswith(f"| {dim} |")
        ]
        if not matching_rows:
            failures.append(f"{rel_path}: missing {dim} row in review dimension reference section")
            continue
        cells = [cell.strip() for cell in matching_rows[0].strip().strip("|").split("|")]
        if len(cells) != 3 or not cells[1] or not cells[2]:
            failures.append(f"{rel_path}: incomplete {dim} reference row")


def main() -> int:
    failures: list[str] = []

    for rel_path, needles in REQUIRED.items():
        try:
            text = read(rel_path)
        except AssertionError as exc:
            failures.append(str(exc))
            continue
        for needle in needles:
            if needle not in text:
                failures.append(f"{rel_path}: missing {needle!r}")

    check_review_dimension_sections(failures)

    for rel_path, needles in FORBIDDEN.items():
        path = ROOT / rel_path
        if not path.exists():
            continue
        text = path.read_text(encoding="utf-8")
        for needle in needles:
            if needle in text:
                failures.append(f"{rel_path}: forbidden {needle!r}")

    for rel_path in NO_TASK_TOOL_TEMPLATES:
        text = read(rel_path)
        if "Task tool" in text:
            failures.append(f"{rel_path}: forbidden Task tool template")

    for rel_path in STALE_TOKEN_PATHS:
        path = ROOT / rel_path
        if not path.exists():
            failures.append(f"missing {rel_path}")
            continue
        for file_path in text_files(path):
            try:
                text = file_path.read_text(encoding="utf-8")
            except UnicodeDecodeError:
                continue
            for needle in STALE_TOKENS:
                if needle in text:
                    rel = file_path.relative_to(ROOT)
                    failures.append(f"{rel}: stale token {needle!r}")

    check_mirror(failures, ".claude/skills", required=False)
    check_policy_hashes(failures)

    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1

    print("super-agent-v3 skill adaptation checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
