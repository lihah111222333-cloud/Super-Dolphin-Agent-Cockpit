# V2↔V3 1:1 对齐：Provider Session 生命周期

## 范围与方法
- 只读对齐，代码读取仅使用 LSP：`text_search`、`document_symbol`、`read_file`。
- V2 基线不是单独的 `SessionManager`；等价实现是 `go-agent-v2/internal/runner/AgentManager` 持有的 `agents map[id]*AgentProcess`，外加 provider `Client` 自身的进程 / 连接 / resume 逻辑。
- V3 对齐对象是 `internal/provider/unified/SessionManager`，以及它在 `thread` / `turn` / `orchestration` / provider driver 中的实际调用链。
- 本文只比较你指定的 5 个维度：
  - `Create / Get / Remove(含 Close) / CloseAll`
  - `SessionResolver` 解析链
  - `claudecli` 进程管理
  - `codexapp` 连接管理 + recovery
  - stale session 防护

## 总览

| 维度 | V2 基线 | V3 现状 | 结论 |
| --- | --- | --- | --- |
| `Create / Get / Remove / CloseAll` | `AgentManager.Launch/Get/Stop/StopAll/KillAll` 闭环完整 | `SessionManager.Register/Get/Remove/CloseAll` 方法齐全，但 `thread/archive` / `thread/delete` / `thread.Recover` 没有统一走 `Remove` | ⚠️ |
| `SessionResolver` 解析链 | 运行态解析会复用 live process；缺失时还能走 binding / `session_uuid` / status 候选并自动 restore+resume | turn RPC 只走 `threadStore -> agentID -> SessionManager.Get`，不接 binding / `session_uuid` / historical restore | ❌ |
| `claudecli` 进程管理 | spawn / `--resume` / `Shutdown` / `Kill` / restart / session id 捕获完整 | V3 也有 `--resume`、进程组管理、等待真实 thread id、重启复用 | ✅ |
| `codexapp` 连接管理 + recovery | websocket reconnect + health/circuit breaker + respawn + `initialize` + `ResumeThread` + auto-continue | 只有 reconnect + health loop；本地 app-server 重新拉起后不补 `initialize` / `thread/resume` | ❌ |
| stale session 防护 | 会识别 dead process / stale active state / 错会话事件，并拒绝对已绑定线程偷偷开 fresh session | `SessionManager.Get` 盲取 map；关闭后的 session 还能留在 manager 里，被 `Recover` 误判为可复用 | ❌ |

## 1. `Create / Get / Remove(含 Close) / CloseAll`

### V2 基线
- 创建入口是 `AgentManager.Launch`。`prepareLaunchProcess` 先分配 port、创建 provider client、放入 `m.agents`；如果 spawn 失败，`rollbackFailedLaunch` 会把 map 项删掉，避免留下半初始化对象。证据：
  - `go-agent-v2/internal/runner/manager_launch.go:156-190`
  - `go-agent-v2/internal/runner/manager_launch.go:253-266`
  - `go-agent-v2/internal/runner/manager_launch.go:268-333`
- 读取入口是 `AgentManager.Get` / `GetProcess`，直接从 `m.agents` 取。证据：
  - `go-agent-v2/internal/runner/manager.go:359-365`
- 删除入口是 `AgentManager.Stop`。它先 `Client.Shutdown()`，失败再 `Kill()`，随后才从 `m.agents` 删除，并补发 active submission 的 synthetic abort。证据：
  - `go-agent-v2/internal/runner/manager_lifecycle.go:19-92`
- 批量关闭是 `StopAll`，并行 stop；必要时还有 `KillAll` 兜底。证据：
  - `go-agent-v2/internal/runner/manager_lifecycle.go:94-137`

### V3 现状
- 创建入口是 `thread.Start/Resume -> unified.Client.StartSession/ResumeSession -> SessionManager.Register`。`SessionManager` 本身没有 `Create`，只做 register。证据：
  - `internal/module/thread/lifecycle.go:44-107`
  - `internal/provider/unified/client.go:29-67`
  - `internal/provider/unified/session.go:31-47`
- 读取入口是 `SessionManager.Get`，按 `agentID` 直接读 map。证据：
  - `internal/provider/unified/session.go:49-58`
  - `internal/provider/unified/session_adapter.go:30-39`
