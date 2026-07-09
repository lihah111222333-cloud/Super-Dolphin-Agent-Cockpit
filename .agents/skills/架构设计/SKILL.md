---
name: 架构设计
description: 当在 super-agent-v3 中评估或修改模块边界、依赖方向、fx 装配、MCP sidecar、provider/runtime、store 或 frontend 架构时使用。
aliases: ["@架构设计", "@architecture"]
---

# super-agent-v3 架构设计

## 事实来源

源码、测试和 LSP 证据仍是事实来源。做架构判断时先用 README / codemap 缩小路径，再用 LSP 确认定义、引用、调用层级和 diagnostics；文档用于解释已经接受的约定，不替代源码真值。

## 项目入口

| 入口 | 作用 |
| --- | --- |
| `README.md` | 当前目录布局、运行入口、SQLite / provider-native skill mirror 等运行事实。 |
| `docs/doc/codemap/README.md` | 大模块导航；按 01-12 分卷缩小阅读边界。 |
| `docs/契约/README.md` | 工程契约索引：模块边界、fx、rungroup、jrpc2、MCP、sqlc、fail-fast、状态机与事件。 |
| `docs/架构/README.md` | 当前框架骨架索引：Fx、RunGroup、jrpc2、event surface、stateless、code guard。 |
| `docs/decisions/*.md` / `docs/adr/*.md` | 已接受的架构决策。 |

## 当前结构

- `cmd/agent-terminal`：Wails desktop host + HTTP/RPC bridge；桌面主进程入口。
- `cmd/mcp-orch`：独立 orchestration / DAG / cron MCP sidecar；不要嵌入桌面进程根图。
- `cmd/mcp-lsp`：generic multi-language LSP MCP peer；不要降级成单语言 gopls 语境。
- `cmd/mcp-ida`：IDA / 外部分析 MCP peer。
- `internal/app`：根 Fx 装配和跨边界 adapter；典型入口是 `internal/app/modules.go`。
- `internal/contract`：跨模块窄接口、DTO 和哨兵错误；不放实现。
- `internal/module`：业务模块，如 thread、turn、prompt、memory、skill、dashboard、uistate、cron。
- `internal/platform`：基础设施薄适配层，如 db、rpc、bus、hooks、mcpcontrol、runner、statemachine、toolbridge。
- `internal/provider`：Codex / Claude / unified provider adapter；provider-native mirror 在启动/acquire 前 reconciled。
- `internal/store`：SQLite + sqlc persistence；`internal/store/module.go` 是 store 根聚合例外。
- `frontend-app`：当前 React/Vite UI；`cmd/agent-terminal/web-dist` 是 embed 产物同步目录。

## 文档矩阵

| 主题 | 先看契约 | 再看骨架 | 代码地图 |
| --- | --- | --- | --- |
| 模块边界 / 依赖方向 | `docs/契约/modularity-convention.md`, `docs/契约/onion-architecture-convention.md` | `docs/架构/skeleton-fx.md` | `docs/doc/codemap/04-app-contract.md`, `07-module.md`, `08-platform.md` |
| Fx 装配 / 生命周期 | `docs/契约/fx-convention.md`, `docs/契约/rungroup-convention.md` | `docs/架构/skeleton-fx.md`, `docs/架构/fx-rungroup-skeleton.md`, `docs/架构/skeleton-rungroup.md` | `04-app-contract.md`, `08-platform.md` |
| RPC / MCP sidecar | `docs/契约/jrpc2-convention.md`, `docs/契约/mcp-service-convention.md` | `docs/架构/skeleton-jrpc2.md` | `02-mcp-orch.md`, `03-mcp-lsp-ida.md`, `06-mcpserver.md` |
| Store / SQL | `docs/契约/sqlc-convention.md` | `docs/架构/skeleton-code-guard.md` | `10-store.md` |
| 状态机 / 事件面 | `docs/契约/statemachine-event-convention.md`, `docs/契约/workflow-runtime-state-contract.md` | `docs/架构/skeleton-stateless.md`, `docs/架构/skeleton-event.md` | `08-platform.md`, `11-memory-prompt-thread.md` |
| Fail-fast / 修复闭环 | `docs/契约/fail-fast-convention.md`, `docs/契约/fix-workflow-convention.md` | `docs/架构/skeleton-code-guard.md` | 受影响分卷 |

## 设计规则

1. 不存在独立后端子模块，不要套用旧项目的数据库 ORM、业务领域或单体目录映射。
2. 依赖方向优先遵守 `docs/契约/modularity-convention.md` 和 `docs/契约/onion-architecture-convention.md`。
3. runtime 装配优先看 `fx.Module`、constructor pattern、`group:"runners"` 和现有 adapter；不要绕过根图新建全局单例。
4. MCP 工具壳在 `cmd/mcp-*`，通用协议在 `internal/mcpserver/common`；`cmd/mcp-orch` 是独立 sidecar，不是桌面主进程内部模块。
5. Store 默认 SQLite + sqlc；`internal/store/module.go` 可聚合 store 子包，但业务逻辑不放在根聚合层。
6. provider mirror 是生成物；skill canonical truth 在 `.agents/skills`，历史 `.agent/skills` 不作为入口。
7. 当前前端是 `frontend-app`；旧 Vue 前端已删除，不再作为编辑或验证目标。
8. 涉及异常、空配置或缺失依赖时按 fail-fast 契约处理；不要新增静默默认值、吞错或隐式兜底。

## 验证

文档-only 架构改动至少跑：

```bash
git diff --check -- docs/契约 docs/架构 .agents/skills scripts/validate_super_agent_skills.py
python3 scripts/validate_super_agent_skills.py
```

架构/guard 或跨模块 Go 改动追加：

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
make guard
```

按受影响包追加 `./scripts/test_with_guard.sh <packages> -count=1`；涉及 codemap、project-map 或 capability-contract 时追加对应 `make codemap-check`、`make project-map-check`、`make capcontract-check`。
