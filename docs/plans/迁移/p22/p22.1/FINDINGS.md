# P22.1 FINDINGS：§10.30 三层分工 11 处违例（P22 R10 deferred）

> **归属声明**：本子任务是 P22 R10 FINAL 阶段显式 deferred 的架构违规遗留，源自：
> - `docs/plans/迁移/p22/JUDGEMENT_R8_QA.md §R10.6` 代码层 deferred 债总账
> - `docs/plans/迁移/p22/JUDGEMENT_R8_QC.md §7` 契约本体 deferred 债
> - §10.30 三层分工铁律（2026-04-22 P22 R8 新教训）
>
> 不是独立新 lane，而是 P22 的子任务收口。文档按 P22 体系延续。

> 本文只做规划与证据归档，不改代码。
> 规则来源：`docs/1/会话习惯.md §10.30`：`fx.Module` 只做 constructor + resource open/close；`BusModule` 管 `bus.subscribers`；`RunnerModule` 管长跑 actor；shutdown 顺序为 `ctx cancel → run.Group 全退 → bus 关停 subscribers → fx.OnStop 释放资源`。
> 总览见 [`README.md`](README.md)，执行 DAG 见 [`DAG.md`](DAG.md)，gate 见 [`GATE_CONTRACTS.md`](GATE_CONTRACTS.md)。

## 1. 证据口径

每条 finding 记录三类 LSP 证据：

- **caller**：`lsp_xref(references)` 或 `call_hierarchy(incoming)` 证明该函数确实被 fx graph / runtime 入口消费。
- **consumer**：函数内部实际消费的 worker / subscriber / shutdown primitive。
- **lifecycle owner**：当前 owner 落在 `fx.Lifecycle` / `fx.Invoke` / root bridge，而非 BusModule / RunnerModule。

## 2. Findings 逐条

### F-1 — root bridge shutdown 顺序错位

- **位置**：`internal/app/runner.go:71-80`
- **违反 §10.30 子项**：shutdown 顺序
- **现状描述**：`BindRuntime.OnStop` 先执行 `ExtractionDrainer.DrainPendingExtraction(ctx)`，随后才 `cancel()` root run context，再等待 `done`。这把 module/worker drain 放在 `ctx cancel → run.Group 全退` 之前。
- **目标态**：先 cancel root ctx，等待 `platformrunner.RunGroup` 退出，再进入 bus dispatcher stop / fx resource close；memory extraction drain 若仍需要，应由 Runner/Bus owner 或资源 close 阶段明确归位。
- **xref 证据**：
  - caller：`lsp_xref(references)` 显示 `BindRuntime` 被 `internal/app/app.go:133` `newFXApp`、`internal/app/app.go:154` `newDesktopFXApp`、`internal/app/modules_graph_test.go:46`、`internal/app/runner_test.go:58` 使用。
  - call_hierarchy：`BindRuntime <- newFXApp/newDesktopFXApp/TestAppModuleGraphIsClosed/TestBindRuntimeDrainsExtractionBeforeCancel`。
  - consumer：`internal/app/runner.go:52` 调 `platformrunner.RunGroup`；`runner.go:72-77` 先 drain 再 cancel。
  - lifecycle owner：`internal/app/runner.go:46-86` root `fx.Hook`。
- **归属 Phase**：1

#### 反向测试证据（2026-04-25 HEAD 修订）

现有测试 `TestBindRuntimeDrainsExtractionBeforeCancel` 明确验证了“先 `ExtractionDrainer.DrainPendingExtraction`，再 `cancel()` root ctx”的行为；这与 `runner.go:71-80` 当前实现一致。但按 §10.30 铁律，正确 shutdown 顺序应为 `ctx cancel → group.Wait → bus.Stop → resource close`。因此该测试不是保护目标态，而是反向锁定了错误顺序；P22.1 Phase 1 实施时必须同步改代码与改测试，避免旧测试继续把 F-1 错序当作回归保护。

