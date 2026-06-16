# P7w2 审查：UI State

## 1. V2 精确盘点

### 1.1 `go-agent-v2/internal/uistate/` 逐文件统计

目录共 50 个文件。按逐文件统计，生产代码集中在 `runtime_state.go`、`runtime_timeline.go`、`event_status.go`、`event_lifecycle.go`、`event_normalizer.go`、`runtime_event_handlers.go` 等文件，说明 V2 UI State 实际上是一个完整的“事件规范化 + 运行时快照 + timeline 投影 + UI 结果拼装”子系统，而不是单一缓存对象。锚点可见 `go-agent-v2/internal/uistate/runtime_state.go:1`、`go-agent-v2/internal/uistate/runtime_timeline.go:1`、`go-agent-v2/internal/uistate/event_status.go:1`、`go-agent-v2/internal/uistate/event_lifecycle.go:1`、`go-agent-v2/internal/uistate/event_normalizer.go:1`、`go-agent-v2/internal/uistate/runtime_event_handlers.go:1`。

| 文件 | 类型 | 行数 |
| --- | --- | ---: |
| `go-agent-v2/internal/uistate/assistant_done_backfill_guard_test.go:1` | test | 291 |
| `go-agent-v2/internal/uistate/assistant_done_shape_guard_test.go:1` | test | 212 |
| `go-agent-v2/internal/uistate/doc.go:1` | prod | 37 |
| `go-agent-v2/internal/uistate/error_path_guard_test.go:1` | test | 115 |
| `go-agent-v2/internal/uistate/event_dispatch.go:1` | prod | 188 |
| `go-agent-v2/internal/uistate/event_dispatch_guardrail_extra_test.go:1` | test | 146 |
| `go-agent-v2/internal/uistate/event_lifecycle.go:1` | prod | 428 |
| `go-agent-v2/internal/uistate/event_normalizer.go:1` | prod | 374 |
| `go-agent-v2/internal/uistate/event_normalizer_guard_test.go:1` | test | 51 |
| `go-agent-v2/internal/uistate/event_normalizer_lifecycle_guard_test.go:1` | test | 984 |
| `go-agent-v2/internal/uistate/event_residual_guard_test.go:1` | test | 290 |
| `go-agent-v2/internal/uistate/event_split_guard_test.go:1` | test | 232 |
| `go-agent-v2/internal/uistate/event_status.go:1` | prod | 475 |
| `go-agent-v2/internal/uistate/event_status_helpers_test.go:1` | test | 854 |
| `go-agent-v2/internal/uistate/internal_message_test.go:1` | test | 40 |
| `go-agent-v2/internal/uistate/preference_manager_cwd_scope_test.go:1` | test | 135 |
| `go-agent-v2/internal/uistate/reasoning_section_break_test.go:1` | test | 42 |
| `go-agent-v2/internal/uistate/replace_threads_source_guard_test.go:1` | test | 58 |
| `go-agent-v2/internal/uistate/round4_final_guard_test.go:1` | test | 776 |
| `go-agent-v2/internal/uistate/runtime_clone.go:1` | prod | 114 |
| `go-agent-v2/internal/uistate/runtime_clone_guardrail_extra_test.go:1` | test | 221 |
| `go-agent-v2/internal/uistate/runtime_concurrency_guard_test.go:1` | test | 94 |
| `go-agent-v2/internal/uistate/runtime_event_handlers.go:1` | prod | 338 |
| `go-agent-v2/internal/uistate/runtime_event_handlers_test.go:1` | test | 391 |
| `go-agent-v2/internal/uistate/runtime_export_freeze_guard_test.go:1` | test | 325 |
| `go-agent-v2/internal/uistate/runtime_memory_stats.go:1` | prod | 50 |
| `go-agent-v2/internal/uistate/runtime_metrics_guard_test.go:1` | test | 95 |
| `go-agent-v2/internal/uistate/runtime_overlay_guard_test.go:1` | test | 416 |
| `go-agent-v2/internal/uistate/runtime_override_guard_test.go:1` | test | 100 |
| `go-agent-v2/internal/uistate/runtime_shape_contract_test.go:1` | test | 351 |
| `go-agent-v2/internal/uistate/runtime_shard.go:1` | prod | 68 |
| `go-agent-v2/internal/uistate/runtime_side_effect_guard_test.go:1` | test | 94 |
| `go-agent-v2/internal/uistate/runtime_state.go:1` | prod | 552 |
| `go-agent-v2/internal/uistate/runtime_state_derive_guard_test.go:1` | test | 776 |
| `go-agent-v2/internal/uistate/runtime_state_guard_test.go:1` | test | 759 |
| `go-agent-v2/internal/uistate/runtime_state_history.go:1` | prod | 379 |
| `go-agent-v2/internal/uistate/runtime_thread_view.go:1` | prod | 106 |
| `go-agent-v2/internal/uistate/runtime_timeline.go:1` | prod | 546 |
| `go-agent-v2/internal/uistate/runtime_timeline_guardrail_extra_test.go:1` | test | 76 |
| `go-agent-v2/internal/uistate/runtime_timeline_merge_test.go:1` | test | 804 |
| `go-agent-v2/internal/uistate/runtime_timeline_plan.go:1` | prod | 330 |
| `go-agent-v2/internal/uistate/runtime_types.go:1` | prod | 116 |
| `go-agent-v2/internal/uistate/state_machine_guard_test.go:1` | test | 508 |
| `go-agent-v2/internal/uistate/status_text_guard_test.go:1` | test | 278 |
| `go-agent-v2/internal/uistate/timeline_tokens.go:1` | prod | 235 |
| `go-agent-v2/internal/uistate/timeline_tokens_guard_test.go:1` | test | 448 |
| `go-agent-v2/internal/uistate/uistate.go:1` | prod | 138 |
| `go-agent-v2/internal/uistate/user_message_metadata.go:1` | prod | 25 |
| `go-agent-v2/internal/uistate/user_message_metadata_test.go:1` | test | 298 |
| `go-agent-v2/internal/uistate/workspace_run_protocol_guard_test.go:1` | test | 104 |

