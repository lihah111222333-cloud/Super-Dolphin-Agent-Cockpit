# 能力+容错审查：Orchestration Agent 管理层

## 审查方式

- 只用 LSP 取证：`text_search`、`workspace_symbol`、`references(compact)`、`call_hierarchy`、`read_file`
- 主审对象：V3 `internal/sidecar/orch/orchestration`、`internal/module/thread`、`internal/module/turn`、`internal/provider/unified`、`internal/store/taskdag`
- V2 仅用于等价性对照：`go-agent-v2/internal/apiserver/methods_orchestration.go`
- 结论口径：
  - `通过`：链路闭合，当前代码可真实到达
  - `部分通过`：主路径存在，但接线、容错或语义不完整
  - `不通过`：当前主路径缺失，或与目标能力明显不等价

## 结论摘要

| 维度 | 结论 | 核心判断 |
| --- | --- | --- |
| 1. `agent.launch` 完整链路 | 部分通过 | RPC 到进程启动和 `idle` 状态成立；但 orchestration 自身不创建 provider session，也没有显式 `running` 状态 |
| 2. `agent.stop` | 部分通过 | `RemoveSession` 会 `Close`；但 `stopped` 依赖异步 `Wait()` 回调，`AgentStopped` 事件在真正 `stopped` 前就先发了 |
| 3. `agent.submit` 真实执行 | 部分通过 | `RPC -> queue -> claim -> TurnStarter -> provider session` 代码已接上；但前提是 session 已由 thread 生命周期预先注册，`agent.launch` 本身不满足这个前提 |
| 4. 进程崩溃恢复 | 部分通过 | `handleProcessExit` 能清资源、转失败态、发事件；但自动恢复只重启进程，不恢复 session，也不重放 active/queued turn |
| 5. `StallDetector` | 部分通过 | 30 秒超时检测真实工作；但只看 `updatedAt`，且恢复动作只是低层 relaunch，误判和丢上下文风险都高 |
| 6. `SubmissionQueue` | 部分通过 | FIFO、空队列、并发访问都成立；但没有满队列语义，也没有任何背压/容量保护 |
| 7. 多 Agent 并发 | 部分通过 | 全局互斥基本挡住竞态；但实现是粗粒度串行，不是高并发友好的调度层 |
| 8. 状态机严格性 | 不通过 | `fireOrForceLocked` 本身不 force；但 `prepareLaunchStateLocked` 直接写 `provisioning` 绕过状态机，且存在 `turn_starting + turn_aborted` 非法迁移风险 |
| 9. DAG 执行能力 | 通过 | `task/dag/create|get|list` 与 `task/node/update` 都真实落到 `taskdag.Store` 和 `sqlc` |
| 10. report 链路 | 部分通过 | `getReport / rememberReportRequest / reportEvent` 在内存内闭环；但 requester 只被 drain 出结果，没有真实 UI/消息投递 |
| 11. `AgentSnapshot` | 部分通过 | JSON tag 是 snake_case；但 `thread_id` 只靠本地提交时回填，`port/provider` 也只是 launch 参数推导，不是 runtime 真值 |
| 12. V2 等价性 | 部分通过 | V2 的 12 个 orchestration RPC 方法里，V3 只直接覆盖 9 个；还缺 `saveSubAgent`、`deleteSubAgent`、`persistSubAgentBinding`，且 `agent.launch` 合约明显缩水 |

## 1. `agent.launch` 完整链路

证据：

- RPC 入口存在：`internal/sidecar/orch/orchestration/rpc.go:17-19` 把 `agent.launch` 直接绑到 `svc.LaunchAgent(...)`，参数映射在 `internal/sidecar/orch/orchestration/rpc.go:79-88`
- service 链路真实存在：`internal/sidecar/orch/orchestration/service.go:110-125` 负责校验、取/建 runtime、清队列、置 launch 状态并启动进程
- 进程启动与状态变更真实存在：`internal/sidecar/orch/orchestration/service.go:234-263` 调 `exec.Command(...).Start()`，成功后触发 `launch_succeeded`，状态机会从 `provisioning -> idle`，随后发 `AgentLaunched`
- 但 session 创建不在这条链上：orchestration `LaunchAgent` 只启动进程；真正 `StartSession/Register` 在 thread 生命周期里，见 `internal/module/thread/lifecycle.go:44-76,204-219` 与 `internal/provider/unified/client.go:29-65`

