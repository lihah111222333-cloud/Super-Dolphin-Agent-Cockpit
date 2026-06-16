# P5 RPC 迁移方案审查 B：依赖方向 + 工厂模式 + 代码量

## 结论

- 方案方向成立，但“`module/*/rpc.go` 只注入 module service facade”目前只对目标态成立，不对当前 V3 契约现状成立。
- `module/thread/rpc.go` 的依赖方向有条件可行；`module/turn/rpc.go`、`module/orchestration/rpc.go` 以当前 `contract.go` 直接落地不可行。
- `ThreadScope`、`CapabilityGate`、`StrictBind` 可以覆盖绝大多数 V2 样式，但有两个硬条件：
  - `ThreadScope` 不能只识别 `threadId`，兼容层还要识别 `threadID` / `thread_id`。
  - `StrictBind` 必须允许 typed struct 内部保留 `json.RawMessage` 字段。
- `V2 3478 行 -> V3 3000-4500 行` 的目标区间基本合理，但更可信的落点是 `3200-4200`。`3000` 偏激进，前提是 thread/turn/orchestration contract 在写 `rpc.go` 前先补齐。

## 1. 依赖方向可行性

### 1.1 总体判断

| `rpc.go` | 只注入对应 service 是否可行 | 结论 |
| --- | --- | --- |
| `module/thread/rpc.go` | 仅在 provider-specific 路由已下沉/合并、且 `thread.Service` 先扩容时可行 | 有条件可行 |
| `module/turn/rpc.go` | 当前 `turn.Service` 缺少 `steer` / `forceComplete` / `review` 能力 | 当前不可行 |
| `module/orchestration/rpc.go` | 当前 `orchestration.Service` 只覆盖 launch/stop/submit/snapshot；缺 list/report/state/sub-agent/DAG/task | 当前不可行 |
| `module/skill/rpc.go` | 需要 service 吞掉 skill manager + auto-match provider contract | 需要完整模块 |
| `module/workspace/rpc.go` | V2 已经是 service 主导，RPC 只是 envelope + notify | 需要完整模块 |
| `module/uistate/rpc.go` | 复杂度主要在 runtime/projector，不在 transport | 需要完整模块 |
| `module/dashboard/rpc.go` | 复杂度主要在 read-model 聚合，不在 transport | 需要完整模块 |
| `module/config/rpc.go` | 更像平台/进程配置读写，不是独立业务域 | 不建议建完整模块 |
| `module/core/rpc.go` | 更像 `platform/rpc` / app-level glue，不是独立业务域 | 不建议建完整模块 |

### 1.2 V2 直接依赖证据

#### `thread`

- V2 直接 provider 调用集中在 `go-agent-v2/internal/apiserver/methods_thread.go:44-359`：
  - `threadStartTyped` -> `s.providerAdapter.ThreadStart`
  - `threadForkTyped` -> `s.providerAdapter.ThreadFork`
  - `threadResumeTyped` -> `s.providerAdapter.ThreadResume`
  - `threadRecoverTyped` -> `s.providerAdapter.ThreadRecover`
  - `threadRollbackTyped` -> `s.providerAdapter.ThreadRollback`
  - `threadList` -> `s.providerAdapter.ThreadList`
  - `threadLoadedList` -> `s.providerAdapter.ThreadLoadedList`
  - `threadConfigGetTyped` / `threadConfigSetTyped` -> `s.providerAdapter.ThreadConfig*`
- V2 直接 provider 调用还散在 `go-agent-v2/internal/apiserver/methods_thread_turn.go:13-110`：
  - `thread/archive` -> `s.getInlineProvider().ThreadArchive`
  - `thread/name/set` -> `s.providerAdapter.ThreadNameSet`
  - `thread/read` -> `s.providerAdapter.ThreadRead`
  - `thread/resolve` -> `s.providerAdapter.ThreadResolve`
  - `thread/messages` -> `s.providerAdapter.ThreadMessages`
  - `thread/compact/start` / `thread/backgroundTerminals/clean` / `thread/undo` / `thread/model|personality|approvals/set` / `thread/mcp/list` / `thread/skills/list` 都直接走 `providerAdapter` slash-command helper
- V2 直接持久化/状态副作用不在 store facade 后面，而在 handler 内：
  - `go-agent-v2/internal/apiserver/methods.go:183-202` `threadUnarchiveTyped` 直接改 binding archived 状态，并触发 process recover
  - `go-agent-v2/internal/apiserver/methods.go:204-227` `threadDeleteTyped` 直接做 archive dir 删除、binding unbind、UI prefs 清理
  - `go-agent-v2/internal/apiserver/methods_thread.go:198-218` `threadStartPref` 直接读 `s.prefManager`
