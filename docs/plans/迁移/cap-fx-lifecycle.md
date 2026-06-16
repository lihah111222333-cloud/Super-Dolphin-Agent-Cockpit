# cap-fx-lifecycle

## 范围

- 审查对象：V3 `internal/app` 装配链、`internal/ui/wails`、`internal/platform/{db,bus,rpc,runner}`、`internal/module/{thread,turn,orchestration,workspace}`、`internal/provider/unified`。
- 桌面入口：`cmd/agent-terminal/main.go:10-15`。
- V2 对照：`go-agent-v2/cmd/agent-terminal/main.go:61-77`、`go-agent-v2/cmd/agent-terminal/main_setup.go:137-183,372-490`、`go-agent-v2/cmd/agent-terminal/app_helpers.go:289-336`。
- 验证手段：仅使用 LSP `text_search / workspace_symbol / references(compact) / call_hierarchy / read_file`。

## 结论先行

- 总体判断：V3 已经具备 `fx` 装配、`BindRuntime` 统一运行时、桌面双向 shutdown 的骨架，但“生命周期硬化”还没达到 V2 等价。
- 最明显的缺口不在“能不能启动”，而在“停得是否可控”：悬空 provider 较多、shutdown 顺序与目标不一致、headless signal 入口不唯一、外层 `fx.Start/fx.Stop` 与 `RunGroup` 没有超时保护、panic 保护只覆盖了部分 subscriber。
- `RunDesktop()` 的正反向 shutdown 已闭环；`runner nil 返回` 问题在 `BindRuntime` 层面已经修到“无条件 `Shutdown()`”，但当前 `uiwails.Runner` 根本不在 live graph 里。
- `Run()` headless 链路本身存在，但当前没有二进制入口引用它；V3 真实运行入口只有 desktop。

## 高风险发现

### R1. `fx` 图能启动，但并不“干净”：存在大量悬空 provider

- `store.Module` 注册了 19 个 leaf store provider：`internal/store/module.go:28-49`。
- 通过 `Store` 接口引用反查，当前只有 5 个被应用层消费：
  - `binding.Store`：`internal/module/thread/service.go:29,43`
  - `commandcard.Store`：`internal/module/skill/module.go:20-26`、`internal/module/skill/service.go:20,29`
  - `taskdag.Store`：`internal/sidecar/orch/orchestration/service.go:34,85`、`internal/sidecar/orch/orchestration/dag.go:20,81,88,103,112`
  - `thread.Store`：`internal/module/thread/service.go:28,42`、`internal/provider/unified/session_resolver.go:13,19`
  - `workspace.Store`：`internal/module/workspace/module.go:10-13`、`internal/module/workspace/service.go:37,45`
- 下列 store provider 仅在各自 `contract.go` 与 `store.go` 自身出现，当前 app graph 没有消费者：
  - `agentstatus`
  - `ailog`
  - `auditlog`
  - `buslog`
  - `cwdlock`
  - `dbquery`
  - `interaction`
  - `prompt`
  - `sharedfile`
  - `systemlog`
  - `taskack`
  - `tasktrace`
  - `topologyapproval`
  - `uipreference`
- `bus.Module` 也有同样问题：`internal/platform/bus/module.go:10-23` 注册了 6 组 emitters，但只有 `WorkspaceEmitters` 被消费：`internal/module/workspace/module.go:10-13`、`internal/module/workspace/service.go:45-55`。`AgentEmitters`、`TurnEmitters`、`ToolEmitters`、`TaskEmitters`、`UIEmitters` 反查只有定义和构造函数，没有应用侧引用。
- 这不会阻止 Fx 启动，因为 Fx 不把 unused provider 当错误；但它说明当前 graph 完整性只是“可运行”，不是“最小闭合”。

### R2. graceful shutdown 顺序与目标不一致

