# module/orchestration 总审查

审查时间：2026-03-21  
审查范围：`internal/sidecar/orch/orchestration/` 下 12 个源码文件  
辅助旁证：`internal/sidecar/orch/orchestration/submission_test.go`、`go-agent-v2/internal/apiserver/methods_orchestration.go`、`go-agent-v2/internal/apiserver/orchestration_report.go`、`internal/store/taskdag/*`、`internal/provider/unified/*`

## 结论摘要

当前 `module/orchestration` 已经不是“只有占位 handler”的状态，DAG、report getter、状态机、runner/fx 接线都已落地；但它距离“V2 orchestration 兼容完成”还有几处硬缺口。

最高优先级问题有 5 个：

1. `agent.submit` / `agent.submitPrompt` 只会入本地队列和改状态，不会把输入送到 provider/session。`TurnSubmission` 在 `claimTurnWork` 后只保留了 `agentID/threadID/turnID`，`Inputs`、skills、schema 全部丢失，`runner_actor.go` 也只做状态推进，不做真实执行。
2. V2 的 12 个 `agent.*` 方法里，`agent.saveSubAgent`、`agent.deleteSubAgent`、`agent.persistSubAgentBinding` 仍然完全缺失；`agent.launch` 也不是 V2 wire。
3. report 链只做到“内存里记 lastReport / requester IDs”。V2 的 requester 向根父代理归并、完成后自动投递 UI/internal message，这里都没有；`orchestration/report` 现在只是 `GetReport` alias。
4. stall auto-recover 机制存在，但判定过于粗糙，且恢复是有损的。`turn_running` 超过 30 秒且 `updatedAt` 没刷新就会触发；恢复时直接清掉 `activeTurnID` 后重启进程，不会 replay 当前 turn。
5. stop 生命周期有事件时序偏差：`StopAgent` 在进程真正退出、状态真正迁到 `stopped` 之前，就先 `publishAgentStopped` 和 `RemoveSession`。

补充旁证：

- `service.go` 381 行，满足你要求的 `<=400`。
- `go test ./internal/sidecar/orch/orchestration` 通过。
- 非测试代码的最高 CC 为 10，未超过你要求的阈值。

## 审查方法

本次主审查按要求以 LSP 为主：

- `document_symbol`：逐文件提取函数/类型边界
- `read_file`：读取函数正文
- `text_search`：查 handler、V2 方法清单、import 方向、死代码调用点
- `references(compact)`：核对 Service/内部方法调用者
- `call_hierarchy`：交叉验证 lifecycle / report / DAG 主链

补充命令只用于客观计数和验证：

- `wc -l internal/sidecar/orch/orchestration/*.go`
- `gocyclo -over 0 internal/sidecar/orch/orchestration/*.go`
- `go test ./internal/sidecar/orch/orchestration`

## 关键发现

### F1. `agent.submit*` 没有真实执行链，submission 内容在 orchestration 内部被丢弃

证据：

- `rpc.go:20-38` 两个 handler 都调用 `svc.SubmitTurn(...)`
- `service.go:157-178` 只把 `TurnSubmission` 入 `SubmissionQueue`
- `service.go:282-308` 出队后只保留 `agentID/threadID/turnID` 到 `turnWork`
- `helpers.go:140-151` `startTurnExecution` 只再触发一次 `turn_accepted`，没有任何 session/provider 调用
- `runner_actor.go:62-66` 只做 `claimTurnWork -> startTurnExecution`
- `internal/dto/turn/model.go:11-19` 的 `Inputs`、`SelectedSkills`、`ManualSkillSelection`、`OutputSchema` 在 orchestration 内再无落点

影响：

- `agent.submit` / `agent.submitPrompt` 虽然 handler 存在、wire 基本兼容，但功能上并没有把请求提交给真实 agent runtime。
- `turn_starting` / `turn_running` 变成“本地自增状态”，不是 provider 确认后的状态。
- 这也是 recover 无法 replay in-flight turn 的直接原因。