### F-2 — desktop shutdown watcher 使用 `context.Background()`

- **位置**：`internal/app/app.go:171-181`
- **违反 §10.30 子项**：shutdown 顺序 / root bridge 例外边界
- **现状描述**：`watchFXShutdown` 用 `runtimesafe.SafeGo(context.Background(), ...)` 监听 `app.Done()`，生命周期不受 root ctx 约束，仅靠返回的 `stop` channel 退出。
- **目标态**：若保留桌面辅助 watcher，必须受明确 owner ctx 或 root bridge 例外精确 allowlist 约束；不得成为新 watcher 复制模板。
- **xref 证据**：
  - caller：`lsp_xref(references)` 显示 `watchFXShutdown` 在 `internal/app/app.go:122` `RunDesktop` 中被调用。
  - consumer：`app.go:173` `SafeGo(context.Background())`，`app.go:175` 消费 `app.Done()`。
  - lifecycle owner：非 fx hook，但 owner 为 `RunDesktop` 局部 stop channel，未进入 RunnerModule。
- **归属 Phase**：1 / 3

### F-3 — memory hooks 直接 Start/Stop worker

- **位置**：`internal/module/memory/module.go:386-443`
- **违反 §10.30 子项**：runner actor / bus subscriber
- **现状描述**：`registerMemoryHooks` 在 module `fx.Invoke` 中构造 `autoDreamScheduler`、`nestedIngestWorker`、`teamSyncCoordinator`，并在 `OnStart` 直接 `Start()`；`OnStop` 调 `drainMemoryHooks` 后取消 subscriptions。
- **目标态**：scheduler / nested / teamSync 均成为 RunnerModule actor；bus callbacks 只 enqueue；subscriptions 由 BusModule wiring。
- **xref 证据**：
  - caller：`lsp_xref(references)` 显示 `module.go:254` 的 `fx.Invoke(... registerMemoryHooks)` 消费该函数，测试位于 `module_test.go:201` 与 `team_sync_lifecycle_test.go:86`。
  - call_hierarchy：`registerMemoryHooks <- init(fx.Module memory)`，另有两个测试 caller。
  - consumer：`module.go:425/428/431` 分别 `scheduler.Start()`、`nested.Start()`、`teamSync.Start()`；`module.go:437-438` drain 后 cancel subscriptions。
  - lifecycle owner：`module.go:422-442` `p.Lifecycle.Append(fx.Hook{OnStart, OnStop})`。
- **归属 Phase**：2

#### overlay 澄清（2026-04-25 HEAD 修订）

§7.5.1 overlay 中 “`registerMemoryHooks.OnStop` 已 drain” 已销账的是 drain **行为**（`drainMemoryHooks` 已存在），不是 ownership。F-3 讨论的是 `fx.OnStart`/`fx.OnStop` 仍直接 Start/Stop scheduler、nested、teamSync worker，并在 lifecycle 中持有 subscription owner；该 ownership 违背 §10.30，二者正交。因此 F-3 仍 in-scope，不是重开已销账的 “OnStop 不 wait/drain” 旧债。

### F-4 — thread bus workers 由 module lifecycle 承担

- **位置**：`internal/module/thread/module.go:57-89`
- **违反 §10.30 子项**：runner actor / bus subscriber
- **现状描述**：`registerSubscriptions` 在 `OnStart` 调 `svc.startBusWorkers()` 后注册 `registerThreadSubscriptions(svc)`；`OnStop` 取消订阅后 `svc.stopBusWorkers(ctx)`。
- **目标态**：thread bus workers 进入 RunnerModule；thread subscriptions 进入 BusModule；module lifecycle 仅做资源 open/close。
- **xref 证据**：
  - caller：`lsp_xref(references)` 显示 `thread.Module` 在 `module.go:54` 通过 `fx.Invoke(registerSubscriptions)` 消费该函数。
  - consumer：`module.go:69` `svc.startBusWorkers()`；`module.go:70` `registerThreadSubscriptions(svc)`；`module.go:85` `svc.stopBusWorkers(ctx)`。
  - lifecycle owner：`module.go:64-88` `p.Lifecycle.Append`。
