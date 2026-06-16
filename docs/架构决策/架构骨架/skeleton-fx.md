# skeleton-fx.md — go.uber.org/fx v1.24.0 DI + 生命周期

> **版本**: v1.24.0 | **模块**: `go.uber.org/fx`
> **定位**: V3 的工厂和装配线
> **参考**: fx+rungroup 完整骨架见 `fx-rungroup-skeleton.md`

---

## 0. 一句话定位

fx = V3 的工厂。所有服务组件的创建、依赖注入、生命周期管理由 fx 统一负责。替代 V2 在 `main.go` 中手动 `New()` → 手动 `Close()` 的脆弱初始化链。

---

## 1. Module 定义范式

### 1.1 基本 Module

```go
// 每个子系统定义为独立的 fx.Module
// 模块内部的 Provide/Invoke 对外部透明

// internal/store/module.go
var StoreModule = fx.Module("store",
    fx.Provide(NewPgxPool),     // *pgxpool.Pool
    fx.Provide(NewQueries),     // db.Querier (sqlc generated)
    fx.Invoke(runMigrations),   // 启动时执行 migration
)

// internal/runner/module.go
var RunnerModule = fx.Module("runner",
    fx.Provide(NewAgentManager),
    fx.Provide(func(m *AgentManager) app.RunnerOut {
        return app.RunnerOut{Runner: m}
    }),
)

// internal/rpcapi/module.go
var RPCModule = fx.Module("rpc",
    fx.Provide(NewService),
    fx.Provide(NewJRPC2Server),
    fx.Provide(func(s *JRPC2Server) app.RunnerOut {
        return app.RunnerOut{Runner: s}
    }),
)
```

### 1.2 Module 命名约定

```go
// Module 名 = 包名，全小写
fx.Module("store", ...)
fx.Module("rpc", ...)
fx.Module("runner", ...)
fx.Module("bus", ...)
fx.Module("desktop", ...)
```

---

## 2. Provider 函数范式

### 2.1 构造函数注入

```go
// Provider 函数就是普通的 Go 构造函数
// fx 根据参数类型自动注入依赖

func NewPgxPool(cfg *Config) (*pgxpool.Pool, error) {
    pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
    if err != nil {
        return nil, fmt.Errorf("connect to database: %w", err)
    }
    return pool, nil
}

func NewQueries(pool *pgxpool.Pool) db.Querier {
    return db.New(pool) // sqlc generated constructor
}

// fx 自动解析依赖链：
//   Config → NewPgxPool → *pgxpool.Pool → NewQueries → db.Querier
```

### 2.2 返回接口，接收具体类型

```go
// ✅ 正确：Provider 返回接口类型
func NewQueries(pool *pgxpool.Pool) db.Querier {
    return db.New(pool)
}

// ❌ 错误：返回具体类型导致消费者耦合实现
func NewQueries(pool *pgxpool.Pool) *db.Queries {
    return db.New(pool)
}
```

### 2.3 多返回值（带 error）

```go
// fx 支持 (T, error) 返回值
// 如果 error != nil，fx 启动失败并打印依赖链
func NewPgxPool(cfg *Config) (*pgxpool.Pool, error) {
    pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
    if err != nil {
        return nil, err // fx 会显示: NewPgxPool failed: ...
    }
    return pool, nil
}
```

---

## 3. fx.In / fx.Out 参数对象范式

### 3.1 当依赖超过 3-4 个时使用参数对象

```go
// ❌ 参数太多，难以维护
func NewService(q db.Querier, log *slog.Logger, cfg *Config, sm *AgentManager, m *Metrics) *Service

// ✅ 使用参数对象
type ServiceParams struct {
    fx.In

    Queries db.Querier
    Logger  *slog.Logger
    Config  *Config
    Manager *AgentManager
    Metrics *Metrics
}

func NewService(p ServiceParams) *Service {
    return &Service{
        queries: p.Queries,
        logger:  p.Logger,
        config:  p.Config,
        manager: p.Manager,
        metrics: p.Metrics,
    }
}
```

