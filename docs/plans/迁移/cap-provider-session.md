# CAP 审查：Provider Session 生命周期 + 容错

## 范围与方法
- 只读审查。
- 仅使用 LSP 路径完成验证：`text_search`、`workspace_symbol`、`references(compact)`、`call_hierarchy`、`read_file`。
- 审查对象覆盖：
  - V3：`internal/provider/*`、`internal/module/thread/*`、`internal/module/turn/*`、`internal/sidecar/orch/orchestration/*`、`internal/store/*`
  - V2 对照：`go-agent-v2/internal/runner/*`、`go-agent-v2/internal/apiserver/*`、`go-agent-v2/legacy-agentsdk/*`

## 总结
- 当前 provider session 骨架已成型：`thread.Start/Resume -> unified.Client -> Driver -> SessionManager.Register`，`StopAgent/进程退出/fx OnStop` 也能走到 `Remove/CloseAll`。
- 但“生命周期 + 容错”还没有闭环。按严重度看，至少有 6 个需要优先处理的问题：
  - `P0` Claude 新建 session 的真实 provider thread/session ID 只回写到 session 内存，不回填 thread/binding 持久层；`ReadHistory` / `Resume` / `Recover` 仍会拿 placeholder `agentID` 当 threadID。
  - `P0` `thread/archive` / `thread/delete` 只 `Close` 不 `SessionManager.Remove`；关闭后的 session 仍留在 manager 里。`thread.Recover` 只看“有没有 session”，会把关闭对象误判成可复用 session，跳过 `ResumeSession`。
  - `P0` `codexapp` recovery 只是 websocket reconnect，不做 `initialize` / `thread/resume`；本地 `codex app-server` 子进程也没有常驻 `Wait()` 监控，异常退出后可能恢复失败并留下僵尸进程窗口。
  - `P1` session 解析路径分裂：turn RPC 走 `threadStore -> SessionResolver`，thread command/history/recover 走 `bindingStore -> resolveBinding`，fork 线程在进程重启后会出现两条链行为不一致。
  - `P1` `CapabilityError` 只在 provider 侧少量使用，生产调用方没有 typed handling；`CapabilityGate` 还会把 `ResolveSession` 失败吞成 “capability not supported”。
  - `P1` `Close(ctx)` 的 `ctx` 参数两边 driver 都基本不生效，`CloseAll(fx OnStop)` 不能真正受停止 deadline 约束。

## 1. Session 创建

### 结论
- 当前并不存在 `SessionManager.Create`；实际创建入口在 `unified.Client.StartSession/ResumeSession`。
- driver 选择逻辑是显式 provider 名称精确匹配，经过 `TrimSpace + strings.ToLower` 归一化；没有 alias，也没有默认 provider fallback。

### 证据
- `thread.Start` / `thread.Resume` 最终进入 `startSession` / `resumeSession`：`internal/module/thread/lifecycle.go:44-76`、`internal/module/thread/lifecycle.go:77-107`、`internal/module/thread/lifecycle.go:204-230`
- `unified.Client` 负责 `registry.Resolve(provider)`，成功后调用 driver，再 `sessions.Register(agentID, session)`：`internal/provider/unified/client.go:29-67`
- registry 只按归一化后的 `factory.Name` 匹配：`internal/provider/unified/registry.go:15-56`
- driver factory 名称：
  - Claude：`internal/provider/claudecli/module.go:12-19`
  - Codex：`internal/provider/codexapp/module.go:13-24`
- thread 层要求 provider 非空：`internal/module/thread/lifecycle.go:171-203`

### 评估
- 创建主链是清楚的，driver 选择也可预测。
- 但 V3 语义和 V2 不等价：
  - V2 registry 有默认 provider `codex`，空 provider 会落默认值：`go-agent-v2/internal/runner/provider_registry.go:12-15`、`go-agent-v2/internal/runner/provider_registry.go:82-133`
  - V3 `thread.Start` 直接要求 provider 必填：`internal/module/thread/lifecycle.go:180-183`

## 2. Session 使用

### 结论
- 当前至少有 3 条 session 解析路径，不是单一路径：
  - turn RPC：`SessionResolver.ResolveSession(threadID)`，走 `threadStore -> agentID -> SessionManager`
  - thread command/history/recover：`thread.service.resolveSession(threadID)`，走 `bindingStore -> SessionProvider`
  - orchestration：直接 `GetSession(agentID)`