补充观察：

- `RuntimeSnapshot` 已直接承载线程列表、状态、timeline、diff、token usage、workspace runs、agent meta、activity stats、alerts 等 UI 结构，见 `go-agent-v2/internal/uistate/runtime_types.go:68`。
- `ThreadView` 是按线程裁剪后的投影视图，直接打包 `Timeline`、`TokenUsage`、`AgentMeta`、`ActivityStats`、`Alerts`，见 `go-agent-v2/internal/uistate/runtime_thread_view.go:23`。
- `ApplyAgentEvent` -> `applyAgentEventLocked` 是运行时更新入口，负责把归一化事件写进快照并派生状态头/详情，见 `go-agent-v2/internal/uistate/runtime_state.go:428`、`go-agent-v2/internal/uistate/event_dispatch.go:147`。

### 1.2 `go-agent-v2/internal/apiserver/` 中 `uistate` / `uiRuntime` 相关方法

| 位置 | 作用 | 证据 |
| --- | --- | --- |
| `Server.New` | 初始化 `uiRuntime`，并把 thread manager 状态作为 `RuntimeProbe` 注入，供派生状态使用 | `go-agent-v2/internal/apiserver/server.go:163`、`go-agent-v2/internal/apiserver/server.go:198`、`go-agent-v2/internal/apiserver/server.go:213` |
| `Adapter.ThreadStart` | 新线程启动后把线程列表回写到 `uiRuntime` | `go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:40`、`go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:65` |
| `Adapter.ThreadList` | 线程列表拉取后用 `ReplaceThreadsWithSource("thread_list", ...)` 刷新 UI runtime | `go-agent-v2/internal/apiserver/codexadapter/adapter_thread_listing.go:102`、`go-agent-v2/internal/apiserver/codexadapter/adapter_thread_listing.go:142` |
| `AgentEventHandler` | provider event -> payload merge -> `NormalizeEventFromPayload` -> `ApplyAgentEvent` -> `notify` | `go-agent-v2/internal/apiserver/server_event_handler.go:19`、`go-agent-v2/internal/apiserver/server_event_handler.go:43`、`go-agent-v2/internal/apiserver/server_event_handler.go:45`、`go-agent-v2/internal/apiserver/server_event_handler.go:54` |
| `applyAgentEventToRuntime` | 控制哪些 provider event 真正进入 UI runtime；diff 更新还会持久化 diff snapshot | `go-agent-v2/internal/apiserver/server_event_handler.go:173` |
| `syncUIRuntimeFromNotifyPayload` | 对 notify-only 路径做二次回放，把 `turn/completed` / `error` / workspace run 等补进 UI runtime | `go-agent-v2/internal/apiserver/server_payload.go:35`、`go-agent-v2/internal/apiserver/server_payload.go:204`、`go-agent-v2/internal/apiserver/server_payload.go:272` |
| `uiStateGet` | 读取完整 UI state；会装配快照、偏好、workspace runs、agentRuntime、archives、bindings | `go-agent-v2/internal/apiserver/methods_ui_state.go:112`、`go-agent-v2/internal/apiserver/methods_ui_state_helpers.go:51`、`go-agent-v2/internal/apiserver/methods_ui_state_helpers.go:266` |
| `uiSidebarGet` | 读取轻量 sidebar payload；保留线程列表/状态/agentRuntime/active ids/prefs | `go-agent-v2/internal/apiserver/methods_ui_sidebar.go:15`、`go-agent-v2/internal/apiserver/methods_ui_sidebar.go:104` |
| `handleWailsModeApproval` | 审批事件直接进 `uiRuntime`，并立即发 `ui/thread/changed`，侧栏再延迟刷新 | `go-agent-v2/internal/apiserver/server_approval.go:351`、`go-agent-v2/internal/apiserver/server_approval.go:359`、`go-agent-v2/internal/apiserver/server_approval.go:364` |
| `appendInternalMessageToUI` | 内部路由消息直接写入目标线程 timeline，并触发 thread refresh | `go-agent-v2/internal/apiserver/internal_messages.go:134`、`go-agent-v2/internal/apiserver/internal_messages.go:146`、`go-agent-v2/internal/apiserver/internal_messages.go:148` |
| `debugRuntime` | 暴露 `TimelineStats` 便于观察 UI runtime 内部状态 | `go-agent-v2/internal/apiserver/methods_turn.go:200`、`go-agent-v2/internal/apiserver/methods_turn.go:224` |

