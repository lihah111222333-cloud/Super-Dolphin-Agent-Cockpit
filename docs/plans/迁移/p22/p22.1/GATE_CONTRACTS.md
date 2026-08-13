# P22.1 archtest gate 硬契约（P22 R10 deferred）

> **归属声明**：本子任务是 P22 R10 FINAL 阶段显式 deferred 的架构违规遗留，源自：
> - `docs/plans/迁移/p22/JUDGEMENT_R8_QA.md §R10.6` 代码层 deferred 债总账
> - `docs/plans/迁移/p22/JUDGEMENT_R8_QC.md §7` 契约本体 deferred 债
> - §10.30 三层分工铁律（2026-04-22 P22 R8 新教训）
>
> 不是独立新 lane，而是 P22 的子任务收口。文档按 P22 体系延续。

> 总览：[`README.md`](README.md)
> Findings：[`FINDINGS.md`](FINDINGS.md)
> DAG：[`DAG.md`](DAG.md)

## 1. archtest gate 总表

| Test | 目标 | 生效 Phase | 初始状态 | 最终状态 |
|---|---|---:|---|---|
| `TestBusSubscriberGroup` | 禁止 module lifecycle / fx.Invoke 内直接 bus Subscribe，除 BusModule 本体 | 0/2 | 可带 11 项临时 allowlist | 0 allowlist 或精确例外 |
| `TestRunnerActorOwnership`（概念名；落地名 `TestRunnerActorGuard/ownership`） | 禁止 module lifecycle 里 worker.Start/Stop，除 RunnerModule 本体 | 0/2 | 可带 worker 迁移 allowlist | 0 allowlist 或精确例外 |
| `TestShutdownOrdering` | root OnStop 顺序：ctx cancel → run.Group wait → bus dispatcher stop → fx resource close | 1 | F-1 失败 | PASS |
| `TestSessionPrivateRuntimeAllowlist` | session-private runtime goroutine 例外清单精确表达 | 3 | 宽松探测 | 精确 allowlist |

### 1.1 现有 archtest 契约映射（2026-04-25 HEAD 修订）

| P22.1 新 gate | 现有 guard_test | 关系 | 落地方式 |
|---|---|---|---|
| `TestBusSubscriberGroup` | `bus_callback_guard_test.go` | 扩展 | 在现有文件加 matcher，复用 bus callback forbidden catalogue，不新建同名重复 guard |
| `TestRunnerActorOwnership` | `runner_actor_guard_test.go`（现名 `TestRunnerActorGuard`） | **改名决策**：扩展现有 matcher，不新建 test 避免重名/语义混淆 | 命名为 `TestRunnerActorGuard/ownership` 子测试；Runner body guard 与 module lifecycle ownership guard 同文件分 subtest |
| `TestShutdownOrdering` | `lifecycle_onstart_guard_test.go` | 扩展（需加 hybrid AST+regex） | 在现有 OnStart/OnStop guard 增 root ordering matcher，识别 cancel/wait/bus stop/resource close 顺序 |
| `TestSessionPrivateRuntimeAllowlist` | 新建 `session_private_allowlist_test.go` | 独立 | 复用 `root_bridge_allowlist.go` 9 字段 schema，不与 root bridge allowlist 混表 |

### 1.2 `t.Skip → live` matcher 清单（2026-04-25 HEAD 修订）

LSP 实测 `lsp_grep "t.Skip" internal/archtest/*_test.go`：多数 directory-not-yet-created skip 属旧目录隔离 guard；P22.1 只接管下列 §10.30 相关 matcher。

