# Reasonix 设计优点吸收计划

> 日期：2026-06-29
> 状态：执行中（Wave 1 / Wave 2 已闭环，Wave 3 已补上下文/权限策略实现，待主控复核）
> 范围：设计吸收计划与执行追踪；生产代码改动必须以源码、测试和 ADR 为准

## 0. 结论

V3 不应该复制 Reasonix 的整体架构。Reasonix 的长处是轻量 agent harness、接口优先、工具/provider registry、prompt prefix 稳定、MCP 命名清晰、上下文压缩和工具错误反馈简单直接；V3 的长处是 Fx 模块化装配、Onion/Clean Architecture 边界、桌面 UI、provider runtime、sidecar MCP、store、event wire 和产品化运行态。

吸收原则：

- 只吸收边界模式，不引入全局 `control.Controller`。
- 只在 owner module 内落状态，不引入 process-global registry。
- 继续保留 `cmd/mcp-orch` / `cmd/mcp-lsp` 独立运行态，不折回桌面主进程。
- 前端继续经 `frontend-app/src/shared/api/backendApi.js` 和 facade 访问后端，不绕过 payload guard。
- 每个吸收点必须有 owner、代码路径、测试面和 fail-fast 语义。

## 1. 当前已具备的基础

| 能力 | 当前代码证据 | 说明 |
| --- | --- | --- |
| Session port | `internal/contract/session.go`, `internal/app/session_ports.go`, `internal/module/thread/contract_adapter.go` | 已有 `SessionLifecyclePort` / `SessionStatusPort` / `SessionPorts` 与 thread adapter。 |
| Prompt prefix shape | `internal/dto/provider/session.go`, `internal/contract/prompt.go`, `internal/module/prompt/assembler.go` | 已有 `PromptAssemblyBoundary` / `PrefixShape`，并由 prompt assembler 构建。 |
| MCP tool namespace | `internal/platform/toolbridge/handler_peer_decode_helpers.go`, `internal/platform/toolbridge/mcp_namespace_test.go` | 已有 `WrapMCPToolName` / `SplitMCPToolName`，但文件名还不是独立 namespace owner。 |
| Event wire golden | `internal/platform/eventsurface/methods_test.go`, `frontend-app/src/shared/api/eventWireMethods.js`, `frontend-app/src/shared/api/eventWire.js` | 已有后端读取前端 event 方法表的校验面。 |
| Frontend session facade | `frontend-app/src/shared/api/sessionApi.js`, `frontend-app/src/entities/client/model/useClientStore.js`, `frontend-app/src/shared/api/backendApi.surface.test.js` | 已有 `sessionApi`，thread start/turn 产品调用已收口到 facade，surface test 禁止直接 import `startThread` / `startTurn`。 |
| Desktop dependency guard | `internal/archtest/desktop_dependency_test.go`, `internal/archtest/dependency_direction_test.go` | 已有 Wails 依赖隔离和依赖方向守卫。 |
| Tool error envelope | `internal/mcpserver/common/tool_error_envelope.go`, `internal/platform/toolbridge/handler_host_tools.go`, `internal/platform/toolbridge/proxy.go` | 已有结构化工具错误和 `isError` 语义。 |

后续执行不要重复造这些基础；优先补齐未闭合的行为边界、迁移剩余调用方和测试覆盖。

### 1.1 2026-06-29 执行记录

已合入 `main` 的相关提交：

- `7bf483f6`：批准 MCP tool lifecycle 实现。
- `4840998c`：新增 per-tool lifecycle owner/store/sqlc/migration。
- `6124ce31`：接入 lifecycle owner backfill。
- `dbf412e7`：接入 toolbridge list filtering 和 direct-call enforcement。

本轮复核后的状态边界：

- Wave 1 基础不退化检查已通过，后端和前端指定测试均已执行。
- Wave 2 的 ADR、owner API、store、backfill、toolbridge filtering、direct-call deny 已落地并合入 `main`。
- Wave 2 的 rollback/export 证据已补齐：owner 可按 workspace 导出全部 lifecycle rows，保留 disabled/suspended/removed 的人工状态、reason、replacement 和 deny code。
- Wave 2 的 frontend guarded API 已补齐：UI 如需控制 lifecycle，必须通过 `backendApi.js` 的 set/list/export facade，不允许页面拼裸 RPC。
- Wave 3 不是本轮完成范围；现有 `memory_read`、PrefixShape、read-only routing 和 tool error envelope 只能算基础，不等同于 Wave 3 全部完成。