- **归属 Phase**：2

### F-5 — cachekeepalive timer + subscription 混在 lifecycle

- **位置**：`internal/platform/cachekeepalive/module.go:25-50`
- **违反 §10.30 子项**：bus subscriber / runner actor
- **现状描述**：`registerKeepaliveLifecycle` 在 `OnStart` 调 `startKeepaliveRelay`，该 relay 同时承担 dispatcher subscription 与 keepalive/timer 行为；`OnStop` 先 cancel 再 `Manager.Shutdown(ctx)`。
- **目标态**：subscription 归 BusModule；timer / manager drain 归 RunnerModule 或明确 resource close；lifecycle 不直接启动 relay。
- **xref 证据**：
  - caller：`lsp_xref(references)` 显示 `cachekeepalive.Module` 在 `module.go:22` `fx.Invoke(registerKeepaliveLifecycle)` 消费该函数。
  - consumer：`lsp_inspect(definition)` 从 `module.go:37` 跳到 `relay.go:14-47 startKeepaliveRelay`，证明该 OnStart 启动 relay。
  - lifecycle owner：`module.go:35-49` `lc.Append(fx.Hook)`。
- **归属 Phase**：2

#### overlay / out-of-scope 澄清（2026-04-25 HEAD 修订）

R10.6 #5 / §7.5.1 overlay 处理的是 TeamSync Pull/Push test-only API 收敛，不包含 `internal/platform/cachekeepalive`。F-5 的 cachekeepalive relay/timer 属于独立平台 lifecycle ownership 问题：subscription 与 timer/manager drain 仍混在 `fx.Hook` 中，按 F-3 同理保留为 P22.1 in-scope。

### F-6 — hooks hookDispatchWorker 由 fx 启停

- **位置**：`internal/platform/hooks/module.go:78-109`
- **违反 §10.30 子项**：runner actor / bus subscriber
- **现状描述**：`registerEventRelayLifecycle` 构造 `newHookDispatchWorker`，在 `OnStart` 调 `worker.Start()` 并 `startEventRelay`，在 `OnStop` cancel 后 `worker.Stop(ctx)`。
- **目标态**：`hookDispatchWorker` 进入 RunnerModule；event relay subscriptions 进入 BusModule。
- **xref 证据**：
  - caller：`lsp_xref(references)` 显示 `hooks.Module` 在 `module.go:24` `fx.Invoke(registerEventRelayLifecycle)` 消费该函数。
  - consumer：`lsp_inspect(definition)` 从 `module.go:91` 跳到 `dispatch_worker.go:65-76 newHookDispatchWorker`；`module.go:95/103` 直接 Start/Stop。
  - lifecycle owner：`module.go:93-108` `lc.Append(fx.Hook)`。
- **归属 Phase**：2

### F-7 — rpc pushNotificationWorker 由 fx 启停

- **位置**：`internal/platform/rpc/module.go:136-170`
- **违反 §10.30 子项**：runner actor / bus subscriber
- **现状描述**：`bindEventBridge` 构造 `newPushNotificationWorker`，`OnStart` 调 `worker.Start()` 后 `subscribeCoreEventPushes`；`OnStop` cancel subscriptions 后 `worker.Stop(ctx)`。
- **目标态**：push worker 进入 RunnerModule；core event pushes 由 BusModule 注册。
- **xref 证据**：
  - caller：`lsp_xref(references)` 显示 `rpc.Module` 在 `module.go:42` `fx.Invoke(bindEventBridge)` 消费该函数。
  - call_hierarchy：`bindEventBridge <- init(fx.Module platform.rpc)`。
  - consumer：`lsp_inspect(definition)` 从 `module.go:148` 跳到 `push_worker.go:77-92 newPushNotificationWorker`；`module.go:153/164` 直接 Start/Stop。
  - lifecycle owner：`module.go:151-169` `lc.Append(fx.Hook)`。
