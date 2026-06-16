# V2↔V3 1:1 对齐：Agent 状态机

## 结论

- 总结论：`❌` 当前 V3 还没有做到和 V2 的 Agent 状态机 1:1 对齐。
- 直接原因：
  - V3 虽然声明了 `10` 个状态、`11` 个触发器，但 `awaiting_user_input`、`user_input_requested`、`user_input_resolved` 目前没有运行时入口。
  - V3 不是纯严格状态机。`prepareLaunchStateLocked()` 和 `forceIdleAfterCompletionError()` 都会绕开 `stateless` 直接写 `agent.state`。
  - V2 的 recover / crash 处理是“状态 + 自动恢复 + replay”一体化；V3 目前主要是“显式 failed/stopped + manual/stall recover”，语义更窄。

## 证据范围

- V2：
  - `go-agent-v2/internal/runner/manager.go`
  - `go-agent-v2/internal/runner/manager_event.go`
  - `go-agent-v2/internal/runner/manager_submission.go`
  - `go-agent-v2/internal/runner/manager_recover.go`
  - `go-agent-v2/internal/runner/manager_lifecycle.go`
  - `go-agent-v2/internal/guards/state_matrix_snapshot.json`
  - `go-agent-v2/internal/apiserver/server_event_handler.go`
  - `go-agent-v2/internal/apiserver/server.go`
- V3：
  - `internal/dto/agent/state.go`
  - `internal/sidecar/orch/orchestration/helpers.go`
  - `internal/sidecar/orch/orchestration/service.go`
  - `internal/sidecar/orch/orchestration/recover.go`
  - `internal/sidecar/orch/orchestration/turn_lifecycle.go`
  - `internal/sidecar/orch/orchestration/module.go`
  - `internal/platform/rpc/approval.go`
  - `internal/platform/rpc/approval_events.go`
  - `internal/platform/rpc/approval_support.go`
  - `internal/provider/codexapp/session_approval.go`
  - `internal/provider/codexapp/event_map.go`

## 基线

- V2 对外可见状态只有 `5` 个：`idle`、`thinking`、`running`、`stopped`、`error`。见 `go-agent-v2/internal/runner/manager.go:17-25`，以及快照 `go-agent-v2/internal/guards/state_matrix_snapshot.json:2-8`。
- V2 额外依赖若干“非状态机位”：`recoveryPending`、`pendingSubmissions`、`activeSubmission`、`queueDispatching`。这些位承载了 V3 里 `turn_queued`、`recovering`、`stopping` 一类语义，见 `go-agent-v2/internal/runner/manager.go:46-60`、`go-agent-v2/internal/runner/manager_recover.go:57-85`、`go-agent-v2/internal/runner/manager_submission.go:75-82,286-297`。
- V3 明确声明 `10` 状态、`11` 触发器，并由 `buildStatesFromDefinitions()` 装配到 `stateless.NewStateMachineWithExternalStorage(...)`。见 `internal/dto/agent/state.go:8-112`、`internal/sidecar/orch/orchestration/helpers.go:16-31`、`internal/sidecar/orch/orchestration/service.go:96-99`、`internal/platform/statemachine/factory.go:28-67`。

## 10 状态对照

