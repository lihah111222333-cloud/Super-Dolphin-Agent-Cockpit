# skeleton-event.md — github.com/kelindar/event v1.5.2 类型安全事件总线

> **版本**: v1.5.2 | **模块**: `github.com/kelindar/event`
> **定位**: V3 进程内事件总线

---

## 0. 一句话定位

kelindar/event = V3 的进程内事件总线。类型即 topic，编译期保证发布/订阅匹配，替代 V2 的 string topic + `map[string]any` payload。

---

## 1. 核心范式

kelindar/event 基于 Go 1.18+ 泛型实现：

- **类型即 Topic**：不需要字符串 topic，事件的 Go 类型就是路由键
- **编译期安全**：发布 `AgentStarted{}` 只会被 `event.On[AgentStarted]` 订阅者收到
- **零反射**：泛型分发，无 `interface{}` 断言，无 `reflect` 开销

---

## 2. 事件定义范式

### 2.1 事件就是普通 struct

```go
// internal/events/agent.go
// 每个事件是一个具体类型——不需要实现任何接口
package events

import "time"

// --- Agent 生命周期事件 ---

type AgentStarted struct {
    AgentID   string
    Provider  string        // "mcp" (V3 统一 provider)
    ThreadID  string
    Timestamp time.Time
}

type AgentStopped struct {
    AgentID  string
    Reason   string        // "user_stop", "turn_complete", "error"
    Duration time.Duration
}

type AgentStateChanged struct {
    AgentID   string
    FromState string
    ToState   string
    Trigger   string
}
```

### 2.2 按领域组织事件文件

```
internal/events/
├── agent.go       ← Agent 生命周期事件
├── turn.go        ← Turn 相关事件
├── store.go       ← 数据变更事件
├── ui.go          ← UI 通知事件
└── tool.go        ← 工具执行事件
```

### 2.3 Turn 事件

```go
// internal/events/turn.go
package events

type TurnSubmitted struct {
    ThreadID string
    TurnID   string
    Message  string
}

type TurnCompleted struct {
    AgentID  string
    ThreadID string
    TurnID   string
    Tokens   int
    Duration time.Duration
}

type TurnError struct {
    AgentID  string
    ThreadID string
    TurnID   string
    Error    string
}
```

### 2.4 Store 变更事件

```go
// internal/events/store.go
package events

type ThreadCreated struct {
    ThreadID    string
    WorkspaceID string
}

type ThreadDeleted struct {
    ThreadID string
}

type CommandCardUpdated struct {
    CardID   string
    ThreadID string
}
```

### 2.5 UI 通知事件

```go
// internal/events/ui.go
package events

type UIUpdate struct {
    Kind    string // "timeline", "sidebar", "state"
    Payload any
}

type UIToast struct {
    Level   string // "info", "warning", "error"
    Message string
}
```

---

## 3. 发布 / 订阅范式

### 3.1 发布事件

```go
import "github.com/kelindar/event"

// 发布：类型安全，编译期检查
event.Emit(events.AgentStarted{
    AgentID:   "agent-1",
    Provider:  "mcp",
    ThreadID:  "thread-1",
    Timestamp: time.Now(),
})

// 如果字段类型不对，编译直接报错——不会到运行时
```

### 3.2 订阅事件

```go
// 订阅：泛型保证只收到匹配类型的事件
event.On[events.AgentStarted](func(ev events.AgentStarted) {
    log.Info("agent started",
        "id", ev.AgentID,
        "provider", ev.Provider,
        "thread", ev.ThreadID,
    )
})

// 多个订阅者可以订阅同一事件——全部会被调用
event.On[events.AgentStarted](func(ev events.AgentStarted) {
    metrics.AgentStartCount.Inc()
})
```

### 3.3 订阅生命周期管理

```go
// event.On 返回 Subscription，可以 Close 取消订阅
sub := event.On[events.TurnCompleted](func(ev events.TurnCompleted) {
    // handle
})

// 不再需要时取消订阅
sub.Close()
```

---

## 4. 与 V2 bus.MessageBus 对比