- `BindRuntime` 的 `OnStop` 最后注册，因此最先执行：`internal/app/runner.go:32-69`。
- 它先 `cancel()` 运行时 context，触发 `runnerActor.Run()` 进入 `ctx.Done()` 分支，然后 `stopAll()`：`internal/sidecar/orch/orchestration/runner_actor.go:37-40,79-81`。
- `StopAllAgents()` 内部顺序是：
  - 先 `stopAgentLocked()`，实际是 `cmd.Process.Kill()`：`internal/sidecar/orch/orchestration/service.go:143-164`、`internal/sidecar/orch/orchestration/helpers.go:303-308`
  - 再 `removeSession(agent.id)`：`internal/sidecar/orch/orchestration/service.go:147-150`
  - `removeSession` 通过 `SessionCleaner` 走到 `SessionManager.Remove()`，并立即 `session.Close(context.Background())`：`internal/provider/unified/session_adapter.go:34-39`、`internal/provider/unified/session.go:59-82`
- 也就是说，真实顺序不是“session close -> agent stop”，而是“agent kill 与 per-agent session close 交织执行”，然后 `unified.registerSessionShutdown` 再兜底关闭剩余 session：`internal/provider/unified/module.go:33-43`
- 之后才是订阅解绑与基础设施关闭。按 hook 注册顺序反推，headless 的 `OnStop` 顺序是：
  - `BindRuntime`
  - `registerSessionShutdown`
  - `registerTurnLifecycle`
  - `rpc.bindEventBridge`
  - `bus.registerLifecycle`
  - `db.registerLifecycle`
- 证据：
  - 模块装配顺序：`internal/app/modules.go:23-44`
  - hook 注册点：`internal/platform/db/module.go:28-40`、`internal/platform/bus/module.go:25-35`、`internal/platform/rpc/module.go:51-69`、`internal/sidecar/orch/orchestration/module.go:25-53`、`internal/provider/unified/module.go:33-43`
  - Fx 本地源码明确 `OnStop` 逆序执行：`/Users/mima0000/go/pkg/mod/go.uber.org/fx@v1.24.0/app.go:709-732`
- 结论：目标顺序“session close -> agent stop -> db close -> bus stop”目前不成立；实际更接近“agent stop/RemoveSession -> leftover session close -> subscription stop -> bus stop -> db close”。

### R3. 外层生命周期没有 timeout；V2 有，V3 没接上

- V3 调用的是：
  - `app.Start(context.Background())`：`internal/app/app.go:35,61`
  - `app.Stop(context.Background())`：`internal/app/app.go:49,65`
- Fx 默认 15s timeout 只在 `app.Run()` 或调用方显式传入带 deadline 的 context 时生效，本地 fx 源码可见：
  - `Run()` 用 `app.StartTimeout()/app.StopTimeout()` 包装：`/Users/mima0000/go/pkg/mod/go.uber.org/fx@v1.24.0/app.go:576-620`
  - `Start/Stop` 只消费调用方传入的 ctx：`/Users/mima0000/go/pkg/mod/go.uber.org/fx@v1.24.0/app.go:660-732,787-819`
- `RunGroup` 也没有超时：`internal/platform/runner/group.go:22-38`
- `internal/platform/config/timeouts.go:8-17` 虽然定义了 `ShutdownTimeout`、`LaunchTimeout`，但 LSP 反查没有生产代码引用。
- 当前只有局部 timeout，而没有 app 级 lifecycle timeout：
  - `WailsLifecycle` 只有 320ms quit grace delay：`internal/ui/wails/lifecycle.go:91-99`
  - turn interrupt settle 6s：`internal/module/turn/service.go:205-255`
  - codex approval/respond 10s：`internal/provider/codexapp/session_approval.go:79-82`
  - codex recovery 等旧 read loop 最多 2s：`internal/provider/codexapp/session_recovery.go:51-57`
  - claude transport close 等待 3s：`internal/provider/claudecli/transport.go:98-109,165-179`
