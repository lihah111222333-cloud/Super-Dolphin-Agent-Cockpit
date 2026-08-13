# V3 架构决策：框架选型 — DI/生命周期/状态机/事件总线

> **日期:** 2026-03-19
> **决策:** 引入 4 个框架替代手写基础设施
> **影响:** 消灭 ~2,500+ 行基础设施样板，彻底解决 Server God Struct、goroutine 泄漏、无类型事件总线

---

## 1. V2 痛点总结（代码级证据）

### 1.1 生命周期管理 — 500+ 行，无中央协调

| 问题 | 位置 | 代码 |
|---|---|---|
| 40+ untracked goroutine | 全项目 SafeGo 调用 | `util.SafeGo(func() { ... })` 只有 panic recovery |
| 6 步手动 shutdown | server_lifecycle.go:71-108 | 83 行清理代码，无原子语义 |
| readLoop 递归重生 | client_appserver_events.go:63 | `util.SafeGo(func() { c.readLoop() })` — 无限递归 |
| context.Background 滥用 | 35+ 处 | 关闭路径用新 Background context，断开父信号 |
| 无启动就绪信号 | main_setup.go:349 | Server 异步 fire-and-forget 启动 |

### 1.2 依赖注入 — 400+ 行手动接线

| 问题 | 位置 | 代码 |
|---|---|---|
| Server 37+ 字段 God Struct | server.go:47-83 | 18 个嵌入状态组 |
| 15 个 store 手动创建 | server_bootstrap.go:86-121 | 逐行 `NewXxxStore(db)` |
| 10 步顺序初始化 | server.go:210-239 | initStores → initRuntimeWiring → ... |
| contracts/ 循环打破包 | contracts/types.go | 8 个纯类型别名 |
| 构造后回调注册 | main_setup.go:355-374 | SetOnEvent、SetNotifyHook 散落各处 |
| 测试 seam 进生产 struct | server.go:75-78 | testInlineProvider 等字段 |

### 1.3 状态机 — 1,000 行，非 table-driven

| 问题 | 位置 | 行数 |
|---|---|---|
| switch/case + 决策树 | manager_event.go | 530 行 |
| 18 个转换函数 | manager.go, manager_event.go | 分散各处 |
| 7+ 副作用内联 | handleNormalizedEvent | 每次转换触发 buffer 更新、报告提取、dispatch 等 |
| 并行 UI 状态机 | uistate/event_lifecycle.go | 428 行独立状态机 |
| 无 guard、无可视化 | — | 手写 if/else 判断 |

### 1.4 事件总线 — 4,600 行，无类型安全

| 问题 | 位置 | 行数 |
|---|---|---|
| json.RawMessage 载荷 | internal/bus/bus.go | 50 种消息类型全部无类型 |
| 67 事件类型映射 | uistate/event_normalizer.go | 手动维护 classifyMap |
| 79 通知映射 | apiserver/notifications.go | 手动维护 eventMethodMap |
| 672 行 Claude 事件解析 | claude/client_cli_events.go | 46 个函数 |
| 1,017 行 Codex 事件解析 | codex/client_appserver_events.go | 50+ 函数 + 75 方法映射 |

---

## 2. 框架选型

### 2.1 uber-go/fx — DI + 生命周期

| 指标 | 值 |
|---|---|
| Stars | 7,400 |
| 维护 | Uber 核心团队，v1 语义版本 |
| 生产用户 | Uber（几乎所有 Go 服务），9,366+ 导入包 |
| 机制 | 运行时反射 DI |

**解决的 V2 痛点：**

```go
// ══ V2: 80 行手动接线 ══
func New(deps Deps) *Server {
    s := &Server{mgr: deps.Manager, lsp: deps.LSP, ...}
    initStores(s, deps.DB)         // 15 个 store
    initRuntimeWiring(s)           // 循环引用
    initSkills(s, deps.SkillsDir)  // 手动
    s.registerMethods()            // 手动
    applyStallConfig(s, deps.Config)
    startMemoryStatsTicker(s)      // 无法停止的 goroutine
    return s
}

// ══ V3: fx 自动解析 ══
fx.New(
    fx.Provide(
        store.NewBundle,        // 一次注册所有 store
        runner.NewManager,      // DI 自动注入依赖
        NewServer,              // 依赖自动注入
    ),
    fx.Invoke(func(s *Server) {}), // 触发构建
).Run()  // 自动 OnStart/OnStop + 优雅关闭
```