判断：

- `agent.launch` 当前能做到的是：`RPC -> LaunchRequest -> OS 进程启动 -> 本地 runtime 建立 -> 状态到 idle`
- 它做不到的是：`provider session 创建`
- V2 返回口径里的 `"status":"running"` 在 V3 并没有对应状态；当前稳定态是 `idle`

## 2. `agent.stop`

证据：

- RPC 入口存在：`internal/sidecar/orch/orchestration/rpc.go:40-42`
- stop 主路径在 `internal/sidecar/orch/orchestration/service.go:127-141`
- `stopAgentLocked` 会先走 `stop_requested`，然后 `queue.Clear()`、清空 `activeTurnID/threadID`，最后 `Kill` 进程，见 `internal/sidecar/orch/orchestration/service.go:155-164`
- `RemoveSession` 确实包含 `Close`：`internal/provider/unified/session_adapter.go:34-39` -> `internal/provider/unified/session.go:59-82`
- 真正 `stopped` 状态要等 `cmd.Wait()` 回来：`internal/sidecar/orch/orchestration/runner_actor.go:48-59` -> `internal/sidecar/orch/orchestration/service.go:355-394`

判断：

- 这条链不是同步的“stop 完即 stopped”
- `StopAgent()` 返回时，状态通常只是 `stopping`
- `AgentStopped` 事件在 `service.StopAgent` 返回路径里提前发出，见 `internal/sidecar/orch/orchestration/service.go:138-139`；真正 `StateStopped` 则依赖稍后的 `handleProcessExitTransition`
- 另外这里是 `Process.Kill()`，不是优雅停止

## 3. `agent.submit` 真实执行

证据：

- RPC 入口存在：`internal/sidecar/orch/orchestration/rpc.go:20-29,30-39`
- `SubmitTurn` 会把 submission 入队，并在 `idle` 时转到 `turn_queued`，见 `internal/sidecar/orch/orchestration/service.go:166-187`
- runner actor 每 200ms 处理一次队列：`internal/sidecar/orch/orchestration/runner_actor.go:33-44,62-66`
- `claimTurnWork` 会 dequeue、设 `ExpectedTurnID`、绑定 `activeTurnID`，并把状态从 `turn_queued -> turn_starting`，见 `internal/sidecar/orch/orchestration/service.go:291-324`
- `startTurnExecution` 会调用 `TurnStarter.StartTurn`，见 `internal/sidecar/orch/orchestration/helpers.go:140-151`
- 当前 `TurnStarter` 实现已经连到 provider session：`internal/module/turn/orchestration_starter.go:22-52`，里面先 `sessions.GetSession(agentID)`，再 `PrepareTurn`，再 `session.StartTurn`
- provider session `StartTurn` 真实存在：`internal/provider/codexapp/session.go:104-125`、`internal/provider/claudecli/session.go:87-113`

关键前提：

- session 只会在 `StartSession/ResumeSession` 后被 `SessionManager.Register(...)`，见 `internal/provider/unified/client.go:47-67`
- orchestration `agent.launch` 自己并不会做这一步，见 `internal/sidecar/orch/orchestration/service.go:110-125`

判断：

- “queue -> claim -> TurnStarter -> provider session” 这条代码链已经接上，不是旧的断链状态
- 但它依赖 session 已经由 thread 生命周期创建；如果只是直接调 `agent.launch -> agent.submit`，队列能进，真正 start 时仍可能在 `GetSession` 失败

## 4. 进程崩溃恢复

证据：

- 监控等待器真实存在：`internal/sidecar/orch/orchestration/runner_actor.go:48-59`
- 退出统一收口在 `internal/sidecar/orch/orchestration/service.go:355-394`
- unexpected exit 会：
  - 清 `cmd`
  - 记录 `exitedAt/updatedAt`
  - `RemoveSession`
  - 若非手动 stop，发 `AgentFailed(recoverable=true)`，见 `recordProcessExitError`
  - 再走状态机 `process_exited` / `launch_failed`
