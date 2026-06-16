# P8 执行计划 v4 — MCP 编排工具族（从 V3 现有实现抽取，不复制 V2）

> 修订：2026-03-22
> 取证基线：`internal/sidecar/orch/orchestration/*`、`internal/module/workspace/*`、`internal/module/skill/*`、`internal/module/dashboard/prompt_rpc.go`、`internal/store/sharedfile/*`、`cmd/mcp-orch/*`、`internal/mcpserver/common/*`、`docs/契约/*`
> 说明：任务要求中的 `docs/doc/codemap/ai-index.json` 当前仓库不存在；本版以下列代码与契约文档为准。

---

## 0. 策略

本节引用核心层职责原则：agent-terminal 只负责 Agent 管理、工具管理（MCP manifest 构建与注入）、Hooks，以及暴露 `ctl/*` RPC 接口；核心层不启动 MCP 进程。编排、DAG、Task、Workspace、Prompt、Command Card、Shared File 等能力必须下沉到独立 `cmd/mcp-orch` binary。MCP 进程是共享服务，`agent_id` 必须从 tool call 参数传入，而不是在 bootstrap 阶段绑定固定 agent。

### 0.1 结论

P8 不再走“从 V2 直接复制 5k 行工具代码”的路线。

`cmd/mcp-orch` 是独立二进制，并且在 P8 终态下要做到**运行时只共享配置、DB 基础设施和数据契约**。
P8 的迁移价值，来自把 `internal/sidecar/orch/orchestration/*`、其依赖的 store 包，以及对应 sqlc 查询/生成代码一起迁到 `cmd/mcp-orch/*`，让 orch-family MCP 服务形成完整自包含对象图。

本期目标是：

1. 以 `internal/sidecar/orch/orchestration/*` 整体迁移 + 相关 `internal/store/*` / `internal/store/sqlc/*` 复制迁移为唯一实现基线。
2. 把 orchestration、DAG、workspace、command、prompt、shared file 的 MCP tool definition、schema、manifest、registry 迁到 `cmd/mcp-orch/`。
3. `internal/sidecar/orch/orchestration/` 在迁移后整体删除；agent runtime、report、runtime snapshot、DAG 全部归 `cmd/mcp-orch` 持有。
4. `cmd/mcp-orch` 同时承载 MCP 协议适配、orchestration owner runtime、本地 store 层和本地 sqlc 层。
5. `cmd/mcp-orch` 运行时只共享 `internal/platform/{config,db}`、`internal/contract/*`、`internal/dto/*`；不再 import `internal/module/*`、`internal/store/*`、`internal/store/sqlc/*` 或 `internal/mcpserver/common`。

### 0.1.1 必须复用的 V3 框架层

| 层 | P8 要求 | 说明 |
| --- | --- | --- |
| `internal/platform/config` | 必须 import 复用 | 复用最小配置读取与运行时参数 |
| `internal/platform/db` | 必须 import 复用 | 复用数据库连接、事务与 sqlc 基础设施 |
| `internal/contract/*` | 必须 import 复用 | 统一领域契约、事件与接口定义 |
| `internal/dto/*` | 必须 import 复用 | 统一 wire shape 与宿主/MCP 共享 DTO |
| `internal/sidecar/orch/orchestration/*` | 整体迁移后删除 | 迁移目标是 `internal/sidecar/orch/orchestration/*`；迁移完成后核心层不再保留该目录 |
| `internal/store/taskdag` | 复制到本地后禁止依赖 | 迁移目标是 `internal/sidecar/orch/store/taskdag/*`；当前宿主仍依赖内部版本，因此基线策略是 copy+keep |
| `internal/store/workspace` | 复制到本地后禁止依赖 | 迁移目标是 `internal/sidecar/orch/store/workspace/*`；当前宿主仍依赖内部版本，因此基线策略是 copy+keep |
| `internal/store/prompt` | 复制到本地后禁止依赖 | 迁移目标是 `internal/sidecar/orch/store/prompt/*`；当前宿主仍依赖内部版本，因此基线策略是 copy+keep |
| `internal/store/commandcard` | 复制到本地后禁止依赖 | 迁移目标是 `internal/sidecar/orch/store/commandcard/*`；当前宿主仍依赖内部版本，因此基线策略是 copy+keep |
| `internal/store/sharedfile` | 复制到本地后禁止依赖 | 迁移目标是 `internal/sidecar/orch/store/sharedfile/*`；当前宿主仍依赖内部版本，因此基线策略是 copy+keep |
| `internal/store/binding` | 按 xref 结果决定是否复制 | 当前 source tree 未发现 orchestration 直连，但该包被宿主 thread/provider 使用；若迁移后的 orchestration 需要它，则复制到 `internal/sidecar/orch/store/binding/*` 并保留内部版本 |
| `internal/store/sqlc/*` | 复制到本地后禁止依赖 | 迁移目标是 `internal/sidecar/orch/store/sqlc/*`；只复制 orch-family 依赖的查询与生成代码 |
| 其他 `internal/module/*` | 禁止依赖 | `cmd/mcp-orch` 对核心模块层零依赖，不 import service、facade、rpc、module、events |
| 其他 `internal/store/*` | 禁止依赖 | `cmd/mcp-orch` 对核心 store 层零依赖；只使用本地复制后的 `internal/sidecar/orch/store/*` |
| `internal/app` | 禁止依赖 | 不把桌面宿主装配带入 MCP binary |
| 其他 `cmd/*` | 禁止依赖 | MCP family 与其他入口保持隔离 |

### 0.2 关键决策