- 删除入口在 manager 层是 `Remove`：先删 map，再用 `closeSession(...)` 调 `session.Close(ctx)`，失败后 `ForceStop()`。批量关闭由 `CloseAll` + `fx OnStop` 完成。证据：
  - `internal/provider/unified/session.go:60-116`
  - `internal/provider/unified/module.go:33-43`
- orchestration 的 stop 路径会调用 `RemoveSession(agentID)`，这部分是闭合的。证据：
  - `internal/sidecar/orch/orchestration/service.go:104-153`
  - `internal/provider/unified/session_adapter.go:34-39`

### 未对齐点
- `thread/archive` 和 `thread/delete` 没有走 `SessionManager.Remove`，而是直接 `session.Close(ctx)`；关闭后的对象仍可能残留在 manager map 中。证据：
  - `internal/module/thread/archive.go:5-13`
  - `internal/module/thread/service.go:102-119`
  - `internal/module/thread/service.go:228-241`
- `thread.Recover` 先尝试 orchestration recover；之后只要 `lookupSession(agentID)` 成功，就不会再 `ResumeSession`。如果 map 里残留的是已关闭 session，这里会直接把 stale object 误判成活 session。证据：
  - `internal/module/thread/lifecycle.go:137-169`
  - `internal/module/thread/lifecycle.go:231-236`

### 判断
- `⚠️`
- 原因不是 API 缺失，而是 V3 只有 manager 方法面做到了对齐，线程归档 / 删除 / recover 没有统一收口到 manager remove，生命周期没有像 V2 一样形成真正闭环。

## 2. `SessionResolver` 解析链

### V2 基线
- V2 turn 主链不是“线程 id 直接查 session map”，而是 `TurnStartSubmissionAndTrack -> EnsureThreadReadyForTurn`。如果 live process 还活着就直接复用；如果进程死了会先 stop，再决定是否 restore 历史线程。证据：
  - `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_operations.go:127-159`
  - `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_logic.go:138-164`
- 恢复候选链是分层的：
  - 先从 binding 取 `SessionUUID`
  - 没有再取 `ProviderThreadID / LegacyThreadID`
  - 还可以再从 status 取 `SessionID`
  - 最终形成 candidate 列表用于 native resume 或 `ResumeThread`
  证据：
  - `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_logic.go:211-257`
  - `go-agent-v2/legacy-agentsdk/service/history/thread_history_core.go:73-112`
  - `go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:162-195`
- Claude 的 `session_uuid` 不是死字段，V2 会在事件处理里把它修进 binding，供未来 resume 使用。证据：
  - `go-agent-v2/internal/apiserver/server_event_handler.go:320-339`
- 有了这些 candidate 之后，V2 会自动决定：
  - 是否 native spawn-time resume
  - 是否 launch 历史进程后再 `ResumeThread`
  - 如果对已绑定线程没有任何有效 resume candidate，就拒绝 fresh session，避免偷偷丢上下文
  证据：
  - `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_logic.go:259-417`

### V3 现状
- turn RPC 统一通过 `contract.SessionResolver` 取 session；`turn/start`、`turn/steer`、capability gate 都依赖这条链。证据：
  - `internal/module/turn/rpc.go:20-57`
  - `internal/platform/rpc/handler.go:22-39`
- 但 `sessionResolver.ResolveSession` 的实现非常短，只做：
  - `threadStore.GetByThreadID(threadID)`
  - 取 `ref.AgentID`
  - `SessionManager.Get(agentID)`
  证据：
  - `internal/provider/unified/session_resolver.go:23-46`
- V3 线程模块其实还有一条更丰富的 binding 解析链：`GetByAgentID -> threadAgents cache -> GetByProviderThread(codex|claude)`；但它只在 thread service 自己的命令 / history / archive / delete / recover 路径里使用，turn RPC 的 `SessionResolver` 并没有复用它。证据：
  - `internal/module/thread/service.go:183-226`
- V3 binding store 仍然保留了 `SessionUUID` 字段与 `UpdateSessionUUID` API：`internal/store/binding/store.go:64-100`。但本轮 LSP 搜索 `UpdateSessionUUID(` 只命中 contract/store 自身，未发现业务写入路径；也就是说，V2 用来喂恢复链的 `session_uuid` 在 V3 里没有形成运行闭环。

