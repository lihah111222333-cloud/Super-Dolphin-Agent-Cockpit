# Turn 执行层能力+容错审查

## 范围与方法

- 范围：V3 `internal/module/turn`、`internal/sidecar/orch/orchestration`、`internal/provider/*`，以及对照 V2 `go-agent-v2/internal/apiserver`、`go-agent-v2/legacy-agentsdk/service/*`。
- 方法：仅用 LSP `text_search`、`workspace_symbol`、`references(compact)`、`call_hierarchy`、`read_file(func_start/func_end)` 逐项落证。
- 口径：只认“代码里已经接通的真实链路”；文档、计划、历史审计不作为主证据。

## 总结

- `turn/start` 在 V3 里不是空壳，RPC -> `PrepareTurn` -> assembler/skills/manifest -> provider `StartTurn` -> 本地 tracker 这条链是真实实现。
- `turn/steer` 名字存在，但语义不是 V2 的 active-turn steer，而是“拿 prompt 新开一个 turn”。
- `turn/forceComplete` 没有独立 provider contract，只是 `Interrupt(Source="force_complete")` 的别名；最终 tracker 状态仍收敛到 `interrupted`。
- `TurnCompleted -> orchestration.CompleteTurn` 只对 orchestration 队列发起的 turn 真闭环；直接 `turn/start` RPC 不会先设置 `activeTurnID`，因此事件会被 `errTurnNotActive` 吞掉。
- approval 后端有 `approval/respond` 响应面，也有 Codex provider 内部回传逻辑，但统一事件推送/UI/状态机接线不完整，`awaiting_user_input` 触发器完全未落地。
- `BinaryDir` 的 manifest 路径问题看起来已修：默认走 `os.Executable()` 目录，显式 override 也有测试验证。

## 结论表

| # | 维度 | 结论 |
| --- | --- | --- |
| 1 | `turn/start` 完整链路 | ✅ 真链路，但 RPC 输入面比 DTO/ V2 窄很多 |
| 2 | `turn/steer` | ❌ 语义不成立，实际是新开 turn |
| 3 | `turn/interrupt` | ⚠️ provider 可中断，但状态回收只部分闭环 |
| 4 | `turn/forceComplete` | ❌ 只是 interrupt 别名，不是独立 force-complete 语义 |
| 5 | `TurnCompleted` 事件闭环 | ⚠️ 仅 orchestration 队列 turn 闭环，直接 RPC 不闭环 |
| 6 | skill 注入 | ⚠️ service/provider 注入真实存在，但 direct RPC 不暴露完整能力 |
| 7 | approval 集成 | ❌ 响应面存在，统一交互/状态闭环不完整，且 Claude 路径缺失 |
| 8 | 错误处理 | ⚠️ 同步错误处理基本有，异步断连/卡死对 orchestration 收敛不足 |
| 9 | 并发 turn | ⚠️ orchestration 串行；直接 RPC 同线程并发依赖 provider，自身无统一保护 |
| 10 | tracker 正确性 | ⚠️ 只跟本地 handle，不跟 provider 事件流，权威性不足 |
| 11 | V2 等价性 | ❌ 仍有结构性差距 |
| 12 | `BinaryDir` | ✅ 已修并有测试，但 override 只在 service 层可传 |

## 1. `turn/start` 完整链路

结论：✅ 真正落地。

证据：
- RPC 入口真实存在：`internal/module/turn/rpc.go:33-46` 的 `turn/start` handler 会先 `buildPrepareInput(...)`，再调 `svc.PrepareTurn(...)` 和 `svc.StartTurn(...)`。
- `buildPrepareInput` 真实构造 `PrepareInput`：`internal/module/turn/rpc_helpers.go:5-14`。
- `PrepareTurn` 不是空壳：`internal/module/turn/service.go:47-74` 实际执行了 `assembler.Assemble`、`skills.Resolve`、`buildOverrides`、`manifest.Build`。
- assembler/skills/manifest 都有真实实现：`internal/module/turn/assembler.go:47-69`、`internal/module/turn/skills.go:11-37`、`internal/module/turn/manifest.go:17-24`。
- `StartTurn` 真实调用 provider session，并登记 tracker：`internal/module/turn/service.go:76-106`。
- provider 侧 `StartTurn` 真实存在：`internal/provider/codexapp/session.go:104-125`、`internal/provider/claudecli/session.go:87-113`。
- tracker 真实存在：`internal/module/turn/tracker.go:34-118`。
- LSP `call_hierarchy` 也验证了 `turn.Service.StartTurn` 的入边来自 `internal/module/turn/rpc.go:41`、`internal/module/turn/orchestration_starter.go:47`、`internal/module/turn/service.go:114`。

