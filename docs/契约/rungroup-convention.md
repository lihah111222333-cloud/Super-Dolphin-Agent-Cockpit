# Platform Runner / RunGroup 契约（快速参考）

> 当前实现事实源：`internal/platform/runner`、`internal/app/runner.go`、`docs/架构/skeleton-rungroup.md`和同包测试。本文只保留调用方必须遵守的稳定契约。

## 1. Runner 契约

业务模块只实现并提供阻塞式 Runner：

```go
type Runner interface {
    Run(ctx context.Context) error
}

type GroupOptions struct {
    EnableSignals bool
}
```

- `Run(ctx)`必须在`ctx.Done()`后尽快返回。
- runner 返回的第一个非取消根因会触发其他 runner 统一取消，并由 RunGroup 返回。
- panic 由 runner 层保护逻辑转换为错误，禁止在组件内自行 recover 后吞掉。
- 没有 runner 是配置错误，RunGroup 必须 Fail-Fast。
- runner 不拥有进程根 context，也不自行创建第二个根 RunGroup。

## 2. Fx 接线

业务模块在自己的`module.go`中把实现注册到`group:"runners"`：

```go
var Module = fx.Module("worker",
    fx.Provide(
        fx.Annotate(
            newWorker,
            fx.As(new(platformrunner.Runner)),
            fx.ResultTags(`group:"runners"`),
        ),
    ),
)
```

Fx 负责构造和收集 Runner，`internal/platform/runner.RunGroup`负责并发启动、统一取消、等待和错误传播。构造器、handler 和 singleton 初始化不得启动未受托管的长跑 goroutine。

## 3. 根生命周期 owner

### 桌面/后台共享根

`internal/app.BindRuntime`是共享根生命周期 owner：

1. 启动前注册 pre-drain。
2. 从 RootCtx 派生可取消的 run context。
3. 在受保护 goroutine 中启动 RunGroup。
4. RunGroup 返回时记录结果并请求 Fx shutdown。
5. `OnStop`取消 run context，等待 RunGroup 结束，再执行 drain。
6. 非主动取消错误与 drain 错误通过`errors.Join`保留。

业务模块不得复制这段根桥接代码。

### MCP sidecar

`cmd/mcp-lsp`、`cmd/mcp-orch`和`cmd/mcp-ida`各自在入口内拥有本进程的`bindRuntime`。sidecar 可以按入口责任选择`EnableSignals`，但仍必须：

- 保存并传播 RunGroup 返回值；
- 在停止路径取消并等待；
- 保证 listener、ticker、subscription 和子 goroutine被回收；
- 不丢弃 RunGroup 返回错误，也不把“只 cancel、不等待”当成正常实现。

## 4. RunGroup 语义

```go
err := platformrunner.RunGroup(ctx, runners, platformrunner.GroupOptions{
    EnableSignals: false,
})
if err != nil && !errors.Is(err, context.Canceled) {
    return err
}
```

- 任意 runner 返回、父 context 取消或已启用的信号触发时，RunGroup 取消共享 context。
- RunGroup 等待所有 runner 返回后再返回。
- 主动取消可以返回`context.Canceled`；真实组件错误不得被取消错误覆盖。
- 桌面根当前由 Fx 生命周期统一收尾，因此传`EnableSignals: false`；sidecar 以各自源码为准。

## 5. 反模式

- 在 module、constructor 或 handler 中自行调用根 RunGroup。
- 使用`context.Background()`切断入口 RootCtx。
- 忽略 RunGroup 返回值，或只依赖日志观察失败。
- `OnStop`只调用 cancel，不等待 runner 和子资源退出。
- 在 runner 内启动没有 owner、上限、取消或 join 路径的 goroutine。
- 把一次性初始化任务放进 RunGroup；初始化失败应在 Fx 构造/启动阶段直接返回。

## 6. 验证

- 聚焦 runner：`./scripts/test_with_guard.sh ./internal/platform/runner -count=1`
- 桌面生命周期：`./scripts/test_with_guard.sh ./internal/app -count=1`
- sidecar 生命周期：运行对应`cmd/mcp-*`包测试
- 架构与未受控 goroutine规则：`./scripts/test_with_guard.sh ./internal/archtest -count=1`，必要时追加`make guard`