已执行验证：

```bash
./scripts/test_with_guard.sh ./internal/contract ./internal/app ./internal/module/thread ./internal/module/prompt ./internal/provider/codexapp ./internal/provider/claudecli ./internal/platform/eventsurface ./internal/archtest -run 'Session|Prefix|Event|Desktop|Dependency|StartAssembly|BuildThreadStartParams' -count=1
cd frontend-app && npm test -- sessionApi.test.js eventWire.test.js backendApi.surface.test.js
./scripts/test_with_guard.sh ./internal/contract ./internal/store/mcpserver ./internal/module/mcp_server ./internal/platform/toolbridge ./internal/app -count=1
./scripts/test_with_guard.sh ./internal/store/mcpserver ./internal/module/mcp_server -run 'Export' -count=1
./scripts/test_with_guard.sh ./internal/module/mcp_server -run 'ToolLifecycle|Lifecycle' -count=1
cd frontend-app && npm test -- backendApi.test.js backendApi.contractMatrix.test.js backendApi.surface.test.js
./scripts/test_with_guard.sh ./internal/module/thread ./internal/module/memory ./internal/platform/toolbridge ./internal/mcpserver/common ./internal/provider/codexapp -run 'Compact|History|Memory|ReadOnly|ToolError|Envelope|StructuredContent' -count=1
make sqlc-verify
make guard
make build-plain
```

## 2. 吸收项 A：Session-facing Ports

### 为什么吸收

Reasonix 把 agent/session 入口收窄成接口，避免 UI、CLI、provider、tool 层直接耦合控制器。V3 当前 thread lifecycle 面很大，继续用 `SessionPorts` 收窄入口，可以让 UI/RPC/worker 调度只依赖 session 语义，不直接吃 thread service 的内部字段。

### 怎么吸收

保留现有 `contract.SessionPorts`。不要把它变成 JSON-RPC wire type，也不要替换 `thread/start` 的解码器。执行路线是：

1. 继续由 `internal/module/thread` 拥有 lifecycle/read 行为。
2. 继续由 `internal/module/thread/contract_adapter.go` 做 `contract.SessionStartRequest` 到 `thread.StartRequest` 的显式映射。
3. 由 `internal/app/session_ports.go` 聚合 lifecycle/status ports，Fx 只负责装配。
4. 迁移调用方时一条路径一条路径切，先读路径，再 mutation 路径。

### 改哪些代码

- `internal/contract/session.go`
  - 仅在确实新增 session 语义时扩展 DTO。
  - 新字段必须有 parity test 或明确 exemption。
- `internal/module/thread/contract_adapter.go`
  - 维护 `startRequestFromSession`。
  - 所有 slice/map/json.RawMessage 必须深拷贝，避免跨层 alias。
- `internal/app/session_ports.go`
  - 保持薄装配，不放业务逻辑。
- `internal/app/modules.go`
  - 只增删 Fx provider，不在 root module 写转换逻辑。
- `internal/module/thread/rpc.go`
  - 如果后续让 RPC handler 走 `SessionPorts`，必须先证明 wire 行为不变。
- `frontend-app/src/shared/api/sessionApi.js`
  - 前端只走 guarded facade，不直接 `callBackend`。
- 测试：
  - `internal/app/session_ports_test.go`
  - `internal/module/thread/*session*_test.go`
  - `internal/contract/*session*_test.go`
  - `frontend-app/src/shared/api/sessionApi.test.js`

### 注意事项

- `SessionStartRequest` 是内部 adapter DTO，不是前端/JSON-RPC 协议。
- 不允许丢 `Sandbox`、`MCP`、`Config`、`LaunchSkillRefs`、`AdditionalWorkingDirectories` 等启动字段。
- `thread/start` 的 alias、unknown field rejection、兼容 JSON tag 必须保留。
- 不要让 provider 反向 import `internal/module/thread`。

## 3. 吸收项 B：稳定 Event Wire

### 为什么吸收