### 3.2 结果对象

```go
type ServiceResult struct {
    fx.Out

    Service *Service
    Runner  app.Runner `group:"runners"`
}

func NewService(p ServiceParams) ServiceResult {
    svc := &Service{queries: p.Queries}
    return ServiceResult{
        Service: svc,
        Runner:  svc, // Service 同时也是 Runner
    }
}
```

---

## 4. Group 标签范式

### 4.1 收集多个 Runner

```go
// 定义：每个模块把 Runner 注册到 "runners" group
fx.Provide(func(s *JRPC2Server) app.RunnerOut {
    return app.RunnerOut{Runner: s} // Runner `group:"runners"`
})

fx.Provide(func(m *AgentManager) app.RunnerOut {
    return app.RunnerOut{Runner: m}
})

fx.Provide(func(d *DesktopRunner) app.RunnerOut {
    return app.RunnerOut{Runner: d}
})

// 消费：一次性注入所有 Runner
fx.Invoke(func(in app.RunnerIn) {
    // in.Runners 包含所有注册的 Runner
    g := app.BuildRunGroup(in.Runners)
    g.Run()
})
```

### 4.2 收集多个 handler.Map 片段

```go
// 多个模块各自贡献 RPC methods
type RPCMethodsOut struct {
    fx.Out
    Methods handler.Map `group:"rpc_methods"`
}

type RPCMethodsIn struct {
    fx.In
    Methods []handler.Map `group:"rpc_methods"`
}

// 合并所有 method maps
func MergeMethodMaps(in RPCMethodsIn) handler.Map {
    merged := make(handler.Map)
    for _, m := range in.Methods {
        for k, v := range m {
            merged[k] = v
        }
    }
    return merged
}
```

---

## 5. Optional 依赖范式

### 5.1 Desktop 组件可选注入

```go
// Desktop 模块仅在桌面模式下加载
// 其他模块通过 optional 标签处理缺失

type ServiceParams struct {
    fx.In

    Queries db.Querier
    Logger  *slog.Logger
    Desktop *DesktopBridge `optional:"true"` // 无桌面时为 nil
}

func NewService(p ServiceParams) *Service {
    svc := &Service{queries: p.Queries, logger: p.Logger}
    if p.Desktop != nil {
        svc.desktop = p.Desktop
        svc.logger.Info("desktop mode enabled")
    }
    return svc
}
```

### 5.2 条件模块加载

```go
func main() {
    modules := []fx.Option{
        StoreModule,
        BusModule,
        RunnerModule,
        RPCModule,
    }

    if cfg.DesktopMode {
        modules = append(modules, DesktopModule)
    }

    fx.New(modules...).Run()
}
```

---

## 6. Lifecycle Hook 范式

### 6.1 OnStart / OnStop

```go
func registerStoreLifecycle(lc fx.Lifecycle, pool *pgxpool.Pool) {
    lc.Append(fx.Hook{
        OnStart: func(ctx context.Context) error {
            // 启动时验证数据库连接
            if err := pool.Ping(ctx); err != nil {
                return fmt.Errorf("database ping failed: %w", err)
            }
            log.Info("database connected")
            return nil
        },
        OnStop: func(ctx context.Context) error {
            // 停止时关闭连接池
            pool.Close()
            log.Info("database pool closed")
            return nil
        },
    })
}
```

### 6.2 执行顺序

```
OnStart 按注册顺序执行：
  1. StoreModule.OnStart  → DB 连接
  2. BusModule.OnStart    → 事件订阅注册
  3. RPCModule.OnStart    → 无（server 由 run.Group 启动）

OnStop 按注册逆序执行：
  3. RPCModule.OnStop     → 无（server 由 run.Group 停止）
  2. BusModule.OnStop     → 取消事件订阅
  1. StoreModule.OnStop   → 关闭 DB 连接池
```

