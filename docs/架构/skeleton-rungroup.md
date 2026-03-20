# skeleton-rungroup.md — github.com/oklog/run v1.2.0 goroutine 编排

> **版本**: v1.2.0 | **模块**: `github.com/oklog/run`
> **定位**: V3 并发引擎
> **参考**: fx+rungroup 完整骨架见 `fx-rungroup-skeleton.md`

---

## 0. 一句话定位

oklog/run = V3 的并发引擎。所有长跑 goroutine 统一编排，一停全停。无论 RPC server、event loop、timer 还是 desktop app，全部以 Actor 形式加入 `run.Group`。

---

## 1. Actor 定义范式

### 1.1 核心概念

`run.Group` 管理一组并发 Actor。每个 Actor 由两个函数组成：

```go
import "github.com/oklog/run"

var g run.Group

// 每个 Actor = (execute, interrupt) 函数对
g.Add(
    func() error {
        // execute: 阻塞运行，直到完成或被中断
        // 正常结束返回 nil，出错返回 error
        return server.ListenAndServe()
    },
    func(error) {
        // interrupt: 收到停止信号时调用
        // 参数是第一个退出的 Actor 返回的 error
        server.Shutdown(context.Background())
    },
)
```

### 1.2 运行语义

```go
err := g.Run()
// 1. 所有 Actor 的 execute 函数并发启动
// 2. 等待任意一个 execute 返回
// 3. 调用所有其他 Actor 的 interrupt 函数
// 4. 等待所有 execute 返回
// 5. 返回第一个退出的 Actor 的 error
```

---

## 2. Runner 接口范式（V3 约定）

### 2.1 接口定义

```go
// internal/app/runner.go
package app

import "context"

// Runner 是所有长跑组件的统一接口
type Runner interface {
    Run(ctx context.Context) error
}

// RunnerOut 用于 fx group 收集
type RunnerOut struct {
    fx.Out
    Runner Runner `group:"runners"`
}

// RunnerIn 用于 fx group 注入
type RunnerIn struct {
    fx.In
    Runners []Runner `group:"runners"`
}
```

### 2.2 Runner → run.Group 适配器

```go
// internal/app/run.go
package app

import (
    "context"
    "github.com/oklog/run"
)

// BuildRunGroup 将所有 Runner 适配为 run.Group Actor
func BuildRunGroup(runners []Runner) run.Group {
    var g run.Group

    for _, r := range runners {
        r := r // capture loop variable
        ctx, cancel := context.WithCancel(context.Background())

        g.Add(
            func() error {
                return r.Run(ctx) // execute: 阻塞运行
            },
            func(error) {
                cancel() // interrupt: 取消 context
            },
        )
    }

    return g
}
```

### 2.3 与 fx 集成的完整启动链

```go
// cmd/agent/main.go
func main() {
    fx.New(
        StoreModule,
        BusModule,
        RunnerModule,
        RPCModule,
        ProviderModule,
        DesktopModule,

        fx.Invoke(func(in app.RunnerIn, lc fx.Lifecycle) {
            lc.Append(fx.Hook{
                OnStart: func(ctx context.Context) error {
                    g := app.BuildRunGroup(in.Runners)

                    // Signal handler Actor（不是 Runner，直接加入 group）
                    g.Add(run.SignalHandler(ctx, os.Interrupt, syscall.SIGTERM))

                    go func() {
                        if err := g.Run(); err != nil {
                            log.Error("run group exited", "error", err)
                        }
                    }()
                    return nil
                },
            })
        }),
    ).Run()
}
```

---

## 3. 常见 Actor 模式

### 3.1 RPC Server Actor

```go
// 参见 skeleton-jrpc2.md 第 3 节
type JRPC2Server struct { /* ... */ }

func (s *JRPC2Server) Run(ctx context.Context) error {
    ctx, s.cancel = context.WithCancel(ctx)
    ln, err := net.Listen("tcp", s.addr)
    if err != nil {
        return err
    }
    defer ln.Close()
    lst := server.NewListener(ln, channel.Line)
    return server.Loop(ctx, lst, server.Static(s.mux), nil)
}
```

### 3.2 Event Loop Actor（subscribe + dispatch）

```go
// 事件驱动的工作循环
type EventLoopRunner struct {
    workCh chan events.TurnSubmitted
}

func NewEventLoopRunner() *EventLoopRunner {
    r := &EventLoopRunner{
        workCh: make(chan events.TurnSubmitted, 64),
    }
    // 订阅事件，写入 channel
    event.On[events.TurnSubmitted](func(ev events.TurnSubmitted) {
        select {
        case r.workCh <- ev:
        default:
            log.Warn("event loop channel full")
        }
    })
    return r
}

func (r *EventLoopRunner) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case ev := <-r.workCh:
            if err := r.handleTurnSubmitted(ctx, ev); err != nil {
                log.Error("handle turn", "err", err)
            }
        }
    }
}
```