- 这会带来真实不一致，不只是实现细节差异。

### 证据
- turn RPC：`internal/module/turn/rpc.go:20-46`
- `SessionResolver`：`internal/provider/unified/session_resolver.go:23-46`
- `threadStore.GetByThreadID` 的 `agent_id` 来自 `agent_provider_binding` 子查询：`internal/store/sqlc/query_agent_thread.go:6-12`
- thread command/history 使用 `resolveSession`：`internal/module/thread/command.go:14-43`、`internal/module/thread/history.go:13-45`、`internal/module/thread/service.go:213-226`
- orchestration starter 直接 `GetSession(agentID)`：`internal/module/turn/orchestration_starter.go:22-52`

### 关键问题 A：fork 线程重启后解析分裂
- `thread.Fork` 持久化新 thread 时 `updateBinding=false`，只写 `threadStore.owner_thread_id`，不写 binding：`internal/module/thread/lifecycle.go:108-135`
- `resolveBinding` 不查 `threadStore.owner_thread_id`，只查：
  - `GetByAgentID(threadID as agentID)`
  - 内存态 `threadAgents`
  - `GetByProviderThread(provider, threadID)`
  见 `internal/module/thread/service.go:183-211`
- 但 `SessionResolver` 依赖的 SQL 会用 `owner_thread_id` 反查 binding.agent_id：`internal/store/sqlc/query_agent_thread.go:6-12`

### 评估
- 结果是：fork 线程在当前进程内可能还能靠 `rememberThreadAgent(...)` 工作；一旦进程重启，`thread/messages` / `thread/config/*` / `thread/recover` 可能失败，而 `turn/start` 仍可能成功解析 session。
- 这不是“代码风格分裂”，而是可观测行为分裂。

### 关键问题 B：Claude fresh start 的 placeholder threadID 未修复
- Claude driver 在 fresh start 时先用 `fallbackThreadID(agentID, spec.threadID)` 初始化 `session.threadID`；fresh start 场景这里就是 `agentID`：`internal/provider/claudecli/driver.go:102-118`
- 真实 session/thread ID 只在 `system:init` 时回写到 session 内存：`internal/provider/claudecli/session_events.go:46-59`
- thread 层在 session 创建后立即 `persistThreadState(...)`，写入的 threadID 来自 `session.ThreadID()`；此时还是 placeholder：`internal/module/thread/lifecycle.go:57-75`
- 当前 V3 没有任何 `AgentLaunched/system:init` 之后的 binding/threadID repair 路径；`persistThreadState(...)` 只在 `Start/Resume/Fork/Recover` 被调用：`internal/module/thread/lifecycle.go:63`、`internal/module/thread/lifecycle.go:98`、`internal/module/thread/lifecycle.go:122`、`internal/module/thread/lifecycle.go:160`
- V2 明确有 `session_configured -> repairBindingOnSessionConfigured(...)`：`go-agent-v2/internal/apiserver/server_event_handler.go:196-295`

### 评估
- V3 Claude 新建线程后，对外暴露的“threadID”更像 placeholder agentID，而不是真实 provider session ID。
- 这会直接影响：
  - `ReadHistory` 目标 ID
  - `ResumeSession`
  - `Recover`
- 这是当前审查里最关键的 V2 不等价点之一。

## 3. Session 关闭

### 结论
- `SessionManager.Remove`、`CloseAll`、fx `OnStop` 都存在，orchestration 的 `StopAgent` / `StopAllAgents` / 进程退出路径也会走到 `RemoveSession`。
- 但关闭语义没有彻底闭环，`Archive/Delete` 没走 `Remove`，而且 driver 侧 `Close(ctx)` 基本不尊重传入 `ctx`。

### 证据
- `SessionManager.Remove` / `CloseAll`：`internal/provider/unified/session.go:59-101`
- fx `OnStop -> sessions.CloseAll(ctx)`：`internal/provider/unified/module.go:33-43`
- orchestration cleanup：
  - `StopAgent`：`internal/sidecar/orch/orchestration/service.go:127-141`
  - `StopAllAgents`：`internal/sidecar/orch/orchestration/service.go:143-153`
  - 进程退出：`internal/sidecar/orch/orchestration/service.go:355-370`