Reasonix 的事件面更薄，模型/工具/前端之间不容易出现方法名漂移。V3 已经有 `eventsurface.Notification`、`ExpandNotifications`、RPC push、Wails bridge 和前端事件消费，最容易退化的是“后端新增事件，前端不知道”或“前端监听 legacy 事件，后端误删”。

### 怎么吸收

以现有 `eventsurface` 为事实源，不另起一套 event bus。把稳定方法表显式化，并用前后端 golden 对齐：

1. 后端维护 typed event method list。
2. 前端维护同名 event wire method list。
3. 后端测试读取前端 golden，双向校验新增/删除。
4. `ExpandNotifications` 继续承担 legacy compatibility，不在迁移期砍 `ui/thread/changed`、`ui/sidebar/changed` 等刷新事件。

### 改哪些代码

- `internal/platform/eventsurface/methods.go`
  - 维护 canonical event method set。
- `internal/platform/eventsurface/methods_test.go`
  - 读取 `frontend-app/src/shared/api/eventWireMethods.js` 并比对。
- `internal/platform/eventsurface/legacy.go`
  - 只维护 compatibility expansion，不新增业务 source of truth。
- `internal/platform/rpc/push.go`
  - provider raw event 到 `eventsurface` 的映射必须受 allowlist 控制。
- `internal/platform/rpc/push_worker.go`
  - 继续过滤空 method，保持 fail-fast/observable。
- `frontend-app/src/shared/api/eventWireMethods.js`
  - 前端可识别方法表。
- `frontend-app/src/shared/api/eventWire.js`
  - 只做解析和已知事件判断，不吞未知事件。
- 测试：
  - `internal/platform/eventsurface/*_test.go`
  - `internal/platform/rpc/push*_test.go`
  - `frontend-app/src/shared/api/eventWire.test.js`

### 注意事项

- 不要把 raw provider event 全量透给 UI。
- 不要把 compatibility 方法当成新的 canonical 领域事件。
- 新增事件必须同时说明 payload 形状、前端消费者和回滚策略。
- legacy expansion 改动必须有旧 UI 刷新路径回归测试。

## 4. 吸收项 C：Prompt Prefix Shape 和上下文稳定性

### 为什么吸收

Reasonix 的 prompt prefix 稳定性意识很强：稳定前缀、动态尾部、低频 compaction、可诊断的上下文变更。V3 已经有更复杂的 prompt assembly，如果没有 shape/telemetry，prompt cache miss、工具面变化、MCP 注入变化很难定位。

### 怎么吸收

继续使用现有 `PromptAssemblyBoundary` / `PrefixShape`，不要再发明平行 DTO。`PrefixShape` 只描述结构，不记录 prompt 正文：

1. `internal/module/prompt` 从 base instructions、developer instructions、boundary、resolved sections、suppressed tools 构造 shape。
2. provider start 时记录 shape hash、section 数量、suppressed tool 数量、cached/uncached 边界。
3. 后续接入 observability 时，只记录 metadata，不记录完整 system prompt。
4. compaction 成功后必须 invalidate prompt assembly，避免旧 shape 误用。

### 改哪些代码

- `internal/dto/provider/session.go`
  - `StartAssembly` 持有 `PrefixShape`。
- `internal/contract/prompt.go`
  - 继续 alias `PromptAssemblyBoundary` / `PrefixShape`，不要复制结构。
- `internal/module/prompt/assembler.go`
  - 维护 `BuildPrefixShape` 调用点。
- `internal/module/prompt/*prefix*_test.go`
  - 锁定 hash 稳定性、section order、suppressed tool order。
- `internal/module/thread/start_session_helpers.go`
  - provider start assembly 转换必须携带 `PrefixShape`，并保持 slice copy 语义。
- `internal/provider/codexapp/driver.go`
  - provider start telemetry 使用 shape metadata。
- `internal/provider/claudecli/driver.go`
  - 如果 Claude CLI 也消费 assembly，同步接入 metadata。
- `internal/module/thread/compact_event_test.go`
  - 保持 compact 后 prompt invalidation 测试。

### 注意事项

- 日志和 trace 里不能出现 prompt 正文、用户正文或 secret。
- `PrefixShape` 变化必须能解释原因，不能只给 hash。
- suppressed tools、MCP tools、memory section 都要 deterministic sort。
- 不要把 `PrefixShape` 变成 provider-specific 字段。