| 设计问题 | 决策 | 代码证据 |
| --- | --- | --- |
| `cmd/mcp-orch` 是否直接持有 orchestration runtime | 是。agent 生命周期、report、runtime snapshot、DAG 都随 `internal/sidecar/orch/orchestration/*` 一起迁入 `internal/sidecar/orch/orchestration/*`。 | `internal/sidecar/orch/orchestration/service.go:35-84` 持有 `agents map[string]*agentRuntime`、`*exec.Cmd`、`SubmissionQueue`、状态机等进程内状态；`dag.go` 已独立承担 DAG 逻辑。 |
| MCP tool handler 是调用什么后端 | `orchestration_*` / `task_*` 调本地 `internal/sidecar/orch/orchestration/*`；`workspace_*`、`command_*`、`prompt_*`、`shared_file_*` 调本地 `internal/sidecar/orch/store/*`；本地 store 再调用 `internal/sidecar/orch/store/sqlc/*`；禁止 raw SQL。 | `internal/store/taskdag/store.go`、`internal/store/workspace/store.go`、`internal/store/prompt/store.go`、`internal/store/commandcard/store.go`、`internal/store/sharedfile/store.go` 都直接落到 sqlc 查询。 |
| 如何复用 V3 而不破坏 MCP 独立性 | orchestration、相关 store 包和依赖的 sqlc 查询/生成代码都复制到 `cmd/mcp-orch`；运行时只共享 config/db/contract/dto。 | xref 结果显示候选 store 包当前都存在宿主消费者，因此基线策略是“复制到 `cmd/mcp-orch`，内部版本先保留”。 |
| `cmd/mcp-orch` 是否复用 `internal/app.Module` | 否。只组装最小模块集。 | `internal/app/modules.go:26-50` 会拉起 dashboard、skill、thread、turn、orchestration、uistate、workspace、provider 等整套桌面应用模块。 |
| `cmd/mcp-orch` 是否必须 import V3 主框架 | 是，但只允许复用 `internal/platform/{config,db}`、`internal/contract/*`、`internal/dto/*`；`internal/store/*`、`internal/store/sqlc/*` 只作为迁移源，不作为运行时依赖。 | `internal/platform/config/config.go` 与 `internal/platform/db/*` 提供最小基础设施；store/sqlc 逻辑迁移后在 `cmd/mcp-orch` 内自持。 |

### 0.3 范围边界

- 工具清单按 20 项列示，但本期交付只包含 19 项。
- 本期 19 项中，8 项来自迁移后的 `internal/sidecar/orch/orchestration/*`（5 个 `orchestration_*` + 3 个 `task_*`），11 项调用迁移后的本地 store 层（5 个 `workspace_*` + 2 个 `command_*` + 2 个 `prompt_*` + 2 个 `shared_file_*`）。
- `task_start_node` 在当前 V3 中只有 `taskdag.Store` 的 wakeup/lease 原语，没有完整 controller 语义，只能明确延后，不计入本期交付，也不进入 manifest。

---

## 1. 工具清单（20 项：19 本期交付 + 1 延后）