- 对照 V2：
  - `runMainOnShutdown()` 有 10s hard deadline 与 3s post-shutdown safety exit：`go-agent-v2/cmd/agent-terminal/main.go:134-155`
  - `App.shutdown()` 有 5s `StopAll` timeout，再 force kill：`go-agent-v2/cmd/agent-terminal/app_helpers.go:304-319`
- 结论：V3 没有外层 timeout 护栏，遇到卡死 hook / 卡死 external close 时，`fx.Stop()` 可能无限等待。

### R4. signal 入口不是唯一；headless 是双入口

- `runApp()` 通过 `<-app.Done()` 等待退出：`internal/app/app.go:60-65`
- `watchFXShutdown()` 在 desktop 里也调用了 `app.Done()`：`internal/app/app.go:78-88`
- Fx 的 `app.Done()` 会启动自己的 signal receiver，本地 fx 源码：
  - `signalReceivers.Start()` 注册 `os.Interrupt/_sigINT/_sigTERM`：`/Users/mima0000/go/pkg/mod/go.uber.org/fx@v1.24.0/signal.go:100-113`
  - `App.Done()` 会 `receivers.Start()`：`/Users/mima0000/go/pkg/mod/go.uber.org/fx@v1.24.0/app.go:734-744`
- 同时，headless 的 `BindRuntime` 又把 `RunGroup` 配成 `EnableSignals: p.Lifecycle == nil`：`internal/app/runner.go:38-40`。`RunGroup` 自己也 `signal.Notify(SIGINT, SIGTERM)`：`internal/platform/runner/group.go:49-64`
- 所以：
  - headless：Fx `app.Done` + `RunGroup` signal actor，双入口
  - desktop：只剩 Fx `app.Done` 这一条；`RunGroup` signal actor 被禁用
- 这意味着 headless 下 OS signal 会同时触发两条路径：
  - `app.Done()` 让外层 `runApp()` 进入 `app.Stop()`
  - `RunGroup` signal actor 让 `BindRuntime` 收到错误并记录 `runtime exited`
- 结论：signal 入口不唯一。headless 模式下有重复处理与错误日志噪声风险。

### R5. panic 防护只覆盖了部分 subscriber；runner/provider goroutine 仍可能直接崩进程

- 已有 recover：
  - `platformbus.ResilientSubscribe` 会 recover 回调 panic 并记日志：`internal/platform/bus/resilient.go:10-29`
  - orchestration / rpc push / wails bridge 都用它：`internal/sidecar/orch/orchestration/module.go:33-44`、`internal/platform/rpc/push.go:82-90`、`internal/ui/wails/bridge.go:53-63`
- 但没有全局保护：
  - 没有 `fx.RecoverFromPanics`
  - `BindRuntime` goroutine 无 recover：`internal/app/runner.go:37-53`
  - `runnerActor.Run()` / `waitForExit()` 无 recover：`internal/sidecar/orch/orchestration/runner_actor.go:26-60`
  - `rpc.Server` accept/serve goroutine 无 recover：`internal/platform/rpc/server.go:92-125`
  - `claudecli.startReadLoop()` 无 recover：`internal/provider/claudecli/session_events.go:13-30`
  - `codexapp` read loop / approval / recovery goroutine 无 recover：`internal/provider/codexapp/session_readloop.go:5-41`、`internal/provider/codexapp/session_approval.go:19-23`、`internal/provider/codexapp/session_recovery.go:29-57`
- 额外注意：当前 active subscriber 里，`LogSink` 仍直接用 `event.Subscribe`，不是 `ResilientSubscribe`：`internal/platform/bus/sink.go:89-99`
- 结论：subscriber panic 只对部分路径安全；runner 与 provider loop 里的 panic 仍会导致整个进程崩溃。

### R6. `Run()` headless 存在，但现在没有 live 入口；V2 等价性因此天然不完整