判断：
- 这条链不是 stub。
- 但 direct RPC 输入面明显缩水。`internal/module/turn/rpc_types.go:5-12` 只有 `prompt/images/files/model/effort`，而 `dto.TurnRequest` 实际还能承载 `skills/manualSkillSelection/outputSchema/mcp`，V2 的入口面也更完整。

## 2. `turn/steer`

结论：❌ 能到 provider，但不是“运行中 turn 的追加输入”。

证据：
- RPC 入口存在：`internal/module/turn/rpc.go:49-58`。
- 参数面只有 `threadId + prompt`：`internal/module/turn/rpc_types.go:14-17`，没有 `expectedTurnId`。
- `SteerTurn` 的真实实现只是 `PrepareTurn(...Prompt...)` 后再 `StartTurn(...)`：`internal/module/turn/service.go:109-115`。
- 单测名字直接说明语义：`internal/module/turn/service_test.go:179-202` 的 `TestSteerTurnStartsPromptAsNewTurn`。
- 对照 V2，`turn/steer` 明确要求 `expectedTurnId`，并先做 active-turn 对齐：`go-agent-v2/internal/apiserver/methods_turn.go:68-82`，`go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:70-106`。

判断：
- prompt 会真实传到 provider。
- 但 V3 传递方式是“重新发一个新 turn/start”，不是 V2 的 active-turn steer。
- 所以从能力定义上看，这项仍然缺失。

## 3. `turn/interrupt`

结论：⚠️ provider 侧中断真实存在，但状态回收不是全局闭环。

证据：
- RPC 入口：`internal/module/turn/rpc.go:60-68`。
- service 层会先找线程上的 active tracked turn，再调用 `session.Interrupt(...)`，如果本地有 tracked handle 就进入 settle wait：`internal/module/turn/service.go:117-141`。
- tracker 会把状态置为 `interrupting`：`internal/module/turn/tracker.go:121-136`。
- Codex provider 的中断是远程 RPC：`internal/provider/codexapp/session.go:127-140`；真正 handle 完成依赖后续 `turn/completed` 或 `turn/aborted` 通知：`internal/provider/codexapp/session.go:231-279`。
- Claude provider 的中断是发 `SIGINT`，然后本地直接 `handle.finish(context.Canceled)` 并 dispatch `turn:interrupted`：`internal/provider/claudecli/session.go:205-226`。
- 两个 provider 都会把中断类原始事件翻译成 `turndto.TurnInterrupted`：`internal/provider/codexapp/event_map.go:86-90`、`internal/provider/claudecli/event_map.go:71-75`。
- orchestration 只订阅 `TurnStarted` 和 `TurnCompleted`，没有消费 `TurnInterrupted`：`internal/sidecar/orch/orchestration/module.go:33-44`。

判断：
- direct turn 路径下，本地 tracker 可以靠 handle settle 收敛。
- orchestration 路径下，若 provider 只发 `TurnInterrupted` 而不再发 `TurnCompleted`，agent 状态不会自动回到 `idle`。Claude 路径就是这种风险。

## 4. `turn/forceComplete`

结论：❌ 没有独立语义，只是 interrupt 的一个 source。

证据：
- RPC 入口存在：`internal/module/turn/rpc.go:70-75`。
- service 实现只是 `session.Interrupt(... Source: "force_complete")`：`internal/module/turn/service.go:143-159`。
- provider contract 里没有独立的 force-complete API，只有 `StartTurn` 和 `Interrupt`：`internal/contract/provider.go:23-37`。
- 单测明确验证“最终状态留给 watcher”，并且最后期待的是 `interrupted`，不是 `completed`：`internal/module/turn/service_test.go:204-245`。
- 对照 V2，`turnForceComplete` 会额外 `notifyTurnCompleted(..., "completed", "force_complete")`：`go-agent-v2/legacy-agentsdk/service/interrupt/turn_interrupt_core.go:271-304`。

