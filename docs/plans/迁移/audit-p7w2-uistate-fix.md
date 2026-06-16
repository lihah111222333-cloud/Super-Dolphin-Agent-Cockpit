# P7w2 审查：UI State 模块实现

## 结论

`internal/module/uistate` 已经在 V3 落地为一个可用的轻量 projection 模块：有 `Service`、有 RPC、已接入 fx 和 app，并且能消费 agent/turn/workspace/token 相关 bus 事件，证据见 `internal/module/uistate/contract.go:5-10`、`internal/module/uistate/rpc.go:21-38`、`internal/module/uistate/module.go:10-33`、`internal/app/modules.go:11-39`。

但它和 V2 的 `go-agent-v2/internal/uistate/` 仍不是同一个量级。V2 在既有审计里被确认是 50 文件、包含 runtime state、timeline、status、normalizer、event handler 的完整子系统，见 `docs/plans/迁移/audit-p7w2-uistate.md:5-7`、`docs/plans/迁移/audit-p7w2-uistate.md:97-102`；当前 V3 只有 6 个文件，主要提供快照聚合和少量事件投影，不包含 V2 等价的 timeline patch / UI projection publish / 完整 turn status 派生闭环。

## 主要发现

1. `TurnCompleted` 的终态信息被丢弃了。DTO 自带 `Success`/`Error`/`Status`/`Reason`，`TurnSummary` 也有 `Success`/`Error`/`CompletedAt` 字段，但 `applyTurnCompleted` 只在 turn ID 匹配时清空 `ActiveTurn`，没有把这些字段写入快照，见 `internal/dto/turn/event.go:11-17`、`internal/module/uistate/state.go:34-43`、`internal/module/uistate/projector.go:59-69`。
2. 当前 `uistate` 只订阅 agent/turn/workspace/UI token 事件，没有订阅 thread、tool、`TurnOutputDelta` 这类 V2 依赖的消息面板事件；`projector.go` 的 import 和订阅列表只覆盖 `dto/agent`、`dto/turn`、`dto/ui`、`dto/workspace`，见 `internal/module/uistate/projector.go:9-27`。而 thread 事件在系统内是存在的，见 `internal/dto/thread/event.go:5-35`。
3. token usage 只做了“消费侧半截”。`uistate` 会消费 `UITokensUpdated` 并写到单个 `TokenUsage` 字段，见 `internal/module/uistate/projector.go:142-151`、`internal/module/uistate/state.go:9-14`、`internal/module/uistate/state.go:45-58`；但本次 LSP 搜索只找到 DTO 定义、log sink 订阅和这里的消费点，未找到 publish 侧，见 `internal/dto/ui/event.go:20-31`、`internal/platform/bus/sink.go:91-94`。
4. preferences / sidebar 只覆盖缩减版能力。当前 RPC 只有 `ui/preferences/get|set`，没有 `getAll`，且 `Preferences` 结构只有 `{CWD, Values}`，未见 V2 中的 `activeThreadId`、`mainAgentId`、`viewPrefs`、`threadPins`、`threadArchives` 等字段，见 `internal/module/uistate/rpc.go:29-37`、`internal/module/uistate/state.go:79-82`，以及本次对 `activeThread` / `mainAgent` / `threadPins` / `viewPrefs` 的 LSP 搜索均无命中。
5. 初始快照是“构造时预热”，不是“首次 `GetState` 时懒加载”。`NewService` 在构造期就调用 `buildInitialState` 读取 thread/agent 服务，而 `GetState` 本身只做加读锁和 clone，见 `internal/module/uistate/service.go:31-52`、`internal/module/uistate/service.go:54-79`、`internal/module/uistate/service.go:111-115`。

## 12 项核对

### 1. 文件清单与行数

目录内当前只有 6 个 Go 文件，全部不超过 400 行。

| 文件 | 行数 | 结论 | 证据 |
| --- | ---: | --- | --- |
| `internal/module/uistate/contract.go` | 11 | 通过 | `internal/module/uistate/contract.go:11` |
| `internal/module/uistate/module.go` | 34 | 通过 | `internal/module/uistate/module.go:34` |
| `internal/module/uistate/projector.go` | 184 | 通过 | `internal/module/uistate/projector.go:184` |
| `internal/module/uistate/rpc.go` | 40 | 通过 | `internal/module/uistate/rpc.go:40` |
| `internal/module/uistate/service.go` | 250 | 通过 | `internal/module/uistate/service.go:250` |
| `internal/module/uistate/state.go` | 264 | 通过 | `internal/module/uistate/state.go:264` |