| V3 状态 | V2 对应 | 判定 | 说明 |
| --- | --- | --- | --- |
| `provisioning` | 无显式态 | `⚠️` | V2 `Launch()` 创建进程记录时直接放进 `StateIdle`，没有独立 provisioning 窗口，见 `go-agent-v2/internal/runner/manager_launch.go:175-189`。V3 把启动期显式化，见 `internal/dto/agent/state.go:9,52`。 |
| `idle` | `idle` | `✅` | 两边都把“可接收新 turn”建模为稳定空闲态，见 `go-agent-v2/internal/runner/manager.go:20`、`internal/dto/agent/state.go:10,53`。 |
| `turn_queued` | `pendingSubmissions` overlay | `⚠️` | V2 有真实排队语义，但不是状态；排队依赖 `pendingSubmissions` 和 `shouldQueueSubmission()`，见 `go-agent-v2/internal/runner/manager_submission.go:75-82,286-297`。V3 把它提升成显式状态，见 `internal/dto/agent/state.go:11,54`。 |
| `turn_starting` | `thinking` 的前半段 | `⚠️` | V2 把“已提交、等待 provider 接受”和“已开始输出前”都折叠进 `thinking`，见 `go-agent-v2/internal/runner/manager_event.go:98-105`、`go-agent-v2/internal/guards/state_matrix_snapshot.json:37-60`。V3 拆成 `turn_starting`。 |
| `turn_running` | `running` | `✅` | 两边都把工具调用、命令执行、审批等待前的活跃执行期落到 running 语义，见 `go-agent-v2/internal/runner/manager_event.go:106-113`、`go-agent-v2/internal/guards/state_matrix_snapshot.json:438-460,572-594`，以及 `internal/dto/agent/state.go:13,56`。 |
| `awaiting_user_input` | 无显式态；V2 通过 approval 通道承载 | `❌` | V2 真的有 request/response 闭环：`request_user_input` 会桥接到统一 approval 流程，`approval/respond` 再解锁等待，见 `go-agent-v2/internal/apiserver/server_event_handler.go:221-233,486-534`、`go-agent-v2/internal/apiserver/server.go:233-238`。但 V3 这个状态虽然声明了，运行时却没有任何 fire 点。 |
| `recovering` | `recoveryPending` overlay | `⚠️` | V2 恢复是真的，但靠 `recoveryPending` 和 `RecoverAgent` 流程，不是显式状态，见 `go-agent-v2/internal/runner/manager_recover.go:57-85,215-332`。V3 把它显式化，见 `internal/dto/agent/state.go:15,58`。 |
| `stopping` | 无显式态 | `⚠️` | V2 `Stop()` 是同步过程，结束后直接写 `StateStopped`，见 `go-agent-v2/internal/runner/manager_lifecycle.go:19-91`。V3 新增了中间态 `stopping`。 |
| `stopped` | `stopped` | `✅` | 两边都存在稳定停止态，V2 见 `go-agent-v2/internal/runner/manager.go:23`、`go-agent-v2/internal/runner/manager_lifecycle.go:74`；V3 见 `internal/dto/agent/state.go:17,60`。 |
| `failed` | `error` | `⚠️` | V2 `error` 基本对应 V3 `failed`，但 V2 启动失败时会直接从 manager map 移除，而不是保留一个可恢复的 failed runtime，见 `go-agent-v2/internal/runner/manager_launch.go:253-266`；V3 会保留为 `failed`，见 `internal/dto/agent/state.go:18,61`。 |

## 11 触发器对照