### 1.3 V2 关键字搜索

| 关键字 | 结果 | 证据 |
| --- | --- | --- |
| `uistate` | 主要集中在 `internal/uistate` 包本体、`internal/apiserver` 接线，以及 SDK turn prepare optimistic timeline 适配 | `go-agent-v2/internal/uistate/uistate.go:1`、`go-agent-v2/internal/apiserver/server.go:61`、`go-agent-v2/internal/apiserver/server_event_handler.go:43`、`go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:110` |
| `ui_runtime` | 未检出命中；V2 代码内部使用的是 `uiRuntime` 字段，不是 `ui_runtime` 标识符 | `go-agent-v2/internal/apiserver/server.go:61` |
| `syncUI` | 只命中 `syncUIRuntimeFromNotifyPayload`，说明 notify 回放是单点逻辑 | `go-agent-v2/internal/apiserver/server_payload.go:35`、`go-agent-v2/internal/apiserver/server_payload.go:204` |
| `UIRuntime` | 命中 apiserver 字段和 SDK prepare adapter 接口，说明 timeline optimistic append 也依赖该运行时 | `go-agent-v2/internal/apiserver/server.go:61`、`go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:108` |

## 2. V2 能力清单

| 能力 | 结论 | 证据 |
| --- | --- | --- |
| thread list 同步 | 已实现。线程启动和线程列表刷新都会落到 `ReplaceThreadsWithSource`，随后由 `ui/state/get` / `ui/sidebar/get` 返回给前端 | `go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:65`、`go-agent-v2/internal/apiserver/codexadapter/adapter_thread_listing.go:142`、`go-agent-v2/internal/uistate/runtime_state.go:222`、`go-agent-v2/internal/apiserver/methods_ui_state_helpers.go:266`、`go-agent-v2/internal/apiserver/methods_ui_sidebar.go:104` |
| agent list 同步 | 已实现。`ui/state/get` / `ui/sidebar/get` 会从 `s.mgr.List()` 组装 `agentRuntimeById`，并与 binding store / archive 信息合并 | `go-agent-v2/internal/apiserver/methods_ui_state_helpers.go:185`、`go-agent-v2/internal/apiserver/methods_ui_state_helpers.go:240`、`go-agent-v2/internal/apiserver/methods_ui_sidebar.go:77`、`go-agent-v2/internal/dashboard/state_service.go:99` |
| turn status 同步 | 已实现。provider/raw notify 经归一化后进入 `ApplyAgentEvent`，再经过 lifecycle/status 派生出 `Statuses`、`StatusHeadersByThread`、`StatusDetailsByThread` | `go-agent-v2/internal/uistate/event_dispatch.go:42`、`go-agent-v2/internal/uistate/event_dispatch.go:147`、`go-agent-v2/internal/uistate/event_lifecycle.go:26`、`go-agent-v2/internal/uistate/event_status.go:253`、`go-agent-v2/internal/uistate/runtime_state.go:428` |
| sidebar/tab 状态 | 已实现。偏好读写走 `ui/preferences/get|set|getAll`，`ui/state/get` / `ui/sidebar/get` 会回传 `activeThreadId`、`activeCmdThreadId`、`mainAgentId`、`viewPrefs.chat`、`viewPrefs.cmd`、`threadPins.chat`、`threadArchives.chat` | `go-agent-v2/internal/apiserver/methods_ui_state.go:31`、`go-agent-v2/internal/dashboard/state_service.go:10`、`go-agent-v2/internal/dashboard/state_service.go:25`、`go-agent-v2/internal/apiserver/methods_ui_state_helpers.go:266`、`go-agent-v2/internal/apiserver/methods_ui_sidebar.go:104` |
| 消息面板实时更新 | 已实现。provider event 会同步/异步 `notify`；同时 `ui/thread/patch` 会根据 `ThreadView` 计算 timeline diff，把变更项、移除项和顺序补丁推给前端；提交 turn 时还会先做 optimistic user message append | `go-agent-v2/internal/apiserver/server_event_handler.go:50`、`go-agent-v2/internal/apiserver/server_payload.go:114`、`go-agent-v2/internal/apiserver/server_thread_patch.go:61`、`go-agent-v2/internal/apiserver/server_thread_patch.go:120`、`go-agent-v2/internal/uistate/runtime_event_handlers.go:123`、`go-agent-v2/internal/uistate/runtime_event_handlers.go:260`、`go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:108` |
| token usage 实时推送 | 已实现。`token_count` 被映射为 `thread/tokenUsage/updated`，而且在 `AgentEventHandler` 中被强制同步 `notify`；runtime 侧更新 `TokenUsageByThread`，thread patch 也会把 `tokenUsage` diff 一并推送 | `go-agent-v2/internal/apiserver/notifications.go:10`、`go-agent-v2/internal/apiserver/server_event_handler.go:51`、`go-agent-v2/internal/uistate/event_lifecycle.go:35`、`go-agent-v2/internal/uistate/timeline_tokens.go:70`、`go-agent-v2/internal/apiserver/server_thread_patch.go:95` |