- `internal/app.Run()` 只有定义，没有 caller；LSP 反查没有命中任何二进制入口。
- 当前唯一真实入口是 desktop：`cmd/agent-terminal/main.go:10-15`
- 这意味着：
  - `Run()` headless 只是代码路径，不是实际发布能力
  - headless signal/timeout/logging 的问题暂时不一定会被真实流量覆盖
  - “V2 启停能力 vs V3” 比较时，V3 目前只能说 desktop 主路径基本成型，headless 主路径还没真正上场

## 12 个维度逐项结论

| 维度 | 结论 | 结论摘要 |
| --- | --- | --- |
| 1. fx 图完整性 | 部分通过 | active graph 能闭合，但 `store.Module` 与 `bus.Module` 有明显悬空 provider；`internal/archtest/fx_graph_test.go:11-15` 只验证 `app.Module`，不验证 desktop graph 与 `BindRuntime`。 |
| 2. optional 依赖 | 部分通过 | `thread.Module` 与 `turn.Module` 把若干关键依赖标成 optional：`internal/module/thread/module.go:7-18`、`internal/module/turn/module.go:7-16`。当前 full app 会提供这些依赖；若缺失，则行为多数退化为 runtime error 或 no-op，而不是图构建失败。 |
| 3. Run() headless | 部分通过 | 调用链完整：`internal/app/app.go:60-65` -> `internal/app/runner.go:32-69` -> `internal/platform/runner/group.go:22-38`。但目前无二进制入口引用它，并且 signal 双入口。 |
| 4. RunDesktop() | 通过 | `fx.Start -> Wails Run -> fx.Stop` 成立：`internal/app/app.go:29-50`。frontend->backend 与 backend->frontend 的 shutdown 也都已接通：`internal/ui/wails/module.go:72-98,111-134`、`internal/ui/wails/lifecycle.go:82-112,145-173`、`internal/app/app.go:78-88`、`internal/app/runner.go:44-53`。残留问题是 post-start nil guard 早退不 `Stop()`。 |
| 5. runner nil 返回 | 通过 | `BindRuntime` 在 `RunGroup` 返回后无条件 `_ = p.Shutdowner.Shutdown()`：`internal/app/runner.go:51-53`。`internal/ui/wails/runner.go:16-35` 的 nil-return guard 代码当前不在 live graph 内。 |
| 6. graceful shutdown 顺序 | 不通过 | 目标顺序未满足。当前先 `BindRuntime` cancel 运行时并触发 `runnerActor.StopAllAgents()`，其中 agent kill 与 `SessionManager.Remove()` 交织发生；随后才有 leftover `CloseAll()`、订阅解绑、`bus` stop、`db` close。 |
| 7. signal 处理 | 部分通过 | desktop 只有 Fx `app.Done()` 这一条仓内 signal 路径；headless 同时有 Fx `app.Done()` 与 `RunGroup` signal actor，入口不唯一。 |
| 8. 超时保护 | 不通过 | `app.Start/Stop(context.Background())` 没有 outer timeout，`RunGroup` 也没有 timeout。`ShutdownTimeout` 常量未接线。 |
| 9. panic 防护 | 部分通过 | event subscriber 只有走 `ResilientSubscribe` 的路径会 recover；runner、rpc serve、provider read loop 仍会把 panic 直接打到进程级。 |
| 10. goroutine 泄漏 | 部分通过 | 多数 goroutine 都有退出条件，但依赖外部进程/socket 正常结束，且 app 级 `Stop` 没有 deadline；`RunDesktop()` post-start nil 早退还会留下已启动 app。 |
| 11. 日志 | 部分通过 | subsystem 级日志还可以，app 级 lifecycle 日志明显弱于 V2。缺少统一 shutdown reason、耗时分段与 begin/end 日志。 |
| 12. V2 等价性 | 不通过 | V3 在 DI 与 desktop 双向 quit 上更清晰，但在 shutdown coordinator、signal 统一入口、hard deadline、StopAll timeout/force kill、phase logs 上仍落后于 V2。 |

## 细节说明

### 1. fx 图完整性

