# P5 RPC 层迁移执行计划

> 修正版，基于三重审查结论

## 1. 范围
- 151 个 RPC 方法（含 23 noop/stub）
- Server 基础设施：`codec` / `transport_ws` / `push` / `approval`
- V2 源码：6,707 行
- V3 目标：4,500-6,500 行

23 个 noop/stub 基线：
- noop 5 个：`initialized`、`feedback/upload`、`fuzzyFileSearch/sessionStart`、`fuzzyFileSearch/sessionUpdate`、`fuzzyFileSearch/sessionStop`
- stub/compat 18 个：`agent-home`、`agent-agents-md`、`git-origins`、`local-environments/list`、`mcp-servers`、`mcp/status`、`open-in-targets`、`platform-info`、`workspace-root-options`、`worktrees/list`、`tasks/get`、`tasks/list`、`inbox-items`、`inbox-items/get`、`pending-automation-runs`、`config/read-all`、`thread/debugMemory`、`mock/experimentalMethod`

Server 基础设施拆分：

| V2 文件 | 行数 | V3 目标 |
|---|---:|---|
| `server_conn_ws.go` | 277 | `platform/rpc/transport_ws.go` |
| `server_payload.go` | 412 | `platform/rpc/codec.go` |
| `server_approval.go` | 483 | `platform/rpc/approval.go` |
| `notifications.go` | 100 | `platform/rpc/push.go` |

## 2. 模块归宿修正

| 旧边界 | 修正后归宿 | 说明 |
|---|---|---|
| `module/core` | 已废弃 | `initialize` -> `platform/rpc/initialize.go`；`approval/respond` -> `module/turn/rpc.go` + `platform/rpc/approval.go`；`log/*` -> `platform/bus` sink + `platform/rpc/push.go` |
| `module/debug` | 已废弃 | `debug/runtime`、`debug/gc`、`thread/debugMemory`、compat/debug stub -> `platform/rpc/debug.go` |
| `module/config` | 已废弃 | `thread/config/*`、`thread/model/set`、`thread/personality/set`、`thread/approvals/set` -> `module/thread/rpc.go`；全局配置 -> `platform/config` |

旧 handler 分组修正：

| 旧分组 | 新归宿 |
|---|---|
| ~~`initialize.go`~~ | `platform/rpc/initialize.go` |
| ~~`thread.go`~~ | `module/thread/rpc.go` |
| ~~`turn.go`~~ | `module/turn/rpc.go` |
| ~~`config.go`~~ | 合并到 `module/thread/rpc.go` |
| ~~`skills.go`~~ | `module/skill/rpc.go` |
| ~~`command.go`~~ | `module/skill/rpc.go` |
| ~~`workspace.go`~~ | `module/workspace/rpc.go` |
| ~~`ui.go`~~ | `module/uistate/rpc.go` |
| ~~`dashboard.go`~~ | `module/dashboard/rpc.go` |
| ~~`orchestration.go`~~ | `module/orchestration/rpc.go` |
| ~~`log.go`~~ | `platform/bus` sink |
| ~~`debug.go`~~ | `platform/rpc/debug.go` |

## 3. 任务拆分（5 波次 12 子任务）

### 波次 0：前置基础设施
- R0a: `platform/rpc` 契约合规 + `codec` + `transport_ws` + `push`（≤650）
- R0b: fx 图闭环（≤150）
- R0c: approval 状态机（≤350 行，互辩修正后口径）
- R0d: `ThreadScope` 多 field + `StrictBind` 兼容（≤80）

### 波次 1：thread + turn RPC
- R1: `module/thread/rpc.go`（≤350）
- R2: `module/turn/rpc.go`（≤250）

### 波次 2：skill + workspace + orchestration RPC
- R3: `module/skill` 新建（≤300）
- R4: `module/workspace/rpc.go`（≤300）
- R5: `module/orchestration/rpc.go`（≤200）

### 波次 3：UI + dashboard
- R6: `module/uistate` 新建（≤500）
- R7: `module/dashboard` 新建（≤300）

### 波次 4：验证
- R8: 151 方法注册完整性测试（≤300）