- Claude `Close(context.Context)` 实际忽略 ctx：`internal/provider/claudecli/session.go:237-280`
- Codex `Close(context.Context)` 实际忽略 ctx：`internal/provider/codexapp/session.go:205-215`

### 评估
- `CloseAll(ctx)` 传了 ctx，但两边 driver 都没真正使用这个 deadline。
- fx 停止超时更多只是“调用层 deadline”，不是 provider close 的硬边界。

## 4. Session 泄漏检测

### 结论
- 存在 session 被保留在 `SessionManager` 中但不再可用的路径。
- 严格说，当前更像“stale/closed session retained”与“manager entry leak”，而不仅仅是“创建后完全没 Close”。

### 明确路径 A：Archive/Delete 只 Close 不 Remove
- `thread/archive`：`internal/module/thread/archive.go:5-13`
- `thread/delete`：`internal/module/thread/service.go:102-119`
- 共同点：都调用 `closeSessionIfActive(...)`，而该函数只 `session.Close(ctx)`，不会 `SessionManager.Remove(...)`：`internal/module/thread/service.go:228-241`

### 明确路径 B：Recover 会误把关闭后的 session 当成可复用 session
- `thread.Recover` 在 `recoverAgent(...)` 之后先 `lookupSession(agentID)`；只要能取到对象，就不会再 `resumeSession(...)`：`internal/module/thread/lifecycle.go:137-169`
- 因为 `Archive/Delete` 没把 session 从 manager 去掉，`lookupSession` 会成功返回一个已关闭 session。

### 条件路径 C：创建成功后，上层后续失败时没有 provider 侧 rollback
- `thread.Start` / `thread.Resume` 在 `StartSession/ResumeSession` 成功后，如果后续 `lookupSession` / `persistThreadState` 出错，只会尝试 `stopAgent(...)`：`internal/module/thread/lifecycle.go:53-74`、`internal/module/thread/lifecycle.go:88-106`
- `stopAgent(...)` 只有在 orchestration 存在时才有实义：`internal/module/thread/lifecycle.go:309-323`

### 评估
- `Archive/Delete` 是已实锤的问题。
- 创建后失败的 rollback 依赖 orchestration；在非 orchestration 场景下，provider session 没有统一 rollback。

## 5. Claude CLI 进程管理

### 结论
- `claudecli` transport 的显式 shutdown / kill 做得相对完整：
  - 独立进程组
  - 后台 `Wait()` 回收
  - 先 `SIGTERM`，超时再 `SIGKILL`
- 但 session 级别没有空闲态进程死亡监控，死 session 仍可能长期留在 manager 中。

### 证据
- `newTransport(...)`：
  - `exec.Command(...)`
  - `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`
  - 后台 `go tr.wait()`
  见 `internal/provider/claudecli/transport.go:33-63`
- `Close()`：先 `closeInput`，再 `SIGTERM`，等 3s，仍存活就 `SIGKILL`：`internal/provider/claudecli/transport.go:98-120`
- `signalProcess` 发信号给进程组 `-pid`：`internal/provider/claudecli/transport.go:181-190`
- `wait()` 会调用 `cmd.Wait()` 回收子进程：`internal/provider/claudecli/transport.go:137-154`

### 评估
- 显式关闭路径的“僵尸进程防护”是成立的。
- 但如果 Claude CLI 在 idle 状态意外退出：
  - `handleReceiveExit(...)` 只会在有 active turn 时 finish handle：`internal/provider/claudecli/session_events.go:61-82`
  - manager 不会自动移除该 session
  - 下一次使用只能在运行时报错，不会预先剔除

## 6. CodexApp 连接管理

### 结论
- websocket 断线后的自动 reconnect 路径已经存在，但它只是 transport-level reconnect，不是 session-level recovery。
- “recovery 已修”不能给通过结论，最多算“只修到了半层”。

### 证据
- transport 建连与本地 app-server 启动：`internal/provider/codexapp/transport.go:63-77`、`internal/provider/codexapp/transport.go:170-190`
- `ReadLoop` 读失败后发 `connection.dead`：`internal/provider/codexapp/transport.go:203-227`
- session 侧收到 `connection.dead` 后触发 `attemptRecovery(...)`：`internal/provider/codexapp/session.go:231-240`、`internal/provider/codexapp/session_recovery.go:23-52`
- recovery manager 只做 `transport.reconnect(...)`：`internal/provider/codexapp/recovery.go:28-44`
- recovery 之后仅重启 `ReadLoop`，没有 `initializeSession(...)` / `resumeRemoteThread(...)`：`internal/provider/codexapp/session_recovery.go:47-50`