## 3. V3 当前状态

### 3.1 V3 bus event 覆盖面

V3 的 typed bus 已覆盖“原始实时事件”层，但还没有覆盖 V2 那种“UI projection”层。

- agent 侧已有 `StateChanged` / `AgentLaunched` / `AgentStopped` / `AgentRecovering` / `AgentFailed`，见 `internal/dto/agent/event.go:6`。
- turn 侧已有 `TurnStarted` / `TurnCompleted` / `TurnInterrupted` / `TurnStalled` / `TurnResumed` / `TurnInputReceived` / `TurnOutputDelta`，见 `internal/dto/turn/event.go:6`。
- tool 侧已有 `ToolCallBegin` / `ToolCallEnd` / `ToolApprovalRequested` / `ToolApprovalResolved`，见 `internal/dto/tool/event.go:6`。
- workspace 侧已有 `WorkspaceRunCreated` / `WorkspaceRunStatusChanged` / `WorkspaceRunMerged` / `WorkspaceRunAborted` / `WorkspaceRunMergeError`，且 service 已发布状态变化事件，见 `internal/dto/workspace/event.go:6`、`internal/module/workspace/service_helpers.go:229`。
- provider translator 已把 raw event 映射到这些 typed event，例如 Codex translator 负责 turn/tool/approval 事件，见 `internal/provider/codexapp/event_map.go:77`、`internal/provider/codexapp/event_map.go:115`；Claude translator 负责 turn/tool 事件，见 `internal/provider/claudecli/event_map.go:55`、`internal/provider/claudecli/event_map.go:87`。

但 UI projection 只停留在 DTO 定义层：

- `dto/ui/event.go` 只定义了 `UIProjectionUpdated`、`UITimelineAppended`、`UITokensUpdated` 三个 UI 事件，见 `internal/dto/ui/event.go:5`。
- `LogSink` 虽然订阅了这三个 UI 事件，但源码中未见对应 publish 点；可见的订阅只在 `bindUI`，见 `internal/platform/bus/sink.go:83`。

判断：

- V3 bus 已覆盖 thread/turn/tool/workspace 的原始 UI 输入事件。
- V3 bus 还没有覆盖 V2 需要的“派生 UI state/projection 事件”。
- token usage 目前尤为缺口明显；现有 provider translator 只覆盖 agent/turn/tool/workspace 事件，未见 V2 风格 `token_count` / `thread/tokenUsage/updated` 映射，当前可见 translator 范围见 `internal/provider/codexapp/event_map.go:77`、`internal/provider/codexapp/event_map.go:115`、`internal/provider/claudecli/event_map.go:55`、`internal/provider/claudecli/event_map.go:87`。

