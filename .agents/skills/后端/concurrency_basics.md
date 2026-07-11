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
package rpc

import (
    "context"

    platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
    "github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
    "go.uber.org/fx"
)

// 错误示范：野生 goroutine
// go func() { worker.Run(context.Background()) }()

// 正确示范：被 RunGroup 托管
type Worker struct{}

func (w *Worker) Run(ctx context.Context) error {
    <-ctx.Done()
    return nil
}

func ProvideWorker() fx.Option {
    return fx.Provide(
        fx.Annotate(
            func() platformrunner.Runner { return &Worker{} },
            fx.ResultTags(`group:"runners"`),
        ),
    )
}
```

### 2. 桥接 fx 与 RunGroup

在 V3 架构中，`fx` 负责构造对象，根入口聚合 `group:"runners"` 并启动 `platformrunner.RunGroup`。业务模块只提供 runner，不直接持有全局 group。

```go
type RuntimeParams struct {
    fx.In
    Runners []platformrunner.Runner `group:"runners"`
}

fx.Invoke(func(lc fx.Lifecycle, params RuntimeParams) {
    runCtx, cancel := context.WithCancel(context.Background())
    lc.Append(fx.Hook{
        OnStart: func(context.Context) error {
            safego.Go(runCtx, nil, "app.runtime.rungroup", func(context.Context) {
                _ = platformrunner.RunGroup(runCtx, params.Runners, platformrunner.GroupOptions{})
            })
            return nil
        },
        OnStop: func(context.Context) error {
            cancel()
            return nil
        },
    })
})
```

---

## 内部并发原语 (Goroutine, Channel, Mutex)

在组件内部或短生命周期请求中，依然使用标准并发原语。

### Goroutine 与 Channel

```go
ch := make(chan int)       // 无缓冲 - 同步
ch := make(chan int, 100)  // 有缓冲 - 异步 (直到满)

func computeAndSend(ch chan int, x, y int) {
    ch <- x + y
}

func main() {
    ch := make(chan int)
    go computeAndSend(ch, 1, 2)
    v1 := <-ch  // 阻塞直到结果可用
}
```

### Select 多路复用与超时控制

**MUST 将 Context 作为多路复用的第一防线。**

```go
func DoSomething(ctx context.Context) error {
    select {
    case result := <-resultCh:
        return result
    case <-ctx.Done(): // 父级取消
        return ctx.Err()
    case <-time.After(5 * time.Second): // 超时控制
        return errors.New("timeout")
    }
}
```

### WaitGroup 任务屏障

适用于 `Fan-Out` (扇出) 短期任务等待。

```go
var wg sync.WaitGroup
for i := 0; i < 10; i++ {
    wg.Add(1)
    go func(id int) {
        defer wg.Done() // 必须保证执行
        // 执行工作
    }(i)
}
wg.Wait()
```

### Mutex 保护内部状态

适用于高频同步的内存状态保护，避免 channel 带来的性能损耗。

```go
var (
    cache   map[string]interface{}
    cacheMu sync.RWMutex
)

func Get(key string) interface{} {
    cacheMu.RLock()
    defer cacheMu.RUnlock()
    return cache[key]
}

func Set(key string, value interface{}) {
    cacheMu.Lock()
    defer cacheMu.Unlock()
    cache[key] = value
}
```

---

## 经典并发模式参考

### Worker Pool

对于高频次请求处理，避免无限创建 goroutine。

```go
func handle(queue <-chan *Request) {
    for r := range queue {
        process(r)
    }
}

func Serve(clientRequests <-chan *Request, quit <-chan bool) {
    for i := 0; i < MaxOutstanding; i++ {
        go handle(clientRequests)
    }
    <-quit
}
```
