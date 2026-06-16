# V2 ↔ V3 事件流 1:1 对齐审计

## 范围

本报告只基于当前源码做对齐，不引用既有迁移文档结论。

- V2 核心来源
  - `go-agent-v2/internal/apiserver/notifications.go:10-100`
  - `go-agent-v2/internal/apiserver/server_payload.go:19-286`
  - `go-agent-v2/internal/apiserver/server_conn.go:136-170`
  - `go-agent-v2/cmd/agent-terminal/main_setup.go:372-375`
  - `go-agent-v2/cmd/agent-terminal/app_bridge.go:13-129`
  - `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:422-456`
  - `go-agent-v2/cmd/agent-terminal/frontend/vue-app/app.js:271-275`
  - `go-agent-v2/cmd/agent-terminal/frontend/vue-app/stores/bridge-event-parser.js:11-64`
- V3 核心来源
  - `internal/dto/shared/event.go:5-128`
  - `internal/dto/agent/event.go:5-44`
  - `internal/dto/turn/event.go:5-57`
  - `internal/dto/tool/event.go:5-38`
  - `internal/dto/task/event.go:5-41`
  - `internal/dto/workspace/event.go:5-52`
  - `internal/dto/ui/event.go:5-31`
  - `internal/provider/unified/event_map.go:12-66`
  - `internal/provider/codexapp/session.go:237-263`
  - `internal/provider/claudecli/session_events.go:46-54`
  - `internal/provider/codexapp/event_map.go:24-154`
  - `internal/provider/claudecli/event_map.go:21-103`
  - `internal/sidecar/orch/orchestration/events.go:13-64`
  - `internal/module/workspace/service.go:40-58`
  - `internal/module/workspace/service_helpers.go:220-284`
  - `internal/platform/rpc/push.go:16-92`
  - `internal/platform/rpc/module.go:54-72`
  - `internal/platform/rpc/approval_events.go:14-74`
  - `internal/ui/wails/bridge.go:16-109`
  - `internal/ui/wails/lifecycle.go:11-122`
  - `internal/ui/wails/module.go:17-134`
  - `internal/ui/wails/frontend/index.html:1-53`

## 结论先行

| 对比项 | 结论 | 结论说明 |
| --- | --- | --- |
| V2 事件频道清单 vs V3 typed event 清单 | ❌ | V2 对外 public method 面更宽；V3 typed event 定义面和 live push/Wails 面不是一回事，不能 1:1 替代。 |
| push method name 兼容性（`ui/state/changed`、`turn/started`、`turn/completed` 等） | ⚠️ | 只有少数热点 method 保留；`turn/started` / `turn/completed` 对齐，`ui/state/changed` 仅名称兼容，语义不等价，其他大量 method 未对齐。 |
| Wails `bridge-event` 频道 | ⚠️ | 后端频道名和 envelope 形状与 V2 一致，而且当前源码已经接线；但缺少 `agent-event` 辅助通道，内嵌 frontend 也没有任何 runtime 订阅。 |
| 双源事件隔离 | ❌ | V2 有 `bridge-event` + `agent-event` 双入口，且有 thread/CWD 隔离；V3 当前只有单路 `bridge-event`，未见对应隔离层。 |

## 1. 端到端链路对比

### V2 实际链路