| guard | 现状 skip | P22.1 激活 |
|---|---|---|
| `fx_invoke_guard_test.go` | `TestFXInvokeGuard` matcher skeleton `t.Skipf` | Phase 0C warning；Phase 3 fail |
| `lifecycle_onstart_guard_test.go` | `TestLifecycleOnStartGuard` owning-slice matcher skeleton `t.Skipf` | Phase 0C warning 覆盖 F-1/F-3~F-11；Phase 3 `t.Errorf` |
| `runner_actor_guard_test.go` | `TestRunnerActorGuard/actor_run_ctx_auxiliary_goroutines_must_join_on_stop` `t.Skipf` | P22.1 不直接翻旧 P1c skeleton；新增 `TestRunnerActorGuard/ownership` 先 warning 后 fail |
| `bus_callback_guard_test.go` | 注释声明 matcher subtests parked as `t.Skip`，当前已有部分 live matcher | P22.1 扩展 subscriber ownership matcher；不回滚既有 live matcher |

### 1.3 AST/hybrid 防绕过 matcher contract（2026-04-25 HEAD 修订）

- `TestBusSubscriberGroup`：用 `go/ast` walk 识别 `fx.Hook{OnStart: ...}`、`fx.Invoke(func(...) { ... })` 内直接 `Subscribe` / `ResilientSubscribe` / `event.Dispatcher.Subscribe`；对 helper 做 **1-hop** 解析（如 `startEventRelay`、`subscribeCoreEventPushes`、`RegisterSubscribers`），超过一跳 fail-closed 并要求 TODO allowlist。
- `TestRunnerActorGuard/ownership`：必须实现 custom `go/ast` walker + one-hop helper resolver，不得只靠 token grep。AST 识别非 RunnerModule `OnStart` 内 `worker.Start()`、`scheduler.Start()`、`svc.startBusWorkers()`，以及 `OnStop` 内 `worker.Stop(ctx)` / `svc.stopBusWorkers(ctx)` / 业务 drain；regex 仅作兜底，覆盖 `\bgo\s+\w+\.(Start|Run|Begin|Serve|Loop|Watch)\(` 与 `\b\w+\.(Start|Run|Begin|Serve|Loop|Watch)\(`，防 Start 改名绕过。
- `TestShutdownOrdering`：hybrid AST+regex。AST 确认 `BindRuntime` root hook 内 call sequence；文本/fixture 回退验证 `cancel` 先于 `RunGroup` wait，wait 先于 bus stop/resource close；检测 cancel 前 `DrainPendingExtraction` 作为 fail。
- FuncLit 递归：`fx.Invoke(func(...) { go func() { ... }() })`、`fx.Hook{OnStart: func(...) { go func(){...}() }}` 内部 goroutine 必须递归识别；不能只扫顶层 CallExpr。

## 2. `TestBusSubscriberGroup`

### 2.1 禁止项

- 在非 BusModule 本体的 `fx.Invoke` target 中直接调用：
  - `event.Dispatcher.Subscribe`
  - `platformbus.ResilientSubscribe`
  - 包装后仍直接订阅的 helper，例如 `startEventRelay`、`subscribeCoreEventPushes`、`registerThreadSubscriptions`、`Subscribe(...)`。
- 在 `fx.Hook.OnStart` 中完成 subscriber 注册并在 `OnStop` 中手写 cancel。
- 在业务 module 中同时拥有 subscription cancel list 与 worker drain list。

### 2.2 允许项

- BusModule / subscriber group 本体：负责收集 declarative subscriber specs 并统一注册。
- 测试 fixture：必须放在 test-only allowlist，并附目标测试名。
- root bridge 不自动豁免 bus subscriber；root bridge 只豁免 RunGroup 汇总桥接。

### 2.3 首批命中应覆盖

- F-3 `internal/module/memory/module.go:433` `registerLifecycleSubscriptions(...)`
- F-4 `internal/module/thread/module.go:70` `registerThreadSubscriptions(svc)`
- F-5 `internal/platform/cachekeepalive/module.go:37` `startKeepaliveRelay(...)`
- F-6 `internal/platform/hooks/module.go:96` `startEventRelay(...)`
- F-7 `internal/platform/rpc/module.go:154` `subscribeCoreEventPushes(...)`
- F-8 `internal/platform/mcpcontrol/module.go:181` `registerConfigChangeSubscriptions(...)`
- F-9 `internal/platform/toolbridge/module.go:173` `platformbus.ResilientSubscribe(...)`
- F-10 `internal/module/insight/module.go:61` `p.Collector.subscribe(...)`
- F-11 `internal/module/turn/observation/module.go:50` `Subscribe(...)`

