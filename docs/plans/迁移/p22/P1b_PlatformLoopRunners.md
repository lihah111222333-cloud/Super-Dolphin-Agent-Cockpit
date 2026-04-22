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

### `rpc`

- `bindApprovalLifecycle` 当前同时做两件事：
  - 合法：启动恢复 `restoreActiveApprovals(...)`
  - 违规：`go startApprovalCleanupLoop(...)` 启动长期 cleanup ticker

这两类逻辑应该拆开：恢复留 lifecycle，loop 交 `Runner`。

## 目标架构

### `mcpcontrol`

- 新增 `NewSweeperRunner(sweeper *Sweeper) platformrunner.Runner`
- 删除 `registerSweeperLifecycle`
- `Module` 改为 `Provide(NewSweeper, fx.Annotate(NewSweeperRunner, fx.ResultTags(\`group:"runners"\`)))`

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

### Step 3：保留 shutdown 兜底

虽然 cleanup loop 挪走了，但 `OnStop` 仍保留：

- `shutdownPendingApprovals(...)`
- registry/lease 的 final cleanup

因为这属于资源释放，不是长期运行。

### Step 4：清理 post-construction mutation

如果实现 `mcpcontrol` 时需要顺手处理 `registerHookLifecycle -> setHookLifecycle(...)` 这类 late wiring，应优先改成 constructor 参数或 `fx.In` 组装，避免继续依赖 `Invoke` 做内部状态注入。

## 验收标准

- `internal/platform/mcpcontrol/module.go` 不再在 `OnStart` 中 `go sweeper.Run(...)`
- `internal/platform/rpc/module.go` 不再在 lifecycle/restore 路径中 `go startApprovalCleanupLoop(...)`
- approval restore 仍在应用启动后生效
- sweeper 与 approval cleanup 在 `ctx.Done()` 后可确定停止
- 至少补以下测试：
  - restore 只执行一次
  - cleanup runner 在 cancel 后退出
  - sweeper stop 不遗留 active timer/ticker
  - `go test ./internal/platform/...` 不出现新的 shutdown hang
