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
| `TestRunnerActorOwnership` | 禁止 module lifecycle 里 worker.Start/Stop，除 RunnerModule 本体 | 0/2 | 可带 worker 迁移 allowlist | 0 allowlist 或精确例外 |
| `TestShutdownOrdering` | root OnStop 顺序：ctx cancel → run.Group wait → bus dispatcher stop → fx resource close | 1 | F-1 失败 | PASS |
| `TestSessionPrivateRuntimeAllowlist` | session-private runtime goroutine 例外清单精确表达 | 3 | 宽松探测 | 精确 allowlist |

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

## 3. `TestRunnerActorOwnership`

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

### 5.1 例外清单字段

每个 session-private runtime 例外必须写明：

| 字段 | 要求 |
|---|---|
| `file` | 精确到文件，不允许目录 |
| `function` | 精确到函数 / method，不允许整文件 |
| `owner` | 谁 cancel / drain / wait |
| `lifetime` | session / request / process；必须不是隐式永久 |
| `stop_signal` | ctx / channel / close primitive |
| `drain` | 是否可 wait；不可 wait 必须有 bounded 说明 |
| `why_not_runner` | 为什么不能进入 RunnerModule |

### 5.2 禁止项

- `internal/app/app.go`、`internal/app/runner.go`、`internal/module/*/module.go` 整文件豁免。
- “历史原因”“P22 已接受”作为豁免理由。
- 没有 drain owner 的 `runtimesafe.SafeGo(context.Background(), ...)`。

### 5.3 Pass / Fail

- **PASS**：session-private runtime 例外都是最小、可解释、可回收的；新增 goroutine 默认必须进 RunnerModule。
- **FAIL**：任一 broad allowlist、任一无 owner/drain 的 session-private goroutine、任一 module lifecycle 新增 long-running worker。

## 6. 与 P22 gate 的边界

- P22 gate 继续覆盖原 Findings 1-10 的实现回归；P22.1 gate 覆盖横切 §10.30 owner 语义。
- P22.1 不把现存 11 条违例标为 P22 BLOCK；它们是 P22.1 子任务 的输入债。
- 若 P22 gate 与 P22.1 gate 对同一文件同时命中，优先按 P22.1 owner 语义修复，但不得删除 P22 已验证的 fail-closed / drain / SSRF / hidden-contract 约束。