判断：
- V3 的 `forceComplete` 和普通 interrupt 的主要差别只剩下 `Source` 字符串。
- 状态机层面没有独立 force-complete 终态。
- 与正常完成的区别没有被模型化；和 V2 不等价。

## 5. `TurnCompleted` 事件闭环

结论：⚠️ provider -> event bus -> orchestration 的链条存在，但只对 orchestration 队列 turn 真正闭环。

证据：
- provider 会产出 `TurnCompleted`：`internal/provider/codexapp/event_map.go:80-85`、`internal/provider/claudecli/event_map.go:76-81`。
- typed event 会被统一 dispatcher 发布：`internal/provider/unified/event_map.go:43-64`。
- orchestration 模块订阅 `TurnCompleted` 后调用 `svc.CompleteTurn(...)`：`internal/sidecar/orch/orchestration/module.go:38-44`。
- `CompleteTurn` 会校验 `activeTurnID`，触发状态机转换，并在成功后清空 `activeTurnID`：`internal/sidecar/orch/orchestration/service.go:326-352`。
- 但 `activeTurnID` 只会在 orchestration queue 路径里预先设入：`internal/sidecar/orch/orchestration/service.go:291-324`。
- `TurnStarted` 订阅里的 `BindActiveTurnID(...)` 只在 `agent.activeTurnID` 已经非空时才会绑定 provider turn id，否则直接返回 `errTurnNotActive`：`internal/sidecar/orch/orchestration/helpers.go:77-98`。
- module 层把 `errTurnNotActive` 当作可忽略错误：`internal/sidecar/orch/orchestration/module.go:34-43`。

判断：
- 通过 `SubmitTurn -> claimTurnWork -> startTurnExecution` 发起的 turn，`TurnCompleted` 闭环是通的。
- 直接 `turn/start` RPC 绕过 orchestration queue，不会先设置 `activeTurnID`，因此 `TurnStarted/TurnCompleted` 事件到 orchestration 后会被静默忽略。

## 6. skill 注入

结论：⚠️ service/provider 注入真实存在，但 direct RPC 暴露不完整。

证据：
- `PrepareTurn` 里真实做了 `skills.Resolve(...)`：`internal/module/turn/service.go:63-72`，具体实现见 `internal/module/turn/skills.go:11-37`。
- manifest 也在同一步构建：`internal/module/turn/manifest.go:17-24`。
- Codex provider 会同时传 `selectedSkills`，并把 skill prompt 注入首个 text input：`internal/provider/codexapp/session.go:327-349`、`internal/provider/codexapp/skill_prompt.go:9-26`。
- Claude provider 会把 skills 列表和 skill prompt 拼进 turn text：`internal/provider/claudecli/session.go:174-180`、`internal/provider/claudecli/skill_prompt.go:9-46`。
- orchestration 提交路径能保留 `SelectedSkills / ManualSkillSelection / OutputSchema`：`internal/module/turn/orchestration_starter.go:54-62`。
- 但 direct `turn/start` 的 RPC 参数没有 `selectedSkills/manualSkillSelection/outputSchema`，`buildPrepareInput` 也没有填这些字段：`internal/module/turn/rpc_types.go:5-12`、`internal/module/turn/rpc_helpers.go:5-14`。

判断：
- skill 注入本身不是假的。
- 但 direct RPC 入口无法把这部分能力完整喂给 `PrepareTurn`，因此“turn 执行时 skill 注入”只在 service/orchestration 面完整成立，在对外 RPC 面不成立。

## 7. approval 集成

结论：❌ 只有局部链路，缺统一事件交付与状态机闭环。