### 2.4 Pass / Fail

- **PASS**：所有非 BusModule subscriber 均通过 subscriber group 注册；业务 module 不保存 cancel list。
- **FAIL**：任一非 BusModule `OnStart` 直接订阅 bus，或用 wrapper 隐藏直接订阅。

## 3. `TestRunnerActorOwnership`（落地为 `TestRunnerActorGuard/ownership`）

### 3.0 改名/扩展决策（2026-04-25 HEAD 修订）

不新建顶层 `TestRunnerActorOwnership`：HEAD 已有 `runner_actor_guard_test.go` / `TestRunnerActorGuard`，再新建相近顶层 test 会造成 Runner body guard 与 module lifecycle worker ownership guard 双重命名混淆。按 R3 drift fix note（GATE_CONTRACTS.md:149-150）顺承，P22.1 在现有 `TestRunnerActorGuard` 下新增子测试 `TestRunnerActorGuard/ownership`，负责识别 module lifecycle 直接 Start/Stop worker；原 `actor_run_ctx_*` 子测试继续覆盖 Run(ctx) 内 fire-and-forget goroutine。

### 3.1 禁止项

- 非 RunnerModule 本体的 `fx.Hook.OnStart` 中直接调用 `worker.Start()` / `svc.startBusWorkers()` / `scheduler.Start()`。
- 非 RunnerModule 本体的 `fx.Hook.OnStop` 中直接调用 `worker.Stop(ctx)` / `svc.stopBusWorkers(ctx)` / 业务 worker drain。
- `run.Group` actor 内 fire-and-forget 新业务 goroutine，除非 session-private allowlist 精确表达。

### 3.2 允许项

- RunnerModule / `group:"runners"` provider：把 worker adapter 成 `platformrunner.Runner`。
- 资源 open/close：listener open、DB close、registry lease cleanup 这类非长跑 worker 的资源 lifecycle。
- P22 P3 已完成的 process owner 模式可作为目标参考，但不自动豁免其它 actor。

### 3.3 首批命中应覆盖

- F-3 `scheduler.Start` / `nested.Start` / `teamSync.Start` in `memory/module.go:425-431`
- F-4 `svc.startBusWorkers` / `svc.stopBusWorkers` in `thread/module.go:69/85`
- F-5 `startKeepaliveRelay` in `cachekeepalive/module.go:37`
- F-6 `worker.Start` / `worker.Stop` in `hooks/module.go:95/103`
- F-7 `worker.Start` / `worker.Stop` in `rpc/module.go:153/164`
- F-8 `worker.Start` / `worker.Stop` in `mcpcontrol/module.go:180/191`

### 3.4 Pass / Fail

- **PASS**：所有长跑 worker 都由 RunnerModule 提供为 runner；module lifecycle 不再负责业务 worker 启停。
- **FAIL**：任一 module lifecycle 仍拥有业务 worker Start/Stop 或长期 goroutine drain。

## 4. `TestShutdownOrdering`

### 4.1 硬顺序

root OnStop 必须满足：

```text
1. cancel root runtime ctx
2. wait run.Group / all runners exit
3. stop bus dispatcher / stop subscriber intake / cancel subscriber group
4. close fx resources
```

### 4.2 禁止项

- 在 root ctx cancel 前执行业务 drain，如当前 F-1 的 `ExtractionDrainer.DrainPendingExtraction(ctx)`。
- 使用 `context.Background()` 创建 root-equivalent watcher 而不受 root owner 管控。
- 把 bus 退订误当 run.Group drain；两者必须有顺序边界。

### 4.3 Pass / Fail

