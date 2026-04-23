# P1b: 平台长跑 loop 抽 Runner

## 目标

把平台层里“启动恢复之后继续长期运行”的 loop 从 lifecycle 路径上拆下来，交给 `RunnerModule` / `run.Group` 托管，同时把 restore / subscriber wiring 留在 `fx.Module` 侧。本单默认 HEAD 仍**没有** platform runner producer：`mcpcontrol` / `rpc` 现在只是 lifecycle 直启 loop，因此 `P1b` 的完成口径是补出显式 runner producer + tag wiring，而不是把现有 `OnStart` loop 文字上重命名成 runner。

## 对应 findings

- Finding 3: `internal/platform/mcpcontrol/module.go:184-199`
- Finding 4: `internal/platform/rpc/module.go:149-166 + 179-197`

## 现状校准

### `mcpcontrol`

- `registerSweeperLifecycle` 在 `OnStart` 里直接 `go sweeper.Run(ctx)`（HEAD 2026-04-23：函数本体 `internal/platform/mcpcontrol/module.go:184-199`；`fx.Invoke` 注册 `module.go:34`；`go sweeper.Run(ctx)` 起点 `module.go:191`）
- `Sweeper.Run` 是长期 `time.NewTimer(nextInterval()) + jitter` loop，语义上属于引擎层而不是初始化（HEAD 2026-04-23：`internal/platform/mcpcontrol/sweeper.go:61-76`）
- 但 `registerRegistryLifecycle` 和 `registerConfigChangeLifecycle` 仍是 stop cleanup / subscription wiring，不属于本单的 Runner 化目标（HEAD 2026-04-23：`registerRegistryLifecycle` 函数本体 `internal/platform/mcpcontrol/module.go:141-155`，`fx.Invoke` 注册 `module.go:32`；`registerConfigChangeLifecycle` 函数本体 `module.go:157-182`，`fx.Invoke` 注册 `module.go:33`）
- 其中 `registerConfigChangeLifecycle` 虽不在本单做 Runner 化，但它注册出的 bus 回调当前仍直接做 config fanout；这属于 `P2` 的 callback slow-path 遗留点，不应在本单里被误写成“已无问题”（HEAD 2026-04-23：同上 `internal/platform/mcpcontrol/module.go:157-182`；fanout 叙事详见 `P2_BusRuntimeDecoupling.md` 对应切片）

### `rpc`

- `bindApprovalLifecycle` 当前同时做两件事：
  - 合法：启动恢复 `restoreActiveApprovals(...)`（HEAD 2026-04-23：`bindApprovalLifecycle` 函数本体 `internal/platform/rpc/module.go:149-166`，`fx.Invoke` 注册 `module.go:36`；`restoreActiveApprovals` 调用点 `module.go:187`，函数定义 `module.go:199-209`）
  - 违规：`go startApprovalCleanupLoop(...)` 启动长期 cleanup ticker（HEAD 2026-04-23：起点 `internal/platform/rpc/module.go:195`；函数定义 `internal/platform/rpc/approval_lifecycle.go:28-48`）

这两类逻辑应该拆开：startup restore 留在 `fx.Module` 的单次 wiring（旧稿简写为“留 lifecycle”；本轮保留该旧称并以本句更正为准），长期 loop 交 `RunnerModule`。

### `rpc push / eventsurface`

- `push.go` 当前仍在 bus 订阅回调里直接 `broadcastNotifications(...)`，同步走 `NotifyAll/NotifyClient`（HEAD 2026-04-23：callsite `internal/platform/rpc/push.go:60, 85`；`broadcastNotifications` 函数本体 `internal/platform/rpc/factory.go:108-116`；`NotifyAll` 函数本体 `internal/platform/rpc/server.go:279-289`）
- 这属于 callback 直做网络 I/O，不是本单的 loop Runner 问题，而是 `P2` 的 callback slow-path 遗留点

## 目标架构

### `mcpcontrol`

- 新增 `NewSweeperRunner(sweeper *Sweeper) platformrunner.Runner`
- 删除 `registerSweeperLifecycle`
- `Module` 改为 `Provide(NewSweeper, fx.Annotate(NewSweeperRunner, fx.ResultTags(\`group:"runners"\`)))`

`NewSweeperRunner` 必须直接同步执行 `sweeper.Run(ctx)`，不能在 runner 内再 `go` 一层。

### `rpc`

- 保留 `bindApprovalLifecycle` 中的恢复/停止清理
- 新增 `ApprovalCleanupRunner`
- `Runner.Run(ctx)` 内执行 `startApprovalCleanupLoop(...)` 的阻塞版主循环

## 实施方式