### 3.2 push bridge / Wails bridge

现状是“桥存在，但只桥了 3 个事件”。

- RPC push bridge 具备泛型发送能力：`BindEventToNotify[T]` 可以把任意 typed event 绑定到任意 method，`Server.NotifyAll` 可以 fanout 到所有连接，见 `internal/platform/rpc/push.go:60`、`internal/platform/rpc/server.go:67`。
- 但默认订阅只绑定 `agentdto.StateChanged`、`turndto.TurnStarted`、`turndto.TurnCompleted`，对应 method 只有 `ui/state/changed`、`turn/started`、`turn/completed`，见 `internal/platform/rpc/push.go:16`、`internal/platform/rpc/push.go:75`。
- Wails bridge 的 `publish(method, payload)` 也能承载任意 payload，并统一发到 `bridge-event`，见 `internal/ui/wails/bridge.go:81`、`internal/ui/wails/lifecycle.go:12`、`internal/ui/wails/lifecycle.go:114`。
- 但 `EventBridge.Start()` 同样只订阅上面 3 个核心事件，见 `internal/ui/wails/bridge.go:42`、`internal/ui/wails/bridge.go:53`。

判断：

- push bridge / Wails bridge 作为“传输层桥”是可复用的。
- 但它们当前不能直接替代 V2 UI state 推送，因为没有订阅 `TurnOutputDelta` / `ToolCall*` / `ToolApproval*` / `WorkspaceRun*` / `UIProjection*`，更没有 `ui/thread/patch` / `ui/thread/changed` / `ui/sidebar/changed` 这类 V2 UI 粒度的事件。

### 3.3 `dto/ui/event.go` 已定义的 UI 事件

| UI 事件 | 字段 | 证据 |
| --- | --- | --- |
| `UIProjectionUpdated` | `UIProjectionHeader + Revision` | `internal/dto/ui/event.go:5` |
| `UITimelineAppended` | `UITurnHeader + ItemID + ItemKind + RequestID + CallID` | `internal/dto/ui/event.go:11` |
| `UITokensUpdated` | `UITurnHeader + InputTokens + OutputTokens + TotalTokens + ContextWindowTokens` | `internal/dto/ui/event.go:20` |

补充：

- 这些 UI 事件对应的 event type 常量已分配在 shared 层，见 `internal/dto/shared/event.go:36`。
- 但目前只有定义和 LogSink 订阅；未见 projection publisher。

### 3.4 按 V2 能力回看 V3

| V2 需求 | V3 状态 | 证据 |
| --- | --- | --- |
| thread list 同步 | 部分具备。已有 `thread/list` 拉取接口，但没有 UI projection store，也没有 `ui/state/get` / `ui/sidebar/get` | `internal/module/thread/rpc.go:42`、`internal/module/thread/rpc.go:48`、`internal/module/thread/rpc.go:52` |
| agent list 同步 | 部分具备。已有 `agent.list` / `agent.snapshot` / `agent.getState`，但没有统一 `agentRuntimeById` UI 结果层 | `internal/sidecar/orch/orchestration/rpc.go:43`、`internal/sidecar/orch/orchestration/rpc.go:46`、`internal/sidecar/orch/orchestration/rpc.go:49` |
| turn status 同步 | 原始事件层基本具备；但桥接层只推了 `turn/started` / `turn/completed`，缺少完整 projection 与 payload assembly | `internal/dto/turn/event.go:6`、`internal/platform/rpc/push.go:18`、`internal/platform/rpc/push.go:85` |
| sidebar/tab 状态 | 缺失。V3 现有 RPC handler 仅分布在 thread/turn/orchestration/skill/workspace 命名空间，`internal/**` 中 `\"ui/` 字面量只出现在 push/Wails bridge 常量 | `internal/module/thread/rpc.go:19`、`internal/module/turn/rpc.go:14`、`internal/sidecar/orch/orchestration/rpc.go:15`、`internal/module/skill/rpc.go:42`、`internal/module/workspace/rpc.go:13`、`internal/platform/rpc/push.go:17`、`internal/ui/wails/bridge.go:17` |
| 消息面板实时更新 | 部分具备。已有 `TurnOutputDelta` / `ToolCall*` / `ToolApproval*` typed event，但 bridge 未订阅这些事件，也没有 V2 等价 timeline patch/projection 发布 | `internal/dto/turn/event.go:44`、`internal/dto/tool/event.go:5`、`internal/provider/codexapp/event_map.go:92`、`internal/provider/codexapp/event_map.go:117`、`internal/ui/wails/bridge.go:53`、`internal/platform/rpc/push.go:81` |
| token usage 实时推送 | 缺失。虽然 `UITokensUpdated` DTO 已定义，但未见发布点；现有 provider translator 也未覆盖 token usage 事件 | `internal/dto/ui/event.go:20`、`internal/platform/bus/sink.go:86`、`internal/provider/codexapp/event_map.go:77`、`internal/provider/claudecli/event_map.go:55` |