| V3 触发器 | V2 对应 | 判定 | 说明 |
| --- | --- | --- | --- |
| `launch_succeeded` | `Launch()` / `RecoverAgent()` 成功后落到 idle | `⚠️` | V2 有成功语义，但没有显式 trigger；V3 用显式 trigger 从 `provisioning/recovering` 进 `idle`，见 `internal/dto/agent/state.go:22,65,79,105`、`internal/sidecar/orch/orchestration/service.go:255-263`。 |
| `launch_failed` | 启动失败 / 恢复失败进 `error` | `⚠️` | V2 启动失败会 `StateError` 后移除 agent，恢复失败则停在 `error`，见 `go-agent-v2/internal/runner/manager_launch.go:253-266`、`go-agent-v2/internal/runner/manager_recover.go:251-303`。V3 则把 provisioning/recovering 失败统一落到 `failed`，见 `internal/dto/agent/state.go:23,66,80,106`、`internal/sidecar/orch/orchestration/service.go:240-243,387-389`。 |
| `turn_enqueued` | `pendingSubmissions` 入队 | `⚠️` | V2 有真实入队，但不是 trigger，不会切到显式 queue state，见 `go-agent-v2/internal/runner/manager_submission.go:286-297`。V3 则由 `SubmitTurn()` / `reconcileReadyStateLocked()` fire，见 `internal/sidecar/orch/orchestration/service.go:180-185`、`internal/sidecar/orch/orchestration/helpers.go:123-127`。 |
| `turn_accepted` | V2 提交后进入 `thinking` / provider 回 `turn_started` | `⚠️` | V2 没有一个单独 trigger 对应“accept”；V3 把一个 trigger 复用成两跳：`turn_queued -> turn_starting` 与 `turn_starting -> turn_running`，见 `internal/dto/agent/state.go:25,68,85,90`、`internal/sidecar/orch/orchestration/service.go:305-309`、`internal/sidecar/orch/orchestration/helpers.go:171-173`。 |
| `turn_completed` | `turn_complete` / `idle` 归位 | `⚠️` | V2 `turn_complete` 能从任意 5 态回 `idle`，见 `go-agent-v2/internal/guards/state_matrix_snapshot.json:63-88,117-140`。V3 只声明了 `turn_starting/turn_running -> idle`，并在非法时用 fallback 直接改 `idle`，见 `internal/dto/agent/state.go:26,69,89,94`、`internal/sidecar/orch/orchestration/turn_lifecycle.go:20-26,43-59`。 |
| `turn_aborted` | `turn_aborted` 回 `idle` | `⚠️` | V2 `turn_aborted` 也能从任意 5 态回 `idle`，见 `go-agent-v2/internal/guards/state_matrix_snapshot.json:90-114`。V3 只声明了 `turn_running/awaiting_user_input -> idle`，`turn_starting` abort 需要靠 `forceIdleAfterCompletionError()` 收口，见 `internal/dto/agent/state.go:27,70,95,101`、`internal/sidecar/orch/orchestration/service.go:341-348`、`internal/sidecar/orch/orchestration/turn_lifecycle.go:43-59`。 |
| `user_input_requested` | `request_user_input` 桥接到 approval | `❌` | V2 有真实入口，见 `go-agent-v2/internal/apiserver/server_event_handler.go:221-233,486-534`。V3 只在 DTO 里定义了 trigger，生产代码没有 fire 点。approval 层只会发布 `ToolApprovalRequested`，见 `internal/dto/agent/state.go:28,71,96`、`internal/platform/rpc/approval_events.go:23-32`。 |
| `user_input_resolved` | `approval/respond` 解锁等待 | `❌` | V2 resolution 链路真实存在，见 `go-agent-v2/internal/apiserver/server.go:233-238`。V3 trigger 只定义不触发；approval 层发布的是 `ToolApprovalResolved`，但 orchestration 没有消费它，见 `internal/dto/agent/state.go:29,72,100`、`internal/platform/rpc/approval_events.go:34-43`。 |
| `recover_requested` | `RecoverAgent(...)` | `⚠️` | V2 会在 `connection_dead`、`system_error`、`early_silent_turn`、submission dead-client 等多条路径自动 recover，见 `go-agent-v2/internal/runner/manager_event.go:307-342`、`go-agent-v2/internal/runner/manager_auto_recover.go:191-305`、`go-agent-v2/internal/runner/manager_submission.go:156-170,211-233`。V3 有显式 trigger，但运行时入口主要是 manual/thread recover 和 stall detector，见 `internal/sidecar/orch/orchestration/recover.go:27-58`、`internal/sidecar/orch/orchestration/runner_actor.go:68-77`、`internal/module/thread/lifecycle.go:137-169`。 |
| `stop_requested` | `Stop()` / `StopAll()` | `⚠️` | V2 有 stop 行为，但没有显式 stopping trigger，见 `go-agent-v2/internal/runner/manager_lifecycle.go:19-117`。V3 用显式 `stop_requested -> stopping`，见 `internal/dto/agent/state.go:31,74,83,87,92,99,104,111`、`internal/sidecar/orch/orchestration/service.go:155-164`。 |
| `process_exited` | `connection_dead` / `shutdown_complete` / stale-active reconcile | `⚠️` | V2 把异常退出拆成 `connection_dead -> error + auto recover`，正常停机是 `shutdown_complete -> stopped`，无事件时还会做 stale-active reconcile，见 `go-agent-v2/internal/guards/state_matrix_snapshot.json:170-193,1687-1710`、`go-agent-v2/internal/runner/manager_event.go:323-342`、`go-agent-v2/internal/runner/manager.go:227-276`。V3 用单一 process-exit 观测点统一转 `failed/stopped`，见 `internal/dto/agent/state.go:32,75,84,88,93,98,103,107`、`internal/sidecar/orch/orchestration/service.go:355-394`。 |

## 专项结论

### 1. strict mode（无 force fallback）

- 结论：`❌`
- 事实：
  - `fireOrForceLocked()` 本身现在是严格的，内部只调用 `agent.sm.FireCtx(...)`，失败就返回错误，不再在这个函数里静默改状态，见 `internal/sidecar/orch/orchestration/service.go:266-289`。
  - 但运行时仍有两条绕过状态机的直接写状态路径：
    - `prepareLaunchStateLocked()` 直接把任意旧态写成 `provisioning`，见 `internal/sidecar/orch/orchestration/helpers.go:52-60`。
    - `forceIdleAfterCompletionError()` 在 completion 处理失败时直接把状态写成 `idle`，并发布一个未在状态机声明里的 `turn_completion_recovered`，见 `internal/sidecar/orch/orchestration/turn_lifecycle.go:14,29-59`。
- 结论含义：
  - “函数名里没有 force”不等于“系统已经 strict”。
  - 当前实现仍然存在 out-of-band state write，因此不能称为“无 force fallback”。

### 2. `awaiting_user_input` 可达性

