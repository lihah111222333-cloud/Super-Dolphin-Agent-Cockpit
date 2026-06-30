# V3 骨架：`fx` + platform runner

## 1. 核心定位

```text
fx                    = 工厂（造出零件，接好线路）
internal/platform/runner = 引擎（所有长跑组件一起转，一个停全部停）
```

当前源码中业务模块不直接暴露底层 runner 库；统一入口是 `platformrunner.Runner` 与 `platformrunner.RunGroup`。

## 2. 三阶段生命周期

### Phase 1: `fx Build + Start`

- 解析 `internal/platform/config.Config`。
- 创建 `Bus`、SQLite `*sql.DB`、Store/sqlc、RPC Server、Provider driver、ToolBridge 等组件。
- 执行 `fx.Lifecycle.OnStart`：SQLite migration、schema/baseline 校验、订阅注册等初始化。

### Phase 2: `platformrunner.RunGroup`

- Fx 通过 `group:"runners"` 收集所有实现 `Run(ctx) error` 的组件。
- `internal/app.BindRuntime` 在 OnStart 中启动 RunGroup。
- 任意 runner、父 context 或可选信号结束时，RunGroup 统一取消其余 runner。

### Phase 3: `fx Stop`

- 取消 runtime context，等待 RunGroup 退出。
- 执行 memory extraction drain 等 pre-drain。
- 关闭 SQLite `*sql.DB`、事件订阅、外部连接和缓存。

## 3. Runner 契约

```go
package runner

type Runner interface {
    Run(ctx context.Context) error
}

type GroupOptions struct {
    EnableSignals bool
}
```

```go
package app

type RunnerResult struct {
    fx.Out
    Runner platformrunner.Runner `group:"runners"`
}
```

说明：

- 统一接口只允许一个运行入口：`Run(ctx) error`。
- `Run` 必须响应 `ctx.Done()`。
- 错误必须返回给 RunGroup，不得只打日志后继续伪装成功。

## 4. 当前主入口骨架

```go
func Run(ctx context.Context) error {
    owner := newAppOwnerContext(ctx)
    var app *application.App

    fxApp := fx.New(
        app.Module,
        fx.Provide(func() RootCtxProvider { return owner }),
        fx.Populate(&app),
    )

    if err := fxApp.Start(owner.RootContext()); err != nil {
        return err
    }
    <-owner.runtimeDone
    return fxApp.Stop(context.Background())
}
```

桌面模式通过 `RunDesktop` 叠加 Wails lifecycle，但 runtime runner 仍由 `BindRuntime` 统一托管。

## 5. 模块分工示例

### `db.Module`

- 提供 SQLite `*sql.DB`。
- 启动时执行 migration、schema version 和 baseline table/column 校验。
- 停止时关闭数据库连接。

### `store.Module`

- 从 `*sql.DB` 创建 `*sqlc.Queries`。
- 提供 `sqlc.Querier` 和各 store 子模块。

### `rpc.Module`

- 提供 `*rpc.Server`、push bridge、approval manager。
- 聚合 `HandlerMapResult`。
- `internal/app.AsRPCRunner` 将 `*rpc.Server` 接入 `group:"runners"`。

### `runner` phase

- `internal/app.BindRuntime` 调用 `platformrunner.RunGroup`。
- `cmd/mcp-orch`、`cmd/mcp-lsp`、`cmd/mcp-ida` 的 standalone 图也复用该 RunGroup。

## 6. 六条红线

1. 禁止在 `fx.Provide` constructor 里启动 goroutine、监听端口或永久阻塞。
2. 禁止在 `fx.OnStart` 里跑无限循环；OnStart 只做初始化和启动 runtime group。
3. 禁止绕过 `Runner` 接口手工维护启动列表。
4. 禁止在 RPC handler 里直接写 SQL、直接改状态字段或直接起后台任务。
5. 禁止用 runner 托管一次性初始化任务或短生命周期函数。
6. 禁止业务代码直接混用全局单例和依赖注入对象。

## 7. V2 → V3 对比

| 维度 | V2 | V3 |
| --- | --- | --- |
| 组件创建 | 手写 wiring 较多 | `fx` 模块化装配 |
| goroutine 管理 | 各处自起 goroutine | platform runner 统一托管 |
| 状态机 | `switch/case` + 分散判断 | `internal/platform/statemachine` 配置化 |
| 事件总线 | 字符串事件 + 动态 payload | typed DTO + `bus` + `eventsurface` |
| RPC | 入口与业务耦合偏重 | `HandlerMapResult` + `rpc.Server` |
| SQL | 手写查询层 | SQLite + `sqlc` generated queries |

## 8. 集成结论

- `fx` 解决“零件从哪里来、如何接线”。
- platform runner 解决“零件如何一起跑、如何一起停”。
- 两者固定顺序：`fx Build+Start -> platformrunner.RunGroup -> fx Stop`。