**淘汰的候选：**
- google/wire — **2025.8 已归档**，不再维护
- samber/do — 社区小，v2 太新
- golobby/container — 无生命周期管理

### 2.2 oklog/run — goroutine 生命周期编排

| 指标 | 值 |
|---|---|
| Stars | 1,700 |
| 生产用户 | Prometheus、Thanos |
| 代码量 | ~100 行全部代码 |

**解决的 V2 痛点：**

```go
// ══ V2: 无法追踪的 SafeGo ══
util.SafeGo(func() { appSrv.Serve(ln) })  // fire-and-forget
util.SafeGo(func() { ticker... })         // 无限循环
util.SafeGo(func() { signalHandler() })   // 无退出机制

// ══ V3: 任何一个退出，全部优雅关闭 ══
var g run.Group

g.Add(  // RPC Server
    func() error { return srv.Serve(ln) },
    func(error) { srv.Shutdown(ctx) },
)
g.Add(  // 心跳 ticker
    func() error { return heartbeatLoop(ctx) },
    func(error) { cancel() },
)
g.Add(  // 信号处理
    func() error { sig := <-sigCh; return fmt.Errorf("signal: %v", sig) },
    func(error) { close(sigCh) },
)
g.Run()  // 阻塞直到全部退出
```

### 2.3 qmuntal/stateless — 状态机

| 指标 | 值 |
|---|---|
| Stars | 1,300 |
| 来源 | .NET Stateless 移植（生产级） |
| 特性 | table-driven + guard + 副作用钩子 + DOT 可视化 + 外部存储 |

**解决的 V2 痛点：**

```go
// ══ V2: 530 行 switch/case 决策树 ══
func deriveNormalizedEventDecision(eventType string) stateDecision { ... }
func errorEventStateDecision(eventType string) stateDecision { ... }
func systemEventDecision(eventType string) stateDecision { ... }
func applyEventTypeOverrides(decision *stateDecision, ...) { ... }
// + 14 more functions, 7+ inline side effects

// ══ V3: 声明式状态机 ══
sm := stateless.NewStateMachineWithExternalStorage(getState, setState, stateless.FiringQueued)

sm.Configure(StateIdle).
    Permit(TriggerStart, StateRunning, guardBudgetAvailable)

sm.Configure(StateRunning).
    OnEntry(func(ctx context.Context, args ...any) error {
        emitTurnStarted(args)
        return nil
    }).
    Permit(TriggerComplete, StateCompleted).
    Permit(TriggerFail, StateFailed).
    Permit(TriggerStall, StatePaused, guardStallThresholdExceeded)

// 可视化调试
dotGraph := sm.ToGraph()  // 输出 DOT 格式，可渲染为图片
```

### 2.4 kelindar/event — 类型安全事件总线

| 指标 | 值 |
|---|---|
| Stars | 553 |
| 性能 | 25-82M ops/sec，零分配 |
| 特性 | Go 泛型类型安全，编译期检查 |

**解决的 V2 痛点：**

```go
// ══ V2: 无类型事件 ══
bus.Publish("agent_events", json.RawMessage(`{"type":"task_complete",...}`))
// 订阅端手动 json.Unmarshal + 类型断言

// ══ V3: 编译期类型安全 ══
type TaskCompleted struct {
    RunID  string
    Result string
}

// 发布 — 类型检查
event.Emit(TaskCompleted{RunID: "r-1", Result: "success"})

// 订阅 — 编译期确保类型匹配
event.On(func(e TaskCompleted) {
    log.Printf("Task %s completed: %s", e.RunID, e.Result)
})
```

### 2.5 保留 envconfig — 配置管理