证据：
- `approval/respond` RPC 已接到 `ApprovalManager.Respond(...)`：`internal/module/turn/rpc.go:82-93`、`internal/platform/rpc/approval.go:108-117`。
- Codex provider 在收到 tool approval 请求时，会调 `ApprovalManager.RequestApproval(...)`，待决策后再调用 provider 侧 `approval/respond`：`internal/provider/codexapp/session_approval.go:26-36,66-82`。
- 但这里调用 `RequestApproval` 时传入的是 `bridge=nil, server=nil`：`internal/provider/codexapp/session_approval.go:31`，所以 `ApprovalManager.publishRequested(...)` 的统一 push/callback 分支不会生效：`internal/platform/rpc/approval.go:154-169`、`internal/platform/rpc/approval_events.go:20-29`。
- 虽然 Codex raw event 仍会被 translator 变成 `ToolApprovalRequested`：`internal/provider/codexapp/event_map.go:132-144`，但通用 RPC push 只桥 `StateChanged / TurnStarted / TurnCompleted`：`internal/platform/rpc/push.go:75-92`。
- Wails bridge 也只桥 `StateChanged / TurnStarted / TurnCompleted`：`internal/ui/wails/bridge.go:53-63`。
- LSP 搜索没有找到 `ToolApprovalRequested` / `ToolApprovalResolved` 的业务订阅者；现有消费者只看到了 log sink：`internal/platform/bus/sink.go:61-66`。
- `awaiting_user_input` 的状态机 trigger 只有定义，没有任何调用点：`internal/dto/agent/state.go:96-101`，`TriggerUserInputRequested/Resolved` 在生产代码中没有引用。
- Claude provider 只有 approval policy 配置，没有 V3 统一 approval request 流：`internal/provider/claudecli/config.go:33-41`。

判断：
- 后端“回应一个已经 pending 的 approval”这件事是能做的。
- 但“turn 执行中遇到需审批 tool call -> 前端收到 -> 状态机进入 awaiting -> 用户回应 -> provider 继续”这条统一链路，在 V3 里并没有完整落地。

## 8. 错误处理

结论：⚠️ 同步错误有基本收敛，异步异常对 orchestration 收敛不足。

证据：
- `session.StartTurn(...)` 同步报错时，turn tracker 会立即 `Complete(..., false, err)`：`internal/module/turn/service.go:92-100`。
- orchestration 的启动失败路径会清掉 `activeTurnID`，并用 `TriggerTurnCompleted` 把状态拉回去：`internal/sidecar/orch/orchestration/helpers.go:176-192`。
- Codex provider 的 `StartTurn`/`Interrupt` 都有显式 RPC timeout：`internal/provider/codexapp/session.go:109-113`、`136-139`。
- Codex `connection.dead` 会先 `failTurns(...)`，再尝试 recovery：`internal/provider/codexapp/session_recovery.go:23-30`。
- Claude read loop 遇到 transport error 时，会 finish handle 并 dispatch 一个失败的 `turn:complete`：`internal/provider/claudecli/session_events.go:61-82`。
- 本地 watcher 还有一个 30 分钟 TTL，超时后 tracker 记为 `stalled`：`internal/module/turn/service.go:184-201`、`internal/module/turn/tracker.go:11,138-153`。
- 但 orchestration 只消费 `TurnCompleted`，没有消费 `TurnInterrupted/TurnStalled`；因此卡死、断连、interrupt-only 场景不会统一回收 agent 状态：`internal/sidecar/orch/orchestration/module.go:33-44`。

判断：
- “当前 turn handle 失败/超时”在 turn service 内部通常能收敛。
- “agent orchestration state 是否同步收敛”则不稳定，尤其是断连、stall、仅中断无 completed 事件的场景。

## 9. 并发 turn

结论：⚠️ orchestration 路径有串行保证，direct RPC 没有统一保证。

证据：
- orchestration 提交是排队的：`internal/sidecar/orch/orchestration/service.go:166-187`。
- `claimTurnWork(...)` 只会在 `StateTurnQueued` 的 agent 上 dequeue 一条 submission，并设置唯一 `activeTurnID`：`internal/sidecar/orch/orchestration/service.go:291-324`。
- turn service 自己的 `StartTurn(...)` 没有做同线程互斥检查：`internal/module/turn/service.go:76-106`。
- Claude session 会拒绝已有未完成 turn：`internal/provider/claudecli/session.go:120-127`。
- Codex session 没有对应保护；它只是发远程 `turn/start` 并把 handle 放进 `map[providerID]`：`internal/provider/codexapp/session.go:104-125`。