### 关键问题 A：恢复只重连 transport，不恢复 session state
- fresh session 初始化和 thread start/resume 只在 driver 创建时做一次：
  - `initializeSession(...)`：`internal/provider/codexapp/driver.go:114-122`
  - `startRemoteThread(...)`：`internal/provider/codexapp/driver.go:124-144`
  - `resumeRemoteThread(...)`：`internal/provider/codexapp/driver.go:146-157`
- recovery 路径不重复这两步。

### 关键问题 B：本地 app-server 子进程缺少常驻 Wait 监控
- 本地 `codex app-server` 通过 `spawnLocal()` 启动，但没有像 Claude 那样单独 goroutine `Wait()`：`internal/provider/codexapp/transport.go:170-190`
- `cmd.Wait()` 只在显式 `stopProcess(...)` 里调用：`internal/provider/codexapp/transport.go:295-316`
- `processRunning()` 依赖 `cmd.ProcessState`：`internal/provider/codexapp/transport.go:318-322`

### 评估
- 如果本地 app-server 异常退出而父进程没 `Wait()`：
  - 有僵尸进程窗口
  - `ProcessState` 可能仍是 `nil`，`processRunning()` 可能误判为 still running
  - `reconnect()` 就不会走 `spawnLocal()`，恢复会失败
- 另外 `connection.dead` 的 typed event 被翻译成 `AgentFailed{Recoverable:false}`，因为 transport payload 没带 `recoverable`/`willRetry`，但 session 实际又会发起恢复：`internal/provider/codexapp/event_map.go:65-70`、`internal/provider/codexapp/transport.go:219-225`
- `recovery.attempt` 事件里的 `attempt` 也被写死为 `1`，不能反映 `maxRetry=3` 的内部重试：`internal/provider/codexapp/session_recovery.go:38-45`

### V2 对照
- V2 Codex app-server client 有更完整的恢复路径：
  - `callWithNotInitializedRecovery(...)`：`go-agent-v2/legacy-agentsdk/codex/client_appserver_helpers.go:171-188`
  - process restart 后会 `Initialize()` + `ResumeThread(...)`：`go-agent-v2/legacy-agentsdk/codex/client_appserver_transport.go:421-516`
- V3 当前明显弱于 V2。

## 7. 双 driver 能力差异

### 结论
- 两个 driver 的能力差异是实质性的，不只是 transport 形态不同。
- 调用方对这种差异的感知并不充分，部分地方只靠 runtime error 暴露。

### 证据
- Claude capability set：`internal/provider/claudecli/driver.go:13-18`
- Codex capability set：`internal/provider/codexapp/driver.go:27-33`
- Claude unsupported：
  - `ListThreads` -> `CapabilityError(thread_list)`：`internal/provider/claudecli/session.go:229-231`
  - `ForkThread` -> `CapabilityError(thread_fork)`：`internal/provider/claudecli/session.go:233-235`
- Codex runtime `Configure` 已实现：`internal/provider/codexapp/session.go:186-229`
- Codex approval bridge 已实现：`internal/provider/codexapp/session_approval.go:14-91`

### 调用方感知现状
- `thread/fork` 没有 capability gate，完全依赖 runtime error：`internal/module/thread/rpc.go:32-35`
- `thread/model/set` 有 capability gate，但 `claude` 仍声明 `model_switch=true`，所以 gate 会放行，随后 `Configure` 再报错：`internal/module/thread/rpc.go:60-62`、`internal/provider/claudecli/session_config.go:13-27`
- `thread/personality/set` / `thread/approvals/set` 根本没有 capability gate：`internal/module/thread/rpc.go:61-62`
- `Session.ListThreads` 在生产代码里没有调用方，当前基本是 dead surface：仅能搜到 interface 定义与测试引用

### 评估
- Claude 的 `model_switch` 在 turn override 语义上并不完全错误，但它不等于“支持 runtime thread/model/set”。
- 当前 capability 设计把 “turn override 能力” 和 “runtime thread config 能力” 混在了一起。

## 8. Configure 运行时变更

### 结论
- “claudecli 已改为 CapabilityError” 这一点已落实。
- 但上层没有把这个 typed error 继续保留下来，也没有把 capability gate 与实际支持矩阵对齐。