- 但 recovery 只有低层 relaunch：`internal/sidecar/orch/orchestration/recover.go:27-58`

关键缺口：

- orchestration 的 `Recover` 只做 `stopProcess -> recover_requested -> startProcessLocked`
- 它不恢复 provider session，也不 replay active turn
- 与之相对，thread 层 `Recover` 会在缺 session 时走 `resumeSession(...)`，见 `internal/module/thread/lifecycle.go:137-169`

判断：

- “崩溃 -> 清理 -> failed/stopped 转态” 是存在的
- “崩溃后自动恢复到可继续执行任务的完整上下文” 当前并不存在

## 5. `StallDetector`

证据：

- actor 在 `Run()` 里固定创建 `StallDetector{threshold: 30 * time.Second}` 并周期检查，见 `internal/sidecar/orch/orchestration/runner_actor.go:27-44`
- `CheckStall` 只认 `state == turn_running` 且 `time.Since(updatedAt) > threshold`，见 `internal/sidecar/orch/orchestration/recover.go:16-25`
- `updatedAt` 只在这些地方刷新：
  - `prepareLaunchStateLocked` / `BindActiveTurnID`，见 `internal/sidecar/orch/orchestration/helpers.go:52-60,77-98`
  - `fireAndPublishLocked`，见 `internal/sidecar/orch/orchestration/service.go:281-289`
  - report 相关写操作，见 `internal/sidecar/orch/orchestration/report.go:135-169`
- 没有任何 turn output delta / input / provider 活动会刷新 orchestration 的 `updatedAt`
- 检测到 stall 后的动作只是 `service.Recover(...)`，见 `internal/sidecar/orch/orchestration/runner_actor.go:68-77`

判断：

- StallDetector 不是死代码，30 秒后会真的触发 recover
- 但它把“30 秒没有 orchestration 本地状态变更”当成“turn stalled”，误报概率很高
- recover 也只是低层 relaunch，不恢复 session 或活跃 turn

## 6. `SubmissionQueue`

证据：

- 实现是互斥锁 + slice：`internal/sidecar/orch/orchestration/submission.go:9-50`
- FIFO 成立：`Enqueue` append，`Dequeue` 总是取 `items[0]`
- 空队列边界存在：`Dequeue/Peek` 都会返回零值和 `false`
- 测试覆盖了顺序、清空、并发访问：`internal/sidecar/orch/orchestration/submission_test.go:17-111`

判断：

- FIFO 和空队列边界都是成立的
- “满队列”在当前实现里根本没有定义：没有容量上限、没有 backpressure、没有拒绝策略
- 所以它不是“满队列处理正确”，而是“压根没有满队列语义”

## 7. 多 Agent 并发

证据：

- service 用一个全局 `sync.RWMutex` 保护全部 agents 运行态，见 `internal/sidecar/orch/orchestration/service.go:29-39`
- `LaunchAgent`、`StopAgent`、`SubmitTurn`、`claimTurnWork`、`handleProcessExit`、`Recover` 都会拿这把锁，见：
  - `service.go:115-116,128-129,167-168,292-293,356-357`
  - `recover.go:28-29`
- process exit waiter 是 goroutine，但最终也串回同一把锁，见 `runner_actor.go:54-59` -> `service.go:355-370`
- 双重 `RemoveSession` 是幂等的：第一次删除并 `Close`，第二次查不到直接返回，见 `internal/provider/unified/session.go:59-82`

判断：

- 多 agent 同时 start/stop 的主要竞态基本被这把全局锁挡住了
- 代价是粗粒度串行：无关 agent 之间也会互相阻塞
- 这更像“安全但保守”的实现，不像高并发 orchestration manager

## 8. 状态机严格性

证据：