- **PASS**：`internal/app/runner.go` 的 root bridge AST / fixture 能证明 cancel 先于 wait，wait 先于 bus/fx close；桌面 watcher 例外有精确 allowlist。
- **FAIL**：cancel 前存在 worker drain、或者 OnStop 未等待 `RunGroup` 退出就进入 resource close。

## 5. `TestSessionPrivateRuntimeAllowlist`

### 5.1 例外清单字段（9 字段，2026-04-25 HEAD 修订）

每个 session-private runtime 例外必须对标 `internal/archtest/root_bridge_allowlist.go` 的 9 字段，不再使用旧 7 字段。9 字段为：

| 字段 | 要求 |
|---|---|
| `DefinitionPath` | 精确到定义文件，不允许目录 |
| `CallSitePath` | 精确到 fx.Invoke / OnStart / SafeGo 调用文件；可与 DefinitionPath 相同但不可省略 |
| `Symbol` | 精确到函数 / method / runner 类型，不允许整文件 |
| `BridgeShape` | `session_private_runtime` / `session_reader` / `session_health` 等枚举，必须说明 goroutine shape |
| `ExceptionClass` | `temporary` 或 `permanent`；默认 temporary |
| `Reason` | 为什么不能立即进入 RunnerModule / BusModule |
| `RemoveWhen` | 明确回收条件；禁止写“以后再说” |
| `RollbackWhen` | 触发回滚的观测条件 |
| `RollbackAction` | 回滚时删除哪条 allowlist / 改回哪种 owner |

#### codexapp SessionRuntime 例外样例（字段名必须逐项出现）

| DefinitionPath | CallSitePath | Symbol | BridgeShape | ExceptionClass | Reason | RemoveWhen | RollbackWhen | RollbackAction |
|---|---|---|---|---|---|---|---|---|
| DefinitionPath=`internal/provider/codexapp/session_runtime.go` | CallSitePath=`internal/provider/codexapp/session_runtime.go` | Symbol=`(*SessionRuntime).Start` | BridgeShape=`session_private_runtime` | ExceptionClass=`temporary` | SessionRuntime 已有 per-session owner/join 语义，但尚未统一进 RunnerModule adapter | RemoveWhen=`SessionRuntime exposes platformrunner.Runner adapter and P1c runtime tests cover reader/health/recovery join` | RollbackWhen=`new unjoined goroutine appears under (*SessionRuntime).Start` | RollbackAction=`remove allowlist entry and require runner adapter before merge` |
| DefinitionPath=`internal/provider/codexapp/session_runtime.go` | CallSitePath=`internal/provider/codexapp/session_runtime.go` | Symbol=`(*SessionRuntime).spawnReader` | BridgeShape=`session_reader` | ExceptionClass=`temporary` | reader goroutine 由 readerDone/readerMu join，当前属于 session-private lifetime | RemoveWhen=`reader loop is represented as child runner or explicit bounded worker` | RollbackWhen=`readerDone is not awaited in shutdown tests` | RollbackAction=`fail TestSessionPrivateRuntimeAllowlist and move reader loop to RunnerModule` |
| DefinitionPath=`internal/provider/codexapp/session_runtime.go` | CallSitePath=`internal/provider/codexapp/session_runtime.go` | Symbol=`(*SessionRuntime).runHealthLoop` | BridgeShape=`session_health` | ExceptionClass=`temporary` | health goroutine 与 session lifetime 绑定但不应成为 process-wide actor | RemoveWhen=`health loop becomes declarative session runner with cancel/wait contract` | RollbackWhen=`health goroutine escapes session cancel` | RollbackAction=`drop allowlist and require bounded drain test` |
| DefinitionPath=`internal/provider/codexapp/session_runtime.go` | CallSitePath=`internal/provider/codexapp/session_runtime.go` | Symbol=`(*SessionRuntime).runRecoveryWorker` | BridgeShape=`session_recovery` | ExceptionClass=`temporary` | recovery worker is session-private and coalesces recovery signals under SessionRuntime owner | RemoveWhen=`recovery worker becomes explicit RunnerModule child runner with bounded drain` | RollbackWhen=`recovery worker can outlive SessionRuntime Stop` | RollbackAction=`drop allowlist and require runner adapter / shutdown test` |