- grouped provider 都有消费者：
  - `group:"drivers"`：`internal/provider/claudecli/module.go:21-26`、`internal/provider/codexapp/module.go:26-31` -> `internal/provider/unified/registry.go:15-25`
  - `group:"rpc_handlers"`：`internal/module/{skill,thread,turn,orchestration,workspace}/rpc.go` -> `internal/platform/rpc/module.go:47-49`
  - `group:"runners"`：`internal/app/modules.go:40-48` 的 `AsRPCRunner` + `internal/sidecar/orch/orchestration/module.go:16-23` 的 `NewRunnerActor` -> `internal/app/runner.go:23-24,38-40`
- singleton provider 里，`newThreadOrchestrationFacade` 也不是悬空：`internal/app/thread_orchestration_adapter.go:14-16` 被 `thread.NewService` 消费：`internal/module/thread/service.go:46-57`
- 明确的 dead code：
  - `internal/ui/wails/runner.go:16-35` 没有 caller，也没有被 `uiwails.Module` 注册
- 测试覆盖缺口：
  - `internal/archtest/fx_graph_test.go:11-15` 只做 `fx.ValidateApp(app.Module)`；没有验证 `newFXApp()` 的 `fx.Invoke(BindRuntime)`，也没有验证 `newDesktopFXApp()` 的 `uiwails.Module`

### 2. optional 依赖

- `thread.NewService` 的 5 个核心依赖都被标成 optional：`internal/module/thread/module.go:9-16`
- 运行时行为：
  - `threadStore == nil`：`List/Get/SetName/Delete/persistThreadState` 都会报 `"thread store is not configured"`，见 `internal/module/thread/service.go:121-181`
  - `bindingStore == nil`：`resolveBinding` 报错，`closeSessionIfActive` 与 `setBindingArchived` 退化，见 `internal/module/thread/service.go:183-256`
  - `sessions == nil`：`resolveSession` / `lookupSession` 报错，见 `internal/module/thread/service.go:213-240`、`internal/module/thread/lifecycle.go:231-235`
  - `starter == nil`：`Start/Resume` 直接失败，见 `internal/module/thread/lifecycle.go:204-230`
  - `orchestration == nil`：`launchAgent`/`recoverAgent` 变 no-op，见 `internal/module/thread/lifecycle.go:298-324`
- `turn.NewTurnHandlers` 的 `resolver` 和 `capResolver` 也是 optional：`internal/module/turn/module.go:11-14`
- 运行时行为：
  - `resolver == nil`：`turn/start`、`turn/steer`、`turn/interrupt`、`turn/forceComplete` 全部报 `"turn rpc: session resolver is not configured"`，见 `internal/module/turn/rpc.go:20-30`
  - `capResolver == nil`：capability gate 必然拒绝，因为 `CapabilitySet.Has()` 对 nil 返回 false，见 `internal/platform/rpc/handler.go:70-96`、`internal/dto/provider/capability.go:30-35`
- 结论：当前 full app 不会真的注入 nil，但 optional 标注让这些能力从“图构建期错误”变成了“运行期错误/静默 no-op”。

### 3. Run() headless

- `Run()` -> `runApp(NewApp())`：`internal/app/app.go:25-27`
- `runApp()` 调用链：
  - `app.Start(context.Background())`
  - `<-app.Done()`
  - `app.Stop(context.Background())`
  - 证据：`internal/app/app.go:60-65`
- `BindRuntime` 的 outgoing call hierarchy 直接指向 `RunGroup` 与 `Shutdowner.Shutdown`，见 LSP `call_hierarchy` 验证与源码：`internal/app/runner.go:28-70`
- 但这条链当前没有 caller，真实运行面仍是 0。

### 4. RunDesktop()

- `RunDesktop()` 的 outgoing call hierarchy 已确认：
  - `newDesktopFXApp`
  - `app.Start`
  - `watchFXShutdown`
  - `wailsApp.Run`
  - `app.Stop`
  - 证据：LSP `call_hierarchy` + `internal/app/app.go:29-50`