- `fireOrForceLocked` 实际上不会 force；它只是 `FireCtx(...)`，失败就返回 `illegal state transition` 并附 allowed trigger，见 `internal/sidecar/orch/orchestration/service.go:266-279`
- 但 `prepareLaunchStateLocked` 直接写 `agent.state = provisioning`，见 `internal/sidecar/orch/orchestration/helpers.go:52-60`
- 这一步没有经过状态机，也没有发 `StateChanged`
- 状态定义里 `turn_starting -> idle` 只允许 `turn_completed`，不允许 `turn_aborted`，见 `internal/dto/agent/state.go:89-95`
- `CompleteTurn(success=false)` 却会在任何当前 active turn 上选择 `TriggerTurnAborted`，见 `internal/sidecar/orch/orchestration/service.go:341-348`

判断：

- “非法转换会报错”这句话对 `fireOrForceLocked` 本身成立
- 但整体状态机并不严格，因为 launch 预备态是直接写入的
- 另外存在一条真实的非法迁移风险：provider 若在 `turn_starting` 阶段回失败完成事件，`CompleteTurn` 会打出 `turn_aborted`，而状态机不允许

## 9. DAG 执行能力

证据：

- RPC 已注册：
  - `task/dag/create`、`task/dag/get`、`task/dag/list`、`task/node/update`
  - 见 `internal/sidecar/orch/orchestration/rpc.go:61-72`
- service 层都真实落到 store：
  - `CreateDAG`：`internal/sidecar/orch/orchestration/dag.go:14-39`
  - `GetDAG`：`41-47`
  - `ListDAGs`：`49-63`
  - `UpdateNodeStatus`：`65-79`
- store 层不是 stub，而是 `sqlc`：
  - `internal/store/taskdag/store.go:21-79`
  - 具体调用 `UpsertTaskDag / GetTaskDag / ListTaskDags / UpdateTaskDagNodeStatus`
- FX 也确实提供了 `taskdag.NewStore`，见 `internal/store/taskdag/module.go:5-7`

判断：

- 用户点名的这 4 个 DAG 方法都已经是真实 store 调用
- 这里能判 `通过`
- 但也要注意：当前 orchestration 模块只覆盖 CRUD 和 node status，不等于“整套 DAG watcher/dispatch 都在这里落地”

## 10. report 链路

证据：

- `agent.getReport`、`agent.rememberReportRequest`、`agent.reportEvent`、`orchestration/report` 全都注册了，见 `internal/sidecar/orch/orchestration/rpc.go:52-75`
- `GetReport` 返回 `agent_id/report/state/metadata.requester_ids`，见 `internal/sidecar/orch/orchestration/report.go:62-70,140-151`
- `RememberReportRequest` 会把 requester 记到 `agent.reportRequesters`，见 `internal/sidecar/orch/orchestration/report.go:73-95,154-163`
- `HandleReportEvent` 会解析 report、必要时 drain requester，并返回 `notified_requester_ids`，见 `internal/sidecar/orch/orchestration/report.go:98-133`
- 但同文件 `97` 行明确写了 TODO：drained requester notifications 还没有真正送进 UI timeline

判断：

- 这条链在“内存状态 + RPC 返回值”层面是闭的
- 在“真实通知 requester/UI”层面没有闭环
- 因此当前只能判 `部分通过`

## 11. `AgentSnapshot`

证据：

- 合约字段与 JSON tag 在 `internal/sidecar/orch/orchestration/contract.go:49-59`，全部是 snake_case
- `snapshotLocked` 的填充逻辑在 `internal/sidecar/orch/orchestration/service.go:220-231`
- `Port` / `Provider` 来自 launch 参数推导，不是 runtime 真值：
  - `launchPort` / `launchProvider` 在 `internal/sidecar/orch/orchestration/helpers.go:233-253`
- `threadID` 只有两类写路径：
  - clear：`helpers.go:56`、`service.go:162,251`
  - 由 submission.ThreadID 回填：`service.go:312-315`
- 没有任何 session/thread resume 逻辑会主动把 provider thread id 回填进 snapshot

判断：

- `json tag snake_case` 没问题
- `last_report` 也确实来自真实内存态
- 但 `thread_id` 不是 provider session truth，`port/provider` 也只是 launch-time heuristic

## 12. V2 等价性