判断：
- 若走 orchestration queue，同一 agent 同时只会有一个 active turn。
- 若直接打 `turn/start` RPC，同一 thread 的并发行为是 provider-specific：Claude 会拒绝，Codex 路径在 V3 内部没有统一 guard。

## 10. tracker 正确性

结论：⚠️ 能追本地 handle，但不是 provider 真相源。

证据：
- tracker 的状态转换只围绕本地 handle 和调用路径：`internal/module/turn/tracker.go:34-118`。
- `watchTurn(...)` 只监听 `handle.Done()` 和 `handle.Err()`，不消费 provider typed event：`internal/module/turn/service.go:173-203`。
- `GetByProviderID(...)` 存在，但 LSP `references` 返回空，生产代码没人用：`internal/module/turn/tracker.go:204-217`。
- `InterruptTurn(...)` 只有在 `ActiveByThread(...)` 找到 tracked turn 时才会把 tracker 置为 `interrupting` 并等待 settle；否则只发 provider interrupt，不更新本地状态：`internal/module/turn/service.go:129-141`。
- `ForceCompleteTurn(...)` 甚至不会 `MarkInterruptRequested`，也不会等待 settle：`internal/module/turn/service.go:143-159`。

判断：
- tracker 适合作为本地、短生命周期的进度视图。
- 它没有和 provider 事件流、orchestration 状态机形成双向校验，所以不能当作 execution truth source。

## 11. V2 等价性

结论：❌ 仍有结构性差距。

主要差距：
- `turn/start` RPC 参数与返回不等价。
- V2 `turnStartParams` 有 `input[] / selectedSkills / manualSkillSelection / cwd / outputSchema`，返回 `{turn:{id,status}}`：`go-agent-v2/internal/apiserver/methods_turn.go:30-66`。
- V3 `turnStartParams` 只有 `prompt/images/files/model/effort`，返回只有 `{turnId}`：`internal/module/turn/rpc_types.go:5-12,39-41`、`internal/module/turn/rpc.go:45`。
- `turn/steer` 不等价。
- V2 强制要求 `expectedTurnId` 并校验 active tracked turn：`go-agent-v2/internal/apiserver/methods_turn.go:68-82`、`go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:70-106`。
- V3 没有 `expectedTurnId`，只是新开 turn：`internal/module/turn/rpc_types.go:14-17`、`internal/module/turn/service.go:109-115`。
- `turn/interrupt` 不等价。
- V2 有 `waitInterruptOutcome`、`notifyTurnCompleted`、tracked-turn terminal wait：`go-agent-v2/legacy-agentsdk/service/interrupt/turn_interrupt_core.go:56-126,215-269`。
- V3 只有本地 handle settle；没有补发 `TurnCompleted`，也没有 `interrupt_no_active_turn` 之类补偿语义：`internal/module/turn/service.go:117-141`。
- `turn/forceComplete` 不等价。
- V2 有独立 `TurnForceComplete`，且会通知 completion：`go-agent-v2/legacy-agentsdk/service/interrupt/turn_interrupt_core.go:271-304`。
- V3 只是 `Interrupt(Source="force_complete")`：`internal/module/turn/service.go:143-159`。
- approval 不等价。
- V2 有统一 notify hook / pending request / `approval/respond` 解锁链：`go-agent-v2/internal/apiserver/server.go:233-238`、`go-agent-v2/internal/apiserver/server_event_handler.go:221-246`、`go-agent-v2/internal/apiserver/server_approval.go:462-483`。
- V3 只有 codex session 内部 `RequestApproval -> approval/respond` 回写，缺统一 push/UI/state machine。
- tracker/stall 不等价。
- V2 有更丰富的 tracker/stall/auto-interrupt 体系：`go-agent-v2/legacy-agentsdk/service/tracker/*`、`go-agent-v2/legacy-agentsdk/service/support/interrupt_state.go`。
- V3 只有一个轻量 in-memory tracker：`internal/module/turn/tracker.go`。