### 证据
- Claude `Configure(...)`：
  - 空 patch 直接返回 nil
  - 非空 patch 返回 `fmt.Errorf(... %w, dto.NewCapabilityError(...))`
  见 `internal/provider/claudecli/session_config.go:13-27`
- turn override 仍然会用 `CapTurnOverride` / `CapModelSwitch` 构建请求内 override：`internal/module/turn/service.go:257-269`

### 评估
- 这意味着：
  - Claude 支持 “turn 级 model override”
  - Claude 不支持 “runtime thread configure”
- 现在的 RPC/能力模型没有把这两个概念拆开，调用方很容易误判。

## 9. ReadHistory

### 结论
- 两个 driver 都实现了 `ReadHistory`。
- metadata 恢复逻辑两边都在，但完整度不同，且 V3 缺少直接测试覆盖。

### 证据
- Claude：
  - `session.ReadHistory(...)`：`internal/provider/claudecli/session_history.go:13-70`
  - history backend 从 `.claude/projects/*/<thread>.jsonl` 读：`internal/provider/claudecli/history.go:18-82`
  - injected file/image hints 恢复 metadata：`internal/provider/claudecli/history.go:119-188`
- Codex：
  - `session.ReadHistory(...)`：`internal/provider/codexapp/session_history.go:13-62`
  - 优先本地 rollout，fallback `thread/read`：`internal/provider/codexapp/history.go:19-39`
  - 本地 rollout metadata 恢复：`internal/provider/codexapp/history_rollout.go:55-109`

### 评估
- Claude 侧 metadata 恢复与 V2 方向一致，能恢复 injected file/image hints。
- Codex 侧能恢复 metadata，但本地 rollout 仍只恢复 `input_image`；源码里还有 TODO：
  - `internal/provider/codexapp/history_rollout.go:101-102`
- 这一点与 V2 Codex 是同级别，不是 V3 独有退化：
  - `go-agent-v2/legacy-agentsdk/codex/rollout_reader.go:216-239`
- 但 V3 有额外风险：Claude fresh start 的 placeholder threadID 未修复，`thread/messages` / `ReadHistory` 很容易把 `agentID` 当 history target。

## 10. EventDispatcher AgentID 回填

### 结论
- `codexapp` 的 AgentID fallback 已落地。
- 但它在 `session.dispatch(...)`，不是 `event_map.go` 里直接做的；而且没有看到对应 V3 测试。

### 证据
- `session.dispatch(...)` 在 payload 非空且缺 `agentId/agent_id` 时注入 session.agentID：`internal/provider/codexapp/session.go:243-255`
- translator 侧 `agentSessionHeader(...)` 读取 `agentId/agent_id`：`internal/provider/codexapp/event_map.go:150-162`

### 评估
- 这一项可以给“已修”。
- 残留点：
  - 只补 `agentId`，不补 `threadId`
  - `connection.dead` 这类 transport 生成事件仍可能缺少 thread 维度

## 11. 错误类型化

### 结论
- `CapabilityError` 定义没问题，provider 侧使用也基本正确。
- 但调用方没有 typed handling，typed error 在 RPC 边界基本丢失。

### 证据
- 定义：`internal/dto/provider/capability.go:17-28`
- 生产代码里的创建点主要只有 Claude unsupported 路径：`internal/provider/claudecli/session.go:229-235`、`internal/provider/claudecli/session_config.go:23-25`
- `errors.As(..., *CapabilityError)` 的命中只在测试里，没有生产调用方：`internal/provider/unified/contract_test.go:146-160`

### 关键问题：CapabilityGate 会吞掉 ResolveSession 错误
- `NewCapabilityResolver(...)` 中，如果 `ResolveSession(...)` 出错或 session 为 nil，就直接返回 nil capability set：`internal/platform/rpc/handler.go:20-31`
- `CapabilityGate(...)` 看到 capability set 不含目标能力，就返回 `CodeCapabilityGate`：`internal/platform/rpc/handler.go:71-86`
- `turn/start` / `turn/steer` / `thread/model/set` / `thread/compact/start` / realtime 路由都走这个 gate：`internal/module/turn/rpc.go:33-58`、`internal/module/thread/rpc.go:60-83`