### 6.3 超时控制

```go
// fx.New 可以配置 start/stop 超时
app := fx.New(
    fx.StartTimeout(30 * time.Second),
    fx.StopTimeout(15 * time.Second),
    StoreModule,
    RPCModule,
)
```

---

## 7. Module 层级范式

### 7.1 嵌套模块

```go
// 顶层 Module 可以包含子模块
var AppModule = fx.Module("app",
    // 基础设施层
    fx.Module("infra",
        StoreModule,
        BusModule,
    ),
    // 业务层
    fx.Module("business",
        RunnerModule,
        ProviderModule,
    ),
    // 接口层
    fx.Module("interface",
        RPCModule,
        DesktopModule,
    ),
)
```

### 7.2 模块依赖关系

```
StoreModule ─── db.Querier ──→ RPCModule (Service)
    │                              │
    └── *pgxpool.Pool              └── JRPC2Server ──→ run.Group
                                       
BusModule ──── event subscriptions ──→ RunnerModule (AgentManager)
                                       │
ProviderModule ── MCP client ─────────→┘
```

---

## 8. 配置注入范式

### 8.1 配置 struct

```go
// internal/config/config.go
type Config struct {
    DatabaseURL  string `env:"DATABASE_URL" default:"postgres://localhost:5432/agent"`
    RPCAddr      string `env:"RPC_ADDR" default:":9090"`
    DesktopMode  bool   `env:"DESKTOP_MODE" default:"false"`
    LogLevel     string `env:"LOG_LEVEL" default:"info"`
    MaxAgents    int    `env:"MAX_AGENTS" default:"4"`
}
```

### 8.2 Config Provider

```go
var ConfigModule = fx.Module("config",
    fx.Provide(func() (*Config, error) {
        var cfg Config
        if err := envconfig.Process("AGENT", &cfg); err != nil {
            return nil, fmt.Errorf("parse config: %w", err)
        }
        return &cfg, nil
    }),
)
```

---

## 9. 测试范式

### 9.1 fxtest 验证 DI 图

```go
func TestDIGraph(t *testing.T) {
    // fxtest.New 验证所有依赖可以解析
    app := fxtest.New(t,
        ConfigModule,
        StoreModule,
        BusModule,
        RunnerModule,
        RPCModule,
        // 替换真实数据库为 mock
        fx.Replace(func() *Config {
            return &Config{DatabaseURL: "postgres://test:test@localhost/test"}
        }),
    )
    defer app.RequireStop()
    app.RequireStart()
}
```

### 9.2 测试单个模块

```go
func TestStoreModule(t *testing.T) {
    app := fxtest.New(t,
        fx.Provide(func() *Config {
            return &Config{DatabaseURL: testDatabaseURL(t)}
        }),
        StoreModule,
    )
    defer app.RequireStop()
    app.RequireStart()
}
```

### 9.3 替换依赖进行测试

```go
func TestServiceWithMockDB(t *testing.T) {
    mockQ := &MockQuerier{}

    app := fxtest.New(t,
        RPCModule,
        // 用 mock 替换真实的 Querier
        fx.Provide(func() db.Querier { return mockQ }),
        fx.Provide(func() *slog.Logger { return slog.Default() }),
        fx.Provide(func() *Config { return &Config{RPCAddr: ":0"} }),
    )
    defer app.RequireStop()
    app.RequireStart()
}
```

---

## 10. 与 run.Group 的分工

> 详见 `fx-rungroup-skeleton.md`