| # | MCP 工具名 | V3 现有源位置 | 当前 V3 surface | P8 处理方式 |
| --- | --- | --- | --- | --- |
| 1 | `orchestration_launch_agent` | `internal/sidecar/orch/orchestration/{service.go,rpc.go,contract.go}` | `agent.launch -> svc.LaunchAgent` | 随 orchestration 模块整体迁移；tool 直接调用本地 `internal/sidecar/orch/orchestration` |
| 2 | `orchestration_send_message` | `internal/sidecar/orch/orchestration/{service.go,rpc.go,contract.go}` | `agent.submit` / `agent.submitPrompt` -> `svc.SubmitTurn` | 随 orchestration 模块整体迁移；tool 直接调用本地 `internal/sidecar/orch/orchestration` |
| 3 | `orchestration_stop_agent` | `internal/sidecar/orch/orchestration/{service.go,rpc.go}` | `agent.stop -> svc.StopAgent` | 随 orchestration 模块整体迁移；tool 直接调用本地 `internal/sidecar/orch/orchestration` |
| 4 | `orchestration_list_agents` | `internal/sidecar/orch/orchestration/{service.go,rpc.go}` | `agent.list -> svc.ListAgents` | 随 orchestration 模块整体迁移；tool 直接调用本地 `internal/sidecar/orch/orchestration` |
| 5 | `orchestration_get_agent_report` | `internal/sidecar/orch/orchestration/{report.go,rpc.go,contract.go}` | `agent.getReport` / `orchestration/report` -> `svc.GetReport` | 随 orchestration 模块整体迁移；tool 直接调用本地 `internal/sidecar/orch/orchestration` |
| 6 | `task_create_dag` | `internal/sidecar/orch/orchestration/{dag.go,contract.go,rpc.go}` | `task/dag/create -> svc.CreateDAG` | 随 orchestration 模块整体迁移；DAG 逻辑收口到 `internal/sidecar/orch/orchestration/dag.go` |
| 7 | `task_get_dag` | `internal/sidecar/orch/orchestration/{dag.go,contract.go,rpc.go}` | `task/dag/get -> svc.GetDAG` | 随 orchestration 模块整体迁移；DAG 逻辑收口到 `internal/sidecar/orch/orchestration/dag.go` |
| 8 | `task_update_node` | `internal/sidecar/orch/orchestration/{dag.go,contract.go,rpc.go}` | `task/node/update -> svc.UpdateNodeStatus` | 随 orchestration 模块整体迁移；DAG 逻辑收口到 `internal/sidecar/orch/orchestration/dag.go` |
| 9 | `task_start_node` | 当前仅有 `taskdag.Store` wakeup/lease 原语：`internal/store/taskdag/contract.go:26-39` | 无现成 controller 入口 | 延后；不计入本期交付，不进入 manifest |
| 10 | `workspace_create_run` | `internal/store/workspace/{contract.go,store.go}` + `sql/queries/workspace_run.sql` + `internal/store/sqlc/workspace_run.sql.go` | `workspace.Store.UpsertRun` + 本地文件系统装配 | 迁移到 `internal/sidecar/orch/store/workspace/*` + `internal/sidecar/orch/store/sqlc/*`，tool 只调本地 store |
| 11 | `workspace_get_run` | `internal/store/workspace/{contract.go,store.go}` + `sql/queries/workspace_run.sql` + `internal/store/sqlc/workspace_run.sql.go` | `workspace.Store.GetRun` | 迁移到本地 `internal/sidecar/orch/store/workspace/*` |
| 12 | `workspace_list_runs` | `internal/store/workspace/{contract.go,store.go}` + `sql/queries/workspace_run.sql` + `internal/store/sqlc/workspace_run.sql.go` | `workspace.Store.ListRuns` | 迁移到本地 `internal/sidecar/orch/store/workspace/*` |
| 13 | `workspace_merge_run` | `internal/store/workspace/{contract.go,store.go}` + `sql/queries/workspace_run.sql` + `internal/store/sqlc/workspace_run.sql.go` | `workspace.Store.GetRun/TransitionRunStatus` + merge 流程 | 迁移到本地 `internal/sidecar/orch/store/workspace/*` |
| 14 | `workspace_abort_run` | `internal/store/workspace/{contract.go,store.go}` + `sql/queries/workspace_run.sql` + `internal/store/sqlc/workspace_run.sql.go` | `workspace.Store.UpdateRunStatus/TransitionRunStatus` | 迁移到本地 `internal/sidecar/orch/store/workspace/*` |
| 15 | `prompt_list` | `internal/store/prompt/{contract.go,store.go}` + `sql/queries/prompt_template.sql` + `internal/store/sqlc/prompt_template.sql.go` | `prompt.Store.List` | 迁移到本地 `internal/sidecar/orch/store/prompt/*`；同时保留宿主 UI 入口 |
| 16 | `prompt_get` | `internal/store/prompt/{contract.go,store.go}` + `sql/queries/prompt_template.sql` + `internal/store/sqlc/prompt_template.sql.go` | `prompt.Store.Get` | 迁移到本地 `internal/sidecar/orch/store/prompt/*`；过滤逻辑留在本地 adapter |
| 17 | `command_list` | `internal/store/commandcard/{contract.go,store.go}` + `sql/queries/command_card.sql` + `internal/store/sqlc/command_card.sql.go` | `commandcard.Store.List` | 迁移到本地 `internal/sidecar/orch/store/commandcard/*` |
| 18 | `command_get` | `internal/store/commandcard/{contract.go,store.go}` + `sql/queries/command_card.sql` + `internal/store/sqlc/command_card.sql.go` | `commandcard.Store.Get` | 迁移到本地 `internal/sidecar/orch/store/commandcard/*` |
| 19 | `shared_file_read` | `internal/store/sharedfile/{contract.go,store.go}` + `sql/queries/shared_file.sql` + `internal/store/sqlc/shared_file.sql.go` | `sharedfile.Store.Get` | 迁移到本地 `internal/sidecar/orch/store/sharedfile/*`；不下沉 SQL |
| 20 | `shared_file_write` | `internal/store/sharedfile/{contract.go,store.go}` + `sql/queries/shared_file.sql` + `internal/store/sqlc/shared_file.sql.go` | `sharedfile.Store.Upsert` | 迁移到本地 `internal/sidecar/orch/store/sharedfile/*`；不下沉 SQL |

### 1.1 与前端直接耦合的现有入口

- `prompts/list` / `prompts/write` / `prompts/delete` 已被前端页面直接调用：`cmd/agent-terminal/frontend/vue-app/pages/SystemPromptPage.js:123,151,168`。
- 因此 prompt 逻辑不能在 P8 中“搬走后删除”；MCP 侧只允许新增本地 `internal/sidecar/orch/store/*` + `internal/sidecar/orch/store/sqlc/*` adapter，同时保留宿主 RPC 入口。

---

## 2. 迁移源清单：V3 现有实现 → `cmd/mcp-orch/`

