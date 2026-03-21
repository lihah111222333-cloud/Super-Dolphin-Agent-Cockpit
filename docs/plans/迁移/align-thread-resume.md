# V2↔V3 1:1 对齐：`thread/resume` + `thread/fork`

## 范围与口径

- 只用 LSP 读代码。
- V2 外层 RPC 对照：`go-agent-v2/internal/apiserver/methods_thread.go:224-249`。
- V2 provider / recovery 对照：`go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:83-107`、`go-agent-v2/pkg/agentsdk/codex/client_appserver_protocol.go:371-428`、`go-agent-v2/pkg/agentsdk/codex/client_appserver_transport.go:276-516`。
- V3 对应实现：`internal/module/thread/rpc.go:33-35,166-179`、`internal/module/thread/rpc_types.go:18-21`、`internal/module/thread/lifecycle.go:77-135,221-229,238-270`、`internal/provider/codexapp/driver.go:96-157`、`internal/provider/codexapp/session.go:174-190`、`internal/provider/codexapp/recovery.go:36-100`。
- 本文同时看两层：
  - RPC 契约是否 1:1。
  - 底层恢复/分叉链路是否等价。

## 总览

| 项目 | 结论 | 结论摘要 |
| --- | --- | --- |
| session 恢复 | ⚠️ | V3 有恢复能力，但从 V2 的“已有 process 上 resume provider thread”改成了“`launchAgent` + `ResumeSession`”；请求面也从 `threadId/path/cwd/model` 收窄成依赖持久化 `provider/agentID/thread meta`。 |
| history 加载 | ❌ | V2 有独立的 provider rollout + runtime hydration 历史装载链路；V3 `Resume/Fork` 主链路不加载 history，独立 `ReadHistory` 也只读本地 rollout，拿不到时直接回空。 |
| provider reconnect | ❌ | V2 reconnect / respawn 后会 `Initialize()`，respawn 还会 `ResumeThread()`，并带 auto-continue；V3 只做 websocket reconnect + readLoop 重启，没有显式 `initialize` / `thread/resume`。 |
| fork 语义 | ⚠️ | V3 codexapp provider 真正调用 remote `thread/fork`，但 module RPC 的请求/响应形状已经不再是 V2；`turnIndex` 也不再接受。 |
| 状态恢复 | ⚠️ | V3 resume 会回写 thread/binding store；fork 只写 threadStore + 内存映射，不写 binding。V2 lifecycle 层持久化较少，但 reconnect 时对会话状态和 active turn 的保护更完整。 |

## `thread/resume`

| 维度 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| session 恢复 | `threadResumeTyped` 把 `threadId/path/cwd/model` 传给 `providerAdapter.ThreadResume(...)`；adapter 再走 `withProcess -> ResolveProcess(manager.GetProcess)`，要求先拿到已有进程，然后 `RunThreadResume` 基于 binding/status/history 解析 provider thread candidates 并在该进程上调用 `ResumeThread`。见 `go-agent-v2/internal/apiserver/methods_thread.go:237-249`、`go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:83-98,182-195`、`go-agent-v2/pkg/agentsdk/service/runtime/turn_runtime_operations.go:162-177`、`go-agent-v2/pkg/agentsdk/service/lifecycle/thread_lifecycle_logic.go:107-129`。 | `newResumeHandler` 先 `svc.Get(threadID)` 取 `AgentID`，再调用 `svc.Resume`；`Resume` 会先 `launchAgent`，然后 `ResumeSession`，最后把 thread state/binding 再写回 store。codexapp driver 的 `ResumeSession` 会先 `initialize`，再调 remote `thread/resume`。见 `internal/module/thread/rpc.go:166-179`、`internal/module/thread/lifecycle.go:77-107,221-229,238-270`、`internal/provider/codexapp/driver.go:96-157`。 | ⚠️ |
| history 加载 | `thread/resume` 本身不直接拉 history，但 V2 有完整独立历史链路：`ThreadMessages` 会从 provider rollout 读全量 history，再与 runtime timeline 做 hydration / page streaming。见 `go-agent-v2/internal/apiserver/codexadapter/thread_messages.go:77-189`。 | `Resume` 主链路不触发 `ReadHistory`；独立 history 路径只是 `session.ReadHistory -> rolloutReader.ReadHistory`，优先本地 rollout，拿不到就记录 `remote history API unavailable; returning empty history` 并返回空数组。见 `internal/module/thread/history.go:13-20`、`internal/provider/codexapp/session_history.go:13-25`、`internal/provider/codexapp/history.go:21-30`。 | ❌ |
| provider reconnect | 单次 WS reconnect 成功后会 `Initialize()`，再 `ensureListenerIfNeeded`；respawn recovery 则是 `restartProcessAndReconnect -> Initialize() -> ResumeThread(...)`，并在恢复后落到 idle 时自动补一条 continue prompt。见 `go-agent-v2/pkg/agentsdk/codex/client_appserver_transport.go:276-368,421-516`、`go-agent-v2/pkg/agentsdk/codex/client_appserver_protocol.go:167-212,371-424`、`go-agent-v2/pkg/agentsdk/codex/client_appserver_events.go:301-315,417-460`。 | `callTransport` 出错后只会触发 `attemptRecovery`；恢复链路是 `Reconnect() -> transport.reconnect() -> waitReadLoopStopped() -> startReadLoop()`。`transport.reconnect()` 只 close socket / spawn local / connect，没有 `initialize`、没有 `thread/resume`、没有 auto-continue。见 `internal/provider/codexapp/recovery.go:36-100`、`internal/provider/codexapp/transport.go:141-152`。 | ❌ |
| 状态恢复 | resume candidate 解析会利用 binding/status/history；`ResumeThread` 还会在 RPC 前先把 `threadID` 预绑定，失败再回滚旧绑定，避免事件窗口错绑。见 `go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:158-195`、`go-agent-v2/pkg/agentsdk/codex/client_appserver_protocol.go:399-416`。 | V3 会把 `ThreadID/AgentID/Provider/CWD/Model/Prompt/CreatedAt` 回写到 `threadStore`，并更新 `bindingStore`。但它要求调用时必须补齐 `provider`，且 `AgentID` 来自已有 thread 记录；记录缺失时无法 resume。见 `internal/module/thread/lifecycle.go:83-106,189-202,238-270`。 | ⚠️ |
| RPC 契约 | 请求是 `threadId/path/cwd/model`，响应是 `{"thread":{"id","status"},"model":...}`。见 `go-agent-v2/internal/apiserver/methods_thread.go:237-249`。 | 请求只收 `threadId/provider`；`provider` 在 service 内又被强制必填。handler 返回值是 `nil`，所以 module 层响应是 `null`；codexapp driver 只能靠 `decodeThreadID(..., fallback=req.ThreadID)` 吃掉这个差异。见 `internal/module/thread/rpc_types.go:18-21`、`internal/module/thread/rpc.go:166-179`、`internal/module/thread/lifecycle.go:189-202`、`internal/provider/codexapp/driver.go:146-169`。 | ❌ |

