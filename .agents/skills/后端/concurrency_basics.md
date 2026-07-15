# Go 并发基础与 V3 引擎托管模式

> **加载条件**: 启动后台任务、托管长跑 goroutine、使用 channel/context 同步时加载。

---

## V3 核心契约：`internal/platform/runner` (RunGroup)

> [!IMPORTANT]
> **V3 严禁在 `fx.Provide`、HTTP Handler 或单例初始化中随意启动野生 `go func()`。**
> 所有的长生命周期组件（如监听器、后台轮询、事件分发队列）MUST 注入 Fx `group:"runners"`，由 `internal/platform/runner.RunGroup` 统一托管。

### 1. Runner / Context 模型

每一个需要后台运行的组件，都必须实现阻塞式 `Run(ctx context.Context) error`；`ctx` 取消后应尽快退出并返回真实错误。进程入口负责聚合 runner 并调用 `platformrunner.RunGroup`。

```go
package worker

import (
    "context"

    platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
    "go.uber.org/fx"
)

// 错误示范：野生 goroutine
// go func() { worker.Run(context.Background()) }()

// 正确示范：被 RunGroup 托管
type Worker struct{}

func (w *Worker) Run(ctx context.Context) error {
    <-ctx.Done()
    return ctx.Err()
}

var Module = fx.Module("worker",
    fx.Provide(
        fx.Annotate(
            func() *Worker { return &Worker{} },
            fx.As(new(platformrunner.Runner)),
            fx.ResultTags(`group:"runners"`),
        ),
    ),
)
```

### 2. 根生命周期归入口所有

业务模块只提供 runner，不复制根桥接代码，也不自行聚合全局 group：

- 桌面/后台共享根入口使用`internal/app.BindRuntime`。它从 RootCtx 派生运行 context，在`OnStop`取消后等待 RunGroup，最后执行 drain，并用`errors.Join`保留 runner 与收尾错误。
- `cmd/mcp-lsp`、`cmd/mcp-orch`和`cmd/mcp-ida`使用各自入口内的`bindRuntime`，但同样必须等待退出并保留 RunGroup 错误。
- `RunGroup`返回意味着根 runtime 已结束；入口必须把结果交给 shutdown/退出路径，禁止丢弃返回错误。
- 不得把`context.Background()`作为模块长跑任务的正常父 context；使用入口传入的根 context。

当前精确实现和测试入口：`internal/app/runner.go`、`internal/app/runner_test.go`、`docs/架构/skeleton-rungroup.md`。

---

## 组件内部并发

短生命周期并发可以使用 channel、`sync.WaitGroup`或 mutex，但必须有明确 owner、上限、取消和等待路径。生产 goroutine 如需 panic 保护和统一观测，使用仓库当前的`internal/platform/runtimesafe.SafeGo`模式；不得通过匿名 goroutine 逃逸 RunGroup 托管。

### Context 与超时

```go
func DoSomething(ctx context.Context) error {
    timer := time.NewTimer(5 * time.Second)
    defer timer.Stop()

    select {
    case result := <-resultCh:
        return result
    case <-ctx.Done():
        return ctx.Err()
    case <-timer.C:
        return errors.New("timeout")
    }
}
```

### 有界扇出

启动数量必须有上限；所有分支都要`Done`，调用方必须等待或在取消路径回收。

```go
var wg sync.WaitGroup
for i := range min(len(items), maxWorkers) {
    wg.Add(1)
    go func(id int) {
        defer wg.Done()
        process(ctx, items[id])
    }(i)
}
wg.Wait()
```

### 共享状态

- mutex 适合单 owner 的小型内存状态；锁范围必须有上限，禁止持锁调用网络、RPC 或未知 callback。
- channel 必须明确发送方、关闭方和缓冲容量；接收方不得假设其他组件会关闭自己不拥有的 channel。
- runner 内创建的 ticker、listener、subscription 和子 goroutine必须在`ctx.Done()`路径停止并等待。
- 并发面使用仓库登记的 race 计划验证，不以一次无`-race`测试替代并发证据。