V2 的 orchestration RPC 方法面，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:14-27`：

1. `agent.launch`
2. `agent.submit`
3. `agent.submitPrompt`
4. `agent.stop`
5. `agent.list`
6. `agent.getReport`
7. `agent.rememberReportRequest`
8. `agent.reportEvent`
9. `agent.getState`
10. `agent.saveSubAgent`
11. `agent.deleteSubAgent`
12. `agent.persistSubAgentBinding`

V3 当前方法面，见 `internal/sidecar/orch/orchestration/rpc.go:15-76`：

- 已覆盖的 9 个：`launch / submit / submitPrompt / stop / list / getReport / rememberReportRequest / reportEvent / getState`
- 新增：`agent.snapshot`、`task/dag/create|get|list`、`task/node/update`、`orchestration/report`
- 缺失的 3 个：`agent.saveSubAgent`、`agent.deleteSubAgent`、`agent.persistSubAgentBinding`

进一步差异：

- V3 `agent.launch` 参数明显缩水，`internal/sidecar/orch/orchestration/rpc_types.go:8-17` 只保留 `agentId/name/cwd/command/parentId/env`
- 同文件 `8-9` 行直接写了 TODO：V2 `agent.launch` 还有 `prompt/instructions/dynamic_tools/config`
- V3 `internal` 里也找不到 `SaveSubAgent/DeleteSubAgent/PersistSubAgentBinding` 对应实现，LSP `text_search` 为 0

判断：

- 只能说方法名覆盖到 `9 / 12`
- 且这 9 个里，`agent.launch` 还是能力缩水版

## 最终判断

- 当前 V3 orchestration agent manager 已经具备最基本的进程管理、队列派发、状态机、report、DAG CRUD 能力，不是空壳。
- 但从“能力 + 容错”角度看，真正大的缺口仍有 4 个：
  - `agent.launch` 不创建 provider session
  - `Recover/StallDetector` 只做进程级 relaunch，不恢复 session/active turn
  - 状态机存在绕过点和至少一条明显非法迁移风险
  - report requester 目前只是本地 drain，没有真实投递
- 如果目标是宣称“Orchestration Agent 管理层迁移完成”，当前证据还不够。

## 互审

### 1. `docs/plans/迁移/cap-turn-execution.md`

1. 这份报告漏掉了一个更基础的 RPC 面问题：V3 turn RPC 里的 `threadId` 基本是死字段。`turnStartParams.ThreadID`、`turnSteerParams.ThreadID`、`turnInterruptParams.ThreadID`、`threadIDOnlyParams.ThreadID` 都定义了，但 LSP `references` 为 0；handler 实际用的是 `rpc.ThreadIDFrom(ctx)`，见 `internal/module/turn/rpc.go:20-29` 与 `internal/platform/rpc/handler.go:88-100`，而 `buildPrepareInput(...)` 也根本不传 `ThreadID`，见 `internal/module/turn/rpc_helpers.go:5-14`。报告讲了“输入面缩水”，但没指出参数已经名存实亡。

2. 这份报告把 orchestration submit 链讲成了“真实可执行”，但漏掉了当前最硬的前提条件：session 必须预先存在。`orchestrationTurnStarter.StartTurn` 第一件事就是 `sessions.GetSession(agentID)`，见 `internal/module/turn/orchestration_starter.go:29-37`；而 session 只会在 `StartSession/ResumeSession` 后被 `SessionManager.Register(...)`，见 `internal/provider/unified/client.go:47-67`。`agent.launch` 自己只起进程，不建 session，见 `internal/sidecar/orch/orchestration/service.go:110-125`。所以“代码链接上了”不等于“`agent.launch -> agent.submit` 当前就一定能跑通”。

3. 这份报告批了 `turn/forceComplete` 的语义，但还少指出一个直接的 RPC 兼容性问题：V3 handler 返回 `nil`，见 `internal/module/turn/rpc.go:70-75`；V2 则把 provider 返回值原样透出，`turn/forceComplete` 实现最终返回的是 `{"confirmed": true, "forceCompleted": true}`，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:67-70` 与 `go-agent-v2/legacy-agentsdk/service/interrupt/turn_interrupt_core.go:271-305`。也就是说，V3 这里不仅“语义不等价”，连返回形状都已经变了。

### 2. `docs/plans/迁移/cap-approval-lifecycle.md`