- `mcpcontrol` 侧优先直接复用 `Sweeper.Run(ctx)`；本单不重写 cadence，只改变 owner 位置。
- `rpc` 侧明确拆成两层：restore 继续留在 `fx.Module`，cleanup loop 进入 `RunnerModule`；不引入第二套 actor contract。
- `platform` 子模块当前没有现成的 root-level runner wiring 样板；本单默认沿用 `platformrunner.Runner + fx.Annotate(..., fx.ResultTags(...))` 的接线方式，不额外发明第二种平台专用包装器。
- `mcpcontrol` / `rpc` 各自只保留一个长期 owner，禁止“新 runner + 旧 lifecycle goroutine”双轨。
- callback slow-path 仍按 `P2` 记账；`P1b` 不把 fanout / keepalive / push / proxy serve 混写成 loop Runner 问题。
- `mcpcontrol` 与 `rpc` 都要落成明确 producer 形状：`NewSweeperRunner` / `NewApprovalCleanupRunner` 通过 `fx.Annotate(..., fx.ResultTags(\`group:"runners"\`))` 或等价 `fx.Out` 输出；`internal/platform/*` 当前没有这类 producer，可视为本单必须补齐的接线底座。
- `startup restore` 与 `OnConnectUI` replay 不共用“只执行一次”计数：前者只指 app start 阶段的一次恢复，后者继续保留为 connect-time 补恢复路径。
- `HEAD 2026-04-23` 现状里 `registerApprovalRestoreOnConnect(...) -> Server.OnConnectUI(...) -> OnConnect(...) -> addOnConnect(...)` 会立即 replay 当前 active UI；因此实现时必须显式建 gate / 计数，不能把现状误读成“startup restore 与 connect-time replay 已天然隔离”。
- stop/join 语义以 runner 退出为准；`OnStop` 只留 final cleanup，不再让 cancel-only lifecycle 继续冒充 long-running owner。

## 实施步骤

### Step 1：分离“恢复”和“长期清理”

把 `startApprovalLifecycle(...)` 拆成两个概念：

- `restoreApprovalState(...)`
- `ApprovalCleanupRunner.Run(ctx)`

这样 `OnStart` 只做 restore，不再隐式带出 cleanup goroutine。

### Step 2：统一接入 `group:"runners"`

两个 runner 都走现有 `platformrunner.Runner` 接口，不新增第二套 actor contract。

补充要求：

- `mcpcontrol` runner 直接复用现有 `Sweeper.Run(ctx)`，尽量不重写 loop
- `rpc` cleanup runner 直接承担 blocking cleanup loop，避免再次包装成 `Run -> go loop`

### Step 3：保留 shutdown 兜底

虽然 cleanup loop 挪走了，但 `OnStop` 仍保留：

- `shutdownPendingApprovals(...)`
- registry/lease 的 final cleanup

因为这属于资源释放，不是长期运行。

补充要求：

- `mcpcontrol` 要明确 sweeper runner 与 registry final cleanup 的 stop 顺序，避免两个路径同时碰 lease 清理
- `rpc` 要明确 `OnStop` 先停 cleanup runner，再做 `shutdownPendingApprovals(...)`

### Step 4：清理 post-construction mutation

`registerHookLifecycle -> setHookLifecycle(...)` 这类 late wiring 已明确转交 `P0/P4`：`P0` 负责守卫 `fx.Invoke` setter 注入，`P4` 负责隐藏契约/owner contract 的边界整理。本单若不处理，必须在实现报告中引用该转交关系，而不是保留成口头上的“可选顺手处理”。

## 收口口径

- 本页关于 `fx.Module` / `BusModule` / `RunnerModule` 的分工，以 `docs/契约/modularity-convention.md §4.4 / §7`、`docs/契约/fx-convention.md §2/§3`、`docs/契约/rungroup-convention.md §2 / §4` 为准；本单只搬走长期 loop，不改写三层角色本体。
- 本单只搬走长期 loop；startup restore 继续留在 `fx.Module` 的单次 wiring/合法 `fx.Invoke`，connect-time replay/补恢复继续留在业务 owner，subscriber wiring 继续留在 `BusModule`；三者都不被误写成 `RunnerModule`。
- `mcpcontrol` / `rpc` 的 runner 都属于 `RunnerModule` 角色；本单不把 group tag / 契约命名清洗当成前置条件。
- `config fanout`、`cachekeepalive`、`rpc push`、`eventsurface`、`toolbridge proxy` 仍按 `P2` 记账；`P1b` 不为这些 callback slow-path 代签收。
- `startup restore` 只执行一次；`OnConnectUI` 的 replay/补恢复是另一条保留路径，不属于“只执行一次”的计数口径。
- stop 顺序以“root cancel -> runner 退出 -> final cleanup”为准；`OnStop` 的资源释放不等于替代 runner owner。
- 本页只描述 runner slice；README/P2/P3 里的“bus stop-intake / 退订 ≠ drain”继续生效，不能把本页的 stop 简写误读成 bus 阶段已被 runner cleanup 吸收。