### V2 的问题

```go
// ❌ V2: string topic + interface{} payload
bus.Publish("agent:started", map[string]any{
    "agentID":  agentID,
    "provider": provider,
})

// 订阅时需要运行时类型断言——编译器无法检查
bus.Subscribe("agent:started", func(payload any) {
    data := payload.(map[string]any)           // runtime panic risk
    agentID := data["agentID"].(string)        // 拼写错误？运行时才知道
})
```

### V3 的改进

```go
// ✅ V3: 类型即 topic
event.Emit(events.AgentStarted{AgentID: agentID, Provider: provider})

event.On[events.AgentStarted](func(ev events.AgentStarted) {
    // ev.AgentID 编译期类型安全
    // ev.AgentI  ← 编译错误，拼写错误立刻发现
})
```

| 维度 | V2 (bus.MessageBus) | V3 (kelindar/event) |
|---|---|---|
| Topic | `string`，可拼错 | Go 类型，编译期检查 |
| Payload | `map[string]any`，需断言 | 强类型 struct |
| 路由 | `switch topic` 匹配 | 泛型自动分发 |
| 新增事件 | 改 topic 字符串 + payload map | 新增 struct 即可 |
| 重构安全 | 无——字符串搜索 | 编译器保证 |
| 性能 | 反射 + map 查找 | 泛型零反射 |

---

## 5. 事件分类范式

### 5.1 分类原则

| 分类 | 生产者 | 消费者 | 示例 |
|---|---|---|---|
| Agent 生命周期 | Runner/Manager | UI、Metrics、Logger | `AgentStarted`, `AgentStopped` |
| Turn 事件 | Provider adapter | UI、Store、StateM | `TurnSubmitted`, `TurnCompleted` |
| Store 变更 | RPC handlers | UI（sidebar 更新） | `ThreadCreated`, `ThreadDeleted` |
| UI 通知 | 各模块 | jrpc2 NotificationBridge | `UIUpdate`, `UIToast` |
| 工具执行 | Tool handlers | Logger、Metrics | `ToolStarted`, `ToolCompleted` |
| 状态机 | stateless SM | UI、Logger | `AgentStateChanged` |

### 5.2 事件命名约定

```
{Domain}{PastTenseVerb}
  AgentStarted       ← agent 域，已启动
  TurnCompleted      ← turn 域，已完成
  ThreadCreated      ← thread 域，已创建
  ToolExecutionFailed ← tool 域，执行失败
```

---

## 6. 与 fx 集成

### 6.1 事件订阅注册

```go
// 在 fx.Invoke 中注册事件订阅——确保在应用启动时绑定
var BusModule = fx.Module("bus",
    fx.Invoke(registerEventSubscriptions),
)

func registerEventSubscriptions(
    logger *slog.Logger,
    metrics *Metrics,
    bridge *NotificationBridge,
) {
    // 日志订阅
    event.On[events.AgentStarted](func(ev events.AgentStarted) {
        logger.Info("agent.started", "id", ev.AgentID)
    })
    event.On[events.AgentStopped](func(ev events.AgentStopped) {
        logger.Info("agent.stopped", "id", ev.AgentID, "reason", ev.Reason)
    })

    // 指标订阅
    event.On[events.TurnCompleted](func(ev events.TurnCompleted) {
        metrics.TurnTokens.Observe(float64(ev.Tokens))
        metrics.TurnDuration.Observe(ev.Duration.Seconds())
    })

    // UI 推送桥接
    bridge.Setup()
}
```

### 6.2 生命周期 Hook

```go
// 如果订阅者需要 cleanup，用 fx.Lifecycle
func registerWithLifecycle(lc fx.Lifecycle, logger *slog.Logger) {
    var subs []event.Subscription

    lc.Append(fx.Hook{
        OnStart: func(ctx context.Context) error {
            subs = append(subs,
                event.On[events.AgentStarted](func(ev events.AgentStarted) {
                    logger.Info("agent started", "id", ev.AgentID)
                }),
            )
            return nil
        },
        OnStop: func(ctx context.Context) error {
            for _, s := range subs {
                s.Close()
            }
            return nil
        },
    })
}
```