### 评估
- 实际效果是：
  - session 不存在
  - threadID 无法解析
  - stale session 被清理后取不到
- 这些本应是 “session resolution failure” 的情况，会被误报成 “capability not supported by active provider”。
- 所以 `CapabilityError` 本身不是主要问题；主要问题是错误在 RPC capability gate 之前就被抹平了。

## 12. V2 等价性

### 结论
- 不能给 “V2 等价”。
- 当前更接近“V3 provider session 抽象已搭起来，但在恢复、回填、fallback、能力面上仍弱于 V2”。

### 主要差异
- Provider 选择语义不同：
  - V2 有默认 `codex` provider：`go-agent-v2/internal/runner/provider_registry.go:12-15`、`go-agent-v2/internal/runner/provider_registry.go:82-133`
  - V3 `thread.Start` 要求 provider 必填：`internal/module/thread/lifecycle.go:180-183`
- Codex transport fallback 不等价：
  - V2 runner 有 app-server -> REST fallback：`go-agent-v2/internal/runner/manager_launch.go:209-250`
  - V3 codex driver 没有第二 transport 形态：`internal/provider/codexapp/driver.go:78-112`
- Codex recovery 不等价：
  - V2 restart recovery 会 `Initialize + ResumeThread`：`go-agent-v2/legacy-agentsdk/codex/client_appserver_transport.go:467-502`
  - V3 recovery 只 reconnect transport：`internal/provider/codexapp/session_recovery.go:32-52`
- Claude session/thread ID repair 不等价：
  - V2 有 `session_configured -> repairBindingOnSessionConfigured(...)`：`go-agent-v2/internal/apiserver/server_event_handler.go:196-295`
  - V3 没有对应 repair 链
- 终态清理不等价：
  - V2 `thread/archive` / `thread/delete` 都会 `stopInlineManager(threadID)`：`go-agent-v2/internal/apiserver/methods_thread_turn.go:13-25`、`go-agent-v2/internal/apiserver/methods.go:204-227`
  - V3 `Archive/Delete` 只 `Close` 当前 session，不 `Remove`：`internal/module/thread/archive.go:5-13`、`internal/module/thread/service.go:102-119`
- 能力面不等价：
  - V3 provider capability set 没有任何一方声明 `context_compact` / `realtime`
  - 但 RPC 仍暴露这些入口：`internal/module/thread/rpc.go:63-83`
  - V2 这些入口至少有完整 API surface：`go-agent-v2/internal/apiserver/methods_thread_turn.go:33-92`
- 抽象边界也不同：
  - V2 `agentcore.Client` 还有 `SendDynamicToolResult` / `RespondError`：`go-agent-v2/legacy-agentsdk/agentcore/client.go:7-20`
  - V3 provider `Session` 没有对应能力；审批被拆到单独桥接：`internal/module/turn/rpc.go:82-94`、`internal/provider/codexapp/session_approval.go:14-91`
- 同时 V3 也有新增统一能力：
  - `Session.ReadHistory(...)` 是 V3 统一 surface 的新增项：`internal/contract/provider.go:22-37`

## 建议
- `P0` 为 Claude 补一条 `system:init/AgentLaunched -> repair thread/binding/provider_thread_id` 链，至少把 fresh start 的 placeholder `agentID` 修正为真实 provider session ID。
- `P0` 把 `thread/archive` / `thread/delete` 从“只 Close”改成“`SessionManager.Remove` + 必要的 orchestration/session cleanup”；`Recover` 也不要只看 `lookupSession(agentID)` 是否存在，而要判断 session 是否仍可用。
- `P0` 重做 `codexapp` recovery：至少补 `initializeSession` 和 `resumeRemoteThread`，并为本地 `codex app-server` 增加常驻 `Wait()`/reap 监控。
- `P1` 统一 session 解析路径；至少让 `resolveBinding` 能 fallback 到 `threadStore.owner_thread_id`，不要让 turn/history/command/recover 走出不同结果。
- `P1` 调整 capability 语义：
  - 把 “turn override 能力” 和 “runtime configure 能力” 拆开
  - 不要让 `CapabilityResolver` 吞掉 `ResolveSession` 错误
  - 让 caller 能稳定区分 “unsupported capability” 与 “session not found/stale”
- `P1` 让 driver `Close(ctx)` 真正 honor ctx，避免 `fx OnStop` 成为表面 deadline。

## 互审