| V3 源文件 | 抽取内容 | 目标位置建议 | 保留 / 清理策略 |
| --- | --- | --- | --- |
| `internal/sidecar/orch/orchestration/*` | `service.go`、`dag.go`、`report.go`、`runtime.go`、`helpers.go`、`contract.go` 等整包逻辑 | `internal/sidecar/orch/orchestration/*`；tool schema 落在 `internal/sidecar/orch/tools/*` | 迁移完成后删除 `internal/sidecar/orch/orchestration/` 整个目录，并更新 `internal/app/modules.go` |
| `internal/store/taskdag/*` + `sql/queries/task_dag.sql` + `internal/store/sqlc/task_dag.sql.go` | `task_*` 依赖的 DAG store 与 sqlc 层 | `internal/sidecar/orch/store/taskdag/*` + `internal/sidecar/orch/store/sqlc/*` | xref 显示宿主 dashboard 仍依赖 `taskdag.Store`；因此当前基线是 copy+keep |
| `internal/store/workspace/*` + `sql/queries/workspace_run.sql` + `internal/store/sqlc/workspace_run.sql.go` | `workspace_*` 的本地 store 与 sqlc 层 | `internal/sidecar/orch/store/workspace/*` + `internal/sidecar/orch/store/sqlc/*` | xref 显示宿主 `internal/module/workspace/service.go` 仍依赖 `workspace.Store`；因此当前基线是 copy+keep |
| `internal/store/prompt/*` + `sql/queries/prompt_template.sql` + `internal/store/sqlc/prompt_template.sql.go` | `prompt_*` 的本地 store 与 sqlc 层 | `internal/sidecar/orch/store/prompt/*` + `internal/sidecar/orch/store/sqlc/*` | xref 显示宿主 `dashboard/prompt_service.go` / `prompt_rpc.go` 仍依赖 `prompt.Store`；因此当前基线是 copy+keep |
| `internal/store/commandcard/*` + `sql/queries/command_card.sql` + `internal/store/sqlc/command_card.sql.go` | `command_*` 的本地 store 与 sqlc 层 | `internal/sidecar/orch/store/commandcard/*` + `internal/sidecar/orch/store/sqlc/*` | xref 显示宿主 `module/skill` 与 `dashboard/service.go` 仍依赖 `commandcard.Store`；因此当前基线是 copy+keep |
| `internal/store/sharedfile/*` + `sql/queries/shared_file.sql` + `internal/store/sqlc/shared_file.sql.go` | `shared_file_*` 的本地 store 与 sqlc 层 | `internal/sidecar/orch/store/sharedfile/*` + `internal/sidecar/orch/store/sqlc/*` | xref 显示宿主 `uistate/config_rpc.go` 与 `dashboard/service.go` 仍依赖 `sharedfile.Store`；因此当前基线是 copy+keep |
| `internal/store/binding/*` + `sql/queries/{agent_provider_binding.sql,thread_binding.sql}` + `internal/store/sqlc/agent_provider_binding.sql.go` | 若迁移后的 orchestration 需要 provider/thread 绑定，则一并复制 | `internal/sidecar/orch/store/binding/*` + `internal/sidecar/orch/store/sqlc/*` | xref 显示宿主 `module/thread` 与 `provider/unified/session_resolver.go` 仍依赖 `binding.Store`；当前 source tree 未发现 orchestration 直连 |

### 2.1 不迁移的内容

- 不复制 V2 `pkg/toolsdk/tools/*`、`tooladapter/*`、`internal/mcp/*`。
- 不保留 `internal/sidecar/orch/orchestration/*` 与 `internal/sidecar/orch/orchestration/*` 双份实现；P8 完成后核心层必须删干净。
- 不让 `cmd/mcp-orch` 在运行时 import `internal/store/*`、`internal/store/sqlc/*`；它们只允许作为迁移源。
- 不把其他 `internal/module/*/rpc.go` 整包 import 后原样暴露成 MCP；否则会把非 P8 方法面一起泄露出去。
- 不把 `internal/app.Module` 整包塞进 `cmd/mcp-orch`。

---

## 3. MCP 服务架构

### 3.1 目标形态

```text
MCP Client
  -> stdio
  -> cmd/mcp-orch local MCP runtime / registry / manifest
  -> internal/sidecar/orch/orchestration   (orchestration_* / task_*)
       -> internal/sidecar/orch/store/taskdag
            -> internal/sidecar/orch/store/sqlc
  -> internal/sidecar/orch/store/workspace   (workspace_*)
       -> internal/sidecar/orch/store/sqlc
  -> internal/sidecar/orch/store/commandcard (command_*)
       -> internal/sidecar/orch/store/sqlc
  -> internal/sidecar/orch/store/prompt      (prompt_*)
       -> internal/sidecar/orch/store/sqlc
  -> internal/sidecar/orch/store/sharedfile  (shared_file_*)
       -> internal/sidecar/orch/store/sqlc
  -> shared layers: internal/platform/{config,db} + internal/contract/* + internal/dto/*
```

### 3.2 装配原则

1. `cmd/mcp-orch/fx.go` 只做 assembly entry。
2. `fx` 负责构造依赖；长跑 stdio server 通过 `run.Group` 托管，不在 constructor 里阻塞。
3. `cmd/mcp-orch` 只注册 orch-family tool definitions；MCP runtime / registry / manifest 也收口在 `cmd/mcp-orch/*`。
4. family-specific server、registry、runtime、resource facade、store、sqlc 只允许放在 `cmd/mcp-orch/*`。
5. MCP handler 只做协议翻译、schema 校验、DTO mapping、错误映射；业务调用必须落到本地 orchestration 包或本地 store 层。
6. `cmd/mcp-orch` 是共享 MCP 服务；`agent_id` 只从 tool call 参数进入业务层，不通过 `GO_AGENT_*_AGENT_ID` env 做进程级绑定。

### 3.3 `cmd/mcp-orch/fx.go` 最小模块集

- 必要共享层：`internal/platform/config`、`internal/platform/db`、`internal/contract/*`、`internal/dto/*`
- 必要本地 sqlc 层：`internal/sidecar/orch/store/sqlc/*`
- 必要本地 store 层：`internal/sidecar/orch/store/taskdag`、`internal/sidecar/orch/store/workspace`、`internal/sidecar/orch/store/commandcard`、`internal/sidecar/orch/store/prompt`、`internal/sidecar/orch/store/sharedfile`，必要时加 `internal/sidecar/orch/store/binding`
- 必要本地业务层：`internal/sidecar/orch/orchestration/*` 作为迁移后的 orchestration owner runtime
- 本地协议层：`internal/sidecar/orch/tools/*`、本地 registry、manifest 与 stdio server

Go `internal` 规则说明：