小结：当前实现满足“逐文件 ≤400”的拆分要求。

### 2. Service 接口

`Service` 接口已定义且 4 个方法齐全：`GetState`、`GetSidebar`、`GetPreferences`、`SetPreference`，见 `internal/module/uistate/contract.go:5-10`。实现侧也有编译期断言 `var _ Service = (*service)(nil)`，并在 `service.go` 中给出了四个方法实现，见 `internal/module/uistate/service.go:29`、`internal/module/uistate/service.go:111-150`。

结论：通过。

### 3. Projector

`registerProjectionSubscriptions` 已明确订阅：

- `agentdto.StateChanged`，见 `internal/module/uistate/projector.go:18`
- `turndto.TurnStarted`，见 `internal/module/uistate/projector.go:19`
- `turndto.TurnCompleted`，见 `internal/module/uistate/projector.go:20`
- `WorkspaceRunCreated/StatusChanged/Merged/Aborted/MergeError`，见 `internal/module/uistate/projector.go:21-25`
- `uidto.UITokensUpdated`，见 `internal/module/uistate/projector.go:26`

结论：题目要求的 `AgentStateChanged` / `TurnStarted` / `TurnCompleted` / `Workspace*` 都已订阅。

补充：订阅面只覆盖 agent/turn/workspace/UI token；`projector.go` import 不含 thread/tool，说明 V2 依赖的 thread/timeline/tool 粒度事件还没进来，见 `internal/module/uistate/projector.go:9-13`。

### 4. 内存快照

`service` 用 `sync.RWMutex` 保护内存快照，见 `internal/module/uistate/service.go:16-23`。

- 读路径使用 `RLock`：`GetState`、`GetSidebar`、fallback 分支的 `GetPreferences`，见 `internal/module/uistate/service.go:111-135`
- 写路径使用 `Lock`：所有 projector `apply*` 方法和 fallback 分支的 `SetPreference`，见 `internal/module/uistate/projector.go:30-151`、`internal/module/uistate/service.go:137-149`
- 对外返回时做 defensive copy：`cloneState`、`cloneSidebar`、`clonePreferences`、`cloneTurn`、`cloneWorkspaceRuns` 等，见 `internal/module/uistate/state.go:84-162`

结论：RWMutex 保护存在，且读写分离明确；快照导出也做了拷贝，避免把内部可变切片/指针直接暴露出去。

### 5. RPC handler

`NewUIStateHandlers` 已注册以下方法：

- `ui/state/get`，见 `internal/module/uistate/rpc.go:23-25`
- `ui/sidebar/get`，见 `internal/module/uistate/rpc.go:26-28`
- `ui/preferences/get`，见 `internal/module/uistate/rpc.go:29-31`
- `ui/preferences/set`，见 `internal/module/uistate/rpc.go:32-37`

而平台层会收集 `group:"rpc_handlers"` 并统一 `server.Register(...)`，见 `internal/platform/rpc/module.go:36-52`。

结论：`ui/state/get`、`ui/sidebar/get`、`ui/preferences/get|set` 已注册到 RPC 框架。

补充：`ui/preferences/*` 目前只有 `get` 和 `set`，本次 LSP 搜索未发现 `ui/preferences/getAll`。

### 6. fx 注册

`uistate.Module` 通过：

- `fx.Provide(NewService)` 提供 service，见 `internal/module/uistate/module.go:10-12`
- `fx.Provide(NewUIStateHandlers)` 提供 RPC handler map，见 `internal/module/uistate/module.go:12`
- `fx.Invoke(registerProjections)` 在生命周期启动时绑定 bus 投影，见 `internal/module/uistate/module.go:13-33`

同时 `NewUIStateHandlers` 的返回类型是 `rpc.HandlerMapResult`，见 `internal/module/uistate/rpc.go:21`。

结论：通过，结构完整。

### 7. app 接入

`internal/app/modules.go` 已导入 `internal/module/uistate`，并在 app 总模块里包含 `uistate.Module`，见 `internal/app/modules.go:11`、`internal/app/modules.go:39`。

结论：通过。

### 8. 初始快照

`NewService` 构造时就调用 `buildInitialState(context.Background(), threads, agents)`，见 `internal/module/uistate/service.go:31-52`。`buildInitialState` 会：