- 结论：
  - 当前 V2 thread handler 不是纯 facade。
  - 如果 V3 `module/thread/rpc.go` 只注入 `thread.Service`，则 `start/resume/recover/fork/rollback/config/read/messages/name/archive/unarchive/delete` 至少要下沉进 service。
  - `thread/compact/start`、`thread/realtime/*`、`thread/backgroundTerminals/clean`、`thread/mcp/list`、`thread/skills/list` 不能继续留在 thread module 主 facade 里，否则 `rpc.go` 仍会被迫直接依赖 provider/skill 侧对象。

#### `turn`

- V2 直接 provider 调用在 `go-agent-v2/internal/apiserver/methods_turn.go:48-198`：
  - `turnStartTyped` -> `s.providerAdapter.TurnStart`
  - `turnSteerTyped` -> `s.providerAdapter.TurnSteerFromInputAligned`
  - `reviewStartTyped` -> `s.providerAdapter.ReviewStart`
- V2 直接 provider 调用还散在 `go-agent-v2/internal/apiserver/methods_thread_turn.go:62-93`：
  - `turn/interrupt` -> `s.getInlineProvider().TurnInterrupt`
  - `turn/forceComplete` -> `s.getInlineProvider().TurnForceComplete`
- 结论：
  - `module/turn/rpc.go` 若只注入当前 `turn.Service`，无法承接 `turn/steer`、`turn/forceComplete`、`review/start`。
  - review 目前仍需要 provider/slash-command 语义；要么把 review 明确收进 turn module 子 service，要么保持单独 compatibility facade，但不能让 `rpc.go` 直碰 provider。

#### `orchestration`

- V2 RPC 直接依赖 `s.mgr` 的点集中在 `go-agent-v2/internal/apiserver/methods_orchestration.go:39-177`：
  - `agentLaunchTyped`
  - `agentSubmitTyped`
  - `agentStopTyped`
  - `agentList`
  - `agentGetReportTyped`
  - `agentGetStateTyped`
- 额外 orchestration 副作用不在现有 module service 中，而在 Server/helper：
  - `go-agent-v2/internal/apiserver/methods_orchestration.go:142-160` `agentRememberReportRequestTyped` -> `rememberReportRequest`
  - `go-agent-v2/internal/apiserver/methods_orchestration.go:185-209` `agentReportEventTyped` -> `AgentEventHandler`
  - `go-agent-v2/internal/apiserver/tool_providers.go:45-116` `PersistSubAgentBinding` / `SaveSubAgent` / `DeleteSubAgent`
  - `go-agent-v2/internal/apiserver/orchestration_report.go:23-137` orchestration report waiter/auto-report 逻辑
- 结论：
  - 当前 `module/orchestration/rpc.go` 只注入 `orchestration.Service` 不可行。
  - 至少需要把 `list/report/state/event-ingest/sub-agent-binding/DAG-task` 全部纳入 orchestration facade 或拆成同域子 facade，不能让 `rpc.go` 直接回调 Server helper。

### 1.3 `s.store` / `s.bus` / `s.providerAdapter` 现状

- 字面 `s.store`：
  - 在 `go-agent-v2/internal/apiserver` 未找到生产代码命中。
  - 这说明 V2 的问题不是“统一 store 字段被 RPC 直用”，而是 `bindingStore`、`prefManager`、`agentThreadStore`、`workspaceMgr`、`sysLogStore` 之类分散字段从 handler 泄漏。
- 字面 `s.bus`：
  - 未找到 thread/turn/orchestration handler 直接命中。
  - 仅 `go-agent-v2/internal/apiserver/dashboard_bindings.go:66-68` 直接读 `s.busLogStore`。
- `s.providerAdapter`：
  - thread/turn/skill/uistate/dashboard 侧都有直接命中，是 V2 handler 层最显著的依赖泄漏点。

## 2. 工厂模式评估

### 2.1 LSP 实际计数

以下计数只看 `go-agent-v2/internal/apiserver` 生产代码，不含文档。

| 模式 | 生产代码命中数 | 其中直接 route 注册/调用 |
| --- | --- | --- |
| `typedHandler(` | 25 | 24 个直接 route 注册 + 1 个 `bindTyped` helper 内部调用 |
| `bindRaw(` | 31 | 29 个直接 route 注册 + 1 个定义 + 1 个 `factory_registry` 适配 |
| `withRequiredThreadID(` | 12 | 11 个直接调用 + 1 个定义 |
| `capabilityGuard(` | 9 | 8 个直接调用 + 1 个定义 |