```
fx 职责（构建期）：
  ├── 解析配置
  ├── 创建对象（Provide）
  ├── 注入依赖（自动）
  ├── OnStart: 初始化（DB ping, migration）
  └── OnStop: 清理（关闭连接池）

run.Group 职责（运行期）：
  ├── 并发启动所有 Actor
  ├── 监听信号
  ├── 一停全停
  └── 错误传播

时间线：
  fx.New()          → 构建 DI 图（编译期检查）
  fx.Start()        → 执行 OnStart hooks（顺序）
  run.Group.Run()   → 所有 Actor 并发运行
  Actor 退出        → run.Group 停止所有 Actor
  fx.Stop()         → 执行 OnStop hooks（逆序）
```

---

## 11. V3 Module 清单

| Module | 包路径 | 主要提供 | 依赖 |
|---|---|---|---|
| `ConfigModule` | `internal/config` | `*Config` | 无 |
| `StoreModule` | `internal/store` | `*pgxpool.Pool`, `db.Querier` | Config |
| `BusModule` | `internal/bus` | event subscriptions | Logger |
| `RunnerModule` | `internal/runner` | `*AgentManager`, `Runner` | Querier, Logger |
| `RPCModule` | `internal/rpcapi` | `*Service`, `*JRPC2Server`, `Runner` | Querier, Manager, Logger |
| `ProviderModule` | `internal/provider` | `*MCPProvider` | Config, Logger |
| `ToolModule` | `internal/tools` | tool handlers | Querier, Provider |
| `DesktopModule` | `internal/desktop` | `*DesktopRunner`, `Runner` (optional) | Config, Service |

---

## 12. 对比 V2 改进

| 维度 | V2 (手动初始化) | V3 (fx) |
|---|---|---|
| 对象创建 | `main.go` 中 200+ 行手动 `New()` | `fx.Provide` 自动解析 |
| 依赖关系 | 隐式传参，参数顺序敏感 | 声明式注入，编译期检查 |
| 生命周期 | `defer close()` 手动管理 | `OnStart`/`OnStop` 自动管理 |
| 清理顺序 | 依赖 defer 调用顺序 | 自动逆序清理 |
| 测试替换 | 需要手动构造整个对象图 | `fx.Replace` 替换单个依赖 |
| 模块化 | 无——所有代码在 `main.go` | `fx.Module` 按领域隔离 |
| 循环依赖 | 运行时 nil pointer | fx 启动时报错 |
| 可选依赖 | if/else 分支 | `optional:"true"` 标签 |

---

## 13. 与其他 5 个框架的集成

| 框架 | 集成点 | 说明 |
|---|---|---|
| **oklog/run** (`skeleton-rungroup.md`) | `group:"runners"` | fx 收集所有 Runner，传入 BuildRunGroup |
| **jrpc2** (`skeleton-jrpc2.md`) | `RPCModule` | fx 注入依赖，创建 JRPC2Server |
| **kelindar/event** (`skeleton-event.md`) | `BusModule` / `fx.Invoke` | fx 启动时注册事件订阅 |
| **stateless** (`skeleton-stateless.md`) | `RunnerModule` | fx 注入 AgentManager（内含状态机） |
| **sqlc** (`skeleton-sqlc.md`) | `StoreModule` | fx 创建 pgxpool + Querier |

---

## 14. 禁止行为（红线）

| 规则 | 原因 |
|---|---|
| ❌ 在 `fx.Provide` 中做 I/O 操作 | Provide 只做对象创建，I/O 放 OnStart |
| ❌ 在 `fx.Invoke` 中创建长期对象 | Invoke 用于副作用注册，长期对象用 Provide |
| ❌ 使用全局变量传递依赖 | 所有依赖通过 fx 注入 |
| ❌ 在 Provider 函数中启动 goroutine | goroutine 在 run.Group 中管理 |
| ❌ 循环依赖 | fx 会检测并报错，但设计上应避免 |
| ❌ Module 之间直接 import 内部类型 | 通过接口解耦 |
| ❌ 在 OnStart 中做可能超时的操作但不传递 ctx | 必须使用 fx 传入的 ctx |
| ❌ 手动调用 `New()` 绕过 fx | 所有组件创建必须通过 fx.Provide |