### 3.3 Timer / Ticker Actor（周期任务）

```go
// 定时清理、心跳、指标上报等
type HeartbeatRunner struct {
    interval time.Duration
    runner   *AgentManager
}

func (r *HeartbeatRunner) Run(ctx context.Context) error {
    ticker := time.NewTicker(r.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            r.runner.SendHeartbeats(ctx)
        }
    }
}
```

### 3.4 Signal Handler Actor

```go
// oklog/run 内置的信号处理
g.Add(run.SignalHandler(ctx, os.Interrupt, syscall.SIGTERM))
// 收到信号后返回 run.SignalError，触发所有 Actor 停止
```

### 3.5 Wails Desktop Actor

```go
// Desktop app 作为 Runner
type DesktopRunner struct {
    app *application.App
}

func (d *DesktopRunner) Run(ctx context.Context) error {
    // Wails v3 的 Run 会阻塞直到窗口关闭
    errCh := make(chan error, 1)
    go func() {
        errCh <- d.app.Run()
    }()

    select {
    case <-ctx.Done():
        d.app.Quit() // 收到停止信号，退出桌面应用
        return ctx.Err()
    case err := <-errCh:
        return err // 用户关闭窗口
    }
}
```

### 3.6 Health Check / Ready Probe Actor

```go
type HealthRunner struct {
    addr    string
    checker func() error
}

func (h *HealthRunner) Run(ctx context.Context) error {
    mux := http.NewServeMux()
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
        if err := h.checker(); err != nil {
            http.Error(w, err.Error(), http.StatusServiceUnavailable)
            return
        }
        w.WriteHeader(http.StatusOK)
    })

    srv := &http.Server{Addr: h.addr, Handler: mux}
    go func() {
        <-ctx.Done()
        srv.Shutdown(context.Background())
    }()
    return srv.ListenAndServe()
}
```

---

## 4. 优雅退出时序

```
时间线：
  t0: 所有 Actor.execute() 并发启动
  t1: Signal Handler 收到 SIGTERM → 返回 SignalError
  t2: run.Group 调用所有其他 Actor.interrupt(SignalError)
      → JRPC2Server.cancel() 取消 context
      → EventLoop.cancel() 取消 context
      → DesktopRunner.cancel() → app.Quit()
      → HeartbeatRunner.cancel() 取消 context
  t3: 各 Actor.execute() 检测到 ctx.Done()，清理资源后返回
  t4: 所有 Actor 退出，g.Run() 返回

关键：interrupt 函数必须立即返回（非阻塞），不做耗时清理
      耗时清理在 execute 函数的 defer 中完成
```

### 4.1 正确的退出模式

```go
func (s *SomeRunner) Run(ctx context.Context) error {
    // 资源获取
    resource, err := acquireResource()
    if err != nil {
        return err
    }
    // defer 中做清理——保证在 execute 返回前完成
    defer resource.Close()

    // 主循环
    for {
        select {
        case <-ctx.Done():
            return ctx.Err() // 触发 defer 清理
        case work := <-s.workCh:
            s.handle(work)
        }
    }
}
```

---

## 5. 错误传播

```go
err := g.Run()
// err 是第一个退出的 Actor 返回的 error

// 判断退出原因
switch {
case errors.Is(err, run.SignalError{}):
    log.Info("graceful shutdown via signal")
case errors.Is(err, context.Canceled):
    log.Info("context canceled")
case err != nil:
    log.Error("actor failed", "error", err)
    os.Exit(1)
default:
    log.Info("clean shutdown")
}
```

### 5.1 Actor 返回 nil vs error

```go
// 返回 nil: Actor 正常结束，但仍然触发全部停止
// 返回 error: Actor 异常结束，error 传播给其他 Actor 的 interrupt

// 例：Wails 窗口关闭 → 返回 nil → 所有 Actor 停止 → 进程退出
func (d *DesktopRunner) Run(ctx context.Context) error {
    return d.app.Run() // 用户关闭窗口时返回 nil
}
```

---

## 6. 与 fx 的分工

> 详见 `fx-rungroup-skeleton.md`

| 职责 | fx | oklog/run |
|---|---|---|
| 对象创建 | ✅ `fx.Provide` | ❌ |
| 依赖注入 | ✅ 自动解析 | ❌ |
| 启动顺序 | ✅ `fx.Lifecycle.OnStart` | ❌ |
| 并发运行 | ❌ | ✅ `g.Run()` |
| 一停全停 | ❌ | ✅ Actor 联动 |
| 优雅退出 | ✅ `OnStop`（短暂清理） | ✅ interrupt → context cancel |