- **归属 Phase**：2

### F-8 — mcpcontrol configFanoutWorker 由 fx 启停

- **位置**：`internal/platform/mcpcontrol/module.go:162-197`
- **违反 §10.30 子项**：runner actor / bus subscriber
- **现状描述**：`registerConfigChangeLifecycle` 构造 `newConfigFanoutWorker`，`OnStart` `worker.Start()` 后 `registerConfigChangeSubscriptions`，`OnStop` cancel 后 `worker.Stop(ctx)`。
- **目标态**：config fanout worker 进入 RunnerModule；config change subscriptions 进入 BusModule。
- **xref 证据**：
  - caller：`lsp_xref(references)` 显示 `mcpcontrol.Module` 在 `module.go:39` `fx.Invoke(registerConfigChangeLifecycle)` 消费该函数。
  - consumer：`lsp_inspect(definition)` 从 `module.go:176` 跳到 `config_fanout_worker.go:76-91 newConfigFanoutWorker`；`module.go:180/191` 直接 Start/Stop。
  - lifecycle owner：`module.go:178-196` `lc.Append(fx.Hook)`。
- **归属 Phase**：2

### F-9 — toolbridge subscriber ownership 分散

- **位置**：`internal/platform/toolbridge/module.go:166-184`
- **违反 §10.30 子项**：bus subscriber
- **现状描述**：`registerDiffFallbackLifecycle` 在 `OnStart` 直接 `platformbus.ResilientSubscribe(dispatcher, tracker.handleToolCallEnd, ...)`，`OnStop` cancel；虽然 proxy serve 已由 `ProxyRunner` 承担，但 diff fallback subscriber 仍散在 module lifecycle。
- **目标态**：diff fallback subscriber 进入 BusModule；module lifecycle 只做 listener open / address publish 这类资源 wiring。
- **xref 证据**：
  - caller：`lsp_xref(references)` 显示 `toolbridge.Module` 在 `module.go:50` 消费 `registerDiffFallbackLifecycle`。
  - call_hierarchy：`registerDiffFallbackLifecycle <- init(fx.Module platform.toolbridge)`。
  - consumer：`module.go:173` 直接 `platformbus.ResilientSubscribe`；`module.go:177-180` cancel。
  - lifecycle owner：`module.go:171-183` `lifecycle.Append(fx.Hook)`。
- **归属 Phase**：2

#### out-of-scope 澄清（2026-04-25 HEAD 修订）

F-9 只处理 diff fallback subscriber ownership：`registerDiffFallbackLifecycle` 在 `OnStart` 直接 `platformbus.ResilientSubscribe`，subscriber owner 散落在 toolbridge module lifecycle。它与 Z-B toolbridge hidden contract / handler fallback（`handler.go` flag fallback、fail-closed 语义）正交；后者已由 P22 主线 commit 10 或独立 hidden-contract lane 覆盖，本 P22.1 不改 handler fallback。

### F-10 — insight collector subscriber 仍在 module lifecycle

- **位置**：`internal/module/insight/module.go:55-70`
- **违反 §10.30 子项**：bus subscriber
- **现状描述**：`registerCollectorLifecycle` 在 `OnStart` 调 `p.Collector.subscribe(p.Dispatcher, p.Logger)`，`OnStop` cancel。虽然 `Flusher` 已经用 `flusherAsRunner` 进入 `group:"runners"`，collector subscriber owner 仍在 module lifecycle。
- **目标态**：collector subscribe 进入 BusModule；flusher runner 保持 RunnerModule ownership。
- **xref 证据**：
  - caller：`lsp_xref(references)` 显示 `insight.Module` 在 `module.go:31` `fx.Invoke(registerCollectorLifecycle)` 消费该函数。
  - consumer：`lsp_inspect(definition)` 从 `module.go:61` 跳到 `collector.go:49-69 subscribe`；`module.go:65` cancel。
  - lifecycle owner：`module.go:59-69` `p.Lifecycle.Append(fx.Hook)`。