## 12. `BinaryDir`

结论：✅ 代码和测试都表明已修。

证据：
- turn service 启动时用 `resolveBinaryDir()` 初始化 manifest builder，默认取 `os.Executable()` 所在目录：`internal/module/turn/service.go:30-45`。
- manifest 构建时会把 `BinaryDir` 和 MCP binary 名称做 `filepath.Join`：`internal/module/turn/manifest.go:17-24`、`internal/dto/provider/manifest.go:30-44`。
- 单测覆盖了默认 executable dir 和显式 override 两条路径：`internal/module/turn/service_test.go:99-134`。
- Claude session 启动侧也用了同类逻辑，优先 `binary_dir/binaryDir` 配置，否则退回 `os.Executable()` 或 `LookPath(...)`：`internal/provider/claudecli/config.go:20-31`。
- Claude transport 最终会把 manifest 里的绝对命令路径写进 MCP config：`internal/provider/claudecli/transport_config.go:164-216`。

判断：
- “manifest 里 MCP server 路径拼错”这个问题，从当前代码和测试看已经修掉。
- 但要注意：direct `turn/start` RPC 并不暴露 `BinaryDir` 参数；显式 override 目前只能从 service 级调用者传入，RPC 面拿不到。

## 额外风险

- 推导风险：`CompleteTurn(success=false)` 在 `StateTurnStarting` 上会走 `TriggerTurnAborted`，而状态机只允许 `TriggerTurnCompleted` 从 `turn_starting -> idle`，不允许 `turn_aborted` 从 `turn_starting` 触发：`internal/dto/agent/state.go:89-95`、`internal/sidecar/orch/orchestration/service.go:341-348`。如果 provider 很快发出失败完成事件，可能触发非法状态迁移。
- 推导风险：直接 `turn/start` RPC 发起的 turn 不会先设置 orchestration `activeTurnID`，因此即使 provider 后续发了 `TurnStarted/TurnCompleted`，orchestration 也不会把它当成当前 active turn。

## 最终判断

- V3 的 Turn 执行层已经不是空骨架，`turn/start`、provider session、manifest、基础 tracker 都已落地。
- 但从“能力 + 容错 + V2 等价”看，当前仍有 4 个硬缺口：`turn/steer` 语义错误、`turn/forceComplete` 只是 interrupt 别名、approval 闭环不完整、`TurnCompleted` 只对 orchestration 队列 turn 闭环。
- 如果目标是宣称 “Turn 执行层迁移完成”，当前代码还不够。

## 互审

### 1. 对 `docs/plans/迁移/cap-thread-lifecycle.md` 的批判

1. 报告把“没有 turn/history 级联清理”当成 `thread/delete` 的缺口，口径并不准确。当前 V3 `ReadHistory/ReadMessages` 直接走 `session.ReadHistory(...)`，见 `internal/module/thread/history.go:13-45`；LSP 在 `internal/store` 下搜索 `ReadHistory`、`history`、`package turn` 都没有命中，而 `DeleteByThreadID` 只存在于 `internal/store/thread/*`。也就是说，这里更像“V3 根本没有本地 turn/history store 可级联”，不是 delete 路径漏删了本地数据。
2. 并发安全一节把“同一个 `agentID` 上并发 `Start/Resume`”列为典型竞态，但少了重要前提。`Start` 在 `AgentID` 为空时会自动生成新 ID，见 `internal/module/thread/lifecycle.go:171-187`；因此这不是默认路径，而是 caller 显式复用 `agentID` 才会触发的 opt-in collision。报告把它直接写成一般性竞态，严重度论证偏松。
3. `thread/resume` 小节把“history 加载没有”抬成标题问题，但没追到更硬的 correctness bug：Claude fresh start 先用 `agentID` 作为 placeholder `threadID` 初始化 session，见 `internal/provider/claudecli/driver.go:102-118`；真实 ID 只在 `system:init` 后回写 session 内存，见 `internal/provider/claudecli/session_events.go:46-59`；而 thread 层早在 `Start` 返回前就把当时的 `session.ThreadID()` 持久化了，见 `internal/module/thread/lifecycle.go:57-75`。LSP 搜索这份报告没有 `placeholder` 相关结论，说明它把次要现象写成标题，却漏掉了更关键的恢复前提错误。

