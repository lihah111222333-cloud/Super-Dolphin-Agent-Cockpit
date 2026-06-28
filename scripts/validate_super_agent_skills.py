#!/usr/bin/env python3
"""Validate repo-local skill adaptation and provider mirror hygiene."""

from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


REQUIRED = {
    "AGENTS.md": [
        "not required to bind their lifecycle to `mcp-orch`",
        "native sub-agent or multi-agent capability",
        "Use `mcp-orch` only when",
    ],
    "CLAUDE.md": [
        "子代理不强制绑定 `mcp-go-agent-orchestration`",
        "原生子代理/多代理能力",
        "可选使用 `task_create_dag`",
    ],
    ".agent/skills/使用超能力/references/codex-tools.md": [
        "子代理生命周期不绑定 mcp-orch",
        "Codex 多代理是本仓库允许的正常派发路径",
        "不要伪造 DAG/node/run 证据",
    ],
    ".agent/skills/子代理驱动开发/SKILL.md": [
        "子代理生命周期不强制绑定 mcp-orch",
        "缺少 mcp-go-agent-orchestration 工具不是阻断条件",
        "未使用 mcp-orch 时伪造 DAG/node/run 证据",
    ],
    ".agent/skills/调度并行代理/SKILL.md": [
        "并行代理不强制绑定 mcp-orch",
        "缺少 mcp-go-agent-orchestration 工具不是阻断条件",
        "不需要伪造 DAG/node 证据",
    ],
    ".agent/skills/并行代理调度/SKILL.md": [
        "平台原生并行代理可直接使用",
        "不要因为没有 mcp-go-agent-orchestration 就阻断派发",
    ],
    ".agent/skills/子代理开发/SKILL.md": [
        "平台原生子代理直接派发",
        "不要把生命周期强制绑定到 mcp-orch",
        "没有 mcp-go-agent-orchestration 工具不是阻断条件",
    ],
    ".agent/skills/请求代码审查/SKILL.md": [
        "可选创建审查 DAG/node",
        "没有 mcp-go-agent-orchestration 工具不是阻断条件",
    ],
    ".agent/skills/MCP协议/SKILL.md": [
        "internal/mcpserver/common",
        "stdio MCP",
        "legacy HTTP",
        "cmd/mcp-orch/sql/queries/task_dag",
    ],
    ".agent/skills/MCP服务器构建/SKILL.md": [
        "internal/mcpserver/common",
        "stdio MCP",
        "legacy HTTP",
    ],
    ".agent/skills/前端/SKILL.md": [
        "frontend-app",
        "cmd/agent-terminal/web-dist",
        "React/Vite",
    ],
    ".agent/skills/UI设计/SKILL.md": [
        "frontend-app",
        "不默认引入 Tailwind",
        "前端",
    ],
    ".agent/skills/TailwindCSS样式规范/SKILL.md": [
        "仅当用户明确要求",
        "super-agent-v3 默认不引入 Tailwind",
    ],
    ".agent/skills/ui-ux-design/SKILL.md": [
        "super-agent-v3 路由约束",
        "前端",
        "只有显式要求或依赖已存在时",
    ],
    ".agent/skills/测试规范/SKILL.md": [
        "./scripts/test_with_guard.sh",
        "make guard",
        "python3 scripts/validate_super_agent_skills.py",
        "canonical 与 mirror 一致",
    ],
    ".agent/skills/测试驱动开发/SKILL.md": [
        "./scripts/test_with_guard.sh path/to/file.go",
        "cd frontend-app && npm test",
    ],
    ".agent/skills/注释规范/SKILL.md": [
        "函数级中文注释",
        "internal/archtest/guardlib.go",
    ],
    ".agent/skills/代码审查维度/SKILL.md": [
        "super-agent-v3",
        "mcp-orch 是可选编排面",
        "不得把子代理生命周期强制绑定到 DAG",
        "frontend-app",
        "SQLite/sqlc",
        "无效豁免",
        "pre-push",
        "validate_super_agent_skills.py",
        "canonical `.agent/skills`",
        "D18 旧兼容路径",
        "没有活跃调用方",
        "兼容路径不得替代主路径",
    ],
    ".agent/skills/后端/project_structure.md": [
        "internal/platform/db",
        "modernc.org/sqlite",
        "cmd/mcp-orch/sql/queries",
    ],
    ".agent/skills/后端/testing_pitfalls.md": [
        'sql.Open("sqlite", ":memory:")',
        "t.Fatalf",
    ],
    "docs/契约/sqlc-convention.md": [
        'engine: "sqlite"',
        "internal/platform/db/sqlite/migrations",
        "modernc.org/sqlite",
        "cmd/mcp-orch/sql/queries",
    ],
    ".agent/skills/refactoring-guardrails/SKILL.md": [
        "禁止静默兜底",
        "return Output{}, fmt.Errorf",
        "./scripts/test_with_guard.sh",
    ],
    ".agent/skills/Git原子提交规范/SKILL.md": [
        "codex/",
        "git status --short",
        "git diff --cached --check",
    ],
    ".agent/skills/Git工作树/SKILL.md": [
        ".worktrees/",
        "codex/",
        "使用git工作区",
    ],
    ".agent/skills/结束开发分支/SKILL.md": [
        "./scripts/test_with_guard.sh",
        "frontend-app",
        "git status --short",
    ],
    ".agent/skills/完成开发分支/SKILL.md": [
        "结束开发分支",
        "frontend-app",
        "git status --short",
    ],
    ".agent/skills/编写技能/SKILL.md": [
        ".agent/skills",
        ".agents/skills",
        "不要强制绑定 `mcp-orch`",
        "可选编排路径",
    ],
    ".agent/skills/架构设计/SKILL.md": [
        "frontend-app",
        "SQLite + sqlc",
        "internal/provider",
    ],
    ".agent/skills/日志与错误处理/SKILL.md": [
        "stdio MCP",
        "fail-fast",
        "./scripts/test_with_guard.sh",
    ],
    ".agent/skills/安全工程师/SKILL.md": [
        "sqlc",
        "MCP tool",
        "fail-fast",
    ],
    ".agent/skills/全量项目地图生成/SKILL.md": [
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
    ".agent/skills/super-dolphin-workflow/SKILL.md": [
        "/Users/ai/Desktop/Super-Dolphin",
    ],
    ".agent/skills/MCP协议/SKILL.md": [
        "go-agent-orchestration",
        "核心配置",
        "github.com/mark3labs",
        "、`sql/queries/task_dag*`",
    ],
    ".agent/skills/vue3/SKILL.md": [
        "当用户需要在 V3 仓库的前端进行开发时加载",
    ],
    ".agent/skills/使用git工作区/SKILL.md": [
        "添加到 .gitignore + 提交该变更",
    ],
    ".agent/skills/使用超能力/references/codex-tools.md": [
        "task_start_node",
        "只有在用户仍要求",
        "子代理强制编排",
        "所有子代理工作必须先进入 mcp-orch",
        "不要启动子代理",
        "不能替代 mcp-orch DAG 状态",
        "除非用户明确要求绕过本仓库编排规则",
    ],
    ".agent/skills/子代理驱动开发/SKILL.md": [
        "task_start_node",
        "降级为 Codex 多代理 fallback",
        "强制前置",
        "生命周期必须先进入 mcp-orch",
        "不要启动子代理",
    ],
    ".agent/skills/调度并行代理/SKILL.md": [
        "task_start_node",
        "Codex 多代理 fallback，则用多个",
        "强制要求",
        "并行代理必须通过 mcp-orch",
        "不要启动子代理",
        "不要把它当成本仓库的子代理执行路径",
    ],
    ".agent/skills/并行代理调度/SKILL.md": [
        "task_start_node",
        "Codex 多代理 fallback",
        "先用 `task_create_dag` 建 DAG",
        "只能改为单代理只读分析或等待工具可用",
    ],
    ".agent/skills/子代理开发/SKILL.md": [
        "task_start_node",
        "Codex fallback",
        "都必须是 mcp-orch",
        "不要启动子代理",
    ],
    ".agent/skills/请求代码审查/SKILL.md": [
        "task_start_node",
        "再使用 Codex 多代理 fallback",
        "优先创建审查 DAG/node，而不是裸派发后台任务",
        "不要启动子代理",
    ],
    ".agent/skills/编写技能/SKILL.md": [
        "task_start_node",
        "工具缺失时说明 fallback",
        "涉及子代理时必须写入",
        "先 mcp-orch DAG/run/node",
        "工具缺失时停止",
    ],
    ".agent/skills/执行计划/SKILL.md": [
        "task_start_node",
        "任何子代理执行都必须先进入 mcp-orch",
    ],
    ".agent/skills/编写计划/SKILL.md": [
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
    ".agent/skills/后端/project_structure.md": [
        "database/        # 数据库连接池",
        "pgxpool",
    ],
    ".agent/skills/后端/testing_pitfalls.md": [
        'sql.Open("sqlite3"',
        "db, _ :=",
    ],
}


NO_TASK_TOOL_TEMPLATES = [
    ".agent/skills/子代理驱动开发/implementer-prompt.md",
    ".agent/skills/子代理驱动开发/spec-reviewer-prompt.md",
    ".agent/skills/子代理驱动开发/code-quality-reviewer-prompt.md",
    ".agent/skills/编写计划/plan-document-reviewer-prompt.md",
    ".agent/skills/头脑风暴/spec-document-reviewer-prompt.md",
]

STALE_TOKEN_PATHS = [
    ".agent/skills/代码审查维度",
    ".agent/skills/全量项目地图生成",
    ".agent/skills/后端",
    ".agent/skills/日志与错误处理",
    ".agent/skills/架构设计",
    ".agent/skills/测试规范",
    ".agent/skills/测试驱动开发",
    ".agent/skills/完成前验证",
    ".agent/skills/接收代码审查",
    ".agent/skills/请求代码审查",
    ".agent/skills/系统化调试",
    ".agent/skills/编写技能",
    ".agent/skills/refactoring-guardrails",
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
    return {
        str(p.relative_to(base))
        for p in base.rglob("*")
        if p.is_file()
    }


def check_mirror(failures: list[str], mirror_rel: str, *, required: bool) -> None:
    canonical_root = ROOT / ".agent/skills"
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
            if (canonical_dir / rel).read_bytes() != (mirror_dir / rel).read_bytes():
                failures.append(f"{mirror_rel}/{name}/{rel}: differs from canonical")


def check_policy_hashes(failures: list[str]) -> None:
    policy_path = ROOT / ".agent/skills/.super-dolphin-skill-policy.json"
    if not policy_path.exists():
        return
    policy = json.loads(policy_path.read_text(encoding="utf-8"))
    for entry in policy.get("keep_selected", []):
        selected = entry.get("selected_source_id", "")
        if selected.startswith("project/"):
            name = selected.split("/", 1)[1]
            skill = ROOT / ".agent/skills" / name / "SKILL.md"
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
            skill = ROOT / ".agent/skills" / name / "SKILL.md"
            if not skill.exists():
                failures.append(f"policy source project skill missing: {name}")
                continue
            digest = hashlib.sha256(skill.read_bytes()).hexdigest()
            if source.get("content_hash") != digest:
                failures.append(f"policy source hash mismatch: {name}")


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

    check_mirror(failures, ".agents/skills", required=True)
    check_mirror(failures, ".claude/skills", required=False)
    check_policy_hashes(failures)

    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1

    print("super-agent-v3 skill adaptation checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