- 结论：`❌`
- 事实：
  - 状态和两个触发器只定义在 `internal/dto/agent/state.go:14,28-29,96-104`。
  - orchestration 里没有任何 `TriggerUserInputRequested` / `TriggerUserInputResolved` fire 点。
  - approval 子系统虽然会把请求默认标成 `awaiting_user_input`，也会发布 `ToolApprovalRequested/Resolved`，见 `internal/platform/rpc/approval_support.go:18-32`、`internal/platform/rpc/approval_events.go:23-43`，但 orchestration 没有消费这些 event。
  - provider 侧 `request_user_input` 已经被桥接到 approval manager，见 `internal/provider/codexapp/session_approval.go:38-44,104-116`，但这条链路仍然没有反向驱动 agent 状态机。

### 3. recover 路径

- 结论：`⚠️`
- V2：
  - recover 不是一个状态，而是一组真实闭环：`recoveryPending` + `RecoverAgent` + replay active submission，见 `go-agent-v2/internal/runner/manager_recover.go:57-85,215-332`。
  - 自动触发点很多：`connection_dead`、`thread_status_system_error`、`early silent turn`、queued/dead client submit，见 `go-agent-v2/internal/runner/manager_event.go:307-342`、`go-agent-v2/internal/runner/manager_auto_recover.go:191-305`、`go-agent-v2/internal/runner/manager_submission.go:156-170,211-233`。
- V3：
  - 声明式 recover 路径完整，允许从 `idle`、`turn_queued`、`turn_starting`、`turn_running`、`awaiting_user_input`、`stopped`、`failed` 进入 `recovering`，见 `internal/dto/agent/state.go:82,86,91,97,102,108,110`。
  - 真实运行时入口偏少：主要是 `service.Recover()`、thread recover 和 stall detector，见 `internal/sidecar/orch/orchestration/recover.go:27-58`、`internal/sidecar/orch/orchestration/runner_actor.go:68-77`、`internal/module/thread/lifecycle.go:137-169`。
  - orchestration recover 只负责停旧进程、切状态、重启进程，不承担 V2 那种 active submission replay 语义，见 `internal/sidecar/orch/orchestration/recover.go:43-58`。

### 4. 进程崩溃状态转换

- 结论：`⚠️`
- V2：
  - provider 明确发 `connection_dead` 时，状态先到 `error`，随后异步 `RecoverAgent`，见 `go-agent-v2/internal/guards/state_matrix_snapshot.json:1687-1710`、`go-agent-v2/internal/runner/manager_event.go:323-342`。
  - 若 crash 被 allowlist 命中，会走 `stopped`，并对活动提交合成 `turn_aborted`，见 `go-agent-v2/internal/runner/manager_event.go:270-289,327-337`。
  - 若 provider 没有补 terminal event，但 client 已死，`effectiveState()` 还会把假活跃态回收到 `idle/error`，见 `go-agent-v2/internal/runner/manager.go:227-276`。
- V3：
  - `runnerActor` 用 `cmd.Wait()` 统一监听退出，见 `internal/sidecar/orch/orchestration/runner_actor.go:48-59`。
  - 退出后的状态分支只有三类，见 `internal/sidecar/orch/orchestration/service.go:355-394`：
    - `stopRequested=true`：`stopping -> process_exited -> stopped`
    - 当前是 `provisioning/recovering`：`launch_failed -> failed`
    - 其他运行态：`process_exited -> failed`
  - 这比 V2 更“干净”，但丢掉了 V2 的 crash-aware auto recover 语义，所以不是 1:1 对齐。

## 最终判定

- `10 状态覆盖`：`⚠️`
- `11 触发器覆盖`：`⚠️`
- `strict mode（无 force fallback）`：`❌`
- ``awaiting_user_input`` 可达性：`❌`
- `recover` 路径：`⚠️`
- `进程崩溃状态转换`：`⚠️`
- 总体：`❌`

V3 现在更像“声明表比 V2 更完整，但运行时仍未把所有声明兑现”。如果目标是严格做到 V2↔V3 1:1，对齐优先级应该是：

1. 把 `request_user_input` / approval resolved 正式接到 orchestration 状态机，打通 `turn_running -> awaiting_user_input -> turn_running`。
2. 去掉 `forceIdleAfterCompletionError()` 这类 out-of-band fallback，至少先把 `turn_starting -> turn_aborted`、`awaiting_user_input -> turn_completed` 这类缺口补进声明表。
3. 决定 `recovering` / `turn_queued` / `stopping` 到底要不要坚持做显式状态；如果要，就必须把 V2 的自动 recover / replay 语义一起补齐，否则只是“有状态名，没有对应行为”。