## 4. 实现方案

### 4.1 UI State 在 V3 中应是独立模块，不应直接揉进 push bridge / Wails bridge

建议把 UI State 实现为“独立的 projection 模块”，而不是把状态逻辑塞进现有 bridge 或 thread/orchestration 模块。

原因：

- V2 的 UI State 本身就是状态机和投影层：`RuntimeManager`、`ApplyAgentEvent`、`ThreadView`、`ReplaceThreadsWithSource`、`TokenUsageByThread`、`StatusHeadersByThread`、`TimelinesByThread` 都在同一投影域内，见 `go-agent-v2/internal/uistate/runtime_state.go:37`、`go-agent-v2/internal/uistate/runtime_state.go:222`、`go-agent-v2/internal/uistate/runtime_state.go:428`、`go-agent-v2/internal/uistate/runtime_thread_view.go:23`、`go-agent-v2/internal/uistate/runtime_types.go:68`。
- V3 的 push bridge / Wails bridge 当前是“事件扇出层”，不持有复杂状态；它们只订阅 event bus 并发消息，见 `internal/platform/rpc/push.go:22`、`internal/platform/rpc/push.go:75`、`internal/ui/wails/bridge.go:22`、`internal/ui/wails/bridge.go:42`。
- `dto/ui/event.go` 已经暗示了“先做 UI projection，再把 projection 事件发出去”的方向，见 `internal/dto/ui/event.go:5`。

推荐落点：

- `internal/module/uistate` 或 `internal/ui/projection`
- 责任是：订阅 typed bus、维护 UI projection store、暴露 `ui/state/get` / `ui/sidebar/get` / `ui/preferences/*` 风格 RPC、发布 `UIProjectionUpdated` / `UITimelineAppended` / `UITokensUpdated`
- `platform/rpc/push.go` 和 `ui/wails/bridge.go` 只负责转运，不负责状态计算

### 4.2 可复用能力

可直接复用：

- provider -> typed event translator：`codexapp` / `claudecli` 已把大量 raw event 提前规范化，能省掉 V2 的整块 `event_normalizer.go` 风格工作量，见 `internal/provider/codexapp/event_map.go:77`、`internal/provider/codexapp/event_map.go:115`、`internal/provider/claudecli/event_map.go:55`、`internal/provider/claudecli/event_map.go:87`。
- typed bus 和 sink 机制：已有统一 `event.Publish(...)` 和 `LogSink.bindUI()` 订阅口，见 `internal/sidecar/orch/orchestration/events.go:13`、`internal/platform/bus/sink.go:83`。
- push / Wails bridge 外壳：已有 fanout 和 `bridge-event` 统一出口，见 `internal/platform/rpc/push.go:60`、`internal/platform/rpc/server.go:67`、`internal/ui/wails/bridge.go:81`。
- workspace 事件与 RPC：V3 已有 workspace run 事件和 `workspace/run/*` RPC，可直接并入 UI state/dashboard 聚合结果，见 `internal/dto/workspace/event.go:6`、`internal/module/workspace/service_helpers.go:229`、`internal/module/workspace/rpc.go:13`。
- thread / agent 拉取接口：V3 已有 `thread/list`、`thread/read`、`thread/messages`、`agent.list`、`agent.snapshot`，可作为初始快照补数来源，见 `internal/module/thread/rpc.go:42`、`internal/module/thread/rpc.go:48`、`internal/module/thread/rpc.go:52`、`internal/sidecar/orch/orchestration/rpc.go:43`、`internal/sidecar/orch/orchestration/rpc.go:46`。

需要新做：

- UI projection store 本身
- thread timeline patch / sidebar refresh 这类 V2 UI 粒度事件
- token usage publisher
- preferences / view prefs / thread pins / main-agent / active-thread 解析层
- `ui/state/get` / `ui/sidebar/get` / `ui/preferences/*` / `ui/dashboard/get` 风格 RPC

### 4.3 预估代码量

这是推断值，不是已有 TODO 预算。