---

## 7. 与 jrpc2 Notification 集成

```go
// 内部事件 → jrpc2 客户端通知的单向桥接
// 这是 event bus 与 RPC 层的唯一连接点

type NotificationBridge struct {
    srv *jrpc2.Server
}

func NewNotificationBridge(srv *jrpc2.Server) *NotificationBridge {
    return &NotificationBridge{srv: srv}
}

func (b *NotificationBridge) Setup() {
    // Agent 状态 → 客户端
    event.On[events.AgentStateChanged](func(ev events.AgentStateChanged) {
        _ = b.srv.Notify(context.Background(), "event/agentState", map[string]any{
            "agentId":   ev.AgentID,
            "fromState": ev.FromState,
            "toState":   ev.ToState,
        })
    })

    // Turn 完成 → 客户端
    event.On[events.TurnCompleted](func(ev events.TurnCompleted) {
        _ = b.srv.Notify(context.Background(), "event/turnComplete", map[string]any{
            "threadId": ev.ThreadID,
            "turnId":   ev.TurnID,
            "tokens":   ev.Tokens,
        })
    })

    // UI toast → 客户端
    event.On[events.UIToast](func(ev events.UIToast) {
        _ = b.srv.Notify(context.Background(), "event/toast", ev)
    })
}
```

---

## 8. 与 stateless 状态机集成

```go
// 状态机的 OnEntry/OnExit 动作发射事件
// 这样状态变更自动广播到所有订阅者

func configureStateMachine(sm *stateless.StateMachine, agentID string) {
    sm.Configure(StateThinking).
        OnEntry(func(ctx context.Context, args ...any) error {
            event.Emit(events.AgentStateChanged{
                AgentID:   agentID,
                FromState: "idle",
                ToState:   "thinking",
                Trigger:   triggerFromArgs(args),
            })
            return nil
        })

    sm.Configure(StateStopped).
        OnEntry(func(ctx context.Context, args ...any) error {
            event.Emit(events.AgentStopped{
                AgentID: agentID,
                Reason:  "stopped",
            })
            return nil
        })
}

// 反向：外部事件触发状态机转移
func handleProviderEvents(sm *stateless.StateMachine) {
    event.On[events.TurnCompleted](func(ev events.TurnCompleted) {
        _ = sm.FireCtx(context.Background(), TriggerTurnComplete)
    })
    event.On[events.TurnError](func(ev events.TurnError) {
        _ = sm.FireCtx(context.Background(), TriggerError)
    })
}
```

---

## 9. 测试范式

### 9.1 捕获事件的测试辅助

```go
// testutil/event_capture.go
package testutil

import (
    "sync"
    "github.com/kelindar/event"
)

// EventCapture 在测试中捕获指定类型的事件
type EventCapture[T any] struct {
    mu     sync.Mutex
    events []T
    sub    event.Subscription
}

func CaptureEvents[T any]() *EventCapture[T] {
    c := &EventCapture[T]{}
    c.sub = event.On[T](func(ev T) {
        c.mu.Lock()
        defer c.mu.Unlock()
        c.events = append(c.events, ev)
    })
    return c
}

func (c *EventCapture[T]) Events() []T {
    c.mu.Lock()
    defer c.mu.Unlock()
    cp := make([]T, len(c.events))
    copy(cp, c.events)
    return cp
}

func (c *EventCapture[T]) Last() (T, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()
    if len(c.events) == 0 {
        var zero T
        return zero, false
    }
    return c.events[len(c.events)-1], true
}

func (c *EventCapture[T]) Close() {
    c.sub.Close()
}
```

### 9.2 使用示例

```go
func TestAgentStartEmitsEvent(t *testing.T) {
    capture := testutil.CaptureEvents[events.AgentStarted]()
    defer capture.Close()

    // 执行被测代码
    manager.StartAgent(ctx, "agent-1", "thread-1")

    // 验证事件被发射
    require.Eventually(t, func() bool {
        return len(capture.Events()) == 1
    }, time.Second, 10*time.Millisecond)

    ev := capture.Events()[0]
    assert.Equal(t, "agent-1", ev.AgentID)
    assert.Equal(t, "thread-1", ev.ThreadID)
}
```