补充：

- `bindTyped(` 才是 V2 typed binding 主力，直接 route 注册共有 42 个。
- 因此 `StrictBind` 不能只对标 `typedHandler`，还必须一并替换 `bindTyped`。

### 2.2 `ThreadScope`

结论：可以覆盖所有“真正 required 的 thread-scope route”，但不能只按字段名精确匹配 `threadId`。

证据：

- `withRequiredThreadID` 11 个直接调用点全部位于 `go-agent-v2/internal/apiserver/methods_thread_turn.go`：
  - `thread/archive`
  - `thread/name/set`
  - `thread/read`
  - `thread/resolve`
  - `thread/messages`
  - `turn/interrupt`
  - `turn/forceComplete`
  - `thread/realtime/start`
  - `thread/realtime/appendAudio`
  - `thread/realtime/appendText`
  - `thread/realtime/stop`
- typed 请求结构里要求字段基本都是 `json:"threadId"`：
  - `go-agent-v2/internal/apiserver/methods_thread.go:220-351`
  - `go-agent-v2/internal/apiserver/methods_turn.go:30-120`
- 但 raw slash-command 兼容路径不是只认 `threadId`：
  - `go-agent-v2/legacy-agentsdk/service/command/slash_command_logic.go:104-120` 同时提取 `threadId`、`threadID`、`thread_id`
- 反例：
  - `go-agent-v2/internal/apiserver/methods_ui_state.go:105-110` 的 `ui/state/get` 也有 `threadId`，但语义是可选筛选，不应被 `ThreadScope` 当成 required middleware。

结论化要求：

- `ThreadScope` 必须是按 route opt-in，不是按字段名全局启用。
- 若兼容层仍保留 raw slash-command wrapper，则 middleware 或前置 normalizer 必须支持 `threadId` / `threadID` / `thread_id` 三别名。

### 2.3 `CapabilityGate`

结论：足够覆盖现有 `capabilityGuard` 形态，没有发现需要复杂条件分支的调用点。

直接调用点共 8 个：

- `thread/compact/start`
- `turn/start`
- `turn/steer`
- `thread/realtime/start`
- `thread/realtime/appendAudio`
- `thread/realtime/appendText`
- `thread/realtime/stop`
- `thread/model/set`

观察：

- 所有调用点都是“固定 capability + 固定 unsupported reason + 现有 handler”三元组。
- 没有看到基于 payload 内容再做二次条件分支的 `capabilityGuard`。
- 真正的复杂性不在 gate，而在 route 自身是否应继续存在：
  - `thread/model/set` 计划并入 `thread/config/set`
  - `thread/realtime/*` 与 `thread/compact/start` 计划下沉到 provider facade

结论化要求：

- `CapabilityGate` 本身是够用的。
- 但它应主要服务于 compatibility wrapper 和仍保留的 provider-capability route；不能拿它掩盖 route 归属错误。

### 2.4 `StrictBind`

结论：可以覆盖全部 `typedHandler` / `bindTyped` 路径；少数 raw passthrough route 仍应保留原始 `json.RawMessage`。

必须支持的 typed+raw 混合字段：

- `go-agent-v2/internal/apiserver/methods_turn.go:30-37`
  - `turnStartParams.OutputSchema json.RawMessage`
- `go-agent-v2/internal/apiserver/methods_orchestration.go:179-183`
  - `agentReportEventParams.EventData json.RawMessage`

`bindRaw` 29 个直接 route 注册里，绝大多数都可以转成 typed request：

- 明确可转 typed empty request：
  - `initialize`
  - `app/list`
  - `skills/list`
  - `model/list`
  - `collaborationMode/list`
  - `experimentalFeature/list`
  - `config/read`
  - `configRequirements/read`
  - `config/mcpServer/reload`
  - `mcpServerStatus/list`
  - `agent.list`
  - `ui/preferences/getAll`
  - `ui/projects/get`
  - `debug/runtime`
  - `debug/gc`
- 明确可转 typed object request：
  - `workspace/run/create|get|list|merge|abort`
  - `ui/state/get`
  - `ui/sidebar/get`
  - `ui/log`
- 真正适合继续 raw passthrough 的 route：
  - `lsp/gui_file`
  - `lsp/gui_grep`
  - `lsp/gui_structure`
  - `lsp/gui_inspect`
  - `lsp/gui_xref`

原因：