## 依赖图（文本）

```text
P0 -> P1b -> P2
P1b -> P4(late wiring / hidden contract cleanup 继续串行记账)
```

## 不在本单闭环的已知遗留点

- `config_change.go` 的 bus 回调直 fanout config notify 归 `P2`
- `cachekeepalive/relay.go` 的 bus 回调直持 keepalive timer/session runtime 归 `P2`
- `push.go` / `eventsurface` 的 callback 直做 RPC notify/network I/O 归 `P2`
- `internal/platform/toolbridge/module.go:130-174` 的 proxy `OnStart -> go ServeProxy(...)` 也属同型 runtime debt，但归 `P2` / Finding 9，不由本单代签收

## 需冻结的兼容语义

### `mcpcontrol`

- 现有 cadence 不是固定 ticker，而是 `time.NewTimer(nextInterval()) + jitter`；Runner 化必须保留首轮延后和每轮重算抖动
- sweep 语义必须保持：`Disconnected` 立即驱逐；超 `timeout` 先标 `Stale`；超 `timeout + staleGrace` 再驱逐
- 停止语义是协作式退出：已进入的 `Sweep()` 与后续 `disconnectLease()` 可以跑完，但不得无界悬挂

### `rpc`

- `restore` 不只发生在 startup，还有 `OnConnectUI` 的补恢复路径；迁移后仍要保留“新 UI 连接进来时重放 pending approvals”
- `restore` 只对 UI peer 生效，startup restore 需要覆盖所有当前 active 的 UI 连接
- 启动顺序必须是“先完成 startup restore，再启动 cleanup runner”
- 成功重发 pending approval 时仍要刷新 TTL，而且仅在实际 `ensureDispatch` 启动成功时刷新
- startup restore 失败仍应中止启动；connect-time restore 继续保持 warn-only

## 可观测 / 回滚约束

- 常量冻结要写成代码真值而不是抽象名词：`DefaultApprovalTimeout = 5 min` 继续受测；本页 live code truth 仍是 `defaultHeartbeatTTL = 30s` / `defaultSweepTick = 5s`，**不是** `heartbeat 5min / TTL 30min`。后者只作为 `P21` cron lease 递延锚点保留（检索锚点：`TTL 30min` / `heartbeat 5min`），不得误写成 P1b 当前实现常量
- `mcpcontrol` / `rpc` runner 至少落地 `runner=start|stop|drain|error|timeout` 结构化日志、`runner_stop_latency_ms` / `runner_drain_seconds` 指标和对应 trace/span；验收时能区分 startup restore、connect-time replay、final cleanup 三个阶段
- 若为了回滚而阶段性保留旧 lifecycle 路径，只能通过显式 feature flag / env opt-in 启用，默认仍是单一 runner owner；回滚卡必须写出 gate carrier、disable steps 与删除时点

### 常量矩阵（代码真值）

| 常量 | 当前真值 | owner | 锚点 |
|---|---|---|---|
| `DefaultApprovalTimeout` | `5 min` | approval restore / cleanup | `internal/platform/rpc/approval_support.go:18-20` |
| `defaultSweepTick` | `5s` | sweeper runner | `internal/platform/mcpcontrol/sweeper.go:12-17` |
| `defaultSweepJitter` | `1s` | sweeper runner | 同上 |
| `defaultHeartbeatTTL` | `30s` | sweeper stale/lease | 同上 |
| `defaultStaleGraceTime` | `5s` | sweeper stale -> evict | 同上 |

### runner lifecycle FSM（本页 authoritative）

| 状态 | owner | 进入 | 退出 |
|---|---|---|---|
| `startup_restore` | lifecycle / restore path | app 启动或 UI reconnect | `runner_active` / `startup_failed` |
| `runner_active` | sweeper / cleanup runner | restore 完成后 runner 接管 | `stopping` |
| `stopping` | root cancel + runner drain | stop 请求 | `drained` / `stop_timeout` |
| `drained` | final cleanup | runner 退出 + final cleanup 完成 | terminal |

- `submitting/submitted/dedupe_key/claim_token` 属 `session-summary` / cron / orchestration 侧 contract，不是 P1b 当前 authoritative FSM；本页只冻结 startup restore / runner active / stopping / drained。

### 最低 observability contract