## 5. 吸收项 D：MCP Namespace 和 Per-tool Lifecycle

### 为什么吸收

Reasonix 的 MCP 工具名更统一，`mcp__server__tool` 这种命名让工具来源清晰。V3 同时有 host tools、MCP sidecar tools、HTTP MCP tools、provider dynamic tools，如果 namespace 和 lifecycle 不硬，很容易出现同名覆盖、禁用工具仍可 direct-call、readOnlyHint 被误信任等问题。

### 怎么吸收

该项已按“先 namespace、后 lifecycle”分波落地。当前仓库仍保留 MCP server config 级 `enabled` 作为 server 开关，但 per-tool lifecycle 已有批准的 owner/storage 决策、持久化 schema、backfill、rollback/export、toolbridge filtering、direct-call deny 和 frontend guarded API：

1. Namespace 先稳定：所有 MCP wrapped name 都走统一 helper。
2. Lifecycle 由 `internal/module/mcp_server` owner 维护事实源，经 `internal/store/mcpserver` 持久化，并由 toolbridge 在 ListTools 和 direct-call 两条路径执行 deny。
3. UI 后续如需控制 lifecycle，只能通过 `frontend-app/src/shared/api/backendApi.js` 的 guarded set/list/export facade，不允许页面或 service 自己拼 raw RPC payload。

当前 lifecycle 状态表：

| 状态 | 含义 | ListTools | Direct Call |
| --- | --- | --- | --- |
| `enabled` | 正常可见可调 | 展示 | 允许 |
| `disabled` | 用户关闭 server/tool | 不展示 | 拒绝，返回 stable tool error |
| `suspended` | 临时策略阻断 | 不展示或展示为 disabled capability | 拒绝，说明策略原因 |
| `removed` | 已迁移/废弃 | 不展示 | 拒绝，给迁移提示 |

### 改哪些代码

- `internal/platform/toolbridge/handler_peer_decode_helpers.go`
  - 当前 `WrapMCPToolName` / `SplitMCPToolName` 所在位置。
  - 后续可抽成 `internal/platform/toolbridge/mcp_namespace.go`，但必须保持测试。
- `internal/platform/toolbridge/handler_peer_decode.go`
  - `addMCPToolsToSurface` 走 namespace helper。
  - 已接入 lifecycle filtering，ListTools 不展示 disabled/suspended/removed 工具。
- `internal/platform/toolbridge/handler_host_tools.go`
  - direct-call 入口已识别 disabled/suspended/removed；不能只靠列表隐藏。
- `internal/contract/mcp_control.go`
  - 已扩展 per-tool lifecycle DTO、store 端口和 policy reader。
- `internal/module/mcp_server/service.go`
  - owner module 维护 server/tool lifecycle，并提供 set/list/export/resolve 行为。
- `internal/store/mcpserver/store.go`
  - 已新增 migration/store/test 持久化 lifecycle，不用内存 registry 当事实源。
- `frontend-app/src/shared/api/backendApi.js`
  - 已新增 guarded set/list/export API；后续 UI 控制入口必须复用它。
- 测试：
  - `internal/platform/toolbridge/mcp_namespace_test.go`
  - `internal/platform/toolbridge/*tools*_test.go`
  - `internal/module/mcp_server/*_test.go`
  - `internal/store/mcpserver/*_test.go`

### 注意事项

- 不信任外部 MCP `readOnlyHint`，只能作为提示，最终策略由 V3 owner 决定。
- ListTools 隐藏不等于安全，direct-call 必须单独 deny。
- alias/canonical name 两条路径都要测。
- lifecycle 状态必须继续以 owner/store 为事实源，不允许在 toolbridge 里新增临时 map。
- `docs/li/reasonix-absorption-spikes/mcp-tool-lifecycle.md` 只保留为历史 spike；当前事实以 ADR 0003、owner/store、toolbridge enforcement 和 guarded facade 为准。
- 不要让 `cmd/mcp-orch` 的 runtime 状态反向成为桌面主进程事实源。

## 6. 吸收项 E：Provider / Tool Capability Registry Discipline

### 为什么吸收