- **归属 Phase**：2

### F-11 — turn observation subscriber 仍在 lifecycle

- **位置**：`internal/module/turn/observation/module.go:43-59`
- **违反 §10.30 子项**：bus subscriber
- **现状描述**：`RegisterSubscribers` 在 `OnStart` 直接 `Subscribe(p.Dispatcher, p.Contract, p.Logger)`，`OnStop` cancel。subscriber owner 与 module lifecycle 绑定。
- **目标态**：observation subscriber 进入 BusModule；module 只提供 `Memory` / `Contract`。
- **xref 证据**：
  - caller：`lsp_xref(references)` 显示 `observation.Module` 在 `module.go:29` `fx.Invoke(RegisterSubscribers)` 消费该函数。
  - call_hierarchy：`RegisterSubscribers <- init(fx.Module module.turn.observation)`。
  - consumer：`lsp_inspect(definition)` 从 `module.go:50` 跳到 `subscribers.go:25-44 Subscribe`；`module.go:54` cancel。
  - lifecycle owner：`module.go:48-58` `p.Lifecycle.Append(fx.Hook)`。
- **归属 Phase**：2

## 3. Phase 汇总

| Phase | Findings |
|---|---|
| Phase 0 | 无单独 finding；为 F-1~F-11 提供 BusModule / RunnerModule / allowlist gate |
| Phase 1 | F-1, F-2 |
| Phase 2 | F-3, F-4, F-5, F-6, F-7, F-8, F-9, F-10, F-11 |
| Phase 3 | F-2 的例外边界 + session-private runtime allowlist 全仓收紧 |

## 红队仲裁（2026-04-25）
详见 `docs/plans/迁移/p22/p22.1/JUDGEMENT.md` §3 与 §6。
整体裁决：🟢 READY / 🟠 NEEDS-FIX / 🔴 BLOCK（以 JUDGEMENT.md §7 为准）。

## R2 发现仍未销账项（2026-04-25 HEAD drift note）
详见 `docs/plans/迁移/p22/p22.1/JUDGEMENT.md` §R2。R2 仲裁结论：🔴 R2 BLOCK。FINDINGS 仍需只加不删补齐：F-1 的 `internal/app/runner_test.go:79-88` 反向测试保护证据；F-3 非重开 R10.6 #9 “OnStop 不 wait/drain”；F-5 非重开 R10.6 #5 TeamSync Pull/Push test-only；F-9 不覆盖 toolbridge handler fallback。


## 3.1 HEAD `a81554c` overlay：F-1~F-11 当前真实状态（2026-04-25，第 6 轮）

> 本节按 §10.31 只加不删追加；上文 §2 保留为 P22.1 规划/历史证据快照。当前 HEAD 锚点为 `a81554c`；实施链锚点为 `25a37ad` → `f737e45` → `17b5ce7` → `dfe12e6` → `b386217` → `a9a018e` → `a81554c`。