### 1. 对 `docs/plans/迁移/cap-turn-execution.md`

1. 第 1 节把 `turn/start` 定性为“真链路”，但结论范围过窄，忽略了 direct RPC 在进入 `PrepareTurn` 之前就可能被 session 解析问题挡住。`turn/start` 走 `rpc.CapabilityThreadHandler(...)`：`internal/module/turn/rpc.go:33-46`；`CapabilityResolver` 先 `ResolveSession(threadID)`，一旦解析失败就返回 `nil caps`：`internal/platform/rpc/handler.go:20-31`。而 `SessionResolver` 又硬依赖 `threadStore.GetByThreadID(...).AgentID`：`internal/provider/unified/session_resolver.go:23-46`。Claude fresh start 时，driver 先把 `agentID` 当 placeholder threadID 填进 session：`internal/provider/claudecli/driver.go:103-118`；`thread.Start` 也会立刻把这个 placeholder 持久化：`internal/module/thread/lifecycle.go:57-75`；真实 ID 只在 `system:init` 时回写 session 内存，不回填 store：`internal/provider/claudecli/session_events.go:46-59`。因此报告第 1 节对 direct RPC 可达性的前提判断过于乐观。

2. 第 8 节“错误处理”漏掉了一个更前置的错误掩盖问题：resolver/session 失败会被 capability gate 统一抹平成 `CodeCapabilityGate`。`NewCapabilityResolver` 对 `ResolveSession(...)` 的任何错误都直接返回 `nil`：`internal/platform/rpc/handler.go:20-31`；随后 `CapabilityGate` 把它转成 “capability not supported by active provider”：`internal/platform/rpc/handler.go:71-86`。这不只是“异步断连/卡死对 orchestration 收敛不足”，而是 `turn/start` / `turn/steer` 的同步错误分类本身就可能被误报。

3. 报告虽然指出 direct RPC 输入面变窄，但仍低估了缩水幅度。V3 自己的 `PrepareInput` 已支持 `Inputs`、`Skills`、`ManualSkillSelection`、`OutputSchema`、`AgentID`、`CWD`、`BinaryDir`：`internal/module/turn/contract.go:27-42`；但 direct RPC 的 `buildPrepareInput(...)` 实际只填 `Prompt/Images/Files/Model/Effort/ThreadCaps`：`internal/module/turn/rpc_helpers.go:5-14`。这说明 direct RPC 不只是“比 V2 窄”，而是连 V3 自己统一 turn 输入模型的大部分字段都绕开了。

4. 第 7 节 approval 集成没有点出当前 turn 主路径上的一个真实竞态：approval request 会先被发到事件总线，再异步注册 pending。`codexapp.session.onNotification` 先 `dispatch(raw event)`，后进入 approval 分支：`internal/provider/codexapp/session.go:233-244`；`handleApprovalRequest` 又起 goroutine：`internal/provider/codexapp/session_approval.go:14-24`；`ApprovalManager.RequestApproval` 里的 `registerPending(...)` 更晚发生：`internal/platform/rpc/approval.go:74-85,127-152`。因此前端/上层可能已经看到了 approval request，但 `approval/respond` 仍会因为 `lookupPending(...)` 未命中而返回 `approval is not pending`：`internal/platform/rpc/approval.go:108-116`。这是 turn 执行期的实质性容错问题，报告未覆盖。

### 2. 对 `docs/plans/迁移/cap-approval-lifecycle.md`

1. 第 8 项和对应总结把 `RestorePending / Cleanup` 说成“只有定义，没有调用点”，这是与实码不符。`bindApprovalLifecycle(...)` 在 RPC module 的 `fx.OnStart` 明确对每个 active server 调 `approvals.RestorePending(...)`：`internal/platform/rpc/module.go:73-88`；在 `fx.OnStop` 明确取 snapshot 后调 `approvals.Cleanup(time.Nanosecond)`：`internal/platform/rpc/module.go:89-100`。`RestorePending` / `Cleanup` 的问题是“只恢复进程内存态 pending，不能跨进程持久化”，不是“完全没接线”。