## `thread/fork`

| 维度 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| fork 语义 | RPC 层收 `threadId` 和 `turnIndex`，但 handler 实际只把 `ThreadID` 传给 `providerAdapter.ThreadFork(...)`，`turnIndex` 没有下传。provider lifecycle 语义是 `ForkThreadRequest{SourceThreadID: sourceThreadID}`，返回 `{ThreadID, ForkedFrom}`。见 `go-agent-v2/internal/apiserver/methods_thread.go:224-235`、`go-agent-v2/pkg/agentsdk/service/lifecycle/thread_lifecycle_logic.go:131-141`、`go-agent-v2/pkg/agentsdk/agentcore/types.go:200-206`。 | module 层只收 `threadId`；`svc.Fork` 通过现有 session 调 `session.ForkThread(ctx, dto.ForkRequest{ThreadID: ...})`，codexapp session 会真正发 remote `thread/fork`。但 module 返回的是 `ForkResult{NewThreadID}`，不是 V2 的 `thread{id,forkedFrom}`。见 `internal/module/thread/rpc.go:33-35`、`internal/module/thread/lifecycle.go:108-135`、`internal/provider/codexapp/session.go:174-190`、`internal/module/thread/contract.go:51-53`。 | ⚠️ |
| provider 支持 | 对 codex app-server transport 而言，`AppServerClient.ForkThread` 直接返回 `fork not supported in app-server mode`。也就是说 V2 外层 RPC 有 fork，但 codex transport 自身并不真支持。见 `go-agent-v2/pkg/agentsdk/codex/client_appserver_protocol.go:426-428`。 | V3 codexapp session 已经直接调用 remote `thread/fork`，provider 侧比 V2 强。见 `internal/provider/codexapp/session.go:174-190`。 | ⚠️ |
| history 加载 | fork 主链路不加载 history；V2 仍依赖独立 `thread/messages` 做 provider rollout + runtime hydration。见 `go-agent-v2/internal/apiserver/codexadapter/thread_messages.go:77-189`。 | fork 主链路同样不加载 history；后续读取仍落到本地 rollout / 空数组的简化 history backend。见 `internal/module/thread/history.go:13-20`、`internal/provider/codexapp/history.go:21-30`。 | ❌ |
| 状态恢复 | V2 fork lifecycle 本身只返回结果，不写 binding/thread store。见 `go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:100-107`。 | V3 fork 会写 `threadStore`，并记录 `OwnerThreadID`；但 `persistThreadState(..., updateBinding=false)` 明确不更新 bindingStore，只靠 `rememberThreadAgent(newThreadID, agentID)` 和既有 agent binding 做后续解析。见 `internal/module/thread/lifecycle.go:117-133,238-260,372-381`、`internal/module/thread/service.go:183-225,258-273`。 | ⚠️ |
| RPC 契约 | 响应是 `{"thread":{"id","forkedFrom"}}`。见 `go-agent-v2/internal/apiserver/methods_thread.go:229-235`。 | module 直接返回 `ForkResult{NewThreadID}`；该 struct 没有 JSON tag，不是 V2 envelope。见 `internal/module/thread/rpc.go:33-35`、`internal/module/thread/contract.go:51-53`。 | ❌ |

## 结论

- `thread/resume` 不是 1:1 对齐。V3 保留了“恢复 thread”的高层能力，但恢复前提、请求面、返回面、reconnect 策略都已经和 V2 不同。
- `thread/fork` 也不是 1:1 对齐。V3 在 codexapp provider 侧反而更强，但 module RPC 契约和 V2 不兼容，状态持久化方式也不同。
- 最硬的对齐缺口有三个：
  - V3 `thread/resume` 的 RPC 形状不兼容 V2，module 返回 `null`。
  - V3 reconnect 没有对齐 V2 的 `Initialize + ResumeThread + auto-continue`。
  - V3 history backend 远弱于 V2 的 provider rollout + runtime hydration。