- 调 `threads.List(ctx)` 构建 `state.Threads`，见 `internal/module/uistate/service.go:56-62`
- 调 `agents.ListAgents(ctx)` 构建 `state.Agents`，见 `internal/module/uistate/service.go:63-69`
- 再按 agent 结果回填 thread 的 `AgentID`，见 `internal/module/uistate/service.go:70-77`

但 `GetState` 本身只返回 `cloneState(s.state)`，没有首次调用时再拉取一次，见 `internal/module/uistate/service.go:111-115`。

结论：初始快照确实来自 `thread.Service + orchestration.Service`，但它是“服务构造期预热”，不是“首次 `GetState` 时懒加载”。

### 9. 事件 → 快照映射

| 事件 | 快照更新 | 证据 |
| --- | --- | --- |
| `agentdto.StateChanged` | 更新 `state.Threads` 的 `{ID, AgentID}`，更新 `state.Agents` 的 `{ID, ThreadID, State}`，随后排序 | `internal/module/uistate/projector.go:30-44`、`internal/module/uistate/state.go:164-246` |
| `turndto.TurnStarted` | 覆盖 `state.ActiveTurn = {ID, AgentID, ThreadID, Status:"running", StartedAt}` | `internal/module/uistate/projector.go:46-57` |
| `turndto.TurnCompleted` | 仅在 turn ID 匹配时把 `state.ActiveTurn = nil` | `internal/module/uistate/projector.go:59-69` |
| `workspacedto.WorkspaceRunCreated` | `workspaceByKey[RunKey]` 合并 `RunKey/DagKey/Status(created)/SourceRoot/WorkspacePath/CreatedBy/UpdatedAt` | `internal/module/uistate/projector.go:71-83` |
| `workspacedto.WorkspaceRunStatusChanged` | 合并 `DagKey/Status(NewStatus)/UpdatedBy/UpdatedAt` | `internal/module/uistate/projector.go:85-95` |
| `workspacedto.WorkspaceRunMerged` | 合并 `Status(merged)/SourceRoot/WorkspacePath/UpdatedBy/MergedFileCount/UpdatedAt` | `internal/module/uistate/projector.go:97-110` |
| `workspacedto.WorkspaceRunAborted` | 合并 `Status(aborted)/UpdatedBy/Message(reason)/UpdatedAt` | `internal/module/uistate/projector.go:112-123` |
| `workspacedto.WorkspaceRunMergeError` | 合并 `Status(merge_error)/SourceRoot/WorkspacePath/UpdatedBy/Conflicts/Errors/Message/UpdatedAt` | `internal/module/uistate/projector.go:125-140` |
| `uidto.UITokensUpdated` | 覆盖 `state.TokenUsage` 的 4 个 token 字段 | `internal/module/uistate/projector.go:142-151` |

补充两点：

- workspace 事件写入的是 `workspaceByKey` 独立 map，再由 `GetSidebar` 组装成 `Sidebar.Workspace.Runs`；`UIState` 本体没有 workspace 字段，见 `internal/module/uistate/service.go:21`、`internal/module/uistate/service.go:152-169`、`internal/module/uistate/state.go:9-14`、`internal/module/uistate/state.go:52-62`。
- `TurnSummary` 明明有 `Success`/`Error`/`CompletedAt` 字段，但 `TurnCompleted` 处理器没有填这些字段，见 `internal/module/uistate/state.go:34-43`、`internal/module/uistate/projector.go:59-69`。

### 10. V2 对照

V2 基线：`go-agent-v2/internal/uistate/` 在既有审计里被确认有 50 个文件，并覆盖 thread list、agent list、turn status、sidebar/tab 状态、消息面板实时更新、token usage 实时推送这 6 组核心能力，见 `docs/plans/迁移/audit-p7w2-uistate.md:5-7`、`docs/plans/迁移/audit-p7w2-uistate.md:97-102`。

当前 V3 对照如下：