波次依赖：
- `R0a` -> `R0b` / `R0c` / `R0d`
- `R0b` + `R0c` + `R0d` -> `R1` / `R2`
- `R1` + `R2` -> `R3` / `R4` / `R5`
- `R3` + `R4` + `R5` -> `R6` / `R7`
- `R6` + `R7` -> `R8`

## 4. 151 方法归宿完整表

完整性基线以 `registeredMethodNames(s)` 的 151 项为准，动态工具面不计入本表。

### 4.1 `platform/rpc/initialize.go`（24）
- 方法：`app/list`, `collaborationMode/list`, `config/batchWrite`, `config/lspPromptHint/read`, `config/lspPromptHint/write`, `config/mcpServer/reload`, `config/read`, `config/read-all`, `config/value/write`, `configRequirements/read`, `experimentalFeature/list`, `externalAgentConfig/detect`, `externalAgentConfig/import`, `feedback/upload`, `fuzzyFileSearch`, `fuzzyFileSearch/sessionStart`, `fuzzyFileSearch/sessionStop`, `fuzzyFileSearch/sessionUpdate`, `initialize`, `initialized`, `mcpServer/oauth/login`, `mcpServerStatus/list`, `model/list`, `windowsSandbox/setupStart`
- 备注：初始化、全局配置、provider capability 配置面和兼容 noop 统一收口；全局配置实际读写归 `platform/config`。

### 4.2 `module/thread/rpc.go`（28）
- 方法：`thread/approvals/set`, `thread/archive`, `thread/backgroundTerminals/clean`, `thread/compact/start`, `thread/config/get`, `thread/config/set`, `thread/delete`, `thread/fork`, `thread/list`, `thread/loaded/list`, `thread/mcp/list`, `thread/messages`, `thread/model/set`, `thread/name/set`, `thread/personality/set`, `thread/read`, `thread/realtime/appendAudio`, `thread/realtime/appendText`, `thread/realtime/start`, `thread/realtime/stop`, `thread/recover`, `thread/resolve`, `thread/resume`, `thread/rollback`, `thread/skills/list`, `thread/start`, `thread/unarchive`, `thread/undo`
- 备注：`thread/model/set`、`thread/personality/set`、`thread/approvals/set` 作为 `thread/config/set` 的兼容包装；provider-specific 能力面先保留兼容 handler，再逐步下沉到 provider facade。

### 4.3 `module/turn/rpc.go`（6）
- 方法：`approval/respond`, `review/start`, `turn/forceComplete`, `turn/interrupt`, `turn/start`, `turn/steer`
- 备注：`approval/respond` 与 turn/review 共享审批上下文，状态迁移由 `platform/rpc/approval.go` 承接。

### 4.4 `module/skill/rpc.go`（15）
- 方法：`command/exec`, `skills/config/read`, `skills/config/write`, `skills/list`, `skills/local/delete`, `skills/local/importDir`, `skills/local/listFiles`, `skills/local/read`, `skills/local/write`, `skills/match/preview`, `skills/remote/export`, `skills/remote/list`, `skills/remote/read`, `skills/remote/write`, `skills/summary/write`
- 备注：旧 `command.go` 与 `skills.go` 合并；transport 面只保留 skill facade 和兼容命令入口。

### 4.5 `module/workspace/rpc.go`（5）
- 方法：`workspace/run/abort`, `workspace/run/create`, `workspace/run/get`, `workspace/run/list`, `workspace/run/merge`
- 备注：create/merge/abort 仍触发 push；状态广播统一通过 `platform/rpc/push.go`。

### 4.6 `module/uistate/rpc.go`（20）
- 方法：`lsp/gui_file`, `lsp/gui_grep`, `lsp/gui_inspect`, `lsp/gui_structure`, `lsp/gui_xref`, `lsp_diagnostics_query`, `ui/code/locate`, `ui/code/open`, `ui/code/save`, `ui/dashboard/get`, `ui/log`, `ui/preferences/get`, `ui/preferences/getAll`, `ui/preferences/set`, `ui/projects/add`, `ui/projects/get`, `ui/projects/remove`, `ui/projects/setActive`, `ui/sidebar/get`, `ui/state/get`
- 备注：`ui/log` 进入 `platform/bus` sink；LSP GUI 方法保持 raw/bridge 入口，但外层统一 `StrictBind` 和错误映射。