| Finding | 历史原违规位置 | HEAD `a81554c` 目标态位置 | 当前状态 |
|---|---|---|---|
| F-1 root bridge shutdown ordering | `internal/app/runner.go:71-80` | `internal/app/runner.go` 已为 cancel → RunGroup wait → drain；`internal/app/runner_test.go` 已改为 `TestBindRuntimeCancelsRunGroupBeforeDrain` | ✅ 已销账 |
| F-2 desktop watcher `context.Background()` | `internal/app/app.go:171-181` | `watchFXShutdown(ctx, app, lifecycle)` 使用 owner ctx；session-private allowlist 记录 desktop watcher | ✅ 已销账 |
| F-3 memory hooks worker/subscriber ownership | `internal/module/memory/module.go:386-443` | memory workers 进入 `group:"runners"`，subscriptions 进入 `NewMemorySubscribers` / BusModule | ✅ 已销账 |
| F-4 thread bus workers/subscribers | `internal/module/thread/module.go:64-88` | `threadBusWorkersAsRunner` + `NewThreadSubscribers` | ✅ 已销账 |
| F-5 cachekeepalive timer + subscription | `internal/platform/cachekeepalive/module.go:35-49` | `NewCacheKeepaliveSubscribers`；manager shutdown 保留为 resource close | ✅ 已销账 |
| F-6 hooks fanout worker | `internal/platform/hooks/module.go:93-108` | `hookWorkerAsRunner` + `NewHooksRelaySubscribers` | ✅ 已销账 |
| F-7 rpc push worker | `internal/platform/rpc/module.go:151-169` | `pushWorkerAsRunner` + `NewRPCPushSubscribers` | ✅ 已销账 |
| F-8 mcpcontrol config fanout | `internal/platform/mcpcontrol/module.go:162-197` | `configFanoutWorkerAsRunner` + `NewMCPConfigChangeSubscribers` | ✅ 已销账 |
| F-9 toolbridge diff fallback subscriber | `internal/platform/toolbridge/module.go:166-184` | `NewToolbridgeDiffFallbackSubscribers`；proxy lifecycle 只保留 listener setup/address publish | ✅ 已销账 |
| F-10 insight collector subscriber | `internal/module/insight/module.go:55-70` | `NewInsightSubscribers` 进入 BusModule；第 6 轮采信主 agent LSP 证伪，Audit-D two-hop subscription 报告不作为 HEAD 事实 | ✅ 已销账 |
| F-11 turn observation subscriber | `internal/module/turn/observation/module.go:43-59` | `NewObservationSubscribers` 进入 BusModule；module 只提供 Memory/Contract/subscriber spec | ✅ 已销账 |

**剩余说明**：F-1~F-11 主体迁移在 HEAD `a81554c` 均不再按“代码 0”处理；后续只保留 cron+uistate cross-file gap、gate 3 处 NEEDS-FIX、`runner.actors` vs `group:"runners"` 契约命名债等 follow-up。

## 3.2 HEAD `5d6a93c` Round-3 overlay：F-1 shutdown ordering 真修正（2026-04-25）

> 本节按 §10.31 只加不删追加；§3.1 的 HEAD `a81554c` 表格保留为历史 overlay。当前 Round-3 修复基线为 HEAD `5d6a93c`。

- F-1 root shutdown ordering：`internal/app/runner.go` 已从历史的 `cancel → drain → wait` 修正为 `cancel → waitForRuntimeDone → drainRuntimeBeforeStop`；此前文档中“已 cancel→wait→drain”的描述在本轮代码修改后才成立。
- Desktop companion：`internal/app/app.go` 的 `preDrainDesktopRuntime` 已对齐为 `WaitRuntimeDone` 先于 `DrainRuntime`。
- Gate evidence：`internal/archtest/lifecycle_onstart_guard_test.go::TestShutdownOrdering` 现在解析 AST statement 顺序，不再依赖 `strings.Index("<-done")` 文本命中。
- Race/vet evidence：memory nested ingest coalesce 测试、thread event fake binding store、app shutdown watcher goroutine fatal 均作为 Round-3 真 BUG 收口项处理。


## 3.3 HEAD `aa09f58` V3-B 锚点修正 overlay（2026-04-25）

> 本节按 §10.31 只加不删追加；§3.2 的 HEAD `5d6a93c` 记录保留为 Round-3 代码修复基线历史 overlay。V3-B 复核实测当前仓库 `git rev-parse --short HEAD` 为 `aa09f58`，因此当前 Findings HEAD 锚点修正为 `aa09f58`。

- F-1 root shutdown ordering 的事实不变：当前代码为 `cancel → waitForRuntimeDone → drainRuntimeBeforeStop`。
- Desktop companion 的事实不变：`preDrainDesktopRuntime` 为 `WaitRuntimeDone → DrainRuntime`。
- F-10 two-hop 仍按 §3.1 / §R7.3 记录为 LSP 缓存/实验状态误判，不作为 HEAD 事实。