2. 第 6 项把 `request_user_input` 定成“当前无调用点，provider 也未桥接”，这也是实码不符。`codexapp` 的 `onNotification` 并不是只桥 exec approval；它走 `case isApprovalBridgeMethod(method)`：`internal/provider/codexapp/session.go:233-244`。而 `isApprovalBridgeMethod(...)` 会把 `isRequestUserInputMethod(method)` 纳入桥接范围：`internal/provider/codexapp/session_approval.go:93-114`。一旦命中这些 method family，`requestApprovalDecision(...)` 还会显式走 `ApprovalManager.RequestUserInput(...)`：`internal/provider/codexapp/session_approval.go:38-44`。报告第 6.3 节用 `text_search("request_user_input") == 0` 推出“未桥接”，被 camelCase / mixed-case method 名绕过去了。

3. 第 2 项把 `approval/respond` 定成“通过”，结论过于乐观，和它自己第 1 节写出的竞态相冲突。报告正文已承认：provider raw event 先发，pending 后注册，抢先 `approval/respond` 可能返回 `approval is not pending`。代码链也完全支持这个判断：`internal/provider/codexapp/session.go:233-244`、`internal/provider/codexapp/session_approval.go:14-24`、`internal/platform/rpc/approval.go:74-85,108-116`。既然主路径存在可复现的时序窗，这一项不该给“通过”。

4. 第 4 项“超时处理”只盯着 deadline/Cleanup，漏掉了更常见的 session-cancel 终止路径。`codexapp.requestToolApproval(...)` 把 `s.ctx` 直接传给 `RequestApproval(...)` / `RequestUserInput(...)`：`internal/provider/codexapp/session_approval.go:31-44`；而 `session.Close()` / `ForceStop()` 都会先 `s.cancel()`：`internal/provider/codexapp/session.go:205-215`。`waitForApproval(...)` 只等 `ctx.Done()` 或 `pending.done`：`internal/platform/rpc/approval_support.go:34-47`；`mapApprovalWaitErr(...)` 也不会把 `context.Canceled` 归一成 timeout：`internal/platform/rpc/approval_support.go:89-97`。这意味着 provider session 一旦 teardown，pending approval 会直接以 cancel 结束，既不走 timeout，也不等恢复；报告没有覆盖这个更实际的失败模式。

### 3. 对 `docs/plans/迁移/cap-workspace-ops.md`

1. 报告多次把默认 workspace 路径写成 `sourceRoot/.workspace/<runKey>`，这与代码不符。`resolveWorkspacePath(...)` 在 `WorkspacePath` 为空时，先取 `base := req.CWD`，只有 `CWD` 为空才回退到 `sourceRoot`，然后才拼 `filepath.Join(base, ".workspace", runKey)`：`internal/module/workspace/service.go:143-163`。而 `CreateRunRequest` 公开暴露了 `CWD` 字段：`internal/module/workspace/contract.go:25-37`。所以默认路径实际是“优先 `<CWD>/.workspace/<runKey>`，否则 `<sourceRoot>/.workspace/<runKey>`”；报告第 1、12 节在这里写死成 `sourceRoot`，证据不准确。

2. 报告提到了 `CreateRunRequest.Status` 和 `UpdateRunStatus` 是 bypass，但仍低估了问题严重性：当前根本没有任何 status 枚举校验。`buildRun(...)` 直接接受 `strings.TrimSpace(req.Status)`，只有空字符串才默认成 `active`：`internal/module/workspace/service.go:93-112`；`UpdateRunStatus(...)` 也直接把任意 `strings.TrimSpace(status)` 透传到 store：`internal/module/workspace/service.go:204-218`；store/SQL 层也没有状态白名单：`internal/store/workspace/store.go:62-77`、`sql/queries/workspace_run.sql:30-56`。因此这不是“多暴露了一个 escape hatch”而已，而是 create/update 两条路径都能写入任意自定义 status。

3. 报告没有指出 `CreateRun` 其实是“按 `runKey` upsert 现有 run”，不是纯 insert。`persistRun(...)` 调的是 `txStore.UpsertRun(...)`：`internal/module/workspace/service_helpers.go:166-179`；底层 SQL `UpsertWorkspaceRun` 明确 `ON CONFLICT (run_key) DO UPDATE`，会覆盖 `dag_key/source_root/workspace_path/status/updated_by/metadata/finished_at`：`sql/queries/workspace_run.sql:1-15`。这意味着同一个 `runKey` 再次 `CreateRun` 会原地改写已有 run，而不只是“创建失败”或“返回已存在”；这是 workspace 生命周期和幂等语义上的重大风险，报告未覆盖。
