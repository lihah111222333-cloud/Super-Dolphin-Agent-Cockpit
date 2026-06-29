# Go 并发基础与 V3 引擎托管模式

> **加载条件**: 启动后台任务、托管长跑 goroutine、使用 channel/context 同步时加载。

---

## V3 核心契约：`oklog/run` (RunGroup)

> [!IMPORTANT]
> **V3 严禁在 `fx.Provide`、HTTP Handler 或单例初始化中随意启动野生 `go func()`。**
> 所有的长生命周期组件（如监听器、后台轮询、事件分发队列）MUST 作为独立 Actor 被 `oklog/run` 统一托管。

### 1. Execute / Interrupt 二元模型

每一个需要后台运行的组件，都必须提供一个执行函数（阻塞）和一个中断函数（触发退出）。

```go
package rpc

import (
    "net"
    "github.com/oklog/run"
)

// 错误示范：野生 goroutine
// go func() { ln.Accept() }()

// 正确示范：被 RunGroup 托管
func setupServer(g *run.Group, ln net.Listener) {
    g.Add(
        func() error {
            // execute: 必须是阻塞的
            _, err := ln.Accept()
            return err
        },
        func(err error) {
            // interrupt: 触发 execute 退出
            _ = ln.Close()
        },
    )
}
```

### 2. 桥接 fx 与 RunGroup

在 V3 架构中，`fx` 负责构造对象，`run.Group` 负责启动。

```go
// 统一的 Runner 接口
type Runner interface {
    Run(ctx context.Context) error
}

func newWorker() Runner {
    return &myWorker{}
}

// 在入口处的 fx.Invoke 中统一装配给 run.Group
fx.Invoke(func(lc fx.Lifecycle, workers []Runner) {
    var g run.Group
    ctx, cancel := context.WithCancel(context.Background())

    for _, w := range workers {
        worker := w
        g.Add(
            func() error { return worker.Run(ctx) },
            func(err error) { cancel() },
        )
    }

    lc.Append(fx.Hook{
        OnStart: func(context.Context) error {
            go g.Run() // 统一启动
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