| V2 能力 | V3 当前状态 | 证据 |
| --- | --- | --- |
| thread list 同步 | 部分覆盖。启动时能从 `threads.List` 拉取初始列表，`AgentStateChanged` 也会回填 thread-agent 关系；但 `uistate` 不订阅 thread 启停/消息分页事件 | `internal/module/uistate/service.go:56-62`、`internal/module/uistate/projector.go:30-44`、`internal/module/uistate/projector.go:9-27`、`internal/dto/thread/event.go:5-35` |
| agent list 同步 | 部分覆盖。启动时能从 `agents.ListAgents` 拉取初始列表，并响应 `StateChanged`；但没有消费 `AgentLaunched` / `AgentStopped` / `AgentRecovering` / `AgentFailed` | `internal/module/uistate/service.go:63-69`、`internal/module/uistate/projector.go:30-44`、`internal/dto/agent/event.go:13-38` |
| turn status 同步 | 部分覆盖。只保留单个 `ActiveTurn` 的开始/结束，不保留完整终态和状态细节 | `internal/module/uistate/projector.go:46-69`、`internal/dto/turn/event.go:11-17`、`internal/module/uistate/state.go:34-43` |
| sidebar/tab 状态 | 部分覆盖。已有 `ui/sidebar/get` 和 `ui/preferences/get|set`，但缺少 V2 的 `activeThreadId`、`mainAgentId`、`viewPrefs`、`threadPins`、`threadArchives` 这类结果层 | `internal/module/uistate/rpc.go:26-37`、`internal/module/uistate/state.go:79-82`、`docs/plans/迁移/audit-p7w2-uistate.md:100` |
| 消息面板实时更新 | 缺失。未见 `TurnOutputDelta` / `ToolCall*` / `ThreadMessagesPage` 投影，也未见 `ui/thread/patch` / `ui/thread/changed` / `ui/sidebar/changed` | `internal/module/uistate/projector.go:9-27`、`internal/dto/turn/event.go:46-59`、`internal/dto/thread/event.go:25-35`，以及本次对 `ui/thread/patch` / `ui/thread/changed` / `ui/sidebar/changed` 的 LSP 搜索均无命中 |
| token usage 实时推送 | 缺失闭环。模块内有 `UITokensUpdated` 消费者，但未找到 publish 侧；并且快照只保留单个全局 `TokenUsage`，不是 V2 的 thread 维度 | `internal/module/uistate/projector.go:142-151`、`internal/module/uistate/state.go:9-14`、`internal/module/uistate/state.go:45-58`、`internal/dto/ui/event.go:20-31`、`internal/platform/bus/sink.go:91-94`、`docs/plans/迁移/audit-p7w2-uistate.md:102` |

结论：按 V2 的 6 组核心能力计，当前 V3 是 `0` 组完全等价、`4` 组部分覆盖、`2` 组缺失闭环；整体仍更接近“轻量快照模块”，不是 V2 那种完整 UI runtime。

### 11. import 方向

当前 `uistate` 的 import 方向是干净的：

- `projector.go` 只依赖 `internal/dto/*` 和 `internal/platform/bus`，见 `internal/module/uistate/projector.go:9-13`
- `rpc.go` 只依赖 `internal/platform/rpc`，见 `internal/module/uistate/rpc.go:8`
- `service.go` 依赖 `internal/sidecar/orch/orchestration`、`internal/module/thread`、`internal/store/uipreference`，见 `internal/module/uistate/service.go:11-13`

本次 LSP 搜索 `internal/module/uistate/**` 下没有任何 `internal/provider/` 命中。

结论：通过；可 import `dto/platform/store/module` 方向，未反向 import `provider/`。

### 12. 函数复杂度

按文档符号范围统计，当前目录内最长函数如下：

| 排名 | 函数 | 行跨度 | 证据 |
| --- | --- | ---: | --- |
| 1 | `buildInitialState` | 26 行 | `internal/module/uistate/service.go:54-79` |
| 1 | `mergeAgentSummary` | 26 行 | `internal/module/uistate/state.go:199-224` |
| 3 | `NewService` | 22 行 | `internal/module/uistate/service.go:31-52` |
| 3 | `fallbackPreferencesLocked` | 22 行 | `internal/module/uistate/service.go:171-192` |

结论：函数整体都比较短，没有出现 V2 那种数百行的状态机函数；复杂度问题不在单函数过长，而在“能力覆盖面仍明显低于 V2”。

## 总评

这版 `uistate` 的模块化边界是清楚的：接口、RPC、fx、app 接入、锁模型都成立，且文件拆分符合要求。问题不在结构失控，而在能力仍偏瘦：

- 已经具备“拉初始快照 + 接少量投影事件 + 提供只读 RPC”的基础闭环。
- 还没有达到 V2 的完整 UI runtime/projection 能力，尤其是 turn 终态细节、timeline/message panel、thread 事件、token usage 闭环、以及 richer preferences/sidebar 结果层。

如果以“P7w2 审查通过但需补功能”来判断，我的结论是：`模块骨架通过，V2 能力对齐未完成`。