#### 5.1.1 9 字段完整性校验规则

`DefinitionPath` 为空、目录/glob、无法 `os.Stat`、或无法与 `Symbol` 做 AST 定位均 fail；`DefinitionPath` 与 `CallSitePath` 即使路径相同也必须双字段填写；新增项必须同时填写 `RemoveWhen`；`DefinitionPath` 漂移按 §10.43 标旧值→新值，否则视为 broad allowlist。

### 5.2 禁止项

- `internal/app/app.go`、`internal/app/runner.go`、`internal/module/*/module.go` 整文件豁免。
- “历史原因”“P22 已接受”作为豁免理由。
- 没有 drain owner 的 `runtimesafe.SafeGo(context.Background(), ...)`。

### 5.3 Pass / Fail

- **PASS**：session-private runtime 例外都是最小、可解释、可回收的；新增 goroutine 默认必须进 RunnerModule。
- **FAIL**：任一 broad allowlist、任一无 owner/drain 的 session-private goroutine、任一 module lifecycle 新增 long-running worker。

## 6. 契约命名债：`runner.actors` deferred（2026-04-25 HEAD 修订）

R8_QC §7 明确：`docs/契约/*` 中仍存在 `runner.actors` vs `group:"runners"` 命名债。本 P22.1 GATE 的 active 实现 tag 一律使用 HEAD 现实装配 `group:"runners"`；`runner.actors` 只作为角色术语/契约本体 deferred 债登记，不在本 lane 修改 `docs/契约/*`。后续应单开契约命名统一 lane，裁定 `RunnerModule` 角色术语与 `group:"runners"` 实现 tag 的二层映射。

## 7. 与 P22 gate 的边界

- P22 gate 继续覆盖原 Findings 1-10 的实现回归；P22.1 gate 覆盖横切 §10.30 owner 语义。
- P22.1 不把现存 11 条违例标为 P22 BLOCK；它们是 P22.1 子任务 的输入债。
- 若 P22 gate 与 P22.1 gate 对同一文件同时命中，优先按 P22.1 owner 语义修复，但不得删除 P22 已验证的 fail-closed / drain / SSRF / hidden-contract 约束。

## 红队仲裁（2026-04-25）
详见 `docs/plans/迁移/p22/p22.1/JUDGEMENT.md` §5。
整体裁决：🟢 READY / 🟠 NEEDS-FIX / 🔴 BLOCK（以 JUDGEMENT.md §7 为准）。

## R2 发现仍未销账项（2026-04-25 HEAD drift note）
详见 `docs/plans/迁移/p22/p22.1/JUDGEMENT.md` §R2。R2 仲裁结论：🔴 R2 BLOCK。GATE_CONTRACTS 主体仍需只加不删补齐 P22.1 与现有 archtest 改动映射、`t.Skip → live` 表、TestShutdownOrdering AST/text hybrid、防绕过 matcher contract（direct + nested FuncLit + one-hop helper；超过一跳 fail-closed/TODO allowlist）、session-private allowlist 的 root bridge 9 字段基座，以及 `group:"runners"` active / `runner.actors` deferred docs debt 说明。

## R3 drift fix note（2026-04-25）
详见 `docs/plans/迁移/p22/p22.1/JUDGEMENT.md` §R3。R3 复查确认：R2 drift note 已覆盖多数 Gate 未销账项，但对 `TestRunnerActorOwnership` 与现有 `TestRunnerActorGuard` 的关系仍偏泛化；后续主体修订必须明确二选一：新建 ownership guard，或扩展 `TestRunnerActorGuard` 的 matcher，并写清 Runner body guard 与 module lifecycle worker ownership guard 的职责边界。