- `cmd/mcp-orch` 与 `internal/` 同在模块根下，因此可合法 import `internal/*`。
- 合法 import 不代表可以任意耦合；P8 明确禁止 `cmd/mcp-orch` 运行时依赖任何 `internal/module/*`、`internal/store/*` 或 `internal/store/sqlc/*`。

建议的 `fx` 组装思路：

1. 先注入共享的 config/db 与本地 sqlc `Queries`。
2. 再注入本地 `internal/sidecar/orch/store/*` 与 `internal/sidecar/orch/orchestration/*`。
3. 最后注入本地 tool registry、manifest 与 stdio server runner。

### 3.4 单通道模型（stdio only）

- `stdio` 通道面向 MCP client：负责 tool list、tool call、manifest 与 MCP 响应编码。
- `cmd/mcp-orch` 自己持有 agent runtime、report、runtime snapshot 与 DAG 逻辑；`orchestration_*` 和 `task_*` 直接调用本地 `internal/sidecar/orch/orchestration/*`。
- `workspace_*`、`command_*`、`prompt_*`、`shared_file_*` 直接调用 `internal/sidecar/orch/store/*`，不经宿主桥接。
- `cmd/mcp-orch` 是 orchestration owner，也是 orch-family 工具的唯一执行面；执行链路在本地闭环。

### 3.5 tool registry

- registry source of truth 在 `cmd/mcp-orch/`。
- 每个 tool 条目同时定义：
  - 名称
  - schema
  - handler
  - manifest 描述
  - 调用后端的 facade 类型
- 禁止把 `internal/module/*/rpc.go` 的 `handler.Map` 直接作为 registry；MCP tool 面与宿主 RPC 面必须解耦。

---

## 4. 迁移步骤

### 4.0 P8 前置条件

1. 在 `dependency_direction_test` 中补 `cmd/mcp-orch` 的单向依赖规则：
   - 只允许依赖 `internal/contract/*`、`internal/dto/*`、`internal/platform/{config,db}` 和 `cmd/mcp-orch/*` 本地包。
2. 把 P8 相关守卫前移到本期：
   - `cmd/mcp-orch` 本地 runtime / store / sqlc 适配层的文件/函数/CC/包文件数守卫不再延后到 P10。
3. Step 1 必须先完整复制 `internal/sidecar/orch/orchestration/*` 到 `internal/sidecar/orch/orchestration/*`，并复制依赖的 `internal/store/*` / `internal/store/sqlc/*` / `sql/queries/*.sql` 到 `internal/sidecar/orch/store/*`。
4. cleanup 前必须对 `taskdag`、`binding`、`workspace`、`prompt`、`commandcard`、`sharedfile` 做 `lsp_xref(references)` 审计，决定“迁移+删除”还是“复制+保留”；当前基线结论是这 6 个包都有宿主消费者，因此先 copy+keep。
5. `prompts/list|write|delete` 的宿主 UI surface 必须持续保留；MCP 侧只允许新增本地 `internal/sidecar/orch/store/prompt` adapter。

### 4.1 三步法

1. Step 1: `copy-agent`
   - 先把 `internal/sidecar/orch/orchestration/` 整个目录复制到 `internal/sidecar/orch/orchestration/`，只复制，不改原文件，也不改复制后的文件。
   - 同时复制 `internal/store/{taskdag,workspace,prompt,commandcard,sharedfile}`；若迁移后的 orchestration 需要 binding，再复制 `internal/store/binding`。
   - 同时复制这些包依赖的 `sql/queries/{task_dag,workspace_run,prompt_template,command_card,shared_file}.sql`，必要时加 `agent_provider_binding.sql` / `thread_binding.sql`，以及对应的 `internal/store/sqlc/*.sql.go`、`querier.go` 等生成代码到 `internal/sidecar/orch/store/sqlc/`。
   - 同步建好 `internal/sidecar/orch/store/`、`internal/sidecar/orch/tools/` 目标目录，供后续本地 adapter 落位。
2. Step 2: `cleanup-agent`
    - 在 Step 1 完成后，删除 `internal/sidecar/orch/orchestration/` 整个目录，并更新 `internal/app/modules.go` 移除 orchestration 模块注册。
    - 硬规则：零残留，禁止双实现。凡已搬到 `cmd/mcp-orch/` 并确认宿主不再依赖的实现，原目录必须删干净，不能保留两份同职责代码。
    - cleanup 检查流程必须固定为：
      1. 先对每个候选包 / 文件执行 `lsp_xref(references)`，确认除 `cmd/mcp-orch` 外是否还有其他引用者。
      2. 若无其他引用者，则整个删除原目录 / 原文件。
      3. 若仍有宿主引用者，则保留宿主版本，但 `cmd/mcp-orch` 必须只使用自己的副本。
      4. 删除完成后必须执行 `go build ./...`，确认宿主和 MCP 都能编译通过。
    - 具体删除清单必须逐项判定：
      - `internal/sidecar/orch/orchestration/`：整个目录删除。
      - `internal/store/taskdag/`：若宿主零引用则整个目录删除。
      - `internal/store/binding/`：若宿主零引用则整个目录删除。
      - `internal/store/workspace/`：若宿主零引用则整个目录删除。
      - `internal/store/prompt/`：若宿主零引用则整个目录删除。
      - `internal/store/commandcard/`：若宿主零引用则整个目录删除。
      - `internal/store/sharedfile/`：若宿主零引用则整个目录删除。
      - `internal/store/sqlc/` 中已迁移到 `internal/sidecar/orch/store/sqlc/` 且宿主零引用的 `.sql.go` 方法 / 文件：删除对应内部版本。
    - 当前基线下 `taskdag`、`binding`、`workspace`、`prompt`、`commandcard`、`sharedfile` 都存在宿主消费者，因此暂定为 copy+keep；但 cleanup-agent 仍要逐项复核，禁止凭印象跳过删除判定。
    - 若有其他包仍引用 `internal/sidecar/orch/orchestration/*`，必须在这一步改到新的 `internal/sidecar/orch/orchestration/*` 或直接删除不再需要的宿主依赖。
    - `cleanup-agent` 负责保证核心层不再残留 orchestration / DAG 能力、不残留双实现，并保持 `go build ./...` 可通过。