### 2. 对 `docs/plans/迁移/cap-orchestration-agent.md` 的批判

1. 首先是 artifact 本身不可复核：对 `docs/plans/迁移/cap-orchestration-agent.md` 做 LSP `read_file` 直接得到 `path not found`，全仓对 `cap-orchestration-agent` / `cap-orchestration-agent.md` 的 `text_search` 也都是空结果。一个无法定位的报告，本身就不满足可审计性。
2. 报告集的 traceability 也坏了。仓库里自己的复核链引用的是 `docs/plans/迁移/review-module-orch.md`，而不是用户指定的 `cap-orchestration-agent.md`，见 `docs/plans/迁移/final-verdict-2.md:3-7`。这说明 orchestration 审查 artifact 的命名和索引已经漂移，后续交叉引用不稳定。
3. 如果这份路径实际意图指向 `docs/plans/迁移/review-module-orch.md`，那其中头号结论已经过时。该文断言 `claimTurnWork` 后只剩 `agentID/threadID/turnID`，`startTurnExecution` 也不做 session/provider 调用，见 `docs/plans/迁移/review-module-orch.md:13,49-52`。当前代码里 `turnWork` 明确保留完整 `submission`，见 `internal/sidecar/orch/orchestration/service.go:73-78`；`claimTurnWork` 也把 `submission` 带入工作项，见 `internal/sidecar/orch/orchestration/service.go:291-324`；`startTurnExecution` 会真实调用 `turnStarter.StartTurn(ctx, work.submission)`，见 `internal/sidecar/orch/orchestration/helpers.go:140-150`；而 `orchestrationTurnStarter` 已把 `Inputs/SelectedSkills/ManualSkillSelection/OutputSchema` 送进 `PrepareTurn -> StartTurn`，见 `internal/module/turn/orchestration_starter.go:22-63`。所以如果它是第二份报告的实际替身，至少这条主判断已经失效。

### 3. 对 `docs/plans/迁移/cap-provider-session.md` 的批判

1. 报告把 stale session 风险主要收束在 `Recover`，影响面判轻了。`Archive/Delete` 确实只 `Close` 不 `Remove`，见 `internal/module/thread/archive.go:5-13`、`internal/module/thread/service.go:102-119,228-241`；而 `SessionManager.Get` 没有任何 health check，见 `internal/provider/unified/session.go:48-57`。因此不只是 `Recover`，`thread/command` / `thread/messages` 的 `resolveSession(...)`，以及 direct `turn/start` 的 `SessionResolver`，都可能拿到一个已关闭 session，见 `internal/module/thread/command.go:14-43`、`internal/module/thread/history.go:13-45`、`internal/module/turn/rpc.go:20-29`、`internal/provider/unified/session_resolver.go:23-46`。这比“recover 会误判可复用 session”更广。
2. 报告没有把 `SessionManager.Register` 的替换语义列为一级风险。当前只要同一 `agentID` 再注册新 session，旧 session 就会被 `ForceStop()`，见 `internal/provider/unified/session.go:30-46`。这直接把 provider session 生命周期绑死到 `agentID` 复用策略上；但它没有进入 summary 的前 6 个问题，见 `docs/plans/迁移/cap-provider-session.md:10-18`，全文 LSP 搜索 `ForceStop` 也没有命中，说明这一条被漏判了。
3. Codex recovery 一节没追到最直接的执行语义破坏：`handleConnectionDead(...)` 在任何恢复尝试之前先 `s.failTurns(...)`，见 `internal/provider/codexapp/recovery.go:60-67`。这意味着即便后续 `attemptRecovery(...)` 成功，所有 in-flight turn 也已经被本地判死。全文 LSP 搜索 `failTurns` 没有命中，说明报告把“本地 app-server 僵尸窗口”和“未做 initialize/resume”写出来了，却漏掉了对用户更可见的 turn 丢失语义。
