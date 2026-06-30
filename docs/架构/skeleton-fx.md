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

// internal/platform/db/module.go + internal/store/module.go
var DBModule = fx.Module("db",
    fx.Provide(NewDB),          // *sql.DB (modernc.org/sqlite)
    fx.Invoke(registerLifecycle), // 启动时执行 migration / schema baseline 校验
)

var StoreModule = fx.Module("store",
    fx.Provide(func(db *sql.DB) *sqlc.Queries { return sqlc.New(db) }),
    fx.Provide(func(q *sqlc.Queries) sqlc.Querier { return q }),
)

// internal/platform/runner/group.go
// Runner 是 platform 层统一的长跑组件 contract。
type Runner interface {
    Run(ctx context.Context) error
}

// internal/app/runner.go
type RunnerResult struct {
    fx.Out
    Runner platformrunner.Runner `group:"runners"`
}

// internal/app/modules.go
func AsRPCRunner(server *rpc.Server) RunnerResult {
    return RunnerResult{Runner: server}
}

var AppModule = fx.Module("app",
    rpc.Module,
    fx.Provide(AsRPCRunner),
)

// internal/platform/rpc/module.go
var RPCModule = fx.Module("rpc",
    fx.Provide(NewServer),
    fx.Provide(NewPushBridge),
    fx.Invoke(registerAllHandlers),
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

func NewDB(cfg *Config) (*sql.DB, error) {
    db, err := sql.Open("sqlite", cfg.SQLitePath)
    if err != nil {
        return nil, fmt.Errorf("open sqlite database: %w", err)
    }
    return db, nil
}

func NewQueries(db *sql.DB) sqlc.Querier {
    return sqlc.New(db) // sqlc generated constructor
}

// fx 自动解析依赖链：
//   Config → NewDB → *sql.DB → sqlc.New → sqlc.Querier
```

### 2.2 返回接口，接收具体类型

```go
// ✅ 正确：Provider 返回接口类型
func NewQueries(db *sql.DB) sqlc.Querier {
    return sqlc.New(db)
}

// ❌ 错误：返回具体类型导致消费者耦合实现
func NewQueries(db *sql.DB) *sqlc.Queries {
    return sqlc.New(db)
}
```

### 2.3 多返回值（带 error）

```go
// fx 支持 (T, error) 返回值
// 如果 error != nil，fx 启动失败并打印依赖链
func NewDB(cfg *Config) (*sql.DB, error) {
    db, err := sql.Open("sqlite", cfg.SQLitePath)
    if err != nil {
        return nil, err // fx 会显示: NewDB failed: ...
    }
    return db, nil
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
// 定义：每个模块把 platformrunner.Runner 注册到 "runners" group
fx.Provide(func(s *rpc.Server) app.RunnerResult {
    return app.RunnerResult{Runner: s} // Runner `group:"runners"`
})

fx.Provide(func(r *cachekeepalive.Runner) app.RunnerResult {
    return app.RunnerResult{Runner: r}
})

fx.Provide(fx.Annotate(NewApprovalCleanupRunner, fx.ResultTags(`group:"runners"`)))

// 消费：一次性注入所有 Runner
type runtimeParams struct {
    fx.In
    Runners []platformrunner.Runner `group:"runners"`
}

fx.Invoke(func(lc fx.Lifecycle, p runtimeParams) {
    app.BindRuntime(lc, p)
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
func registerLifecycle(lc fx.Lifecycle, db *sql.DB, cfg *config.Config) {
    lc.Append(fx.Hook{
        OnStart: func(ctx context.Context) error {
            // 启动时执行 SQLite migrations、schema version 和 baseline table 校验
            if err := autoMigrate(ctx, db, cfg); err != nil {
                return fmt.Errorf("migrate sqlite database: %w", err)
            }
            return nil
        },
        OnStop: func(ctx context.Context) error {
            return db.Close()
        },
    })
}
```

### 6.2 执行顺序

```
OnStart 按注册顺序执行：
  1. db.Module.OnStart    → SQLite migration / schema baseline 校验
  2. bus.Module.OnStart   → 事件订阅注册
  3. app.BindRuntime      → platformrunner.RunGroup 托管 runners

OnStop 按注册逆序执行：
  3. app.BindRuntime      → 取消 runners、等待退出、drain 收尾
  2. bus.Module.OnStop    → 取消事件订阅
  1. db.Module.OnStop     → 关闭 SQLite *sql.DB
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
DBModule ─── *sql.DB ──→ StoreModule ─── sqlc.Querier ──→ RPCModule / Module services
                              │
                              └── sqlc.Queries
                                       
BusModule ──── event subscriptions ──→ RunnerModule (AgentManager)
                                       │
ProviderModule ── MCP client ─────────→┘
```

---

## 8. 配置注入范式

### 8.1 配置 struct

```go
// internal/platform/config/config.go
type Config struct {
    SQLitePath   string `env:"SUPER_DOLPHIN_SQLITE_PATH"`
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
            return &Config{SQLitePath: filepath.Join(t.TempDir(), "super-dolphin.db")}
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
            return &Config{SQLitePath: filepath.Join(t.TempDir(), "super-dolphin.db")}
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

## 10. 与 platform runner 的分工

> 详见 `fx-rungroup-skeleton.md`

```
fx 职责（构建期）：
  ├── 解析配置
  ├── 创建对象（Provide）
  ├── 注入依赖（自动）
  ├── OnStart: 初始化（DB ping, migration）
  └── OnStop: 清理（关闭连接池）

platformrunner.RunGroup 职责（运行期）：
  ├── 并发启动所有 Actor
  ├── 监听信号
  ├── 一停全停
  └── 错误传播

时间线：
  fx.New()          → 构建 DI 图（编译期检查）
  fx.Start()        → 执行 OnStart hooks（顺序）
  platformrunner.RunGroup() → 所有 Actor 并发运行
  Actor 退出                → platform runner 停止所有 Actor
  fx.Stop()         → 执行 OnStop hooks（逆序）
```

---

## 11. V3 Module 清单

| Module | 包路径 | 主要提供 | 依赖 |
|---|---|---|---|
| `config.Module` | `internal/platform/config` | `*config.Config` | 无 |
| `db.Module` | `internal/platform/db` | `*sql.DB`、migration/schema 校验 | Config |
| `store.Module` | `internal/store` | `*sqlc.Queries`, `sqlc.Querier`, 各 store 子模块 | `*sql.DB` |
| `bus.Module` | `internal/platform/bus` | typed event dispatcher / emitters | Logger |
| `runner.Module` | `internal/platform/runner` | `Runner` contract / runtime group helper | 无 |
| `rpc.Module` | `internal/platform/rpc` | `*Server`, `HandlerMapResult`, push bridge, runners | Config, Logger, grouped handlers |
| `provider.*.Module` | `internal/provider/{unified,claudecli,codexapp}` | provider drivers / sessions | Config, Logger, skill mirror reconciler |
| `uiwails.Module` | `internal/ui/wails` | Wails desktop lifecycle / HTTP-RPC bridge | App, Config, RPC server |

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
| **platform runner** (`skeleton-rungroup.md`) | `group:"runners"` | fx 收集所有 Runner，传入 `internal/platform/runner.RunGroup` |
| **jrpc2** (`skeleton-jrpc2.md`) | `rpc.Module` + `HandlerMapResult` | fx 注入依赖，分组收集 handler.Map 后注册到 `rpc.Server` |
| **kelindar/event** (`skeleton-event.md`) | `BusModule` / `fx.Invoke` | fx 启动时注册事件订阅 |
| **stateless** (`skeleton-stateless.md`) | `RunnerModule` | fx 注入 AgentManager（内含状态机） |
| **sqlc** (`docs/契约/sqlc-convention.md`) | `db.Module` + `store.Module` | fx 创建 SQLite `*sql.DB`，store.Module 创建 `sqlc.Queries/Querier` |

---

## 14. 禁止行为（红线）

| 规则 | 原因 |
|---|---|
| ❌ 在 `fx.Provide` 中做 I/O 操作 | Provide 只做对象创建，I/O 放 OnStart |
| ❌ 在 `fx.Invoke` 中创建长期对象 | Invoke 用于副作用注册，长期对象用 Provide |
| ❌ 使用全局变量传递依赖 | 所有依赖通过 fx 注入 |
| ❌ 在 Provider 函数中启动 goroutine | 长跑 goroutine 通过 platform runner / safego 管理 |
| ❌ 循环依赖 | fx 会检测并报错，但设计上应避免 |
| ❌ Module 之间直接 import 内部类型 | 通过接口解耦 |
| ❌ 在 OnStart 中做可能超时的操作但不传递 ctx | 必须使用 fx 传入的 ctx |
| ❌ 手动调用 `New()` 绕过 fx | 所有组件创建必须通过 fx.Provide |