3. Step 3: `adapt-agents`
   - 多个 Agent 并行修改 `cmd/mcp-orch/` 中复制过来的文件。
   - `adapt-orchestration` 负责调整 `internal/sidecar/orch/orchestration/*` 的 import path、装配方式和 runtime owner 关系。
   - `adapt-store-sqlc` 负责把复制过来的 store 包改为依赖本地 `internal/sidecar/orch/store/sqlc/*`，并修正对应 query / querier 组合。
   - `adapt-tools` 负责 `workspace_*`、`command_*`、`prompt_*`、`shared_file_*` 与 `task_*` 的 schema/handler 对接。
   - `server-wire` 负责完成 `cmd/mcp-orch/fx.go` 与本地 stdio server 接线。

### 4.2 Step 3 的适配约束

1. orchestration：
   - `internal/sidecar/orch/orchestration/*` 整体迁到 `internal/sidecar/orch/orchestration/*`，迁移后核心层不再保留该模块。
   - `cmd/mcp-orch` 本地持有 agent runtime、report、runtime snapshot 与 process manager。
   - `orchestration_*` tool 直接调用本地 `internal/sidecar/orch/orchestration`，不再存在宿主 bridge。
2. DAG：
   - `dag.go` 随 orchestration 模块一起迁入 `internal/sidecar/orch/orchestration/dag.go`。
   - 复制过来的 DAG 逻辑改为直接注入 `internal/sidecar/orch/store/taskdag.Store`，并且该本地 store 只依赖 `internal/sidecar/orch/store/sqlc/*`。
   - `task_create_dag`、`task_get_dag`、`task_update_node` 调迁移后的本地 DAG 逻辑；`task_start_node` 继续延后。
3. workspace：
   - 使用复制后的 `internal/sidecar/orch/store/workspace.Store`。
   - `workspace_*` 的文件系统操作、merge 流程和状态迁移都在 `internal/sidecar/orch/store/workspace/*` / `tools/*` 里封装。
4. skill command card：
   - 只复用复制后的 `internal/sidecar/orch/store/commandcard.Store.List/Get`，不要把 `skills/local/*`、`skills/remote/*` 一并迁入 P8。
5. prompt：
   - `prompt_*` 直接复用复制后的 `internal/sidecar/orch/store/prompt.Store`。
   - `prompt_get` 与 `prompt_list` 的过滤 / visibility 逻辑收口在 `cmd/mcp-orch` 的本地 adapter，不允许裸透传未经筛选的 store 结果。
6. shared file：
   - 直接在 `cmd/mcp-orch` 上包一层 adapter 调本地 `internal/sidecar/orch/store/sharedfile.Store.Get/Upsert`。
 7. sqlc：
   - `internal/sidecar/orch/store/sqlc/*` 只保留 orch-family 实际依赖的查询与生成代码，不再 import `internal/store/sqlc/*`。
   - 每个迁移过来的 store 包都要用 `call_hierarchy(outgoing)` / 文本审计确认实际命中的 sqlc 方法集合。
8. MCP handler 职责：
   - 只保留 schema、参数解码、返回编码、错误映射。
   - 不重写 DB 访问，不复制 V2 工具实现；业务调用只落到本地 orchestration 包、本地 store 层或本地 sqlc 层。

### 4.3 编译与兼容要求

- 复制和清理必须拆给不同 Agent，避免 cleanup 阶段遗漏目标代码或遗漏删除原实现。
- 顺序必须是“先复制，后清理，再适配”；任何阶段都要保证仓库可编译。
- cleanup 阶段禁止出现双实现残留：
  - 禁止两个地方同时保留同一个 orchestration service。
  - 禁止两个地方同时保留同一个 store 实现。
  - 禁止两个 sqlc 包里重复定义同一组已迁移方法。
- `prompts/list|write|delete` 继续保留给前端；P8 只新增基于 `internal/sidecar/orch/store/prompt` 的 MCP adapter。
- 当前仓库未搜索到前端对 `workspace/run/create`、`command/card/list`、`agent.getReport` 的直接 JS 调用；这些 RPC 可以在兼容期保留，但不再作为 MCP source of truth。
- `internal/sidecar/orch/orchestration/` 在 cleanup 后必须整体删除；其他 `internal/module/*/rpc.go` 只保留宿主/UI 仍需要的 RPC，不再承担 MCP tool registry。
- 候选 store 包只有在 xref 证明“宿主零消费者”时才能删除；当前基线下 `taskdag`、`binding`、`workspace`、`prompt`、`commandcard`、`sharedfile` 都先保留内部版本。
- `cmd/mcp-orch/*` 成为 orch-family MCP tool 的唯一注册点；`internal/sidecar/orch/orchestration/*` 成为 orchestration owner；`internal/sidecar/orch/store/*` 与 `internal/sidecar/orch/store/sqlc/*` 成为本地数据层。

---

## 5. Agent 拆分（三步法）