Reasonix 的 provider/tool registry 简单直接，新增能力通过 interface + registry 进入系统。V3 不能照搬 process-global registry，但可以吸收“能力集中声明、schema 稳定、调用路径可枚举”的纪律，减少 provider/toolbridge 中的 string switch 和散落别名。

### 怎么吸收

使用 Fx 提供 registry，不使用全局变量。registry 只表达能力，不拥有运行态状态：

1. provider 由 `internal/provider/unified` 统一选择。
2. dynamic tool schema 由 `internal/platform/toolbridge` 统一生成。
3. host tools 用 `HostToolRegistry` 组合，不让 peer MCP 覆盖 host-direct tool。
4. capability 变化必须进入 contract/test，不只改字符串。

### 改哪些代码

- `internal/provider/unified/module.go`
  - 继续作为 provider registry 的 Fx owner。
- `internal/provider/unified/client.go`
  - provider lookup fail-fast，不 silent fallback。
- `internal/contract/provider.go`
  - provider-facing contract 扩展时先加 typed field。
- `internal/contract/toolbridge.go`
  - dynamic tool schema / setter contract。
- `internal/platform/toolbridge/host_tools.go`
  - host tool registry 组合入口。
- `internal/platform/toolbridge/handler_peer_decode.go`
  - peer MCP tools 到 Codex dynamic tools 的唯一转换入口。
- 测试：
  - `internal/provider/unified/*_test.go`
  - `internal/platform/toolbridge/*_test.go`
  - `internal/contract/*_test.go`

### 注意事项

- registry 不持久化用户状态。
- registry 不做权限判定，只声明能力；policy 在 owner/service 层。
- 新 provider 不允许直接 import UI/store 具体实现。
- unknown provider / unknown tool 必须返回明确错误。

## 7. 吸收项 F：Frontend Guarded Facade

### 为什么吸收

Reasonix 的 CLI/API 面很薄，调用入口清晰。V3 前端更复杂，如果页面直接绕过 `backendApi.js`，payload 校验、method 常量、contract matrix 都会失效。

### 怎么吸收

继续把 session 相关调用收束到 `sessionApi`，把 raw bridge 隐藏在 `backendApi.js` 内部：

1. 页面/store 不直接 import `callBackend`。
2. session 操作通过 `sessionApi.start`、`sessionApi.startTurn`、`sessionApi.interrupt`、`sessionApi.messages`。
3. 新 facade 只能包已有 guarded backend API，不能构造裸 RPC payload。
4. 迁移消费方时一处一处切，先高频 thread start/list/messages，再 fork/resume/approval。

### 改哪些代码

- `frontend-app/src/shared/api/backendApi.js`
  - RPC method、payload builder、guarded exports。
- `frontend-app/src/shared/api/backendApi.surface.test.js`
  - 禁 raw bridge surface，并禁止产品代码直接从 `backendApi.js` import `startThread` / `startTurn`。
- `frontend-app/src/shared/api/sessionApi.js`
  - session facade。
- `frontend-app/src/shared/api/sessionApi.test.js`
  - 证明 facade 不调用 raw `callBackend`。
- `frontend-app/src/entities/client/model/useClientStore.js`
  - thread start/turn 注入点、stopped-thread recovery、dashboard command turn 都走 `sessionApi`。
- `frontend-app/src/entities/client/model/forkSlice.js`
  - fork/resume 入口通过 `useClientStore` 传入的 `sessionApi` deps 调用。
- `frontend-app/src/pages/workflows/WorkflowPage.jsx`
  - workflow 页面里的 session 操作迁移到 facade。
- `frontend-app/src/pages/workflows/services/workflowPageService.js`
  - workflow 服务层通过 `sessionApi` 转发 `startThread` / `startTurn`。

### 注意事项

- 不要新增 `window.go.main.App.*` 直连。
- 不要在页面里手写 RPC method 字符串。
- payload normalization 留在 `backendApi.js`，页面只传业务字段。
- facade 测试要 mock guarded exports，而不是 mock raw bridge。

## 8. 吸收项 G：Desktop Dependency Isolation

### 为什么吸收

Reasonix 的核心 harness 不依赖桌面 UI。V3 是桌面产品，但 core module/provider/platform 不能知道 Wails，否则后续 sidecar、CLI、测试和云化都会被 UI 依赖绑死。

### 怎么吸收

保留现有 archtest，并把例外范围写清：