- 正向 shutdown：
  - frontend quit -> `WailsLifecycle.ShouldQuit/OnShutdown` -> `requestBackendShutdown()` -> `fx.Shutdowner.Shutdown()`
- 反向 shutdown：
  - backend runtime error / backend done -> `NotifyBackendFailed()` -> `quitAllowed=true` + `Quit()`
- 这条闭环已经成立。
- 唯一显式缺口：
  - `app.Start()` 成功之后若 `wailsApp == nil` 或 `lifecycle == nil`，函数直接返回错误，不会 `app.Stop()`：`internal/app/app.go:35-43`

### 5. runner nil 返回

- `BindRuntime` 的关键语义现在是“`RunGroup` 一旦返回，永远触发 Fx shutdown”：
  - 先写入 `done`
  - 再按需记错误 / 通知 frontend
  - 最后无条件 `_ = p.Shutdowner.Shutdown()`
  - 证据：`internal/app/runner.go:37-53`
- `internal/ui/wails/runner.go:20-35` 的 nil guard 仍在，但它不影响当前主路径，因为 `NewRunner` 没被提供进 `group:"runners"`。

### 6. graceful shutdown 顺序

- desktop 比 headless 多一个 `uiwails.bindEventBridge` hook：`internal/ui/wails/module.go:120-134`
- 因而 desktop 的逆序大致是：
  - `BindRuntime`
  - `uiwails.bindEventBridge`
  - `registerSessionShutdown`
  - `registerTurnLifecycle`
  - `rpc.bindEventBridge`
  - `bus.registerLifecycle`
  - `db.registerLifecycle`
- `db` 在 `bus` 之后关闭，不是之前。
- `session close` 不是完整独立阶段；它一部分已经在 `StopAllAgents -> removeSession` 中提前发生，一部分才在 `CloseAll` 兜底。

### 7. signal 处理

- headless：
  - Fx `app.Done()` 收 `SIGINT/SIGTERM`
  - `RunGroup` signal actor 也收 `SIGINT/SIGTERM`
- desktop：
  - `watchFXShutdown` 通过 `app.Done()` 启动 Fx signal receiver
  - `RunGroup` signal actor 被禁用
- 结论：
  - desktop 的 signal 入口在仓内是单一路径
  - headless 不是

### 8. 超时保护

- app-level lifecycle timeout：没有
- run-group timeout：没有
- shutdown phase timeout：没有
- 现有 timeout 都是局部组件自己的，不是外层生命周期的 SLA

### 9. panic 防护

- 目前最有价值的保护是 `ResilientSubscribe`
- 但 live runner / provider goroutine 无 recover 仍是显著缺口
- 如果要达到 V2 等价，至少要把 runtime main loop 与 provider read loop 纳入统一 panic 策略

### 10. goroutine 泄漏

- 已看到的主要 goroutine 与退出条件：
  - `watchFXShutdown`：`app.Done()` 或 `stop` channel，见 `internal/app/app.go:78-88`
  - `BindRuntime`：`RunGroup` 返回，`OnStop` 等 `done`，见 `internal/app/runner.go:29-69`
  - `runnerActor` waiter：`cmd.Wait()` 返回；shutdown 时由 `StopAllAgents` kill 进程，见 `internal/sidecar/orch/orchestration/runner_actor.go:48-60`
  - turn watch goroutine：`handle.Done()` 或 `trackerTTL`，见 `internal/module/turn/service.go:173-203`
  - rpc serveConn goroutine：连接 ctx 结束，见 `internal/platform/rpc/server.go:103-125`
  - claude transport wait goroutine：进程退出，见 `internal/provider/claudecli/transport.go:60-63,137-143`
  - codex read loop：`ctx.Done()` 或 transport close，见 `internal/provider/codexapp/session_readloop.go:29-56`、`internal/provider/codexapp/transport.go:112-227`