| Agent | 负责范围 | 写入边界 | 说明 |
| --- | --- | --- | --- |
| Step 1 `copy-agent` | 复制 `internal/sidecar/orch/orchestration/*`、候选 `internal/store/*`、依赖的 `sql/queries/*.sql` 与 `internal/store/sqlc/*.sql.go` 到 `cmd/mcp-orch/*` | `internal/sidecar/orch/orchestration/*`、`internal/sidecar/orch/store/*`、`internal/sidecar/orch/store/sqlc/*`、`internal/sidecar/orch/tools/*` | 只复制与搭骨架，不改原文件，也不清理原目录 |
| Step 2 `cleanup-agent` | 删除 `internal/sidecar/orch/orchestration/` 并按 xref 结果决定是否删除已迁移 store / sqlc 包 | `internal/sidecar/orch/orchestration/*`、候选 `internal/store/*`、候选 `internal/store/sqlc/*`、`internal/app/modules.go` 及其调用点 | 硬规则是零残留、禁止双实现；当前基线下只确定删除 orchestration，store/sqlc 先 copy+keep 但必须逐项复核 |
| Step 3A `adapt-orchestration` | 迁移后的 agent runtime、report、runtime snapshot、schema/handler 适配 | `internal/sidecar/orch/orchestration/*`、`internal/sidecar/orch/tools/orchestration*` | `cmd/mcp-orch` 成为 orchestration owner，不再需要宿主 bridge |
| Step 3B `adapt-store-sqlc` | 本地 store/sqlc 适配、query 裁剪与 import path 调整 | `internal/sidecar/orch/store/*`、`internal/sidecar/orch/store/sqlc/*` | 保证 `cmd/mcp-orch` 运行时零依赖 `internal/store/*` / `internal/store/sqlc/*` |
| Step 3C `adapt-tools` | `task_*`、`workspace_*`、`command_*`、`prompt_*`、`shared_file_*` 的 schema/handler 对接 | `internal/sidecar/orch/tools/task*`、`internal/sidecar/orch/tools/workspace*`、`internal/sidecar/orch/tools/command*`、`internal/sidecar/orch/tools/prompt*`、`internal/sidecar/orch/tools/shared_file*` | 只依赖本地 orchestration/store/sqlc 与共享 DTO |
| Step 3D `server-wire` | `cmd/mcp-orch/fx.go`、registry、manifest、local stdio server | `cmd/mcp-orch/fx.go`、`cmd/mcp-orch/main.go`、本地 server/runtime 相关文件 | 只做装配与 stdio server 接线 |

执行顺序：

1. `copy-agent` 先行，先复制整个 orchestration 模块。
2. `cleanup-agent` 在 Step 1 完成后执行，删除 `internal/sidecar/orch/orchestration/`，逐项复核 store/sqlc 删除条件，并更新宿主装配。
3. `adapt-orchestration`、`adapt-store-sqlc`、`adapt-tools` 并行修改 `cmd/mcp-orch/` 中复制过来的文件。
4. `server-wire` 最后接线。
5. 主 Agent 统一跑 archtest / fx graph / build 验证。

---

## 6. 框架契约遵守清单

### 6.1 代码守卫

- 文件有效行数 `<= 400`
- 函数有效行数 `<= 80`
- CC `<= 10`
- 包内非测试文件 `<= 15`
- 证据：`docs/plans/迁移/arch-code-guard.md:18-40,66-77`

### 6.2 `fx` 契约

- `fx` 只出现在 `cmd/`、`internal/app/`、`module.go`
- 证据：`internal/archtest/dependency_direction_test.go:164-175`
- `cmd/mcp-orch/fx.go` 只能做构造与装配，不在 constructor 中跑长循环
- 证据：`docs/契约/fx-convention.md:19-27,121-127`

### 6.3 `run.Group` 契约

- stdio server 属于长跑 actor，必须交给 runner/group 托管
- 证据：`docs/契约/rungroup-convention.md:19-27,121-140`

### 6.4 `jrpc2` 契约

- 统一注册表，不再开第二条平行注册链
- handler 进入声明式 map / registry
- transport 与 handler 解耦
- 证据：`docs/契约/jrpc2-convention.md:20-27,155-158`

### 6.5 模块化契约

- `cmd/mcp-orch` 运行时只通过本地 `cmd/mcp-orch/*`、DTO 与共享 config/db 通信
- 禁止 `cmd/mcp-orch` 把宿主大对象当 service locator
- `internal/sidecar/orch/orchestration/*` 是一次性迁移源，不是 P8 运行时依赖；迁移完成后该目录必须删除
- `internal/store/*` 与 `internal/store/sqlc/*` 也是一次性迁移源，不是 P8 运行时依赖；是否删除由 xref 结果决定
- 除共享的 `internal/platform/{config,db}`、`internal/contract/*`、`internal/dto/*` 外，`cmd/mcp-orch` 禁止 import 其他 `internal/*`
- Go `internal` 规则允许 `cmd/mcp-orch` import `internal/*`，因为它们同处模块根；这只是合法性，不是放宽耦合边界
- 证据：`docs/契约/modularity-convention.md:43-58,102-108`

### 6.6 SQL / Store 契约

- tool handler 不写 raw SQL
- `cmd/mcp-orch` 的本地 store 只通过 `internal/sidecar/orch/store/sqlc/*` 调查询
- 要复制的 sqlc 查询集合必须由 `lsp_xref(references)` + `call_hierarchy(outgoing)` 反推确认，避免整包搬运无关查询
- 证据：`docs/契约/sqlc-convention.md:23-30,48-52`

### 6.7 MCP 家族隔离