V2 的 kelseyhightower/envconfig 已经够好，不换。

---

## 3. 框架组合：V3 整体架构

```
                         ┌─────────────────────────────┐
                         │     uber-go/fx              │
                         │  DI 容器 + 生命周期管理      │
                         └─────────┬───────────────────┘
                                   │ 依赖注入
              ┌────────────────────┼────────────────────┐
              │                    │                    │
    ┌─────────▼───────┐  ┌────────▼────────┐  ┌───────▼────────┐
    │  jrpc2 Server   │  │  oklog/run      │  │  stateless     │
    │  (RPC 框架)      │  │  (goroutine     │  │  (状态机)       │
    │  ADR-002        │  │   生命周期)      │  │                │
    └────────┬────────┘  └────────┬────────┘  └───────┬────────┘
             │                    │                    │
             └────────────────────┼────────────────────┘
                                  │
                        ┌─────────▼─────────┐
                        │  kelindar/event   │
                        │  (类型安全事件总线) │
                        └───────────────────┘
```

### 组合关系

```go
func main() {
    fx.New(
        // P0: 基础设施
        fx.Provide(config.Load),                    // envconfig
        fx.Provide(store.NewBundle),                // 所有 store 一次注入
        fx.Provide(event.NewDispatcher),            // kelindar/event

        // P2: 核心引擎
        fx.Provide(runner.NewStateMachine),         // qmuntal/stateless
        fx.Provide(runner.NewManager),              // 依赖 store + event + stateMachine

        // P3: 工具层
        fx.Provide(toolsdk.NewRegistry),
        fx.Provide(mcp.NewServer),

        // P5: RPC 服务
        fx.Provide(apiserver.NewServer),            // 依赖全部，jrpc2 handler
        fx.Provide(apiserver.NewSSEBridge),

        // 生命周期
        fx.Invoke(func(lc fx.Lifecycle, srv *apiserver.Server, sse *apiserver.SSEBridge) {
            var g run.Group

            g.Add(srv.ServeActor())                 // jrpc2 server
            g.Add(sse.ServeActor())                 // SSE notifications
            g.Add(signalActor())                    // graceful shutdown

            lc.Append(fx.Hook{
                OnStart: func(ctx context.Context) error { go g.Run(); return nil },
                OnStop:  func(ctx context.Context) error { /* g.Run exits */ return nil },
            })
        }),
    ).Run()
}
```

---

## 4. 代码量影响

| 区域 | V2 行数 | V3 预估 | 减少 | 原因 |
|---|---|---|---|---|
| Server 构造 + 接线 | ~400 | ~50 | -350 | fx 自动 DI |
| goroutine 生命周期 | ~500 | ~100 | -400 | oklog/run + fx 钩子 |
| 状态机 | ~1,000 | ~200 | -800 | stateless table-driven |
| 事件总线 + 映射 | ~4,600 | ~1,500 | -3,100 | kelindar/event 类型安全 |
| **合计** | **~6,500** | **~1,850** | **~-4,650** | |

### 全部 ADR 累计效果

| ADR | 减少行数 |
|---|---|
| ADR-001 Provider 收敛 | ~6,286 |
| ADR-002 jrpc2 框架 | ~2,000-2,770 |
| **ADR-003 四框架引入** | **~4,650** |
| **累计** | **~12,900-13,700** |

V2 原始 83,583 行 → V3 目标：**≤35,000 行**（压缩 58%）

---

## 5. 淘汰的候选

| 框架 | 淘汰原因 |
|---|---|
| google/wire | 2025.8 已归档，不再维护 |
| samber/do | 社区小，v2 太新 |
| golobby/container | 无生命周期管理 |
| looplab/fsm | guard 需要 callback 取消模式，不如 stateless 干净 |
| asaskevich/EventBus | 无类型安全（interface{}） |
| RxGo | 永远 beta，不适合 Go 并发模型 |
| go-micro | 有自己的 RPC 层，与 jrpc2 冲突 |
| go-kit | 无 DI，样板多，已停更 |
| spf13/viper | 过重，V2 的 envconfig 已够用 |