### 判断
- `❌`
- V3 `SessionResolver` 只够做“当前进程内、当前 `agentID` 还挂着 live session”的直查，不具备 V2 那种 binding-aware、`session_uuid` aware、historical restore aware 的解析能力，和 V2 不是 1:1。

## 3. `claudecli` 进程管理

### V2 基线
- `CLIClient.spawnWithResume` 负责 spawn；恢复时支持 `--resume <session_id>`。证据：
  - `go-agent-v2/legacy-agentsdk/claude/client.go:163-239`
- `Shutdown` 是 `SIGTERM -> 等待 -> 超时 SIGKILL`，`Kill` 直接强杀。证据：
  - `go-agent-v2/legacy-agentsdk/claude/client.go:414-466`
- `dispatchEvent` 会在 `session_configured` 时把真实 `threadID/sessionID` 写回 client，用于后续恢复和 restart。证据：
  - `go-agent-v2/legacy-agentsdk/claude/client.go:617-626`
- `RestartWithParams` / `SwitchModel` / `CompactContext` 都是 kill 后带 `session_id` resume。证据：
  - `go-agent-v2/legacy-agentsdk/claude/client.go:525-584`

### V3 现状
- `launchCLI` 同样支持 `--resume`；transport 进程是独立 process group，便于整组信号管理。证据：
  - `internal/provider/claudecli/transport_config.go:23-48`
  - `internal/provider/claudecli/transport.go:33-45`
- transport 的 `Close` / `Kill` 同样是 `SIGTERM -> wait -> SIGKILL` 与强杀分流。证据：
  - `internal/provider/claudecli/transport.go:98-120`
  - `internal/provider/claudecli/transport.go:165-190`
- driver 在返回 session 前会等待真实 thread id，不会像 V2 fresh start 那样长期依赖 placeholder。证据：
  - `internal/provider/claudecli/driver.go:87-142`
  - `internal/provider/claudecli/thread_identity.go:24-114`
  - `internal/provider/claudecli/session_events.go:46-77`
- turn 级配置变更会通过 `restartIfNeededLocked` 拉起新 CLI，并复用旧 thread id 做 resume。证据：
  - `internal/provider/claudecli/session.go:286-314`

### 判断
- `✅`
- 进程管理主体已经对齐：都有 spawn/resume、进程组级 stop/kill、真实 thread id 回写、restart 复用。
- 需要单独记一笔的是 `Close(ctx)` deadline 语义。V3 `session.Close(context.Context)` 最终并不 honor 调用方 ctx；这属于 close deadline 问题，不影响“Claude CLI 进程管理是否 1:1”这个维度本身。

## 4. `codexapp` 连接管理 + recovery

### V2 基线
- 启动链是 `Spawn -> dialWS -> Initialize -> ThreadStart / ResumeThread`。证据：
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_runtime.go:103-200`
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_transport.go:24-118`
- recovery 主体是 `reconnectWS(...)`：
  - websocket 指数退避重连
  - reconnect health 计数
  - circuit breaker / not-initialized 统计
  - background event 反馈
  - 必要时升级到 respawn
  证据：
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_transport.go:209-367`
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_health.go:1-184`
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go:141-231`
- respawn 路径不是简单重连，而是：
  - 杀老进程
  - 起新 app-server
  - 重建 ws
  - `Initialize`
  - `ResumeThread`
  - 必要时对落到 idle 的恢复场景自动 continue
  证据：
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_transport.go:370-540`
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go:334-560`
- 事件侧还有会话保护：收到 conversation mismatch 的 thread-scoped event 会丢弃，只在必要时回补 lifecycle。证据：
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go:277-331`

### V3 现状
- 启动链是 `newTransport -> connect -> initialize -> thread/start or thread/resume`。证据：
  - `internal/provider/codexapp/transport.go:63-77`
  - `internal/provider/codexapp/driver.go:78-157`
- session recovery 的主体是 `attemptRecovery`：
  - `transport.reconnect(...)`
  - 等旧 read loop 停止
  - 再起 read loop
  证据：
  - `internal/provider/codexapp/recovery.go:36-100`
- `transport.reconnect` 虽然会在本地模式下检测 `processRunning()` 并重新 `spawnLocal()`，但 recovery 路径没有跟进重新 `initialize`，也没有重新 `thread/resume`。证据：
  - `internal/provider/codexapp/transport.go:141-152`
  - `internal/provider/codexapp/transport.go:170-190`
  - `internal/provider/codexapp/recovery.go:36-100`