- 剩余风险主要来自“没有 app-level deadline”而不是“完全没有退出路径”。

### 11. 日志

- 已有的关键日志：
  - `db pool ready / closed`：`internal/platform/db/module.go:30-37`
  - `rpc server listening`：`internal/platform/rpc/server.go:77-90`
  - `runtime exited`：`internal/app/runner.go:44-49`
  - `handler panic`：`internal/platform/bus/resilient.go:18-21`
  - session close failure：`internal/provider/unified/session.go:76-99`
  - approval callback interruption：`internal/platform/rpc/approval.go:207-218`
- 缺失：
  - `Run()` / `RunDesktop()` 没有 start/stop begin/end 日志
  - 没有统一 shutdown reason
  - 没有像 V2 那样的 phase elapsed 日志：`go-agent-v2/cmd/agent-terminal/main.go:181-219`

### 12. V2 等价性

- V2 明确具备而 V3 尚未补齐的能力：
  - signal coordinator + cleanup：`go-agent-v2/cmd/agent-terminal/main_setup.go:137-183`
  - 10s hard shutdown deadline：`go-agent-v2/cmd/agent-terminal/main.go:134-143`
  - 3s post-shutdown safety exit：`go-agent-v2/cmd/agent-terminal/main.go:151-155`
  - 5s `StopAll` timeout + force kill：`go-agent-v2/cmd/agent-terminal/app_helpers.go:304-319`
  - shutdown phase logs：`go-agent-v2/cmd/agent-terminal/main.go:189-219`
- V3 已有但 V2 没有这么清晰的部分：
  - `fx` 模块化 DI 装配：`internal/app/modules.go:23-44`
  - `BindRuntime` 统一 runners 与 shutdown：`internal/app/runner.go:28-70`
  - desktop 双向 quit 闭环：`internal/ui/wails/lifecycle.go:82-173`
- 结论：V3 还不能宣称“启停能力与 V2 等价”；最多能说“desktop 主路径已闭环，但 shutdown hardening 仍明显弱于 V2”。

## 最终判断

- `fx DI`：能跑，但存在较多悬空 provider，图不够收敛。
- `应用生命周期`：desktop 主链路已经闭环；headless 主链路尚未真正接入发布入口。
- `优雅关闭`：已有骨架，但顺序、超时、signal 去重、panic 策略、日志分段都还没达到 V2 水平。

## 互审

### 1. 对 `docs/plans/迁移/cap-store-resilience.md` 的批判

1. `统一错误包装 = 3 / 19` 的结论分母取法偏粗，会放大严重性。LSP 回证：`internal/store/module.go:28-49` 确实注册了 19 个 store module，但对 `internal/store/agentstatus/contract.go:9`、`internal/store/sharedfile/contract.go:8`、`internal/store/tasktrace/contract.go:9` 做 `references(compact)` 时，只命中各自 `store.go` 实现，没有应用侧消费者。把这些 dormant store 和 live store 放在同一分母里，不足以直接推出“全仓统一错误包装不通过”的严重度。
2. `V2 store 等价性` 段把 repo API 差异直接等同为能力缺失，论证跨层。LSP 回证：V3 虽没有独立 `AgentThreadBindingStore`，但 thread 模块已在更上层组合出一部分旧 binding 能力，例子包括 `internal/module/thread/archive.go:5-20` 的 archived 维护、`internal/module/thread/service.go:102-119` 的 binding 删除、`internal/module/thread/history.go:22-45` 与 `internal/module/thread/lifecycle.go:137-169` 的 binding-assisted session/history/recover。更准确的结论应是“repo 形状不等价”，而不是直接把 module/service 层能力也判没了。
3. `级联删除` 段把 workspace orphan 风险写成当前 reachable bug，风险分级偏高。LSP 回证：`internal/store/workspace/contract.go:9-19` 没有任何 delete API，`internal/module/workspace/rpc.go:13-23` 也没有 `workspace/run/delete` 路由。当前 shipped path 只能 create/get/list/status/update/merge/abort；因此这里首先是 schema debt 和未来扩展风险，而不是现有入口可以稳定触发的线上缺陷。