1. 这份报告第 6 节把 `request_user_input` 判成“无真实调用点 / provider 未桥接”，这个结论已经过时。当前 `codexapp` 的 `onNotification(...)` 会把 `request_user_input` 家族方法纳入审批桥接，见 `internal/provider/codexapp/session.go:233-238`；`isRequestUserInputMethod(...)` 明确识别 `request_user_input`、`codex/event/request_user_input`、`item/tool/request_user_input` 等变体，见 `internal/provider/codexapp/session_approval.go:102-113`；随后 `requestApprovalDecision(...)` 会真实调用 `ApprovalManager.RequestUserInput(...)`，见同文件 `38-44`。所以“API 包装存在但没有真实 caller”已经不成立。

2. 这份报告第 7 节说当前 internal 只覆盖 command-exec + legacy approval，file/skill 族没有接上，这个判断也不对。`codexapp` 入口 `isApprovalBridgeMethod(...)` 已经包含 `item/fileChange/requestApproval` 和 `skill/requestApproval`，见 `internal/provider/codexapp/session_approval.go:93-100`；translator 也会把这两类事件翻成 `ToolApprovalRequested`，见 `internal/provider/codexapp/event_map.go:132-140`；而 `normalizeApprovalCallbackMethod(...)` 对 file/skill method 还是保留原值，不会丢，见 `internal/platform/rpc/approval_events.go:56-72`。换句话说，当前 V3 不是“只有 exec 子集”，而是 codex ingress 已经覆盖 exec/file/skill 三类。

3. 这份报告第 9 节说 `sendApprovalDecision` 绕过 recovery wrapper，证据已经失效。当前实现不是直接 `transport.Call(...)`，而是 `s.callTransport(...)`，见 `internal/provider/codexapp/session_approval.go:74-90`；`callTransport(...)` 在遇到可重连错误时会先 `attemptRecovery(...)`，再重试一次 transport call，见 `internal/provider/codexapp/recovery.go:49-58,69-94`。所以“本地已 resolve 但 provider 回传不走 recover/retry”这个批评在当前代码上站不住。

### 3. `docs/plans/迁移/cap-event-push.md`

1. 这份报告把 approval 的前端可达性几乎完全讲成了 bus/push/Wails 问题，范围没有收干净。代码里实际上还有一条独立的 direct callback lane：`ApprovalManager.ensureDispatch(...) -> dispatchApproval(...) -> PushBridge.CallbackClient(...) -> server.Callback(...)`，见 `internal/platform/rpc/approval.go:154-205` 与 `internal/platform/rpc/push.go:42-57`，callback method 还会经过 `internal/platform/rpc/approval_events.go:44-72` 归一化。当前 codex live path 因为传了 `nil, nil` 而没有走这条路，见 `internal/provider/codexapp/session_approval.go:41-43`；但报告如果不把结论限定成“当前 codex live path 的 bus fanout”，就会把“总能力缺失”和“当前接线没用上”混在一起。

2. 这份报告第 10 节里“Push / Wails 实际外放了多少，只外放 3 个”这个表述不够精确。对 bus-bridged typed event 来说，这句话是对的，见 `internal/platform/rpc/push.go:75-92` 与 `internal/ui/wails/bridge.go:53-63`；但 Wails 后端并不只会发这 3 个事件，生命周期层还会独立发 `app-will-quit`，见 `internal/ui/wails/lifecycle.go:12-15,82-99,137-143`。所以这里至少应该收窄成“bus-derived bridge methods 只有 3 个”。

3. 这份报告强调“状态机成功迁移后必发 `StateChanged`”，但漏掉了 orchestration 里有一个关键生命周期阶段根本不走状态机：`prepareLaunchStateLocked(...)` 直接写 `agent.state = provisioning`，见 `internal/sidecar/orch/orchestration/helpers.go:52-60`。真正会统一 `publishStateChanged(...)` 的只有 `fireAndPublishLocked(...)`，见 `internal/sidecar/orch/orchestration/service.go:281-289`。这意味着 bus/push/Wails 并不能观察到所有重要生命周期变化，至少 `provisioning` 进入点现在就是静默的。报告如果只强调 `StateChanged` 的稳定性，会高估当前生命周期事件面的完整度。