- V3 还缺了 V2 那些 recovery 护栏：
  - conversation mismatch 过滤
  - reconnect health / circuit breaker 指标
  - respawn 后 auto-continue
  - `rpc not initialized` 专门恢复分支

### 判断
- `❌`
- V3 有“连接重试”的形，但没有 V2 的“恢复语义”闭环。尤其是本地 app-server 异常退出后，V3 重新拉起进程并不能把旧线程上下文自动带回来，这和 V2 不在一个级别上。

## 5. stale session 防护

### V2 基线
- `effectiveState` 会在 provider 进程已经退出、但 UI 还停在 running/thinking 时主动把状态 reconcile 掉，避免长期挂假活跃。证据：
  - `go-agent-v2/internal/runner/manager.go:220-276`
- `EnsureThreadReadyForTurn` 遇到 dead process 会先 stop 掉旧对象，再走 restore/resume，不会把 dead client 当 live session 复用。证据：
  - `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_logic.go:154-164`
- 对已绑定线程，如果一个有效 resume candidate 都没有，V2 会拒绝 fresh session，防止上下文悄悄丢失。证据：
  - `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_logic.go:403-417`
- Codex event 侧还会丢弃错 conversation 的 thread-scoped event，避免 stale 事件把当前 turn lifecycle 污染掉。证据：
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go:277-299`

### V3 现状
- `SessionManager.Get` 只是 map lookup，没有任何 liveness / closed-state 校验。证据：
  - `internal/provider/unified/session.go:49-58`
- `thread/archive` / `thread/delete` 只 `session.Close(ctx)`，不 `Remove`；因此“已经关闭但仍挂在 manager 里”的 stale session 是可形成的。证据：
  - `internal/module/thread/archive.go:5-13`
  - `internal/module/thread/service.go:102-119`
  - `internal/module/thread/service.go:228-241`
- `thread.Recover` 只要 `lookupSession(agentID)` 成功，就不会再 `ResumeSession`。这意味着 stale session 一旦残留，recover 会短路在旧对象上。证据：
  - `internal/module/thread/lifecycle.go:137-169`
  - `internal/module/thread/lifecycle.go:231-236`
- `codexapp` recovery 失败时会 `failTurns(...)`，但不会主动把 session 从 `SessionManager` 中剔除；manager 侧也没有 stale invalidation。证据：
  - `internal/provider/codexapp/recovery.go:73-100`
  - `internal/provider/unified/session.go:49-58`
- 另外，V3 虽然也有 `binding.SessionUUID` 字段和 `UpdateSessionUUID` API，但本轮未发现业务写路径；这使得“把真实 provider session identity 修回持久层，用于未来 stale 排除 / 恢复”的能力并未形成闭环。

### 判断
- `❌`
- 这是当前 V2→V3 最明显的非 1:1 断点之一。V2 的思路是“先证明 live，再允许复用”；V3 现在是“map 里还有对象，就默认它还能用”。

## 收口结论
- 本轮 5 项里，只有 `claudecli` 进程管理可以判 `✅`。
- `Create/Get/Remove/CloseAll` 是 `⚠️`：方法面有了，但线程归档 / 删除 / recover 没统一收口到 manager remove。
- `SessionResolver`、`codexapp` recovery、stale session 防护都是 `❌`，而且三者是同一组问题：V3 还没有把“真实 provider session identity、binding、恢复候选、session liveness”做成一条统一链路。

## 对齐建议
1. 让 `thread/archive`、`thread/delete`、recover 前置清理统一走 `SessionManager.Remove(agentID)`，不要直接 `session.Close(ctx)`。
2. 把 turn RPC 的 `SessionResolver` 提升成 binding-aware resolver，至少复用 `bindingStore`、`SessionUUID`、`ProviderThreadID` 和 alias/cache，而不是只看 `threadStore.agent_id`。
3. 补回 Claude `session_uuid` 持久化修复链；V3 已有表字段和 store API，但没有业务写路径。
4. 把 `codexapp` recovery 从“纯 reconnect”补成“reconnect / respawn 后重新 initialize，并在需要时 `thread/resume`”。
5. 给 `SessionManager.Get` 或其上层调用点加 liveness/staleness 判定，避免 closed object 被当成 live session 复用。