### 4.7 `module/dashboard/rpc.go`（12）
- 方法：`dashboard/agentStatus`, `dashboard/aiLogs`, `dashboard/auditLogs`, `dashboard/busLogs`, `dashboard/commandCards`, `dashboard/dagDetail`, `dashboard/dags`, `dashboard/prompts`, `dashboard/sharedFiles`, `dashboard/skills`, `dashboard/taskAcks`, `dashboard/taskTraces`
- 备注：所有 dashboard 查询走只读 projection，不回流到业务状态写面。

### 4.8 `module/orchestration/rpc.go`（17）
- 方法：`agent.deleteSubAgent`, `agent.getReport`, `agent.getState`, `agent.launch`, `agent.list`, `agent.persistSubAgentBinding`, `agent.rememberReportRequest`, `agent.reportEvent`, `agent.saveSubAgent`, `agent.stop`, `agent.submit`, `agent.submitPrompt`, `inbox-items`, `inbox-items/get`, `pending-automation-runs`, `tasks/get`, `tasks/list`
- 备注：agent 生命周期、任务面、兼容 inbox surface 统一收口；DAG/phase1 读写面归编排模块。

### 4.9 `platform/bus` sink + `platform/rpc/push.go`（3）
- 方法：`log/filters`, `log/list`, `log/relay`
- 备注：旧 `log.go` 不再保留独立模块文件；日志查询、relay 与 push fanout 共用总线侧桥接。

### 4.10 `platform/rpc/debug.go`（21）
- 方法：`account/login/cancel`, `account/login/start`, `account/logout`, `account/rateLimits/read`, `account/read`, `agent-agents-md`, `agent-home`, `debug/gc`, `debug/runtime`, `diff/get`, `git-origins`, `local-environments/list`, `mcp-servers`, `mcp/status`, `ml-interceptor/status`, `mock/experimentalMethod`, `open-in-targets`, `platform-info`, `thread/debugMemory`, `workspace-root-options`, `worktrees/list`
- 备注：兼容/调试/系统探测面统一集中；其中 `mock/experimentalMethod`、`thread/debugMemory` 等保留为显式 compat stub，不再扩散到业务模块。

## 5. 工厂模式
- `StrictBind[P]`：统一封装 `handler.New` + `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()`，只用于公开方法。
- `RawHandler`：保留 `ui/log`、`lsp/gui_*` 等 raw payload surface，但外层仍走统一错误映射和审计。
- `ThreadScope`：从 params 和 request context 解析 `threadId` / `agent_id` / `cwd` / scope 字段，消灭 handler 内手工取值。
- `CapabilityGate`：把 provider capability、compat stub、unsupported surface 统一前置到 middleware。
- `Logging`：请求日志、错误码映射、审计事件和 bus sink 统一收口。
- `push.Bridge`：typed event -> RPC notify，负责 fanout、背压、订阅解绑和 UI refresh envelope。

## 6. 守卫预检
- 冻结 `registeredMethodNames(s)` 的 151 项快照，并单列 23 个 noop/stub 快照。
- 先拆 `server_conn_ws.go`、`server_payload.go`、`server_approval.go`、`notifications.go`，再迁业务 handler，避免 transport 与业务互锁。
- 先建立 `handler.Map` value-group 和 `fx.ValidateApp` 闭环，再开始模块分批迁移。
- 所有公开方法先过 `StrictBind`，再挂 `ThreadScope` / `CapabilityGate` / `Logging`，禁止边迁移边保留旧闭包链。
- push notify envelope、approval pending/resolve/reject 流程、workspace run 广播先做 golden/contract，再接 UI。
- `platform/rpc` 不得 import `module/*` concrete 实现；模块只导出 facade + `handler.Map` 片段。

## 7. Done 标准
- 151 个方法全部完成注册，且与快照逐项对齐。
- 所有公开方法都使用 `handler.Check().SetStrict(true)`。
- server -> client push 通道可用，workspace / turn / approval / UI refresh 事件全链路打通。
- approval 状态机迁移完成，不再依赖 V2 的隐式等待/通知路径。
- fx 图闭环：所有模块 `handler.Map` 自动注册，无手写旁路链。
- `platform/rpc/{transport_ws,codec,approval,push}` 全部落地。
- 不存在第二套手写注册链，也不存在 `server_context.go` 风格的 nil-guard 汇总层。