- 这 5 个 GUI LSP bridge 的 payload 由 `action` 决定具体 schema；当前 `go-agent-v2/internal/apiserver/methods_ui_lsp_gui.go:11-119` 本质是 transport bridge，不是稳定 typed 领域请求。

## 3. 代码量评估

### 3.1 基线是否可信

仅 main handler-adjacent 文件，用 LSP 读取到的总行数已经接近用户给定基线：

| 文件 | 行数 |
| --- | --- |
| `go-agent-v2/internal/apiserver/methods.go` | 389 |
| `go-agent-v2/internal/apiserver/methods_thread.go` | 360 |
| `go-agent-v2/internal/apiserver/methods_thread_turn.go` | 114 |
| `go-agent-v2/internal/apiserver/methods_turn.go` | 261 |
| `go-agent-v2/internal/apiserver/methods_command.go` | 316 |
| `go-agent-v2/internal/apiserver/methods_config.go` | 309 |
| `go-agent-v2/internal/apiserver/methods_orchestration.go` | 242 |
| `go-agent-v2/internal/apiserver/workspace_methods.go` | 168 |
| `go-agent-v2/internal/apiserver/dashboard_bindings.go` | 164 |
| `go-agent-v2/internal/apiserver/methods_ui_state.go` | 245 |
| `go-agent-v2/internal/apiserver/methods_ui_projects.go` | 280 |
| `go-agent-v2/internal/apiserver/methods_ui_sidebar.go` | 127 |
| `go-agent-v2/internal/apiserver/methods_ui_code_open.go` | 32 |
| `go-agent-v2/internal/apiserver/methods_ui_lsp_gui.go` | 134 |
| `go-agent-v2/internal/apiserver/methods_log_relay.go` | 121 |
| `go-agent-v2/internal/apiserver/orchestration_report.go` | 138 |

上述 16 个文件合计约 `3400` 行；加上剩余通知/小型 glue 文件，`3478` 基线可信。

### 3.2 `> 50` 行的直接 handler

按 document symbol 统计，直接 RPC 方法里超过 50 行的只有两处：

- `go-agent-v2/internal/apiserver/methods_config.go:23-79`
  - `configRead`
  - 57 行
- `go-agent-v2/internal/apiserver/methods_command.go:40-90`
  - `commandExecTyped`
  - 51 行

结论：

- V2 不是“少数超大 handler”主导，而是“很多中小 handler + 大量同文件 helper/副作用”主导。
- 因此 P5 的主要压缩来源不是删掉几条巨型 handler，而是统一 binder/middleware/registry，并把 helper 与副作用移回模块 service。

### 3.3 真正的复杂度集中点

虽然直接 RPC 方法超过 50 行的不多，但包级复杂度很高：

- `go-agent-v2/internal/apiserver/methods_ui_state_helpers.go`：498 行
- `go-agent-v2/internal/apiserver/server_event_handler.go`：558 行
- `go-agent-v2/internal/dashboard/state_service.go`：366 行

这三个点说明：

- `uistate` 复杂度在 projector/runtime helper，不在 `ui/state/get` 那个 route 本身。
- `dashboard` 复杂度在跨 store 聚合和 scope 过滤，不在单条 RPC envelope。
- `orchestration` 的事件注入和 report 路径已经越过“简单 handler”范围。

### 3.4 对 `3000-4500` 区间的判断

判断：合理，但下限 `3000` 偏紧。

理由：

- 可以压缩的部分：
  - `bindRaw` / `bindTyped` / `typedHandler` / `withRequiredThreadID` / `capabilityGuard` 的重复 transport glue
  - 第二套手写注册链
  - route-level nil guard 与 envelope glue
- 不会消失的部分：
  - thread/turn/orchestration contract 扩容后的领域逻辑
  - `uistate` / `dashboard` 的 projector/read-model 代码
  - strict binder / response contract / golden tests 的新增样板

因此：

- `3000-3200`：只有在 provider-specific route 大幅下沉、config/core 不新建伪模块、并且不保留兼容旁路时才可能达到。
- `3200-4200`：更可信。
- `4200-4500`：若 orchestration facade 拆成多面并把 report/sub-agent/DAG 一并补齐，也仍可接受。

## 4. 新模块是否需要完整三件套

### 4.1 需要完整模块

- `skill`
  - 证据：`go-agent-v2/internal/apiserver/methods_command.go:92-315` 同时拥有 skill manager 选择、auto-match、CRUD、notify。
  - 结论：必须有 `module + contract + service + rpc`。否则 `rpc.go` 会继续直接依赖 skills manager 与 provider auto-match。
