# P1b: 平台长跑 loop 抽 Runner

## 目标

把平台层里“启动恢复之后继续长期运行”的 loop 从 lifecycle 路径上拆下来，交给 `run.Group` 托管。

## 对应 findings

- Finding 3: `internal/platform/mcpcontrol/module.go:184-197`
- Finding 4: `internal/platform/rpc/module.go:149-196`

## 现状校准

### `mcpcontrol`

- `registerSweeperLifecycle` 在 `OnStart` 里直接 `go sweeper.Run(ctx)`
- `Sweeper.Run` 是长期 ticker loop，语义上属于引擎层而不是初始化
- 但 `registerRegistryLifecycle` 和 `registerConfigChangeLifecycle` 仍是 stop cleanup / subscription wiring，不属于本单的 Runner 化目标
- 其中 `registerConfigChangeLifecycle` 虽不在本单做 Runner 化，但它注册出的 bus 回调当前仍直接做 config fanout；这属于 `P2` 的 callback slow-path 遗留点，不应在本单里被误写成“已无问题”

### `rpc`

- `bindApprovalLifecycle` 当前同时做两件事：
  - 合法：启动恢复 `restoreActiveApprovals(...)`
  - 违规：`go startApprovalCleanupLoop(...)` 启动长期 cleanup ticker

这两类逻辑应该拆开：恢复留 lifecycle，loop 交 `Runner`。

### `rpc push / eventsurface`

- `push.go` 当前仍在 bus 订阅回调里直接 `broadcastNotifications(...)`，同步走 `NotifyAll/NotifyClient`
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

## 不在本单闭环的已知遗留点

- `config_change.go` 的 bus 回调直 fanout config notify 归 `P2`
- `cachekeepalive/relay.go` 的 bus 回调直持 keepalive timer/session runtime 归 `P2`
- `push.go` / `eventsurface` 的 callback 直做 RPC notify/network I/O 归 `P2`

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

## TDD 与旧实现清理

- 先补失败测试：`OnStart` 不再直接拉 sweeper/cleanup loop、startup restore 顺序、connect-time replay、timer+jitter cadence、stop 不悬挂。
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
- `config_change` 与 `cachekeepalive` 若未在本单落地，也已在 `P2` 被显式归口，不再处于无人认领状态
- 至少补以下测试：
  - restore 只执行一次
  - cleanup runner 在 cancel 后退出
  - sweeper stop 不遗留 active timer/ticker
  - `go test ./internal/platform/...` 不出现新的 shutdown hang
  - 新 UI 连接后 pending approvals 仍会重放
  - startup restore 先于 cleanup runner 启动
