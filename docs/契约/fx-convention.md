# `go.uber.org/fx` 契约（快速参考）

> **完整规范见 [`modularity-convention.md`](./modularity-convention.md)**
> 本文是快速参考卡，覆盖 fx 的核心范式和反模式。

## 1. 选型理由

| 方案 | 结论 | 说明 |
| --- | --- | --- |
| `go.uber.org/fx` | 采用 | 模块化、生命周期、value group 都成熟，适合 V3 的工厂定位。 |
| 手写装配 | 不采用 | 小项目可行，V3 多模块组合下极易退化成隐式依赖。 |
| `google/wire` | 不作为主方案 | 编译期生成很干净，但对运行时生命周期和 value group 支持不如 `fx` 直接。 |
| `dig` 直接使用 | 不采用 | 能力是 `fx` 的底层子集，缺少 `Lifecycle` 和模块组织约束。 |

V3 明确把 `fx` 定位为工厂：造零件、接线路、管理生命周期，但不承担长跑编排。

## 2. Paradigm

### 2.1 constructor 只做创建，不做启动

```go
package main

import (
	"context"

	"go.uber.org/fx"
)

type Config struct {
	DBPath string
}

type Store struct {
	dbPath string
}

func NewConfig() Config {
	return Config{DBPath: ".super-dolphin/super-dolphin.db"}
}

func NewStore(lc fx.Lifecycle, cfg Config) *Store {
	store := &Store{dbPath: cfg.DBPath}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error { return nil },
		OnStop:  func(context.Context) error { return nil },
	})
	return store
}

func main() {
	_ = fx.New(fx.Provide(NewConfig, NewStore))
}
```

### 2.2 按职责建 `fx.Module`

```go
package main

import "go.uber.org/fx"

type Store struct{}
type Bus struct{}

func NewStore() *Store { return &Store{} }
func NewBus() *Bus     { return &Bus{} }

var StoreModule = fx.Module("store", fx.Provide(NewStore))
var BusModule = fx.Module("bus", fx.Provide(NewBus))

func main() {
	_ = fx.New(StoreModule, BusModule)
}
```

### 2.3 用 value group 自动收集 `Runner`

```go
package main

import (
	"context"

	"go.uber.org/fx"
)

type Runner interface {
	Run(ctx context.Context) error
}

type RunnerOut struct {
	fx.Out
	Runner Runner `group:"runners"`
}

type RunnerIn struct {
	fx.In
	Runners []Runner `group:"runners"`
}

type HTTPServer struct{}

func (HTTPServer) Run(context.Context) error { return nil }

func NewHTTPServer() HTTPServer { return HTTPServer{} }

func AsRunner(s HTTPServer) RunnerOut {
	return RunnerOut{Runner: s}
}

func main() {
	var in RunnerIn
	_ = fx.New(
		fx.Provide(NewHTTPServer, AsRunner),
		fx.Populate(&in),
	)
}
```

## 3. Best Practice

- constructor 只返回对象和错误，不偷跑 goroutine。
- `fx.Module` 必须按职责边界命名：`store`、`bus`、`runner`、`rpc`。
- 生命周期只放资源初始化和释放，长跑逻辑留给 `run.Group`。
- 统一通过 value group 收集 `Runner`，不要手写大列表。
- 参数多时优先 `fx.In`，多返回值优先 `fx.Out`。

## 4. Anti-pattern

- 在 `Provide` 的 constructor 里打开监听端口并永久阻塞。
- 用 `fx.Invoke` 做业务流程编排。
- 把所有模块都塞进一个超大 `Module`，失去边界。
- 把 `fx` 当 service locator，到处 `Populate`。

## 5. 与其他 5 个框架的集成方式

- 与 `oklog/run`：`fx` 提供 `Runner` 集合，`run.Group` 统一运行它们。
- 与 `jrpc2`：`fx` 负责装配 handler/service/server，不负责 RPC 循环。
- 与 `kelindar/event`：总线包装器通过 `fx` 注入到 service。
- 与 `qmuntal/stateless`：状态机宿主 service 在 `fx` 中构造。
- 与 `sqlc`：数据库 pool 和生成的 `Queries` 由 `fx` 管理。