1. provider raw event 先经 `eventMethodMap` 归一化成 public method，入口在 `go-agent-v2/internal/apiserver/notifications.go:10-100`。
2. `notify()` 会先同步 UI runtime / patch / refresh，再走 `broadcastNotification()` 把 JSON-RPC notification 发到 SSE / WS，并触发 `notifyHook`，见 `go-agent-v2/internal/apiserver/server_payload.go:32-46`、`go-agent-v2/internal/apiserver/server_conn.go:136-170`。
3. desktop 入口把 `notifyHook` 接到 `handleBridgeNotification()`，见 `go-agent-v2/cmd/agent-terminal/main_setup.go:372-375`。
4. `handleBridgeNotification()` 总是发 `bridge-event`，有 `threadId` 时再补发 `agent-event`，并带 CWD 过滤，见 `go-agent-v2/cmd/agent-terminal/app_bridge.go:40-129`。
5. frontend 同时订阅 `agent-event` 和 `bridge-event`，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:422-456`、`go-agent-v2/cmd/agent-terminal/frontend/vue-app/app.js:271-275`。

### V3 实际链路

1. provider 侧 `codexapp` / `claudecli` 都会把 raw event 送入 `EventDispatcher.Dispatch()`，见 `internal/provider/codexapp/session.go:237-263`、`internal/provider/claudecli/session_events.go:46-54`、`internal/provider/unified/event_map.go:43-66`。
2. translator 把 raw event 翻译为 typed event，再 `event.Publish()` 进 bus，见 `internal/provider/codexapp/event_map.go:24-154`、`internal/provider/claudecli/event_map.go:21-103`、`internal/provider/unified/event_map.go:49-64`。
3. orchestration 和 workspace 也直接往 typed bus 发事件，见 `internal/sidecar/orch/orchestration/events.go:13-64`、`internal/module/workspace/service.go:49-58`、`internal/module/workspace/service_helpers.go:220-284`。
4. jrpc2 push live path 只订阅 3 个 typed event 并发 3 个 method：`ui/state/changed`、`turn/started`、`turn/completed`，见 `internal/platform/rpc/push.go:16-92`、`internal/platform/rpc/module.go:54-72`。
5. Wails bridge live path 也只订阅这 3 个 typed event，但统一封装到 `bridge-event` 信道，见 `internal/ui/wails/bridge.go:42-89`、`internal/ui/wails/module.go:120-134`。
6. 当前内嵌 frontend 只是占位页，没有任何 event runtime 订阅代码，见 `internal/ui/wails/frontend/index.html:1-53`。

结论：V3 当前真实形态是“typed bus 面 > RPC/Wails 对外面 > frontend 消费面”。和 V2 的“更宽 public method 面 + 双前端入口”不是同一层级。

## 2. V2 public method 面 vs V3 typed event 面

### V2 public method 面

- `eventMethodMap` 当前守卫长度为 `71`，见 `go-agent-v2/internal/apiserver/server_event_mapping_matrix_guard_test.go:9-12`。
- 这 71 条映射覆盖的 public method 家族很宽，至少包含：
  - thread 生命周期与元数据：`thread/started`、`thread/name/updated`、`thread/tokenUsage/updated`、`thread/compacted`
  - turn 生命周期：`turn/started`、`turn/completed`、`turn/diff/updated`、`turn/plan/updated`
  - item / tool / exec 流：`item/started`、`item/completed`、`item/agentMessage/delta`、`item/reasoning/*`、`item/commandExecution/*`、`item/fileChange/*`、`item/tool/call`
  - mcp / account / app / fuzzy / system：`agent/event/mcp_*`、`account/*`、`app/list/updated`、`fuzzyFileSearch/*`、`deprecationNotice`、`configWarning`、`error`、`agent/event/background_event`
- 除了 public method，V2 apiserver 还会合成 UI refresh 通知 `ui/thread/changed` 和 `ui/sidebar/changed`，见 `go-agent-v2/internal/apiserver/server_payload.go:23-24,114-122,145-201`。
- Wails/desktop 前端通道层还有 `bridge-event` 与 `agent-event`，见 `go-agent-v2/cmd/agent-terminal/app_bridge.go:94-115`。

### V3 typed event 面

- V3 typed event 定义分成 6 族：
  - agent：`StateChanged`、`AgentLaunched`、`AgentStopped`、`AgentRecovering`、`AgentFailed`，见 `internal/dto/agent/event.go:5-44`
  - turn：`TurnStarted`、`TurnCompleted`、`TurnInterrupted`、`TurnStalled`、`TurnResumed`、`TurnInputReceived`、`TurnOutputDelta`，见 `internal/dto/turn/event.go:5-57`
  - tool：`ToolCallBegin`、`ToolCallEnd`、`ToolApprovalRequested`、`ToolApprovalResolved`，见 `internal/dto/tool/event.go:5-38`
  - task：`TaskDagCreated`、`TaskNodeStatusChanged`、`TaskWakeupDispatched`、`TaskWakeupCompleted`，见 `internal/dto/task/event.go:5-41`
  - workspace：`WorkspaceRunCreated`、`WorkspaceRunStatusChanged`、`WorkspaceRunMerged`、`WorkspaceRunAborted`、`WorkspaceRunMergeError`，见 `internal/dto/workspace/event.go:5-52`
  - ui：`UIProjectionUpdated`、`UITimelineAppended`、`UITokensUpdated`，见 `internal/dto/ui/event.go:5-31`
- 当前源码能确认 live publish 的主要是：
  - agent：provider translator + orchestration 都会发，见 `internal/provider/codexapp/event_map.go:40-75`、`internal/provider/claudecli/event_map.go:35-53`、`internal/sidecar/orch/orchestration/events.go:13-64`
  - turn：`TurnStarted` / `TurnCompleted` / `TurnInterrupted` / `TurnInputReceived` / `TurnOutputDelta` 有 publisher，见 `internal/provider/codexapp/event_map.go:77-113`、`internal/provider/claudecli/event_map.go:55-85`
  - tool：4 个都有 publisher，见 `internal/provider/codexapp/event_map.go:115-154`、`internal/provider/claudecli/event_map.go:87-103`、`internal/platform/rpc/approval_events.go:23-43`
  - workspace：5 个都有 publisher，见 `internal/module/workspace/service.go:49-58`、`internal/module/workspace/service_helpers.go:220-284`
- 当前源码未看到对应 publisher 的 typed 事件：
  - `TurnStalled`、`TurnResumed`
  - 整个 task 族
  - 整个 ui 族
  - 这里是“当前树内未见发布者”的源码结论，不是协议层永久结论

### 对齐判断

| 家族 | V2 public 面 | V3 typed 面 | 1:1 结论 |
| --- | --- | --- | --- |
| agent / turn 核心生命周期 | V2 有 `thread/started`、`turn/started`、`turn/completed`、`turn/aborted`、`error` | V3 有 agent/turn typed 事件 | ⚠️ 只有 `turn/started` / `turn/completed` 真正继续对外暴露；`thread/started` 等没有保留为对外 method。 |
| output / item / tool / approval | V2 有大量 `item/*`、`item/commandExecution/*`、`item/fileChange/*`、`item/tool/call` | V3 有 `TurnOutputDelta`、tool call、approval typed 事件 | ❌ typed bus 有语义，但 V3 对外 push/Wails 没有保留这些 public method。 |
| thread metadata / UI refresh | V2 有 `thread/name/updated`、`thread/tokenUsage/updated`、`thread/compacted`、`ui/thread/changed`、`ui/sidebar/changed` | V3 只有 UI typed DTO 定义，当前未见发布者，也未桥到前端 | ❌ |
| mcp / account / app / fuzzy / deprecation / background | V2 public 面存在 | V3 当前 typed 面没有 1:1 对应族 | ❌ |
| workspace / task / UI projection | V2 不是 `eventMethodMap` 主干；V2 apiserver 另有 workspace UI 同步逻辑 | V3 定义了 workspace/task/ui typed 族，其中 workspace 已发布 | ⚠️ 这是 V3 自己的内部 typed 扩展，不等于对 V2 public 面做了 1:1 迁移。 |

结论：如果目标是“V2 public method 面 1:1 迁到 V3”，当前状态是 **未对齐**。V3 typed event 体系只是内部抽象层，不能直接视作 V2 public method 的等价物。

## 3. push method name 兼容性

### V3 当前 live push method

V3 jrpc2 push live path 当前只有 3 个 method 常量：

- `ui/state/changed`
- `turn/started`
- `turn/completed`

来源：`internal/platform/rpc/push.go:16-20,75-92`

Wails bridge 这边内部也只消费同样 3 个 method 名，再统一封装到 `bridge-event`，见 `internal/ui/wails/bridge.go:16-20,53-63,81-89`。

### 逐项判断

| method | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| `turn/started` | 在 `eventMethodMap` 中存在，见 `go-agent-v2/internal/apiserver/notifications.go:14` | jrpc2 push 和 Wails bridge 都保留，见 `internal/platform/rpc/push.go:18,85-87`、`internal/ui/wails/bridge.go:18,57-59` | ✅ |
| `turn/completed` | 在 `eventMethodMap` 中存在，且 `turn_aborted` / `idle` 也折叠到它，见 `go-agent-v2/internal/apiserver/notifications.go:15-17` | jrpc2 push 和 Wails bridge 都保留；`codexapp` 也把 `turn/aborted` 翻译为 `TurnCompleted`，见 `internal/platform/rpc/push.go:19,88-90`、`internal/ui/wails/bridge.go:19,60-62`、`internal/provider/codexapp/event_map.go:81-86` | ✅ |
| `ui/state/changed` | 当前源码里能确认它被 frontend/desktop 视作兼容热点 method，但在本次扫描中没看到它来自 `eventMethodMap` 的直接生产路径，见 `go-agent-v2/cmd/agent-terminal/app_helpers.go:104-111` | V3 会把 `agentdto.StateChanged` 直接推成这个 method，见 `internal/platform/rpc/push.go:17,82-84`、`internal/ui/wails/bridge.go:17,54-56` | ⚠️ 只有名字兼容；V2 侧更像 UI refresh / compat method，V3 侧是 agent state transition typed event。 |
| `thread/started` | V2 存在，见 `go-agent-v2/internal/apiserver/notifications.go:11` | V3 当前没有任何 push/Wails method 保留它；只有内部 typed `AgentLaunched`，见 `internal/dto/agent/event.go:13-18` | ❌ |
| `thread/tokenUsage/updated` / `thread/compacted` / `turn/diff/updated` / `turn/plan/updated` | V2 存在，见 `go-agent-v2/internal/apiserver/notifications.go:13,18-21` | V3 当前没有对应 push/Wails method | ❌ |
| `item/*` / `item/commandExecution/*` / `item/fileChange/*` / `item/tool/call` | V2 存在，见 `go-agent-v2/internal/apiserver/notifications.go:24-58` | V3 有内部 typed 事件，但 push/Wails 没有同名 method 输出 | ❌ |

结论：push method 层面的兼容只覆盖了极小子集，不能说“已对齐”，只能说“保留了几个热点 method 名”。

## 4. Wails `bridge-event` 频道

### 已对齐的部分

| 项目 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| 频道名 | `bridge-event`，见 `go-agent-v2/cmd/agent-terminal/app_bridge.go:94-95` | `bridgeEventName = "bridge-event"`，见 `internal/ui/wails/lifecycle.go:11-15` | ✅ |
| envelope 形状 | `{type, payload}`，见 `go-agent-v2/cmd/agent-terminal/app_bridge.go:13-20,94-95` | `{type, payload}`，见 `internal/ui/wails/bridge.go:81-89` | ✅ |
| runtime emitter 接线 | `notifyHook -> handleBridgeNotification -> emitUIEvent(...)`，见 `go-agent-v2/cmd/agent-terminal/main_setup.go:372-375`、`go-agent-v2/cmd/agent-terminal/app_bridge.go:40-129` | fx `bindEventBridge()` 会在启动时 `bridge.Start()`，`SetEventEmitter()` 绑定到 Wails runtime，见 `internal/ui/wails/module.go:72-98,120-134` | ✅ |

### 未对齐的部分

| 项目 | 现状 | 结论 |
| --- | --- | --- |
| 通道宽度 | V2 除了 `bridge-event` 还有 `agent-event`，V3 当前只有 `bridge-event` | ⚠️ |
| bridge 覆盖面 | V2 bridge 承接的是整个 apiserver public method 面；V3 bridge 当前只承接 3 个 typed event | ⚠️ |
| frontend 消费方 | V2 frontend 真正订阅 `bridge-event`，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:440-456`、`go-agent-v2/cmd/agent-terminal/frontend/vue-app/app.js:274-275`；V3 当前内嵌 frontend 只是静态占位页，见 `internal/ui/wails/frontend/index.html:1-53` | ❌ |

结论：`bridge-event` 这个后端桥本身是接上的，而且 envelope 兼容；但把它说成“前端事件流已经对齐”会过宽，整体只能判 **⚠️**。

## 5. 双源事件隔离

本报告把“双源事件隔离”定义为：

- 前端同时存在 `bridge-event` 与 `agent-event` 两条入口
- `agent-event` 带 thread/agent 维度
- backend 在发往当前窗口前做 thread/CWD 隔离

### V2

- `handleBridgeNotification()` 总是发 `bridge-event`；有 `threadId` 时再发 `agent-event`，见 `go-agent-v2/cmd/agent-terminal/app_bridge.go:94-115`
- 发往前端前会做 CWD 过滤，避免跨窗口串流，见 `go-agent-v2/cmd/agent-terminal/app_bridge.go:62-92,163-194`
- frontend 同时订阅两路事件，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:422-456`、`go-agent-v2/cmd/agent-terminal/frontend/vue-app/app.js:271-275`
- parser 会从 `threadId` / `thread_id` / `agent_id` 等字段里抽 thread identity，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/stores/bridge-event-parser.js:11-35`

### V3

- Wails bridge 当前只定义并发出 `bridge-event`，见 `internal/ui/wails/lifecycle.go:11-15`、`internal/ui/wails/bridge.go:81-89`
- 在当前 `internal/ui/wails/*.go` 里没有 `agent-event` 对应定义
- 当前 `internal/ui/wails/*.go` 里也没有和 V2 等价的 thread/CWD 过滤逻辑
- 当前内嵌 frontend 没有任何 runtime 订阅代码，见 `internal/ui/wails/frontend/index.html:1-53`

结论：这一项是 **❌**。V2 的双入口 + 窗口隔离在 V3 当前源码里没有被迁入。

## 最终判断

如果“1:1 对齐”的意思是：

- V2 事件发布面
- V2 对外 push method 面
- V2 Wails bridge / frontend 入口面

都要在 V3 保持可消费契约不变，那么当前结论是 **未对齐，整体判 ❌**。

更准确地说：

- **✅ 已对齐**
  - `turn/started`
  - `turn/completed`
  - Wails 后端 `bridge-event` 名称与 `{type, payload}` envelope
- **⚠️ 部分对齐**
  - `ui/state/changed` 只做到了名称复用，没有证明语义完全一致
  - V3 typed bus 本身比对外面更宽，但这不等于前端契约已迁移
  - `bridge-event` 后端桥已接线，但前端消费面未接上
- **❌ 未对齐**
  - V2 更宽的 public method 面
  - `thread/*` / `item/*` / `mcp` / `account` / `fuzzy` / `background` 等 method 家族
  - `agent-event` 辅助通道
  - thread/CWD 级双源隔离

如果要继续做 1:1 迁移，最小缺口是：

1. 明确 V3 对外 method map，不要只停留在 typed bus。
2. 决定 `ui/state/changed` 是保留 V2 refresh 语义，还是正式改成 agent state transition。
3. 决定 `agent-event` 与 thread/CWD 隔离是恢复、兼容，还是显式废弃。
4. 给 Wails frontend 加上真实 runtime 订阅与解析层，否则后端桥接没有终点。