### 2. 对 `docs/plans/迁移/cap-wails-desktop.md` 的批判

1. 总结里把 PB1/PB2 写成“已修”，范围只覆盖了 happy path，没覆盖异常路径和 outer timeout。LSP 回证：`internal/app/app.go:29-50` 中 `RunDesktop()` 在 `app.Start()` 成功后，如果 `wailsApp == nil` 或 `lifecycle == nil` 会直接 `return error`，不会 `app.Stop()`；同一函数还把 `ctx := context.Background()` 同时用于 `Start`/`Stop`，没有桌面主链路的外层 deadline。结论应收窄为“正常路径 wiring 已闭环”。
2. `ShouldQuit` 判“通过”偏乐观。LSP 回证：`internal/ui/wails/lifecycle.go:124-135` 的 `activeAgentCount()` 在计数失败时只记 warning 并返回 `0`；`internal/ui/wails/lifecycle.go:82-99` 的 `ShouldQuit()` 随即走“无活跃 agent”分支直接 `requestBackendShutdown()`。这意味着统计失败会 fail-open 跳过 overlay，与“保护活跃 agent”目标并不等价。
3. `窗口配置` 条目标题写“通过”，但正文自己已经承认 drop 行为未等价，结论层级前后不一致。LSP 回证：V3 `internal/ui/wails/window.go:11-33` 只有 `EnableFileDrop: true` 和 `NewWithOptions(...)`，没有 `OnWindowEvent(...)`、也没有 `files-dropped` 事件外发；按行为面审查，这更像 `部分通过`，不能只因窗口尺寸和 flag 到位就判全通过。

### 3. 对 `docs/plans/迁移/cap-thread-lifecycle.md` 的批判

1. `thread/stop` 一节把“缺专用 RPC”与“没有 public stop effect”混在一起，表述偏重。LSP 回证：虽然 `internal/module/thread/rpc.go:18-84` 里没有 `thread/stop`，但 `internal/module/thread/archive.go:5-13` 和 `internal/module/thread/service.go:102-119` 都会调用 `closeSessionIfActive()`；而 provider 的 `Session.Close()` 本身就是真 stop path，见 `internal/provider/codexapp/session.go:207-216` 与 `internal/provider/claudecli/session.go:238-281`。更准确的批判应是“缺统一 dedicated stop 语义”，不是“公共停止能力基本缺失”。
2. `thread/start` 一节把“没有 thread 生命周期事件”当作 start 链路不闭环的证据，论证层级错位。LSP 回证：当前对外 bridge 本来就只转发 `agentdto.StateChanged`、`turndto.TurnStarted`、`turndto.TurnCompleted`，见 `internal/platform/rpc/push.go:75-92` 和 `internal/ui/wails/bridge.go:42-64`。也就是说，仓内现有外部事件模型就不是“thread 专属事件模型”；这更像产品/契约缺口，不宜直接算作 `thread/start` 生命周期链路自身未闭环。
3. `并发安全` 一节把 `SessionManager` 单槽替换写成普遍风险，blast radius 说大了。LSP 回证：普通 `thread/start` 在未显式传 `AgentID` 时会自动生成新的 `agent-*` ID，见 `internal/module/thread/lifecycle.go:171-187`；只有同 `agentID` 的路径才会命中 `internal/provider/unified/session.go:30-45` 的 replace-and-`ForceStop` 语义。真正更尖锐的热点是 `internal/module/thread/lifecycle.go:122-131` 的 `Fork()` 明确复用 `binding.AgentID` 且 `updateBinding=false`，以及 `internal/module/thread/lifecycle.go:189-229` 的 `Resume()` 需要显式 `AgentID`。报告应把风险范围收窄到 resume/shared-agent/fork，而不是泛化为所有 `thread/start`。
