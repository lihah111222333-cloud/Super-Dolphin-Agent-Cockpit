# AI 项目地图（Super-Dolphin）

> 已索引文件：**4579**
>
> 扫描规则：allowlisted project files; excludes: .git/**, .idea/**, .claude/**, .workspace/**, .worktrees/**, .agent/code_exec/**, .agent/workspaces/**, .agnet/report/**, .agnet/shared/**, bin/**, reports/**, docs/archive/**, **/node_modules/**, **/dist/**, **/web-dist/**, **/coverage/**, **/.vite/**, **/.tmp/**, **/tmp/**, **/.gocache/**, **/.gomodcache/**, **/.npm-cache/**, docs/doc/codemap/project-map/**, docs/doc/codemap/ai-index.json, go.sum, test_output.txt, naked_go.txt
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

| 索引文件 | 文件数 | 大小 | 覆盖范围 |
|---|---:|---:|---|
| `docs/doc/codemap/project-map/index/app-ui.tsv` | 610 | 124.2 KB | 桌面应用、Wails host、React/Vite 前端与 UI 测试 |
| `docs/doc/codemap/project-map/index/orchestration.tsv` | 391 | 88.0 KB | mcp-orch 编排 peer、DAG、workspace、prompt、command、shared-file 工具 |
| `docs/doc/codemap/project-map/index/modules.tsv` | 732 | 142.7 KB | 业务模块层：dashboard、memory、prompt、skill、thread、turn、uistate 等 |
| `docs/doc/codemap/project-map/index/platform-provider.tsv` | 1031 | 189.8 KB | 基础设施与 provider 集成：RPC、hooks、toolbridge、Claude/Codex/统一 provider |
| `docs/doc/codemap/project-map/index/store-sql.tsv` | 204 | 29.8 KB | 持久化层：store、sqlc、SQL queries、migrations |
| `docs/doc/codemap/project-map/index/docs-agent.tsv` | 944 | 169.2 KB | 代码地图、ADR/决策、计划与 docs 项目知识 |
| `docs/doc/codemap/project-map/index/other.tsv` | 667 | 130.1 KB | 公共库、脚本、测试、配置与其他根级资源 |

**检索示例：**

```bash
# 1) 先读此 MAP.md 确定目标域
# 2) 搜索对应 TSV 分片
rg "thread.*resume|fork" docs/doc/codemap/project-map/index/modules.tsv
rg "provider.*manifest|toolbridge" docs/doc/codemap/project-map/index/platform-provider.tsv
rg "lsp.*diagnostics|grep" docs/doc/codemap/project-map/index/platform-provider.tsv
rg "ChatPage|composer|timeline" docs/doc/codemap/project-map/index/app-ui.tsv
# 3) 打开目标源码和同包测试
rg --line-number "func .*Resume|func .*Fork" internal/module/thread -g '*.go'
```

## 3. 顶层结构

| 模块 | 文件数 | 职责 |
|---|---:|---|
| `internal` | 2165 | 应用内部模块、平台、provider、store 与守卫 |
| `docs` | 936 | 代码地图、ADR、计划、迁移和内部说明 |
| `cmd` | 648 | 可执行入口与 MCP peer |
| `frontend-app` | 608 | 当前 React/Vite 新 UI |
| `scripts` | 132 | 工程自动化脚本 |
| `pkg` | 30 | 可复用公共库 |
| `sql` | 29 | SQL query 源文件 |
| `(root)` | 12 | 仓库根级配置和说明 |
| `test` | 9 | 测试夹具和辅助资源 |
| `third_party` | 9 | 第三方参考材料 |
| `tests` | 1 | 跨包和脚本级测试资源 |

## 4. 运行入口地图

| 运行单元 | 入口文件 | 默认端口/端点 | 说明 |
|---|---|---|---|
| Desktop host | `cmd/agent-terminal/main.go` | local desktop host | Wails desktop host, HTTP/RPC bridge, frontend embed host |
| MCP orchestration peer | `cmd/mcp-orch/main.go` | stdio / managed peer | Agent lifecycle, DAG, wakeup, workspace and shared file tools |
| MCP LSP peer | `cmd/mcp-lsp/main.go` | stdio / managed peer | Generic multi-language LSP peer and code intelligence tools |
| React UI | `frontend-app/src/main.jsx` | 5175 dev server | Current React/Vite frontend entry |
| macOS dev runner | `run-new-ui-desktop.sh` | 5175 dev UI + local desktop host | Desktop host plus Vite dev flow |
| Windows dev runner | `run-new-ui-desktop.ps1` | 5175 dev UI + local desktop host | PowerShell desktop host plus Vite dev flow |

## 5. Root Fx 装配阅读顺序

`internal/app/modules.go` 是根装配清单，不是严格的业务执行时序。阅读时先按下面的依赖层理解，再用 Fx graph tests 确认供给点是否闭合。

| 步骤 | 层 | 锚点 | AI 阅读提示 |
|---:|---|---|---|
| 1 | Root shell | `internal/app/modules.go` | NewLogger、pidregistry、config/db/bus/rpc/hooks/runner/observability；先读作基础设施供给层，不读作业务执行顺序。 |
| 2 | Persistence and control plane | `internal/store、internal/platform/mcpcontrol、internal/mcpserver` | store 与 MCP 控制面先提供持久化、peer 注册、server/bootstrap 能力，后续 module 通过 contract 端口消费。 |
| 3 | Store adapters | `internal/app/storeadapter` | 把 canonical Store 实现投影为业务 module 消费的窄端口；按 domain child 路由，业务映射留在各 child。 |
| 4 | Business semantics | `internal/module/{dashboard,memory,prompt,skill,thread,turn,uistate}` | memory/prompt/skill 支撑 thread/turn；thread 负责 start/resume/fork 绑定真相源，turn 负责回合执行与审批调度，uistate 投影事件给 UI。 |
| 5 | Provider and tools | `internal/provider/{unified,codexapp}、internal/platform/toolbridge` | unified 管 session/manifest，codexapp 提供 provider driver，toolbridge 把 host/MCP tools 暴露给 provider；claudecli 当前不在 root Module 中启用。 |
| 6 | Runtime adapters | `internal/app/runtimeadapter` | 为 mcpcontrol/toolbridge/cachekeepalive/builtintools 等 runtime consumer 提供窄端口与 root-scope 接线。 |
| 7 | Root adapters | `internal/app/modules.go:fx.Provide` | AsRPCRunner、DAGRuntime、thread.OrchestrationFacade、RuntimeReporter、SessionPorts 是仍由 root 持有的跨边界裁剪端口。 |
| 8 | Runtime owner | `internal/app/app.go、internal/app/runner.go` | newFXApp/newDesktopFXApp 叠加 Module + BindRuntime；桌面态额外装 uiwails.Module；实际 start/stop 由 Fx 依赖图与 group:"runners" 决定。 |
| 9 | Graph guards | `internal/app/modules_graph_test.go、internal/archtest/fx_graph_test.go` | ValidateApp 与定向 Populate 测试冻结 app 图闭合、thread/turn 配置、toolbridge lifecycle、datasource、workflowtemplate、orchestration facade 等供给点。 |

## 6. 快速定位路由

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
| 理解 root Fx 装配顺序 | `internal/app/modules.go` | `internal/app/modules_graph_test.go` | `app module fx graph modules runtime order toolbridge provider` |
| 修改 App adapter 分包 | `internal/app/storeadapter/` | `internal/app/runtimeadapter/` | `store runtime adapter` |
| 修改控制面/bootstrap | `internal/platform/mcpcontrol/` | `internal/mcpserver/common/bootstrap/` | `peer register bootstrap hooks` |
| 修改持久化/SQL | `internal/store/` | `sql/queries/` | `store sqlc migration queries` |
| 修改代码地图 | `docs/doc/codemap/` | `scripts/codemap_index.go` | `codemap ai-index make codemap-refresh` |
| 修改架构守卫 | `internal/archtest/` | `internal/archtest/freeze_baseline.json` | `guard baseline ratchet freeze` |
| 查 AI maintenance gates | `scripts/ai_maintenance/` | `.githooks/pre-push` | `ai maintenance gates validation local hooks generated files` |
| 查 runtime skill 行为 | `internal/module/skill/` | `internal/provider/shared/provider_home.go` | `skill canonical mirror provider home personal hub` |
| 查 LSP 工作流规则 | `docs/internal-notes/LSP系统提示词.md` | `cmd/mcp-lsp/tools/` | `lsp diagnostics grep inspect xref` |
| 查 provider bridge | `internal/provider/` | `internal/platform/toolbridge/` | `provider manifest session toolbridge codex claude` |

## 7. 重点子系统地图

### internal/app assembly and adapters

| 子系统 | 文件数 | 职责 |
|---|---:|---|
| `internal/app/storeadapter` | 50 | 业务 Store 到 module 窄端口的适配器 |
| `internal/app/runtimeadapter` | 9 | runtime consumer 的 Store/module 窄端口适配器 |
| `internal/app/internal/storeguard` | 2 | adapter 共享的 typed-nil fail-fast 检查 helper |
| `internal/app` | 91 | 全域汇总（root + adapter packages） |

### internal/module

| 子系统 | 文件数 | 职责 |
|---|---:|---|
| `internal/module/thread` | 109 | thread start/resume/fork/stop 生命周期与绑定真相源 |
| `internal/module/turn` | 68 | turn 启动、执行、审批与 provider 调度 |
| `internal/module/prompt` | 91 | prompt 模板、启用条件与 system prompt 组装 |
| `internal/module/memory` | 151 | memory canonical 管理、检索与持久化接线 |
| `internal/module/skill` | 95 | skill canonical 管理与 provider-native mirror |
| `internal/module/uistate` | 55 | UI 事件投影与 timeline/sidebar 状态 |

### internal/platform

| 子系统 | 文件数 | 职责 |
|---|---:|---|
| `internal/platform/rpc` | 43 | JSON-RPC transport、dispatch、push 与审批框架 |
| `internal/platform/mcpcontrol` | 35 | MCP 控制平面与 peer 注册 |
| `internal/platform/toolbridge` | 80 | provider 与 MCP tools 桥接 |
| `internal/platform/hooks` | 33 | hook 配置、执行与三阶段拦截 |
| `internal/platform/config` | 8 | 运行配置、env、provider 与超时策略 |

### internal/provider

| 子系统 | 文件数 | 职责 |
|---|---:|---|
| `internal/provider/codexapp` | 126 | Codex app/server provider 集成 |
| `internal/provider/claudecli` | 88 | Claude CLI provider 集成 |
| `internal/provider/shared` | 20 | provider home、配置和共享 helpers |
| `internal/provider/unified` | 30 | 统一 provider 会话解析与 manifest |

### cmd peers

| 子系统 | 文件数 | 职责 |
|---|---:|---|
| `cmd/mcp-orch/tools` | 76 | mcp-orch MCP tool schema、registry 与 handler |
| `cmd/mcp-orch/orchestration` | 175 | agent 生命周期、DAG、wakeup、report 与 hook 消费 |
| `cmd/mcp-lsp/tools` | 63 | LSP MCP tools 实现 |
| `cmd/mcp-lsp/multilsp` | 72 | 多语言 LSP manager、transport 与缓存 |

## 8. 文档与知识地图

- 主线文档（L1）：`README.md`、`docs/doc/codemap/README.md`、`docs/adr/*`、`docs/decisions/*`
- 工作文档（L2）：`docs/plans/*`、`docs/internal-notes/*`
- 历史归档（L3）：`docs/archive/`（默认不递归索引）
- Agent 体系：`.agents/skills/*/SKILL.md` 是 repo-local skill 指令入口；不要把 `.agents` 当作普通项目源码递归扫描。

## 9. 索引字段说明

| 字段 | 含义 |
|---|---|
| `path` | 相对路径 |
| `module` | 顶层模块 |
| `domain` | project-map 分片域 |
| `type` | 文件类型 |
| `size_bytes` | 文件大小（字节） |
| `purpose` | 文件职责说明 |
| `search_keys` | 建议检索关键词 |

## 10. 维护命令

```bash
node scripts/generate_ai_project_map.mjs
node scripts/generate_ai_project_map.mjs --check
node scripts/generate_ai_project_map.mjs --strict-drift
node scripts/generate_ai_project_map.mjs --rules path/to/overrides.json
```

现有手写代码地图仍以 `docs/doc/codemap/README.md` 和 `make codemap-check` / `make codemap-refresh` 为准；本目录提供低 token 的全仓文件级索引补充。
