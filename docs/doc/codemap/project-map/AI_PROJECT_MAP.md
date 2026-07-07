# AI 项目地图（Super-Dolphin）

> 生成时间：2026-07-07
>
> 已索引文件：**3984**
>
> 漂移状态：**OK**（详见 `docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md`）

## 1. 项目功能总览

Super-Dolphin / super-agent-v3 是一个本地多 Agent 桌面应用与 MCP peer 体系，核心由以下能力构成：

- **桌面控制台**：`cmd/agent-terminal` 提供 Wails/Go host、HTTP/RPC 桥，`frontend-app` 提供 React/Vite 前端。
- **编排 peer**：`cmd/mcp-orch` 管理 agent 生命周期、DAG、wakeup、workspace、prompt、command card 与 shared file tools。
- **代码智能 peer**：`cmd/mcp-lsp` 提供多语言 LSP、文件搜索、结构和诊断工具。
- **业务模块层**：`internal/module` 承载 dashboard、memory、prompt、skill、thread、turn、uistate 等运行语义。
- **基础设施与 provider**：`internal/platform`、`internal/provider` 负责 RPC、hooks、toolbridge、控制面、Claude/Codex provider 集成。
- **持久化与治理**：`internal/store`、`sql`、`migrations`、`internal/archtest`、`docs/doc/codemap` 提供数据访问、schema、架构守卫和代码地图。

## 2. 索引路由表

| 索引文件 | 文件数 | 覆盖范围 |
|---|---:|---|
| `docs/doc/codemap/project-map/index/app-ui.tsv` | 288 | 桌面应用、Wails host、React/Vite 前端与 UI 测试 |
| `docs/doc/codemap/project-map/index/orchestration.tsv` | 406 | mcp-orch 编排 peer、DAG、workspace、prompt、command、shared-file 工具 |
| `docs/doc/codemap/project-map/index/modules.tsv` | 716 | 业务模块层：dashboard、memory、prompt、skill、thread、turn、uistate 等 |
| `docs/doc/codemap/project-map/index/platform-provider.tsv` | 962 | 基础设施与 provider 集成：RPC、hooks、toolbridge、Claude/Codex/统一 provider |
| `docs/doc/codemap/project-map/index/store-sql.tsv` | 306 | 持久化层：store、sqlc、SQL queries、migrations |
| `docs/doc/codemap/project-map/index/docs-agent.tsv` | 843 | 代码地图、ADR/决策、计划与 docs 项目知识 |
| `docs/doc/codemap/project-map/index/other.tsv` | 463 | 公共库、脚本、测试、配置与其他根级资源 |

每个 TSV 字段为：`path`、`module`、`domain`、`type`、`size_bytes`、`purpose`、`search_keys`。

## 3. 顶层结构

| 模块 | 文件数 | 职责 |
|---|---:|---|
| `internal` | 1923 | 应用内部模块、平台、provider、store 与守卫 |
| `docs` | 840 | 代码地图、ADR、计划、迁移和内部说明 |
| `cmd` | 641 | 可执行入口与 MCP peer |
| `frontend-app` | 286 | 其他项目资源 |
| `migrations` | 111 | 数据库 migration |
| `scripts` | 102 | 工程自动化脚本 |
| `sql` | 30 | SQL query 源文件 |
| `pkg` | 25 | 可复用公共库 |
| `test` | 9 | 测试夹具和辅助资源 |
| `third_party` | 9 | 其他项目资源 |
| `(root)` | 7 | 仓库根级配置和说明 |
| `tests` | 1 | 跨包测试资源 |

## 4. 快速定位路由

| 目标 | 首选路径 | 次选路径 | 检索关键词 |
|---|---|---|---|
| 修改桌面 Go/Wails host | `cmd/agent-terminal/` | `internal/ui/wails/` | `wails binding rpc app host` |
| 修改 React 聊天 UI | `frontend-app/src/pages/chat/` | `frontend-app/src/entities/client/model/` | `ChatPage composer timeline store sendDraft` |
| 修改 DAG 编排执行 | `cmd/mcp-orch/orchestration/` | `cmd/mcp-orch/store/taskdag/` | `dag wakeup nodeexec dispatcher retry` |
| 修改 MCP orchestration tools | `cmd/mcp-orch/tools/` | `cmd/mcp-orch/orchestration/rpc.go` | `task_dag agent_launch schema registry` |
| 修改 LSP 工具 | `cmd/mcp-lsp/tools/` | `cmd/mcp-lsp/multilsp/` | `lsp tool grep file search diagnostics` |
| 修改 thread/turn 生命周期 | `internal/module/thread/` | `internal/module/turn/` | `thread start resume fork turn provider` |
| 修改 memory/prompt/skill | `internal/module/memory/` | `internal/module/prompt/` | `memory prompt skill canonical mirror` |
| 修改 provider 接入 | `internal/provider/` | `internal/platform/toolbridge/` | `claude codex provider session manifest toolbridge` |
| 修改控制面/bootstrap | `internal/platform/mcpcontrol/` | `internal/mcpserver/common/bootstrap/` | `peer register bootstrap hooks` |
| 修改持久化/SQL | `internal/store/` | `sql/queries/` | `store sqlc migration queries` |
| 修改代码地图 | `docs/doc/codemap/` | `scripts/codemap_index.go` | `codemap ai-index make codemap-refresh` |
| 修改架构守卫 | `internal/archtest/` | `internal/archtest/baseline.json` | `guard baseline ratchet freeze` |

## 5. 维护命令

```bash
node scripts/generate_ai_project_map.js
node scripts/generate_ai_project_map.js --check
node scripts/generate_ai_project_map.js --strict-drift
```

现有手写代码地图仍以 `docs/doc/codemap/README.md` 和 `make codemap-check` / `make codemap-refresh` 为准；本目录提供低 token 的全仓文件级索引补充。