1. `internal/app` 可以装配 Wails。
2. `internal/ui/wails` 可以依赖 Wails。
3. `internal/module/*`、`internal/provider/*`、非 UI `internal/platform/*` 禁止 import Wails。
4. `internal/platform/rpc` 可以出现 wails websocket 命名，但不能 import Wails runtime。

### 改哪些代码

- `internal/archtest/desktop_dependency_test.go`
  - 维护 forbidden import 范围。
- `internal/archtest/dependency_direction_test.go`
  - 保持 module/provider/platform 依赖方向。
- `internal/app/app.go`
  - Wails 装配集中在这里。
- `internal/app/runner.go`
  - lifecycle binding 保持 app 层。
- `internal/ui/wails/*`
  - Wails adapter 和 bridge owner。

### 注意事项

- 不要为了修一个 UI 问题把 Wails type 塞进 contract。
- `internal/app` 是 composition root，不是业务逻辑层。
- archtest 的 allowlist 必须写原因和移除条件。
- 出现 import guard 失败时，优先改依赖方向，不优先扩大 allowlist。

## 9. 吸收项 H：上下文压缩和历史/记忆检索

### 为什么吸收

Reasonix 把低频 compaction、history retrieval、memory retrieval 作为减少 prompt 膨胀的核心手段。V3 已经有 thread compact、persisted history、memory_read host tool，可以吸收 Reasonix 的“少塞 prompt，多让模型按需取”的策略。

### 怎么吸收

不要把所有历史和记忆预塞进 system prompt。执行路线：

1. thread history 继续由 `internal/module/thread` 提供分页读取。
2. memory retrieval 继续通过 host-direct `memory_read`，不 fallback 到 peer MCP memory tool。
3. compact 成功后发布 compacted event 并 invalidate prompt assembly。
4. `history_read` 是 host-direct bounded tool，只允许 `scope=current_thread`、显式 `limit=1..50` 和可选非空 `cursor`。

### 改哪些代码

- `internal/module/thread/history.go`
  - history read owner。
- `internal/module/thread/read_view_test.go`
  - persisted history / fail-fast 行为。
- `internal/module/thread/command.go`
  - `/compact` 命令入口。
- `internal/module/thread/compact_event_test.go`
  - compact event + prompt invalidation。
- `internal/platform/toolbridge/memory_read_tool.go`
  - host-direct memory retrieval 和 current-thread history retrieval。
- `internal/platform/toolbridge/host_tools_memory_*_test.go`
  - 禁 peer fallback、disabled stable envelope。
- `internal/module/memory/module.go`
  - memory reader provider。

### 注意事项

- retrieval 工具必须有 limit、scope、visibility 校验。
- memory/history 不存在时返回 typed error，不返回空假数据。
- compact 不能静默吞 provider 不支持错误。
- 不要把完整历史 dump 到 prompt prefix。

## 10. 吸收项 I：权限、Read-only Hint 和 Tool Trust

### 为什么吸收

Reasonix 的工具权限语义更直接。V3 接入外部 MCP、host tools、provider tools 后，最危险的是把外部声明的 read-only 当成可信事实，或者把 plan/read-only 模式和真实执行混在一起。

### 怎么吸收

把 hint、policy、execution 分开：

1. MCP/tool schema 的 `readOnlyHint` 只能作为 hint。
2. V3 自己的 policy 决定工具是否能执行。
3. disabled/suspended 工具 direct-call 返回 stable tool error。
4. provider 侧 read-only 解析保持保守，malformed input 按非只读处理。

### 改哪些代码

- `internal/provider/codexapp/driver_pool_routing.go`
  - read-only routing 解析和保守策略。
- `internal/contract/provider.go`
  - tool filter mode / policy contract。
- `internal/platform/toolbridge/memory_read_tool.go`
  - host tool guard 模式可作为参考。
- `internal/platform/toolbridge/handler_host_tools.go`
  - direct-call policy gate。
- `internal/platform/toolbridge/handler_peer_decode.go`
  - ListTools 时不要把 hint 当 allow decision。
- `internal/dto/mcp/tool.go`
  - 如果引入 hint 字段，要保持注释说明“不可信”。

### 注意事项