**分工原则**：fx 负责 "构建 + 初始化"，run.Group 负责 "运行 + 停止"。

```
应用启动流程：
  fx.New()
    → fx.Provide: 创建所有组件
    → fx.Lifecycle.OnStart: 初始化（DB 连接、migration 等）
    → run.Group.Run(): 所有 Actor 并发运行
    → 某个 Actor 退出 → 全部停止
    → fx.Lifecycle.OnStop: 清理（关闭 DB 连接池等）
```

---

## 7. 测试范式

### 7.1 测试单个 Runner

```go
func TestEventLoopRunner(t *testing.T) {
    runner := NewEventLoopRunner()

    ctx, cancel := context.WithCancel(context.Background())

    // 在后台运行 Runner
    errCh := make(chan error, 1)
    go func() {
        errCh <- runner.Run(ctx)
    }()

    // 发射事件触发处理
    event.Emit(events.TurnSubmitted{ThreadID: "t1", TurnID: "turn-1"})
    time.Sleep(100 * time.Millisecond) // 等待处理

    // 停止 Runner
    cancel()

    err := <-errCh
    assert.ErrorIs(t, err, context.Canceled)
}
```

### 7.2 测试 run.Group 集成

```go
func TestRunGroupShutdown(t *testing.T) {
    var g run.Group

    started := make(chan struct{})
    runner1 := &mockRunner{startedCh: started}
    ctx1, cancel1 := context.WithCancel(context.Background())

    g.Add(
        func() error { return runner1.Run(ctx1) },
        func(error) { cancel1() },
    )

    // 添加一个快速退出的 Actor
    g.Add(
        func() error {
            <-started // 等 runner1 启动
            return errors.New("deliberate exit")
        },
        func(error) {},
    )

    err := g.Run()
    assert.EqualError(t, err, "deliberate exit")
    assert.True(t, runner1.stopped) // runner1 也被停止了
}
```

### 7.3 测试优雅退出时序

```go
func TestGracefulShutdownOrder(t *testing.T) {
    var order []string
    var mu sync.Mutex
    record := func(s string) {
        mu.Lock()
        order = append(order, s)
        mu.Unlock()
    }

    var g run.Group
    ctx, cancel := context.WithCancel(context.Background())

    g.Add(
        func() error {
            <-ctx.Done()
            record("actor1_stopped")
            return ctx.Err()
        },
        func(error) { cancel() },
    )

    g.Add(
        func() error {
            time.Sleep(50 * time.Millisecond)
            record("actor2_exited")
            return nil // 正常退出触发全部停止
        },
        func(error) { record("actor2_interrupted") },
    )

    _ = g.Run()

    mu.Lock()
    defer mu.Unlock()
    assert.Contains(t, order, "actor2_exited")
    assert.Contains(t, order, "actor1_stopped")
}
```

---

## 8. 与其他 5 个框架的集成

| 框架 | 集成点 | 说明 |
|---|---|---|
| **fx** (`skeleton-fx.md`) | `group:"runners"` | fx 收集所有 Runner，传入 `BuildRunGroup` |
| **jrpc2** (`skeleton-jrpc2.md`) | `JRPC2Server` 实现 `Runner` | RPC server 作为 Actor 运行 |
| **kelindar/event** (`skeleton-event.md`) | Event Loop Actor | 事件驱动的工作循环作为 Actor |
| **stateless** (`skeleton-stateless.md`) | Agent Manager Actor | 管理多个状态机实例的长跑循环 |
| **sqlc** (`skeleton-sqlc.md`) | 无直接集成 | DB pool 由 fx.Lifecycle 管理，不是 Actor |

---

## 9. 禁止行为（红线）

| 规则 | 原因 |
|---|---|
| ❌ 在 Actor 外部 `go func()` | 所有长跑 goroutine 必须通过 run.Group 管理 |
| ❌ Actor 内部再嵌套 `run.Group` | 一个进程只有一个 run.Group |
| ❌ interrupt 函数做耗时操作 | interrupt 必须立即返回，清理在 execute 的 defer 中 |
| ❌ Actor 之间直接通信 | 通过 event bus 或 channel 解耦 |
| ❌ Actor 忽略 `ctx.Done()` | 必须响应 context 取消信号 |
| ❌ 在 interrupt 中 panic | 会导致其他 Actor 无法正常停止 |
| ❌ Actor 的 execute 立即返回 | 会触发全部停止——execute 必须阻塞到工作完成或被中断 |