### 9.3 测试事件链

```go
func TestTurnSubmitEventChain(t *testing.T) {
    // 捕获整条事件链
    submitted := testutil.CaptureEvents[events.TurnSubmitted]()
    defer submitted.Close()
    stateChanged := testutil.CaptureEvents[events.AgentStateChanged]()
    defer stateChanged.Close()
    completed := testutil.CaptureEvents[events.TurnCompleted]()
    defer completed.Close()

    // 提交一个 turn
    svc.TurnSubmit(ctx, &TurnSubmitParams{ThreadID: "t1", Message: "hello"})

    // 验证事件链：Submitted → StateChanged → Completed
    require.Eventually(t, func() bool {
        return len(completed.Events()) == 1
    }, 5*time.Second, 50*time.Millisecond)

    assert.Len(t, submitted.Events(), 1)
    assert.True(t, len(stateChanged.Events()) >= 2) // idle→thinking, thinking→idle
}
```

---

## 10. 并发安全说明

- `event.Emit()` 是并发安全的——可以从任意 goroutine 调用
- `event.On()` 注册的回调在调用 `Emit` 的 goroutine 中同步执行
- 回调函数 **不得** 阻塞——如果需要耗时操作，在回调内启动 goroutine 或写入 channel
- 多个订阅者对同一事件类型的执行顺序不保证

```go
// ✅ 正确：非阻塞回调
event.On[events.TurnCompleted](func(ev events.TurnCompleted) {
    metrics.Counter.Inc() // 快速操作
})

// ✅ 正确：需要耗时操作时异步处理
event.On[events.TurnCompleted](func(ev events.TurnCompleted) {
    select {
    case workCh <- ev: // 写入 channel，由 worker 处理
    default:
        log.Warn("work channel full, dropping event")
    }
})

// ❌ 错误：阻塞回调会阻塞 Emit 调用者
event.On[events.TurnCompleted](func(ev events.TurnCompleted) {
    time.Sleep(5 * time.Second)    // 阻塞！
    http.Post(webhookURL, ...)     // 阻塞！
})
```

---

## 11. 与其他 5 个框架的集成

| 框架 | 集成点 | 说明 |
|---|---|---|
| **jrpc2** (`skeleton-jrpc2.md`) | `NotificationBridge` | 内部事件 → jrpc2 notification 推送到客户端 |
| **fx** (`skeleton-fx.md`) | `BusModule` / `fx.Invoke` | 在 fx 启动时注册所有事件订阅 |
| **oklog/run** (`skeleton-rungroup.md`) | Event Loop Actor | 事件驱动的 Actor 从 channel 消费事件 |
| **stateless** (`skeleton-stateless.md`) | 双向桥接 | 事件触发状态机 / 状态机发射事件 |
| **sqlc** (`skeleton-sqlc.md`) | Store 变更事件 | RPC handler 写入数据库后发射 `ThreadCreated` 等事件 |

---

## 12. 禁止行为（红线）

| 规则 | 原因 |
|---|---|
| ❌ 使用 `string` 作为事件 topic | 编译期无法检查，V2 的核心问题 |
| ❌ 使用 `interface{}` 或 `any` 作为事件 payload | 必须用强类型 struct |
| ❌ 在事件回调中做阻塞 I/O | 会阻塞 `Emit` 调用者 |
| ❌ 在事件回调中调用 `event.Emit` 同类型事件 | 可能死循环 |
| ❌ 在事件 struct 中存储指针或可变引用 | 多个订阅者并发读取时不安全 |
| ❌ 依赖订阅者执行顺序 | 执行顺序不保证 |
| ❌ 用事件总线替代函数调用做同步操作 | 事件总线用于解耦，不用于请求-响应模式 |
| ❌ 不注册就直接 `Emit` 而不管有没有订阅者 | 至少应该有日志订阅者，否则事件静默丢失 |