我判断如果以“达到 V2 主要闭环”为目标，V3 生产代码大致需要 `1.2k ~ 1.8k` 行，测试大致需要 `1.5k ~ 2.5k` 行。

依据：

- V2 同类核心生产代码本体已经达到多文件规模：`runtime_state.go` 552 行、`runtime_timeline.go` 546 行、`event_status.go` 475 行、`event_lifecycle.go` 428 行、`event_normalizer.go` 374 行、`runtime_event_handlers.go` 338 行、`runtime_timeline_plan.go` 330 行、`timeline_tokens.go` 235 行，见 `go-agent-v2/internal/uistate/runtime_state.go:1`、`go-agent-v2/internal/uistate/runtime_timeline.go:1`、`go-agent-v2/internal/uistate/event_status.go:1`、`go-agent-v2/internal/uistate/event_lifecycle.go:1`、`go-agent-v2/internal/uistate/event_normalizer.go:1`、`go-agent-v2/internal/uistate/runtime_event_handlers.go:1`、`go-agent-v2/internal/uistate/runtime_timeline_plan.go:1`、`go-agent-v2/internal/uistate/timeline_tokens.go:1`。
- V3 已有 typed translator、bus、push bridge、Wails bridge、workspace event/RPC，可以明显缩小“接线成本”，见 `internal/provider/codexapp/event_map.go:77`、`internal/platform/rpc/push.go:60`、`internal/ui/wails/bridge.go:81`、`internal/module/workspace/rpc.go:13`。
- 但 V3 目前没有 UI projection publisher、也没有 ui/preferences/ui/state/ui/sidebar/dashboard 结果层，所以不可能是几十行补丁级别的工作，见 `internal/dto/ui/event.go:5`、`internal/platform/bus/sink.go:83`、`internal/module/thread/rpc.go:19`、`internal/sidecar/orch/orchestration/rpc.go:15`。

### 4.4 与 Dashboard 的关系

V2 的 Dashboard 不是“展示层皮肤”，而是 UI State 的决策和拼装层。

- `ResolveState` 负责 aliases、mainAgentId、activeThreadId、activeCmdThreadId 的解析，见 `go-agent-v2/internal/dashboard/state_service.go:57`。
- `BuildAgentRuntimeByID` 负责把 manager/runtime 结果转成 UI payload 结构，见 `go-agent-v2/internal/dashboard/state_service.go:99`。
- `BuildUIStateResult` 负责最终 payload 形状，见 `go-agent-v2/internal/dashboard/state_service.go:165`。
- `uiStateGet` / `uiSidebarGet` 都显式依赖这些 helper，见 `go-agent-v2/internal/apiserver/methods_ui_state_helpers.go:65`、`go-agent-v2/internal/apiserver/methods_ui_state_helpers.go:267`、`go-agent-v2/internal/apiserver/methods_ui_sidebar.go:30`。

因此 V3 最稳妥的做法是：

- 保留一个纯 helper 层，承担 `ResolveState` / payload assembly / pref key policy
- 让新的 UI projection 模块调用该 helper
- 不要把这些策略散落到 push bridge、Wails bridge 或 thread/orchestration handler 里

## 5. V2 RPC 对照

### 5.1 V2 `ui/` 前缀请求方法

V2 注册的 `ui/` 前缀请求方法如下，统一入口见 `go-agent-v2/internal/apiserver/methods.go:262`。

