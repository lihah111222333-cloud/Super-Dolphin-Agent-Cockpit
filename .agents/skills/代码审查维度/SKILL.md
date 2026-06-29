---
name: 代码审查维度
description: 当在 super-agent-v3 仓库做代码审查、生产风险审计、环形审查、裁决子代理发现或编排修复任务时使用；尤其适用于 Go/Wails/MCP/provider/skill/runtime/store/frontend 变更。
aliases: ["@代码审查维度", "@review-dimensions"]
---

# super-agent-v3 代码审查维度

## 先定边界

1. 先看 `git status --short`，保留 unrelated dirty 文件。
2. 路径发现按 README、`docs/doc/codemap/README.md`、相关 codemap、源码/测试顺序。
3. 行为判断以源码和测试为准，再看 `docs/decisions`、`docs/adr`、`docs/契约`。
4. 不要使用其他仓库的路径、命令或业务领域审查维度。

## 维度

| 维度 | 审查重点 |
|---|---|
| D01 架构边界 | `cmd` / `internal/app` / `internal/contract` / `internal/module` / `internal/platform` / `internal/provider` / `internal/store` 依赖方向 |
| D02 Fail-fast | 配置缺失、字段缺失、provider/tool 错误不能静默兜底 |
| D03 MCP 协议 | stdio 帧、legacy HTTP 兼容、tool schema、payload envelope、stdout 污染 |
| D04 LSP 工具 | workspace root、range edit、replace/update、诊断和多语言边界 |
| D05 Provider/runtime | Claude/Codex adapter、provider home、skill mirror、toolbridge、turn/session 生命周期 |
| D06 Orchestration/DAG/Cron/Wakeup | mcp-orch 是可选编排面，不得把子代理生命周期强制绑定到 DAG；审查真实 mcp-orch 改动时再覆盖 `task_create_dag`、`task_start_dag`、`task_dispatch_node`、`task_update_node` 和 `task_dag_apply_ops` |
| D07 Store/sqlc | SQLite/sqlc 查询、migration、事务、幂等、baseline 数据 |
| D08 Skill/Memory/Prompt/Thread | canonical skill root、provider mirror、prompt snapshot、memory/dream/auto-dream、thread resume/fork |
| D09 Frontend | 默认 `frontend-app` React/Vite；旧 Vue 前端已删除；Wails bridge 和状态边界 |
| D10 Security | secrets、命令注入、路径穿越、权限、tool 审批、日志泄露 |
| D11 Observability | 结构化日志、错误码、状态可解释、诊断不吞证据 |
| D12 Testing | 单文件 guard、受影响包测试、前端 lint/test/build、SQL/codemap 验证；skill 文档改动跑 `python3 scripts/validate_super_agent_skills.py` 和 `git diff --check` |
| D13 Release/Install | package/embed、manifest、update/install 签名和平台差异 |
| D14 Performance | 热路径、轮询、watcher、并发泄漏、后台任务托管 |
| D15 UX/Product | 真实用户路径、状态反馈、失败提示、避免隐藏阻塞 |
| D16 Git/Workflow | owned files、atomic commit、no `git add .`、dirty 边界和 worktree 清理 |
| D17 字段守卫 | 生产字段新增后，消费侧未登记必须有测试失败；检查枚举、注册表、豁免、fail-first、CI/pre-push 和禁止兜底 |

## 字段守卫精简要求

出现“生产字段 -> 消费侧”映射时，审查必须确认：

1. 生产字段由反射、AST、类型系统或 schema 自动枚举；不得把手工字段数组当事实源。
2. 每个生产字段都在 mapper、select、snapshot、DTO 或 contract registry 中显式登记，或在豁免表中写明 `Field`、`Direction`、`Reason`；空原因、暂时不用、以后再加、不知道用途都按无效豁免处理。
3. 新增字段未登记时至少一个自动化测试 fail；不得用默认值、空结构、吞错或兼容旧字段掩盖漂移。
4. 单向 mapper 标明方向；双向 mapper 做 roundtrip；map、slice、pointer 字段按需验证深拷贝。
5. 新增守卫必须有 fail-first 证据：测试名、临时破坏后的失败摘要、恢复后的通过命令。
6. 守卫必须进入 CI、pre-push 或仓库强制门禁；仅本地可运行不算通过，靠降低 baseline、删除 snapshot、注释测试通过的修改按 P1 处理。

## Skill 文档审查补充

审查 repo-local skill 变更时，还要确认 canonical `.agent/skills` 是事实源，`.agents/skills` 与 `.claude/skills` mirror 没有漂移；若 `.agent/skills/.super-dolphin-skill-policy.json` 已登记该 skill，必须同步对应 sha256 hash。

## 输出格式

审查 finding 使用：

```text
severity | dimension | file:line | problem | fix
```

严重级别：

- `P0`：数据损坏、secret 泄露、核心路径不可用、误导系统继续错误执行。
- `P1`：发布阻塞、契约破坏、fail-fast 破坏、测试/工具链结果不可信。
- `P2`：边界错误、诊断不准、默认体验退化、可维护性风险。
- `P3`：文档、命名、非阻塞清理。

每条 finding 必须能被源码、测试、命令输出或文档契约支撑；不能只写感觉。