- read-only 不是安全沙箱，只是 policy 输入。
- malformed read-only metadata 必须 fail closed 或按非只读处理。
- 工具是否可见和是否可调用要分别测试。
- 审批/plan mode 的语义不能放在前端按钮状态里兜底。

## 11. 吸收项 J：结构化 Tool Error Envelope

### 为什么吸收

Reasonix 的 agent loop 倾向把工具失败反馈给模型，让模型能修正参数或换策略，而不是把整个 session 打断。V3 已经有 `ToolErrorEnvelope`，应该继续把“工具业务错误”和“基础设施失败”分开。

### 怎么吸收

统一所有 host/direct/MCP tool result 的错误形态：

1. handler 已选定后，业务错误返回 `ToolErrorEnvelope`。
2. envelope 同时进入 plain text 和 `structuredContent`。
3. `isError` 根据 envelope 或 `success=false` 判断。
4. transport/bootstrap/registry 缺失等基础设施错误仍然返回 RPC error，不伪装成工具结果。
5. host-direct 现有 `kind=host_tool_error` 结果形态进入统一 envelope 前，必须先写明兼容策略和迁移测试；不能静默改掉已被 UI/provider 消费的稳定字段。

### 改哪些代码

- `internal/mcpserver/common/tool_error_envelope.go`
  - envelope schema、分类、plain text。
- `internal/mcpserver/common/tool_result.go`
  - `isError` 判断。
- `internal/mcpserver/common/server.go`
  - MCP tools/call 错误包装。
- `internal/platform/toolbridge/handler_host_tools.go`
  - host-direct result marshal。
- `internal/platform/toolbridge/proxy.go`
  - proxy result to MCP payload。
- `internal/platform/toolbridge/handler_peer_decode.go`
  - structuredContent failure 识别。
- `internal/provider/codexapp/session_rollout_events.go`
  - provider event 中工具结果展示。

### 注意事项

- 不要把所有 error 都吞成 tool result；启动失败、协议错误、鉴权失败应保留硬错误。
- envelope code 要稳定，方便 UI、模型和测试判断。
- plain text 只放模型立即可用的信息，完整结构放 `structuredContent`。
- secret、token、绝对敏感路径不能进入 envelope meta。
- host-direct error 迁移必须保留旧字段兼容期，或用明确版本门控切换；任何直接改 wire shape 的实现都需要先回到 NEEDS_APPROVAL。

## 12. 不吸收清单

| Reasonix 设计 | 不吸收原因 |
| --- | --- |
| 全局 `control.Controller` | V3 已有 Fx graph 和 owner module，复制会形成第二套 composition root。 |
| process-global provider/tool registry | 会绕过 Fx 生命周期、测试注入和模块边界。 |
| 把 `cmd/mcp-orch` 合进桌面 app | 会破坏 sidecar 隔离和故障边界。 |
| 把 React UI 移回旧 frontend | 会扩大迁移面，且不解决架构问题。 |
| 前端 raw RPC / raw Wails bridge | 会绕过 `backendApi.js` payload guard 和 contract matrix。 |
| 信任外部 MCP readOnlyHint | hint 不是 policy，不能当安全依据。 |
| 用文档状态替代生产状态 | 每次执行必须重新读源码和测试。 |

## 13. 推荐执行顺序

### Wave 1：确认已吸收基础不退化

- [x] 跑 session ports 相关单测。
- [x] 跑 prompt prefix shape 相关单测。
- [x] 跑 event wire 前后端 golden。
- [x] 跑 frontend `sessionApi` 测试。
- [x] 跑 desktop dependency guard。

建议命令：

```bash
./scripts/test_with_guard.sh ./internal/contract ./internal/app ./internal/module/thread ./internal/module/prompt ./internal/provider/codexapp ./internal/provider/claudecli ./internal/platform/eventsurface ./internal/archtest -run 'Session|Prefix|Event|Desktop|Dependency|StartAssembly|BuildThreadStartParams' -count=1
cd frontend-app && npm test -- sessionApi.test.js eventWire.test.js backendApi.surface.test.js
```

### Wave 2：MCP lifecycle 决策与闭环（已闭环）