- `cmd/mcp-orch` 禁止依赖 `internal/tool/lsp`、`internal/tool/ida`
- `cmd/mcp-orch` 禁止依赖其他 `cmd/*`、`internal/app`、以及除共享 config/db/contract/dto 外的任何 `internal/*`
- 证据：`internal/archtest/mcp_family_isolation_test.go:16-18`
- P8 前置要补 `dependency_direction_test`，直接约束 `cmd/mcp-orch` 的允许依赖集合与单向依赖
- 证据：`internal/archtest/dependency_direction_test.go:164-175`

---

## 7. 风险

| 风险 | 严重度 | 说明 | 缓解 |
| --- | --- | --- | --- |
| `internal/sidecar/orch/orchestration` 与 `internal/sidecar/orch/orchestration` 双份并存 | 高 | 会产生两套 agent/DAG owner，迁移目标失效 | Step 2 必须删除核心层整个 orchestration 目录，并更新 `internal/app/modules.go` |
| `cmd/mcp-orch` 仍在运行时 import `internal/store/*` / `internal/store/sqlc/*` | 高 | “完全独立”目标失效，后续演进继续受宿主牵制 | Step 3 必须把所有 import 切到 `internal/sidecar/orch/store/*` 与 `internal/sidecar/orch/store/sqlc/*` |
| prompt 迁移导致前端页面失效 | 高 | `SystemPromptPage.js` 直接调用 `prompts/list|write|delete` | MCP 侧只做 `internal/sidecar/orch/store/prompt` adapter，宿主 RPC 入口继续保留 |
| `task_start_node` 被硬凑成假实现 | 高 | 当前只有 wakeup/lease store 原语，没有 controller 语义 | 直接列为延后项，不在 P8 冒充完成 |
| 直接复用宿主 module handler 导致暴露超范围方法 | 中 | 原 handler map 包含 P8 外 surface | 只在 `internal/sidecar/orch/tools/*` 定义目标工具的最小 schema/handler |
| `cmd/mcp-orch` 误用 `internal/app.Module` | 中 | 会拉起与 orch-family 无关的桌面依赖 | 只组装最小模块集 |
| 迁移 orchestration 后 import path 残留旧核心层路径 | 中 | cleanup 后容易留下编译断点或错误依赖 | Step 2 统一清理引用，Step 3 再批量改 `internal/sidecar/orch/orchestration/*` 的 import path |

---

## 8. 成功标准

### 8.1 P8 必达

- `cmd/mcp-orch` 能以独立二进制启动，并通过 stdio 暴露 orch-family MCP server。
- MCP tool definition、schema、manifest、registry 只存在于 `cmd/mcp-orch/`。
- `internal/sidecar/orch/orchestration/*` 本地持有 `orchestration_*` 与 `task_*` 所需的 agent runtime、report、runtime snapshot 与 DAG 逻辑。
- `internal/sidecar/orch/orchestration/` 已整体删除；核心层不再保留 orchestration 模块。
- `internal/sidecar/orch/store/*` 与 `internal/sidecar/orch/store/sqlc/*` 已本地化；`cmd/mcp-orch` 运行时零依赖 `internal/store/*` 与 `internal/store/sqlc/*`。
- `workspace_*`、`command_*`、`prompt_*`、`shared_file_*` 不在 tool handler 中写 SQL，只调用本地 store。
- `prompts/list|write|delete` 宿主 UI 能力保持可用。
- `cmd/mcp-orch` 通过家族隔离守卫、`fx` import 守卫和代码尺寸守卫。

### 8.2 交付口径

- 本期交付矩阵必须闭环说明 19 项交付工具。
- 其中：
  - 8 个为“orchestration 模块整体迁移后的本地工具面”
  - 11 个为“基于迁移后本地 store/sqlc 的薄 adapter 补齐”
  - `task_start_node` 明确延后，不计入本期交付，也不进入对外 manifest

---

## 附录 A：已移除的旧计划项

以下内容属于旧版误判，已从本计划移除：

| 旧项 | 原判断 | 现结论 |
| --- | --- | --- |
| 从 V2 直接复制代码 | `cmd/mcp-orch` 直接搬 V2 `toolsdk` / `mcp` | 废弃；P8 只允许从 V3 现有实现抽取 |
| orchestration 必须留在宿主并通过 bridge 调用 | `cmd/mcp-orch` 只做薄壳，agent runtime 不可迁出 | 废弃；P8 改为把 `internal/sidecar/orch/orchestration/*` 整体迁到 `internal/sidecar/orch/orchestration/*` |
| guard/archtest 可豁免 | 先编过再说 | 废弃；P8 仍受 V3 代码守卫与家族隔离约束 |
| 2 Agent 串行 copy/wire | 复制任务，不需要设计 | 废弃；当前是重构抽取任务，至少拆 3-4 个并行 agent |

---

## 附录 B：延后项与非 P8 基线

### B.1 明确延后

- `task_start_node`
  - 原因：当前 V3 没有等价 handler/service；只有 `taskdag.Store` 的 wakeup/lease 原语，缺 controller 级依赖检查与派发语义。

### B.2 不纳入本期 19 项交付

- orchestration 额外 surface：`agent.snapshot`、`agent.getState`、`agent.rememberReportRequest`、`agent.reportEvent`
- workspace 额外 surface：`workspace/run/files/list`、`workspace/run/file/get`
- prompt 宿主 UI surface：`prompts/write`、`prompts/delete`
- skill / command 额外 surface：`command/card/create|update|delete|run|versions`、`skills/*`

这些能力不是丢弃，而是不作为本期 19 项交付目标。