- `workspace`
  - 证据：`go-agent-v2/internal/apiserver/workspace_methods.go:41-167` 只是对 `WorkspaceManager` 的 envelope/notify 包装。
  - 结论：必须有完整模块；RPC 不应再持有 merge/abort/create 的业务细节。
- `uistate`
  - 证据：`methods_ui_state.go` 245 行，`methods_ui_state_helpers.go` 498 行，且还依赖 `go-agent-v2/internal/dashboard/state_service.go`。
  - 结论：不是“补一个 `rpc.go`”能解决的问题，必须有 runtime/projector 级完整模块。
- `dashboard`
  - 证据：`go-agent-v2/internal/apiserver/dashboard_bindings.go:24-163` 直接扇入多类 store、scope 过滤、技能查询、DAG detail 聚合。
  - 结论：必须有 `service/projection/rpc`，不能只做 transport wrapper。

### 4.2 不建议建完整模块

- `config`
  - 证据：`go-agent-v2/internal/apiserver/methods_config.go:23-308` 混合了进程 env、pref、log store、LSP status、MCP reload。
  - 结论：这不是单一业务域。更合理的归属是：
    - `platform/config`：`config/read`、env write、prompt hint
    - `mcpserver` / `platform/lsp`：`mcpServerStatus/list`、`config/mcpServer/reload`
    - `platform/log`：`log/list`、`log/filters`
  - 单独建 `module/config` 会产生伪聚合。
- `core`
  - 证据：`initialize`、`app/list`、`model/list`、`collaborationMode/list`、`experimentalFeature/list` 本质都是入口/展示/能力枚举。
  - 结论：应留在 `platform/rpc` 或 app-level facade，不建议建 `module/core`。

## 5. V3 已有 module 的 RPC 兼容性

### 5.1 `module/thread`

现有 `internal/module/thread/contract.go:9-21` 仅提供：

- `List`
- `Get`
- `ReadHistory`
- `ReadMessages`
- `Archive`
- `Unarchive`
- `ListByStatus`
- `ListByCWD`
- `SendCommand`
- `SetName`
- `Delete`

缺口：

- `thread/start`
- `thread/resume`
- `thread/recover`
- `thread/fork`
- `thread/rollback`
- `thread/config/get`
- `thread/config/set`
- `thread/read` 的完整 response facade
- `thread/loaded/list` 合并后的过滤语义

判断：不够。

备注：

- `SendCommand` 能覆盖 `/model`、`/personality`、`/approvals`、`/interrupt` 一类 compatibility command。
- 但这不足以承接 P5 线程主面。

### 5.2 `module/turn`

现有 `internal/module/turn/contract.go:12-17` 仅提供：

- `PrepareTurn`
- `StartTurn`
- `InterruptTurn`
- `TrackTurn`

缺口：

- `turn/steer`
- `turn/forceComplete`
- `review/start`
- 与兼容层对应的稳定 response facade

判断：不够。

备注：

- `turn/start` 本身也不是直接一跳可用；RPC 还需要组合 `PrepareTurn` + `StartTurn`。
- `review/start` 当前完全不在 contract 里。

### 5.3 `module/orchestration`

现有 `internal/sidecar/orch/orchestration/contract.go:10-17` 仅提供：

- `LaunchAgent`
- `StopAgent`
- `SubmitTurn`
- `CompleteTurn`
- `Recover`
- `Snapshot`

缺口：

- `agent.list`
- `agent.getReport`
- `agent.getState`
- `agent.rememberReportRequest`
- `agent.reportEvent`
- `agent.saveSubAgent`
- `agent.deleteSubAgent`
- `agent.persistSubAgentBinding`
- 迁移方案中规划的 DAG / task / phase1 相关 RPC

判断：明显不够。

## 6. 需要先修正的方案点

1. 先扩 `thread.Service`、`turn.Service`、`orchestration.Service`，再写 `rpc.go`。否则 `rpc.go` 只能重新变成 God Object adapter。
2. `ThreadScope` 必须 route opt-in，并支持 `threadId` / `threadID` / `thread_id` 兼容提取。
3. `StrictBind` 必须同时替换 `typedHandler` 和 `bindTyped`，并允许 typed struct 内嵌 `json.RawMessage`。
4. `bindRaw` 29 条 route 里，除了 5 个 `lsp/gui_*` bridge，其他都应转 strict typed request；否则 P5 factory 收益会被削弱。
5. `config` 与 `core` 不应为了“目录对称”硬建新 module；这两个区域更适合 `platform/*` + 薄 RPC facade。
6. `uistate`、`dashboard`、`skill`、`workspace` 都不是 rpc-only 问题，必须按完整模块处理。