- [x] 决策文档：新增并批准 `docs/adr/0003-mcp-tool-lifecycle-owner-storage.md` 或等价决策，明确 per-tool lifecycle 的 owner、状态枚举、server/tool 边界、store schema、migration/backfill、rollback、可观测性和验收测试。
- [x] owner API：决策批准后再扩展 `internal/contract/mcp_control.go` + `internal/module/mcp_server`。
- [x] store：决策批准后新增 `internal/store/mcpserver` migration/store/test；不能把现有 server config 级 `enabled` 当成 per-tool lifecycle 事实源。
- [x] toolbridge filtering：决策批准后，ListTools 不展示 disabled/suspended/removed。
- [x] direct-call deny：决策批准后，隐藏工具被调用时返回 stable tool error。
- [x] rollback/export：补 lifecycle state 导出或降级验证，证明回滚时不会丢失用户显式关闭、暂停或移除的状态。
- [x] frontend guarded API：如果 UI 需要控制 lifecycle，必须通过 `backendApi.js` guarded API；本轮已补 set/list/export facade 与 RPC handler。

建议命令：

```bash
./scripts/test_with_guard.sh ./internal/contract ./internal/store/mcpserver ./internal/module/mcp_server ./internal/platform/toolbridge -run 'MCP|Lifecycle|Tool|Namespace|Disabled|Suspended|Removed' -count=1
cd frontend-app && npm test -- backendApi.test.js backendApi.contractMatrix.test.js backendApi.surface.test.js
```

### Wave 3：上下文和权限策略

- [x] 为 history/memory retrieval 定义 bounded tool contract；`history_read` 已作为 app host-direct toolbridge registry 接入 `contract.SessionStatusPort.ReadMessages(ctx, threadID, limit, before)`，threadID 只来自可信 `HostToolCall.ThreadID` metadata，schema 只允许 `scope/limit/cursor`。
- [x] 补 plan/read-only/tool trust 策略测试；覆盖 read-only sandbox 保守解析、`readOnlyHint` 不升级为本地 policy、`memory_read/memory_write/history_read` host-only reserved list/call filtering 和 no-peer-fallback。
- [x] 把 PrefixShape telemetry 接入 observability；Codex provider start 已记录 prefix hash、section 列表和 cached/uncached 字节元数据。
- [x] 先批准 host-direct error 兼容策略，再标准化所有 host-direct tool error envelope；采用“加字段兼容，不替换旧 kind/approval”策略，保留 `kind/tool/error/code/approval`，新增 `success:false/retryable/hint/meta` 双协议镜像，普通 MCP `ToolErrorEnvelope` 不带 host-direct `kind`。

建议命令：

```bash
./scripts/test_with_guard.sh ./internal/module/thread ./internal/module/memory ./internal/platform/toolbridge ./internal/mcpserver/common ./internal/provider/codexapp -run 'Compact|History|Memory|ReadOnly|ToolError|Envelope|StructuredContent' -count=1
```

### 合并前统一验证

按实际变更面执行，不能用单个 narrow test 代替最终 gate：

```bash
make guard
./scripts/test_with_guard.sh ./internal/module/thread ./internal/module/prompt ./internal/provider/codexapp ./internal/provider/claudecli -run 'PrefixShape|ToProviderStartAssembly|StartAssembly|BuildThreadStartParams' -count=1
cd frontend-app && npm run lint && npm test && npm run build
make sqlc-verify
```

- 没有前端变更时可跳过 frontend 三连，但必须说明跳过原因。
- 没有 store/schema/migration 变更时可跳过 `make sqlc-verify`，但 Wave 2 一旦触碰 store 必须执行。
- 如果 `frontend-app` 依赖未安装，先按 lockfile 安装依赖；缺少 `vitest` 或 node 依赖不能算 PASS。

## 14. 最终验收标准

- 每个吸收点都有 owner module，不存在“临时全局状态”。
- 每个跨层入口都有 contract 或 facade，不直接 import 具体实现。
- 每个工具可见性变化都有 direct-call deny 测试。
- 每个前端 RPC 新入口都经过 `backendApi.js` payload guard。
- 每个 prompt/context 变化都能用 `PrefixShape` 或 trace 解释。
- 所有变更保持 `cmd/mcp-orch` / `cmd/mcp-lsp` 独立运行态。
- MCP per-tool lifecycle 进入实现前，必须已有批准的 owner/storage 决策文档。
- 任何架构 guard allowlist 都有原因和移除条件。