- `log`：`runner.start`、`runner.stop`、`runner.drain.done`、`runner.timeout`、`startup_restore.begin/end`、`connect_replay.begin/end`
- `metric`：`runner_stop_latency_ms`、`runner_drain_seconds`、`approval_restore_total`、`approval_replay_total`、`sweeper_stale_total`、`sweeper_evict_total`
- `trace`：`mcpcontrol.sweeper.run`、`rpc.approval_cleanup.run`、`rpc.approval_restore`
- timer / jitter / shutdown 测试默认使用 fake clock、deterministic shutdown、禁裸 `time.Sleep`

## 非目标

- 不改判 sweeper cadence、approval timeout、TTL refresh 等业务规则；本单只移动 owner 与 stop 边界。
- 不顺手修 `config_change`、`cachekeepalive`、`rpc push`、`eventsurface`、`toolbridge proxy` 的 callback slow-path；这些继续由 `P2` 负责。
- 不把 `mcpcontrol` / `rpc` 的其余 hidden contract 一次性并入本单。

## TDD 与旧实现清理

- 先补失败测试：`OnStart` 不再直接拉 sweeper/cleanup loop、startup restore 顺序、connect-time replay、timer+jitter cadence、stop 不悬挂。
- 先补失败测试：startup restore 失败仍 startup-fatal；`OnConnectUI` replay 失败继续保持 warn-only。
- 先补失败测试：`Disconnected -> immediate evict`、`timeout -> Stale`、`timeout + staleGrace -> evict` 这三段 sweeper 状态迁移不漂移。
- 测试名固定到可派单级别：`TestSweeperRunnerBlocksUntilContextDone`、`TestSweeperRunnerPreservesJitterAndStaleTransitions`、`TestApprovalCleanupRunnerStartsAfterStartupRestore`、`TestStartupRestoreFailureIsFatal`、`TestOnConnectUIReplayWarnOnly`、`TestRunnerStopDrainsBeforeFinalCleanup`、`TestSweeperHeartbeatUsesCodeTruthTTL30s`
- 验证命令固定写法：`go test ./internal/platform/mcpcontrol/... -run 'Test(SweeperRunnerBlocksUntilContextDone|SweeperRunnerPreservesJitterAndStaleTransitions|RunnerStopDrainsBeforeFinalCleanup|SweeperHeartbeatUsesCodeTruthTTL30s)' -count=1 -v` 与 `go test ./internal/platform/rpc/... -run 'Test(ApprovalCleanupRunnerStartsAfterStartupRestore|StartupRestoreFailureIsFatal|OnConnectUIReplayWarnOnly)' -count=1 -v`
- 修复完成后必须删掉 `registerSweeperLifecycle` 和 `startApprovalLifecycle(...)` 里负责起长期 loop 的旧路径；不能保留“新 runner + 旧 lifecycle goroutine”双轨。
- `mcpcontrol`/`rpc` 若拆出新的 runner 类型，旧的混合入口应同步拆平为纯 restore / pure runner 两层；不接受保留混合入口只是不再调用。
- 若 `registerHookLifecycle -> setHookLifecycle(...)` 被一并整改，旧 late-wiring helper 也要同步删掉或内联，避免留下只服务旧模型的 dead code。

## 验收标准

- `internal/platform/mcpcontrol/module.go` 不再在 `OnStart` 中 `go sweeper.Run(...)`
- `internal/platform/rpc/module.go` 不再在 lifecycle/restore 路径中 `go startApprovalCleanupLoop(...)`
- approval restore 仍在应用启动后生效
- sweeper 与 approval cleanup 在 `ctx.Done()` 后可确定停止
- `mcpcontrol` 的 timer+jitter cadence 与 stale/evict 语义未漂移
- `rpc` 的 startup restore / connect-time restore / TTL refresh 语义未漂移
- `DefaultApprovalTimeout = 5 min` 与本页 live code truth（`defaultHeartbeatTTL = 30s` / `defaultSweepTick = 5s`）有文档与 fake-clock 守卫；`TTL 30min` / `heartbeat 5min` 仅以 `P21` 递延锚点形式保留，不再误写成 P1b 当前实现常量
- start / stop / drain / timeout 已有最低日志 / metric / trace 口径，shutdown 顺序可观测
- `config_change` 与 `cachekeepalive` 若未在本单落地，也已在 `P2` 被显式归口，不再处于无人认领状态
- 至少补以下测试：
  - startup restore 只执行一次；connect-time replay 继续按每次 UI 连接触发
  - cleanup runner 在 cancel 后退出
  - sweeper stop 不遗留 active timer/ticker
  - `go test ./internal/platform/...` 不出现新的 shutdown hang
  - 新 UI 连接后 pending approvals 仍会重放
  - startup restore 先于 cleanup runner 启动