| V2 方法 | V2 状态 | V3 是否已有 | 对照证据 |
| --- | --- | --- | --- |
| `ui/preferences/get` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:264`; V3 handler 命名空间仅见 `thread` / `turn` / `orchestration` / `skill` / `workspace`，见 `internal/module/thread/rpc.go:19`、`internal/module/turn/rpc.go:14`、`internal/sidecar/orch/orchestration/rpc.go:15`、`internal/module/skill/rpc.go:42`、`internal/module/workspace/rpc.go:13` |
| `ui/preferences/set` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:264`; V3 同上 |
| `ui/preferences/getAll` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:264`; V3 同上 |
| `ui/projects/get` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:265`; V3 同上 |
| `ui/projects/add` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:265`; V3 同上 |
| `ui/projects/remove` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:265`; V3 同上 |
| `ui/projects/setActive` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:266`; V3 同上 |
| `ui/code/open` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:266`; V3 同上 |
| `ui/code/locate` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:266`; V3 同上 |
| `ui/code/save` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:267`; V3 同上 |
| `ui/dashboard/get` | 已注册 | 否，只有若干可拼装的子接口 | V2: `go-agent-v2/internal/apiserver/methods.go:267`; V3 可复用的子接口见 `internal/sidecar/orch/orchestration/rpc.go:43`、`internal/sidecar/orch/orchestration/rpc.go:61`、`internal/module/workspace/rpc.go:15`、`internal/module/skill/rpc.go:44`、`internal/module/skill/rpc.go:56` |
| `ui/state/get` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:268`; V3 `internal/**` 中 `\"ui/` 只命中 push/Wails bridge 常量，见 `internal/platform/rpc/push.go:17`、`internal/ui/wails/bridge.go:17` |
| `ui/sidebar/get` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:268`; V3 同上 |
| `ui/log` | 已注册 | 否 | V2: `go-agent-v2/internal/apiserver/methods.go:270`; V3 未见对应 handler，现有 handler 入口仍只在 thread/turn/orchestration/skill/workspace，见 `internal/module/thread/rpc.go:19`、`internal/module/turn/rpc.go:14`、`internal/sidecar/orch/orchestration/rpc.go:15`、`internal/module/skill/rpc.go:42`、`internal/module/workspace/rpc.go:13` |

### 5.2 V2 `ui/` 前缀推送/通知方法

| V2 方法 | V2 状态 | V3 是否已有 | 对照证据 |
| --- | --- | --- | --- |
| `ui/thread/patch` | 已发送，承载 thread-level 增量 patch（timeline、tokenUsage、diff、status 等） | 否 | V2: `go-agent-v2/internal/apiserver/server_thread_patch.go:15`、`go-agent-v2/internal/apiserver/server_thread_patch.go:61`; V3 未见对应 method，当前 push 只推 3 类核心事件，见 `internal/platform/rpc/push.go:16` |
| `ui/thread/changed` | 已发送，承载 thread refresh 节流通知 | 否 | V2: `go-agent-v2/internal/apiserver/server_payload.go:24`、`go-agent-v2/internal/apiserver/server_payload.go:114`、`go-agent-v2/internal/apiserver/server_payload.go:159`; V3 未见对应 method，见 `internal/platform/rpc/push.go:16` |
| `ui/sidebar/changed` | 已发送，承载 sidebar refresh 节流通知 | 否 | V2: `go-agent-v2/internal/apiserver/server_payload.go:23`、`go-agent-v2/internal/apiserver/server_payload.go:114`、`go-agent-v2/internal/apiserver/server_payload.go:155`; V3 未见对应 method，见 `internal/platform/rpc/push.go:16` |
| `ui/state/changed` | V2 搜索只看到兼容性过滤字符串，未见明确发送路径 | 是，但仅 V3 push/Wails bridge 常量级支持 | V2: `go-agent-v2/internal/apiserver/server_payload.go:145`; V3: `internal/platform/rpc/push.go:17`、`internal/platform/rpc/push.go:82`、`internal/ui/wails/bridge.go:17`、`internal/ui/wails/bridge.go:54` |

## 结论

V2 UI State 不是一个小型 adapter，而是一整套投影子系统：线程列表、agent runtime、turn 状态、timeline、diff、token usage、alerts、workspace runs、prefs、sidebar payload 都被收拢到了 `uistate + dashboard + apiserver glue` 里，核心证据见 `go-agent-v2/internal/uistate/runtime_types.go:68`、`go-agent-v2/internal/uistate/runtime_state.go:428`、`go-agent-v2/internal/apiserver/methods_ui_state_helpers.go:266`、`go-agent-v2/internal/dashboard/state_service.go:165`。

V3 现状是“底座够了，投影没做完”：typed bus、translator、workspace event、push bridge、Wails bridge 都已经有，但 UI projection 只停在 DTO 定义层，bridge 实际也只推 `ui/state/changed`、`turn/started`、`turn/completed` 三类事件，证据见 `internal/dto/ui/event.go:5`、`internal/platform/bus/sink.go:83`、`internal/platform/rpc/push.go:16`、`internal/ui/wails/bridge.go:16`。

因此，V3 的可行实现不是把逻辑塞进现有 bridge，而是新增一个独立的 UI projection 模块，复用现有 typed bus / translator / workspace / push/Wails 桥，再把 Dashboard 继续保留为纯策略与 payload assembly helper。按 V2 体量和 V3 现有复用面判断，这条路可行，但当前仓库还没到“可直接替换 V2 UI State”的状态，核心缺口是 `ui/state|get` / `ui/sidebar|get` / `ui/preferences/*`、timeline patch、token push、sidebar/tab 偏好与 projection publisher。