结论：`runner_actor` 当前是“状态泵”，不是“执行 actor”。

### F2. V2 12 方法仍缺 3 个，`agent.launch` 也不是 V2 wire

证据：

- V2 清单：`go-agent-v2/internal/apiserver/methods_orchestration.go:14-27`
- 当前清单：`internal/sidecar/orch/orchestration/rpc.go:15-76`

具体问题：

- `agent.saveSubAgent`、`agent.deleteSubAgent`、`agent.persistSubAgentBinding` 完全未注册，也没有对应 `Service` 方法
- V2 `agent.launch` 参数是 `id/name/prompt/cwd/instructions/dynamic_tools/config`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:29-37` 和 `methods_schema_contract_a_m_test.go:252-257`
- 当前 `launchParams` 只有 `agentId/name/cwd/command/parentId/env`，见 `internal/sidecar/orch/orchestration/rpc_types.go:10-17`
- 当前 `launchParams` 没有 `UnmarshalJSON` 补齐 `id`，V2 调用会把 `AgentID` 留空，最终在 `validateLaunchRequest` 报 `agent id is required`
- 当前 `agent.launch` handler 返回 `nil`，不是 V2 的 `{agent_id,name,status}`

### F3. report getter 已落地，但 requester/event 链只有最小内存版

证据：

- `GetReport` / `RememberReportRequest` / `HandleReportEvent` 在 `report.go:62-133`
- V2 report 语义在 `go-agent-v2/internal/apiserver/orchestration_report.go:23-137`

差异：

- 当前 `RememberReportRequest` 只是把 `requesterID` 直接塞进 `agent.reportRequesters`，没有 V2 的根父代理归并逻辑
- 当前 `HandleReportEvent` 在 terminal/report 事件时只 `drainReportRequestersLocked` 并把结果塞回 `NotifiedRequesterIDs`
- 没有任何实际通知投递链；`NotifiedRequesterIDs` 在当前代码中没有消费者
- `orchestration/report` 现在直接调用 `svc.GetReport`，`reportParams.Report` 完全未使用
- `SetReport` 仍在 `Service` 和 `report.go` 里，但仓库内无调用者，是死接口

### F4. StallDetector + Recover 存在，但 auto-recover 现在会误伤正常长 turn，且恢复有损

证据：

- `runner_actor.go:27-44` 每 200ms 轮询，stall 阈值固定 30s
- `recover.go:16-25` 只在 `state == turn_running` 且 `time.Since(updatedAt) > threshold` 时认定 stall
- `service.go` / `helpers.go` 中 `updatedAt` 只在状态迁移、report 更新、remember/drain requester、BindActiveTurnID 时刷新
- `recover.go:43-54` 恢复时直接 `stopProcess -> activeTurnID = "" -> startProcessLocked`

影响：

- 长时间无 report 事件的正常 turn，超过 30 秒就可能被误判为 stall
- 已经出队的 active turn 在恢复时不会回队，也没有 replay 入口，等价于“静默丢 turn”
- `Recover` 无论是手工触发还是 stall detector 触发，`publishAgentRecovering` 都写死 reason=`manual`

### F5. Stop 事件时序早于真实 process exit / state transition

证据：

- `service.go:118-132` `StopAgent` 在 `stopAgentLocked` 成功后，立刻 `removeSession` 并 `publishAgentStopped`
- `service.go:146-155` `stopAgentLocked` 只是发 `stop_requested`、清理 runtime 字段、`Process.Kill()`
- 真正的 `process_exited -> stopped/failed` 状态推进在 `service.go:342-381`

影响：

- `AgentStopped` 事件先于真实 `stopped` 状态
- session 会在进程 waiter 回收前被提前关闭
- 对外部订阅者来说，stop lifecycle 语义不是严格顺序

## 18 项逐项审查

### 1. 文件清单与行数

| 文件 | 行数 | 结论 |
| --- | ---: | --- |
| `contract.go` | 169 | 正常 |
| `dag.go` | 256 | 正常 |
| `events.go` | 101 | 正常 |
| `helpers.go` | 260 | 正常 |
| `module.go` | 53 | 正常 |
| `recover.go` | 58 | 正常 |
| `report.go` | 239 | 正常 |
| `rpc.go` | 214 | 正常 |
| `rpc_types.go` | 209 | 正常 |
| `runner_actor.go` | 81 | 正常 |
| `service.go` | 381 | ✅ 满足 `<=400` |
| `submission.go` | 50 | 正常 |

补充：

- `submission_test.go` 265 行，不在正式源码范围内，但本次作为旁证读取

### 2. handler 完整性

当前 `rpc.go` 注册了 15 个 key：

| 分类 | key |
| --- | --- |
| `agent.*` | `agent.launch` |
| `agent.*` | `agent.submit` |
| `agent.*` | `agent.submitPrompt` |
| `agent.*` | `agent.stop` |
| `agent.*` | `agent.list` |
| `agent.*` | `agent.snapshot` |
| `agent.*` | `agent.getState` |
| `agent.*` | `agent.getReport` |
| `agent.*` | `agent.rememberReportRequest` |
| `agent.*` | `agent.reportEvent` |
| `task/dag/*` | `task/dag/create` |
| `task/dag/*` | `task/dag/get` |
| `task/dag/*` | `task/dag/list` |
| `task/node/*` | `task/node/update` |
| extra | `orchestration/report` |

结论：当前 handler map 是完整的、非占位的；但 V2 12 方法面并不完整，因为少了 3 个 `agent.*`，多了 `agent.snapshot`。

### 3. V2 12 方法精确对照

判定标准：

- `✅`：当前 wire / result / 主行为与 V2 基本对齐
- `⚠️`：handler 存在，但 wire/result/语义有明显偏差
- `❌`：缺失，或关键行为不存在

| V2 方法 | 现状 | 结论 | 说明 |
| --- | --- | --- | --- |
| `agent.launch` | 已注册 | ❌ | V2 用 `id/.../config`，当前只认 `agentId/.../command/env`；V2 调用会因缺 `AgentID` 失败，且结果不是 `{agent_id,name,status}` |
| `agent.submit` | 已注册 | ❌ | 参数兼容、结果是 `{success:true}`，但不会真实提交到 provider，submission 内容在 orchestration 内被丢弃 |
| `agent.submitPrompt` | 已注册 | ❌ | 同 `agent.submit` |
| `agent.stop` | 已注册 | ⚠️ | 可以停进程，但返回 `null`，不是 V2 的 `{success:true}`；且 stop 事件时序提前 |
| `agent.list` | 已注册 | ✅ | 无参数，结果字段与 V2 list snapshot 对齐：`cwd/id/last_report/name/parent_id/port/provider/state/thread_id` |
| `agent.getReport` | 已注册 | ⚠️ | 必需字段对齐，但依赖的是当前最小版 `lastReport` 内存链；额外暴露了可选 `metadata.requester_ids` |
| `agent.rememberReportRequest` | 已注册 | ⚠️ | 接受 V2 `sender_id/worker_id`，但结果字段变成 `{success,agent_id,requester_id}`，且无根 requester 归并 |
| `agent.reportEvent` | 已注册 | ⚠️ | 接受 V2 `agent_id/event_type/event_data`，但只做内存更新和 requester drain，不做 V2 的自动报告投递 |
| `agent.getState` | 已注册 | ✅ | 参数/结果与 V2 对齐 |
| `agent.saveSubAgent` | 缺失 | ❌ | 未注册，`Service` 也无对应方法 |
| `agent.deleteSubAgent` | 缺失 | ❌ | 未注册，`Service` 也无对应方法 |
| `agent.persistSubAgentBinding` | 缺失 | ❌ | 未注册，`Service` 也无对应方法 |

额外暴露但不在 V2 12 方法中的 key：

- `agent.snapshot`
- `task/dag/create`
- `task/dag/get`
- `task/dag/list`
- `task/node/update`
- `orchestration/report`

### 4. Service 接口

`contract.go` 的 `Service` 方法与调用者关系如下：

| 方法 | RPC 调用 | 非 RPC 调用 | 结论 |
| --- | --- | --- | --- |
| `LaunchAgent` | `agent.launch` | `internal/app/thread_orchestration_adapter.go` | 正常 |
| `ListAgents` | `agent.list` | 无 | 正常 |
| `StopAgent` | `agent.stop` | thread adapter | 正常 |
| `SubmitTurn` | `agent.submit*` | 无 | 有入口，但后续执行链缺失 |
| `CompleteTurn` | 无 | `registerTurnLifecycle` 订阅 `TurnCompleted` | 正常 |
| `Recover` | 无 | thread adapter、`runner_actor.go` | 正常 |
| `Snapshot` | `agent.snapshot` | `submissionThreadID` | 正常 |
| `SetReport` | 无 | 无 | ⚠️ 死接口 |
| `GetState` | `agent.getState` | 无 | 正常 |
| `GetReport` | `agent.getReport`、`orchestration/report` | 无 | 正常 |
| `RememberReportRequest` | `agent.rememberReportRequest` | 无 | 正常 |
| `HandleReportEvent` | `agent.reportEvent` | 无 | 正常 |
| `CreateDAG` | `task/dag/create` | 无 | 正常 |
| `GetDAG` | `task/dag/get` | 无 | 正常 |
| `ListDAGs` | `task/dag/list` | 无 | 正常 |
| `UpdateNodeStatus` | `task/node/update` | 无 | 正常 |

结论：接口和当前 RPC 基本对齐，但保留了未接线的 `SetReport`，而缺失 V2 的 sub-agent 持久化方法。

### 5. agent 生命周期

当前链路可以画成：

`LaunchAgent -> provisioning -> idle -> SubmitTurn -> turn_queued -> turn_starting -> turn_running -> CompleteTurn -> idle -> StopAgent -> stopping -> process_exited -> stopped/failed`

正向证据：

- `service.go:101-116` launch
- `service.go:225-255` `startProcessLocked`
- `service.go:157-178` submit queue
- `service.go:282-311` `claimTurnWork`
- `helpers.go:140-151` `startTurnExecution`
- `module.go:33-44` 订阅 `TurnStarted` / `TurnCompleted`
- `service.go:313-340` `CompleteTurn`
- `service.go:118-155` stop
- `service.go:342-381` process exit

结论：生命周期骨架是完整的，但有两处关键偏差：

1. `turn_starting -> turn_running` 不是由 provider 确认触发，而是 runner 自己立即第二次 `turn_accepted`
2. `StopAgent` 的 `AgentStopped` 事件先于真实 `process_exited -> stopped`

### 6. 状态机集成：`fireOrForceLocked` 是否严格模式

结论：✅ 是严格模式，没有 force fallback。

证据：

- `service.go:257-268` 里只有 `fireAndPublishLocked`
- 如果 `FireCtx` 失败，会返回 `errIllegalStateTransition`，并附带 `AllowedTriggers`
- 没有任何直接改 `agent.state` 的 fallback 分支

备注：函数名带 `OrForce`，但实现并不 force。

### 7. turn 事件订阅

结论：✅ 订阅链正确，`BindActiveTurnID` 只有一个定义。

证据：

- `module.go:33-37` 订阅 `turndto.TurnStarted`，调用 `svc.BindActiveTurnID(...)`
- `module.go:38-44` 订阅 `turndto.TurnCompleted`，调用 `svc.CompleteTurn(...)`
- `helpers.go:77-98` 是 `BindActiveTurnID` 的唯一实现
- `references(compact)` / `call_hierarchy` 显示 `BindActiveTurnID` 只有 `registerTurnLifecycle` 一个调用点

### 8. DAG 实现

结论：✅ 4 个方法都落到了真实 `taskdag.Store`。

明细：

- `CreateDAG`：`dag.go:14-39`，先 `dagStoreOrErr`，再 `store.WithTx`，事务内调用 `UpsertDAG`、`UpsertNode`、`GetDAG`、`ListNodes`
- `GetDAG`：`dag.go:41-47`，调用 `loadDAGDetail -> store.GetDAG + store.ListNodes`
- `ListDAGs`：`dag.go:49-63`，调用 `store.ListDAGs`
- `UpdateNodeStatus`：`dag.go:65-79`，调用 `store.UpdateNodeStatus`

补充注意：

- `CreateDAG` 当前是 upsert 语义，不会删除“这次请求里没带上的旧节点”

### 9. report 实现

结论：⚠️ getter 已完成，但读/写/requester/event 只完成了最小链。

拆开看：

- 读：`GetReport` 正常，返回 `{agent_id, report, state, metadata?}`
- 写：`SetReport` 存在但无调用者，是死链
- requester：`RememberReportRequest` 只记当前 `requesterID`，不做 V2 根 requester 归并
- event：`HandleReportEvent` 能从 `report` 或 `event_data` 抽取文本，也能识别 terminal event，但只是返回 `NotifiedRequesterIDs`，没有真实通知 side effect
- alias：`orchestration/report` 当前直接走 `GetReport`，不是独立 report 写入口；`reportParams.Report` 未被使用

### 10. `AgentSnapshot`

结论：✅ 字段完整，json tag 为 snake_case，构造点单一。

核对结果：

- 定义：`contract.go:45-55`
- 字段包含 `port` / `provider` / `last_report`
- 唯一构造点：`service.go:211-223` `snapshotLocked`
- 对外出口：`ListAgents` 和 `Snapshot` 都复用 `snapshotLocked`

备注：

- `port` / `provider` 来自 launch 请求推断，不是 runtime 回读值

### 11. submit 参数

结论：✅ 参数层面已满足“`agent_id` + 双格式反序列化”要求。

证据：

- `rpc_types.go:70-100` `submitParams`
- 主字段就是 V2 wire：`agent_id/prompt/images/files`
- `UnmarshalJSON` 额外兼容 `agentId`
- 同时保留 legacy `input`，`rpc.go:103-134` 支持单 item / item 数组双格式反序列化
- `submitPromptParams` 只是 `type alias` 到 `submitParams`

备注：参数兼容不等于执行链完整；执行缺口见 F1。

### 12. `runner_actor`

结论：❌ 没有 execute/interrupt 模式，只有轮询、wait、状态推进和 recover。

证据：

- `runner_actor.go:26-45` 主循环只做三件事：`startWaiters`、`processTurnQueues`、`recoverStalledAgents`
- `runner_actor.go:62-66` `processTurnQueues` 只调用 `claimTurnWork` / `startTurnExecution`
- `helpers.go:140-151` `startTurnExecution` 只做状态机推进
- `internal/sidecar/orch/orchestration` 范围内没有任何 `turn/interrupt` 或 provider/session submit 调用

结论细化：

- execute：不存在
- interrupt：不存在
- 实际上它更像“进程 watcher + local queue state pump”

### 13. `recover`

结论：⚠️ 机制存在，但误判和数据丢失风险都比较高。

现状：

- `StallDetector`：只看 `turn_running` 且 `updatedAt` 超 30 秒
- auto-recover：`runner_actor.go:68-77` 会自动调用 `service.Recover`
- `Recover`：`recover.go:27-41`
- `recoverAgent`：`recover.go:43-54`

问题：

- `updatedAt` 刷新面太窄，正常长 turn 容易被判 stall
- 恢复时 active turn 不回放、不回队
- 自动恢复对外 reason 仍报 `manual`

### 14. `SubmissionQueue`

结论：✅ FIFO 正确，线程安全基本合格。

证据：

- `submission.go:14-18` append enqueue
- `submission.go:20-29` 从 `items[0]` 出队，保持 FIFO
- `submission.go:31-50` `Peek/Len/Clear` 完整
- `submission_test.go` 覆盖了顺序、并发、Clear、report 参数兼容等场景

小注：

- `q.items = q.items[1:]` 会保留 backing array，极端大队列场景有潜在内存滞留，但不影响 FIFO 正确性

### 15. `SessionCleaner`

结论：✅ 是窄接口，`Remove` 最终会关闭 session。

证据：

- 窄接口定义：`contract.go:30-32`
- orchestration 只依赖 `SessionCleaner`
- 绑定实现：`internal/provider/unified/module.go:20-28`、`session_adapter.go:21-33`
- 真实关闭：`internal/provider/unified/session.go:59-82`

行为：

- `Remove` 先从 map 删除，再 `session.Close(context.Background())`
- `Close` 失败时再 `ForceStop`

### 16. import 方向

结论：✅ `internal/sidecar/orch/orchestration` 内没有直接 import `internal/provider/...`。

结果：

- 对 `internal/sidecar/orch/orchestration` 做 `text_search("internal/provider/")` 和 `text_search("provider/")` 都无命中
- provider 侧反向 import orchestration 的窄接口用于 `fx.As(new(orchestration.SessionCleaner))`，这是可接受方向

### 17. fx 注册

结论：✅ `rpc_handlers` 和 `runners` 都已正确注册并被消费。

证据：

- orchestration 模块：`module.go:15-23`
- `NewOrchestrationHandlers` 返回 `rpc.HandlerMapResult`
- `internal/platform/rpc/module.go:33-48` 把 `group:"rpc_handlers"` 聚合后注册到 RPC server
- `module.go:20` 用 `fx.ResultTags(group:"runners")` 注册 `NewRunnerActor`
- `internal/app/runner.go:18-60` 聚合 `[]platformrunner.Runner` 并在运行时统一启动

### 18. 函数复杂度

结论：✅ 非测试代码的最高 CC 为 10，没有超过阈值；最长函数也可枚举。

按长度 Top 5：

| 函数 | 行数 |
| --- | ---: |
| `NewOrchestrationHandlers` | 63 |
| `(*service).HandleReportEvent` | 36 |
| `(*service).startProcessLocked` | 31 |
| `(*service).claimTurnWork` | 30 |
| `(*service).CompleteTurn` | 28 |

按 `gocyclo` 的非测试代码 CC Top 5：

| 函数 | CC |
| --- | ---: |
| `inputItemsFromSubmitParams` | 10 |
| `(*service).reconcileReadyStateLocked` | 10 |
| `registerTurnLifecycle` | 8 |
| `(*service).HandleReportEvent` | 8 |
| `(*service).claimTurnWork` | 7 |

补充：

- 测试函数 `TestReportParamCompatibility` 的 CC 为 17，但测试文件不在本次源码审查阈值内

## 总体判定

`module/orchestration` 当前更接近“状态/存储/订阅骨架已齐，执行链尚未接通”的阶段。

可以给出的最终判定是：

- 当前模块不是空壳，DAG、snapshot、state/report getter、状态机、runner/fx 接线都已具备
- 但它还不能被判定为“V2 orchestration 兼容完成”
- 真正阻塞迁移的不是细枝末节，而是 `agent.submit*` 执行链缺失、V2 3 个方法缺失、report auto-delivery 缺失、recover 语义过粗

如果要按迁移 blocker 排序，建议顺序如下：

1. 打通 `agent.submit* -> session/provider` 真执行链，并保留完整 `TurnSubmission`
2. 补齐 `agent.saveSubAgent` / `agent.deleteSubAgent` / `agent.persistSubAgentBinding`
3. 修正 `agent.launch` V2 wire 和返回 shape
4. 完成 report requester 归并和 terminal auto-delivery
5. 重做 stall/recover 判定与 replay 语义
6. 统一 stop 事件与 process exit 的时序
