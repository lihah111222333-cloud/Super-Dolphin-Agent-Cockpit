# V3 模块化与依赖注入契约

版本：V3

状态：强制规范

适用范围：`super-agent-v3` 全仓库

DI 框架基线：`go.uber.org/fx` `v1.24.0`

相关框架：`fx`、`oklog/run`、`jrpc2`、`stateless`、`kelindar/event`、`sqlc`

---

## 背景与目标

V2 的根问题不是“字段太多”，而是对象边界失效。

`go-agent-v2/internal/apiserver.Server` 同时承担了以下职责：

- 资源拥有者
- 运行时状态容器
- Store 聚合器
- Provider 适配器装配点
- RPC 方法表
- 动态工具注册中心
- 后台 goroutine 启动器
- 偏好恢复器
- 子 Agent 恢复器
- SSE / WS / HTTP 传输状态入口

这直接导致：

- `Server` 变成 God Object
- `go-agent-v2/internal/apiserver/server_context.go` 出现大量 nil-guard 包装函数
- 构造依赖靠 mutation 分阶段塞字段
- 模块边界不清晰，Store 全是具体类型泄漏
- 单测必须构造整台 `Server`
- 启动步骤和运行步骤耦合在 `New()` 中

V3 必须把这些问题一次性切掉。

V3 的核心原则只有一句：

**运行时装配只通过 `fx` 容器完成，模块之间只通过接口和 DTO 通信，禁止通过共享大对象和字段赋值传递依赖。**

---

## 1. 模块化原则

### 1.1 总原则

1. 每个参与运行时装配的边界包必须导出一个 `Module`。
2. 模块之间只能依赖接口、DTO、事件、配置切片，不能依赖对方的具体实现。
3. 构造阶段只能用 `fx.Provide`，启动副作用只能放在 `fx.Invoke` 或 `fx.Lifecycle`。
4. 禁止任何形式的 mutation 注入，例如 `initStores(s, db)`、`s.xxx = newXxx(...)`。
5. 禁止把 `*Server`、`*App`、`*Container` 作为服务定位器继续向下传。
6. 纯叶子包例外：`dto`、`contract`、`sqlcgen`、常量包可以不导出 `Module`，但它们不能承载运行时行为。
7. 一个模块只拥有自己的状态，不拥有别的模块的内部状态。

### 1.2 包的边界定义

在 V3，下面这些才叫模块：

- `internal/platform/config`
- `internal/platform/db`
- `internal/platform/bus`
- `internal/platform/runner`
- `internal/platform/rpc`
- `internal/platform/provider`
- `internal/platform/statemachine`
- `internal/platform/monitor`
- `internal/provider/unified`
- `internal/provider/claudecli`
- `internal/provider/codexapp`
- `internal/tool/registry`
- `internal/tool/lsp`
- `internal/tool/orchestration`
- `internal/tool/ida`
- `internal/mcpserver/common`
- `internal/mcpserver/lsp`
- `internal/mcpserver/orch`
- `internal/mcpserver/ida`
- `internal/module/thread`
- `internal/module/skill`
- `internal/module/workspace`
- `internal/module/orchestration`
- `internal/module/uistate`
- `internal/module/ida`
- `internal/module/coderun`
- `internal/module/dashboard`
- `internal/ui/runtime`
- `internal/ui/dashboard`

下面这些不是模块，而是叶子依赖：

- `internal/contract/...`
- `internal/dto/...`
- `internal/store/sqlcgen/...`
- `internal/proto/...`

### 1.3 强制约束

- 模块对外导出：接口、参数对象、结果对象、DTO、`Module`
- 模块对外不导出：具体 service、具体 store、内部状态结构、临时缓存 map
- 模块只依赖下层平台模块，不能横向偷依赖
- 业务模块不能直接 import 别的业务模块 concrete type

### 1.4 示例

```go
package thread

import "go.uber.org/fx"

type Store interface {
	Save(threadID string) error
}

type Service interface {
	StartTurn(threadID string) error
}

type service struct {
	store Store
}

func NewService(store Store) *service {
	return &service{store: store}
}

func (s *service) StartTurn(threadID string) error {
	return s.store.Save(threadID)
}

var Module = fx.Module(
	"thread",
	fx.Provide(
		fx.Annotate(NewService, fx.As(new(Service))),
	),
)
```

上面的契约含义是：

- `thread` 包自己拥有 `service`
- 外部只能看到 `Service`
- 外部不需要也不允许依赖 `*service`

---

## 2. V3 包结构规范

### 2.1 完整目录树

```text
super-agent-v3/
├── cmd/
│   ├── super-agent/     ← 主桌面应用
│   │   └── main.go
│   ├── mcp-lsp/         ← LSP + RUN 工具家族
│   │   └── main.go
│   ├── mcp-orch/        ← 编排 + DAG 工具家族
│   │   └── main.go
│   ├── mcp-ida/         ← IDA 工具家族
│   │   └── main.go
│   └── migrate/         ← 数据库迁移
│       └── main.go
├── internal/
│   ├── app/
│   │   ├── module.go
│   │   └── bootstrap.go
│   ├── contract/
│   │   ├── rpc/
│   │   ├── event/
│   │   ├── runner/
│   │   ├── store/
│   │   └── provider/
│   ├── dto/
│   │   ├── rpc/
│   │   ├── event/
│   │   └── config/
│   ├── platform/
│   │   ├── config/
│   │   │   ├── module.go
│   │   │   ├── load.go
│   │   │   └── types.go
│   │   ├── db/
│   │   │   ├── module.go
│   │   │   ├── pool.go
│   │   │   └── queries.go
│   │   ├── bus/
│   │   │   ├── module.go
│   │   │   ├── publisher.go
│   │   │   ├── subscriber.go
│   │   │   └── registry.go
│   │   ├── runner/
│   │   │   ├── module.go
│   │   │   ├── actor.go
│   │   │   ├── group.go
│   │   │   └── lifecycle.go
│   │   ├── rpc/
│   │   │   ├── module.go
│   │   │   ├── server.go
│   │   │   ├── method.go
│   │   │   └── lifecycle.go
│   │   ├── provider/
│   │   │   ├── module.go
│   │   │   ├── registry.go
│   │   │   ├── codex/
│   │   │   └── claude/
│   │   ├── statemachine/
│   │   │   ├── module.go
│   │   │   └── factory.go
│   │   └── monitor/
│   │       ├── module.go
│   │       └── memory.go
│   ├── provider/            ← Provider 收敛层
│   │   ├── unified/         ← 统一语义：session/turn/capability/manifest
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   ├── session.go
│   │   │   └── manifest.go
│   │   ├── claudecli/       ← Claude CLI transport driver
│   │   │   ├── module.go
│   │   │   ├── driver.go
│   │   │   └── transport.go
│   │   └── codexapp/        ← Codex app-server transport driver
│   │       ├── module.go
│   │       ├── driver.go
│   │       └── transport.go
│   ├── tool/                ← 工具实现（按家族编译）
│   │   ├── registry/        ← 统一 tool registry + schema
│   │   │   ├── module.go
│   │   │   ├── schema.go
│   │   │   └── registry.go
│   │   ├── lsp/             ← → cmd/mcp-lsp
│   │   │   ├── module.go
│   │   │   ├── toolset.go
│   │   │   └── manifest.go
│   │   ├── orchestration/   ← → cmd/mcp-orch
│   │   │   ├── module.go
│   │   │   ├── toolset.go
│   │   │   └── manifest.go
│   │   └── ida/             ← → cmd/mcp-ida
│   │       ├── module.go
│   │       ├── toolset.go
│   │       └── manifest.go
│   ├── mcpserver/           ← 三家族 MCP binary
│   │   ├── common/          ← stdio loop + manifest
│   │   │   ├── module.go
│   │   │   ├── stdio.go
│   │   │   └── manifest.go
│   │   ├── lsp/
│   │   │   ├── module.go
│   │   │   ├── server.go
│   │   │   └── compose.go
│   │   ├── orch/
│   │   │   ├── module.go
│   │   │   ├── server.go
│   │   │   └── compose.go
│   │   └── ida/
│   │       ├── module.go
│   │       ├── server.go
│   │       └── compose.go
│   ├── store/
│   │   ├── sqlcgen/
│   │   │   ├── models.go
│   │   │   ├── querier.go
│   │   │   └── *.sql.go
│   │   ├── dag/
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   └── store.go
│   │   ├── prompt/
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   └── store.go
│   │   ├── sharedfile/
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   └── store.go
│   │   ├── workspace/
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   └── store.go
│   │   ├── binding/
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   └── store.go
│   │   └── ...
│   ├── module/
│   │   ├── thread/
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   ├── service.go
│   │   │   ├── rpc.go
│   │   │   └── events.go
│   │   ├── skill/
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   ├── service.go
│   │   │   └── loader.go
│   │   ├── workspace/
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   ├── service.go
│   │   │   └── rpc.go
│   │   ├── orchestration/
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   ├── service.go
│   │   │   ├── recover.go
│   │   │   └── runner_actor.go
│   │   ├── uistate/
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   ├── runtime.go
│   │   │   └── projection.go
│   │   ├── coderun/
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   ├── service.go
│   │   │   └── tool.go
│   │   ├── ida/
│   │   │   ├── module.go
│   │   │   ├── contract.go
│   │   │   ├── service.go
│   │   │   └── lifecycle.go
│   │   └── dashboard/
│   │       ├── module.go
│   │       ├── contract.go
│   │       ├── service.go
│   │       └── rpc.go
│   ├── ui/                  ← UI 视图层
│   │   ├── runtime/         ← 事件投影、timeline
│   │   │   ├── module.go
│   │   │   ├── projector.go
│   │   │   └── timeline.go
│   │   └── dashboard/       ← SSE、code_open
│   │       ├── module.go
│   │       ├── sse.go
│   │       └── rpc.go
│   └── archtest/
│       ├── dependency_direction_test.go
│       └── fx_graph_test.go
└── docs/
    └── 契约/
        └── modularity-convention.md
```

### 2.2 各层职责

| 层 | 目录 | 职责 | 允许依赖 |
|---|---|---|---|
| 入口层 | `cmd/*` | 只组装 `fx.New(...)` 并选择目标 binary，不写业务逻辑 | `internal/app`、`internal/mcpserver/*` |
| 应用层 | `internal/app` | 聚合桌面应用模块，定义启动顺序 | `platform`、`provider`、`tool`、`store`、`module`、`ui` |
| 平台层 | `internal/platform/*` | 提供基础设施能力 | 标准库、第三方库 |
| Provider 收敛层 | `internal/provider/*` | 统一 provider 语义，屏蔽 Claude CLI / Codex transport 差异，对上暴露 session / turn / capability / manifest | `contract`、`dto`、`platform`、`tool/registry` |
| Tool 层 | `internal/tool/*` | 统一 tool schema / registry，并按家族实现 LSP / orchestration / IDA 工具集 | `contract`、`dto`、`platform`、`store`、`module` |
| MCP Server 层 | `internal/mcpserver/*` | 组装 stdio loop、manifest 和工具家族，服务 `cmd/mcp-*` 二进制 | `platform`、`provider`、`tool` |
| 存储层 | `internal/store/*` | 包装 `sqlc` 和 DB 访问，对外暴露 store 接口 | `platform/db`、`sqlcgen` |
| 业务层 | `internal/module/*` | 承载领域逻辑、RPC 注册、事件处理 | `contract`、`dto`、`platform`、`provider/unified`、`store` |
| UI 视图层 | `internal/ui/*` | 运行时事件投影、timeline、dashboard SSE / code_open 等视图适配 | `contract`、`dto`、`platform`、`provider`、`module` |
| 契约层 | `internal/contract/*` | 纯接口、事件、常量 | 无运行时依赖 |
| DTO 层 | `internal/dto/*` | 纯数据结构 | 无运行时依赖 |
| 架构测试层 | `internal/archtest` | 依赖方向检查、图校验 | 全仓库只读 |

### 2.3 文件级规范

每个模块包至少包含：

- `module.go`：唯一的 `fx.Module`
- `contract.go`：对外接口和入出参 DTO
- `service.go`：核心领域逻辑
- `rpc.go` / `events.go` / `lifecycle.go`：按需要拆分

`provider/*`、`tool/*`、`mcpserver/*`、`ui/*` 也遵守同一原则，只是文件名会按职责替换成 `driver.go`、`registry.go`、`server.go`、`projector.go`、`timeline.go`、`sse.go` 等更贴近语义的名字。

`module.go` 只做装配，不写业务逻辑。

---

## 3. 接口契约范式

### 3.1 契约规则

1. 每个模块必须定义自己的 outward interface。
2. 接口定义放在拥有者模块，不放在消费者模块。
3. 模块对外暴露接口，对内保留 unexported concrete type。
4. Store 对外只能暴露接口，例如 `ThreadStore`，不能把 `*ThreadSQLStore` 传到外面。
5. DTO 只能是数据，不带行为依赖。

### 3.2 命名规范

- 服务接口：`Service`、`ThreadService`、`WorkspaceService`
- Store 接口：`Store`、`ThreadStore`、`PromptTemplateStore`
- 具体实现：`service`、`sqlStore`、`gateway`、`registry`
- 外部模块只依赖接口名，不依赖实现名

### 3.3 典型结构

```go
package workspace

import "context"

type Store interface {
	CreateRun(ctx context.Context, id string) error
}

type Service interface {
	CreateRun(ctx context.Context, id string) error
}

type service struct {
	store Store
}

func NewService(store Store) *service {
	return &service{store: store}
}

func (s *service) CreateRun(ctx context.Context, id string) error {
	return s.store.CreateRun(ctx, id)
}
```

### 3.4 通过 `fx.As` 暴露接口

```go
package workspace

import "go.uber.org/fx"

var Module = fx.Module(
	"workspace",
	fx.Provide(
		fx.Annotate(
			NewService,
			fx.As(new(Service)),
		),
	),
)
```

### 3.5 什么时候允许暴露具体类型

只允许在模块内部需要共享同一实例时，通过 `fx.Self()` 同时暴露 concrete type 和 interface。

```go
package workspace

import "go.uber.org/fx"

var Module = fx.Module(
	"workspace",
	fx.Provide(
		fx.Annotate(
			NewService,
			fx.As(new(Service)),
			fx.As(fx.Self()),
		),
	),
)
```

这表示：

- 外部优先注入 `Service`
- 模块内部极少数场景才允许使用 `*service`
- 不允许消费者模块依赖 `*service`

---

## 4. `fx.Module` 定义范式

### 4.1 模块结构

一个合格的模块必须把三类东西明确分开：

- `Provide`：构造对象
- `Invoke`：连接对象
- `Lifecycle`：启动和停止副作用

### 4.2 模板

```go
package rpc

import (
	"context"
	"net"

	"go.uber.org/fx"
)

type Server struct {
	addr string
	ln   net.Listener
}

func NewServer(lc fx.Lifecycle) *Server {
	s := &Server{addr: "127.0.0.1:9000"}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ln, err := net.Listen("tcp", s.addr)
			if err != nil {
				return err
			}
			s.ln = ln
			return nil
		},
		OnStop: func(context.Context) error {
			if s.ln != nil {
				return s.ln.Close()
			}
			return nil
		},
	})
	return s
}

func RegisterDefaultMethods(_ *Server) {
	// 这里只做对象连线，不做资源创建。
}

var Module = fx.Module(
	"rpc",
	fx.Provide(NewServer),
	fx.Invoke(RegisterDefaultMethods),
)
```

### 4.3 强制规则

- `Module` 变量名统一叫 `Module`
- `fx.Module("name", ...)` 的名字必须等于包名或稳定别名
- `Provide` 中禁止出现启动 goroutine 的副作用
- `Invoke` 中禁止创建共享资源并藏起来不返回
- `Lifecycle` 中禁止再手动查找依赖

### 4.4 `Invoke` 的正确定位

`Invoke` 只能做以下几类事情：

- RPC handler 注册到 server
- event subscriber 注册到 bus
- runner actor 注册到 `run.Group`
- 启动期状态恢复
- 必须执行一次的图连线验证

`Invoke` 不能替代 `Provide` 成为“另一个构造器”。

---

## 5. `fx.Provide` 范式

### 5.1 构造函数签名规范

V3 统一采用下面的签名规则：

- 输入：接口、配置切片、轻量参数对象、平台对象
- 输出：具体类型或 `fx.Out`
- 错误：最后一个返回值是 `error`
- 禁止输入：`*Server`、`*App`、全局上下文对象、临时 map 容器
- 禁止输出：接口切片的隐式拼装、匿名函数逃逸状态

### 5.2 推荐签名

```go
func NewService(store Store, clock Clock, cfg Config) *service
func NewGateway(p Params) (*gateway, error)
func NewRPCMethod(svc Service) MethodOut
func NewSubscribers(svc Service) SubscriberOut
```

### 5.3 不推荐签名

```go
func NewService(server *Server) *Service
func NewService(db *sql.DB, q *Queries, cfg *Config, logger *Logger, cache *Cache, bus *Bus, runner *Runner, rpc *RPC, bridge *Bridge) *Service
func BuildModule(container map[string]any) any
```

### 5.4 典型示例

```go
package thread

import "go.uber.org/fx"

type Clock interface {
	NowUnix() int64
}

type Store interface {
	Create(id string, createdAt int64) error
}

type Service interface {
	Create(id string) error
}

type service struct {
	clock Clock
	store Store
}

func NewService(clock Clock, store Store) *service {
	return &service{clock: clock, store: store}
}

func (s *service) Create(id string) error {
	return s.store.Create(id, s.clock.NowUnix())
}

var Module = fx.Module(
	"thread",
	fx.Provide(
		fx.Annotate(NewService, fx.As(new(Service))),
	),
)
```

### 5.5 `fx.Private` 使用规范

平台模块可以把不允许外部拿到的 concrete constructor 标成私有。

```go
package db

import "go.uber.org/fx"

type Pool struct{}

type Queries struct {
	pool *Pool
}

func NewPool() *Pool {
	return &Pool{}
}

func NewQueries(pool *Pool) *Queries {
	return &Queries{pool: pool}
}

var Module = fx.Module(
	"db",
	fx.Provide(NewPool),
	fx.Provide(NewQueries, fx.Private),
)
```

这条规则尤其适合：

- `sqlc` 生成的 `Queries`
- 内部缓存容器
- 模块私有 registry

---

## 6. `fx.In` / `fx.Out` 范式

### 6.1 使用目的

V2 的一个显著问题是长参数列表和分阶段赋值并存。

V3 规定：

- 参数超过 3 个，优先切成 `fx.In`
- 结果超过 1 个，优先切成 `fx.Out`
- 需要 named/group/optional 时，必须用 `fx.In` 或 `fx.Out`

### 6.2 `fx.In` 示例

```go
package workspace

import "go.uber.org/fx"

type Store interface {
	Save(id string) error
}

type Publisher interface {
	Publish(topic string, payload any) error
}

type Params struct {
	fx.In

	Store Store
	Bus   Publisher
}

type Service struct {
	store Store
	bus   Publisher
}

func NewService(p Params) *Service {
	return &Service{store: p.Store, bus: p.Bus}
}
```

### 6.3 `fx.Out` 示例

```go
package rpc

import "go.uber.org/fx"

type Handler interface {
	Name() string
}

type healthHandler struct{}

func (healthHandler) Name() string { return "health.check" }

type HandlerOut struct {
	fx.Out

	Handler Handler `group:"rpc.handlers"`
}

func NewHealthHandler() HandlerOut {
	return HandlerOut{Handler: healthHandler{}}
}
```

### 6.4 消费 group 的示例

```go
package rpc

import "go.uber.org/fx"

type Handler interface {
	Name() string
}

type ServerIn struct {
	fx.In

	Handlers []Handler `group:"rpc.handlers"`
}

type Server struct {
	Handlers []Handler
}

func NewServer(in ServerIn) *Server {
	return &Server{Handlers: in.Handlers}
}
```

### 6.5 规范结论

- group 用于多实现聚合
- name 用于同类型不同实例
- optional 用于可降级能力
- 复杂构造统一进 `fx.In`

---

## 7. 模块分组规范

V3 的分组不是按文件分，而是按**依赖角色**分。

### 7.1 `StoreModule`

职责：

- DB pool
- `sqlc` `Queries`
- 各类持久化 store 接口实现

输出约束：

- 对外只暴露接口
- `Queries` 默认 `fx.Private`

建议 group：无

### 7.2 `BusModule`

职责：

- `kelindar/event` bus 实例
- publisher
- subscriber 注册表

建议 group：`bus.subscribers`

### 7.3 `RunnerModule`

职责：

- `oklog/run.Group`
- runner actor 聚合
- 后台 worker 的 start/stop 编排

Runner 契约约束：

```go
package runner

import "context"

type Runner interface {
	Run(ctx context.Context) error
}
```

`platform/runner` 负责把 `Runner` 适配成 `run.Group` actor，业务模块不导出 `Register(*run.Group)` 形式的旧接口。

建议 group：`runner.actors`

### 7.4 `RPCModule`

职责：

- `jrpc2` server
- 方法注册
- handler 聚合

建议 group：`rpc.handlers`

### 7.5 `ProviderModule`

职责：

- `internal/provider/unified` 统一 Provider 语义和 session / turn / capability / manifest
- `internal/provider/claudecli`、`internal/provider/codexapp` 提供具体 transport driver
- provider registry / adapter 装配
- 外部桥接器

建议 group：`provider.adapters`

补充约束：

- `tool.handlers` 由 `internal/tool/*` 家族输出，不再混在 provider concrete package 中
- provider driver 只依赖统一语义与 tool registry，不直接依赖 UI 层

### 7.6 分组示例

```go
package runner

import (
	"github.com/oklog/run"
	"go.uber.org/fx"
)

type Actor struct {
	Execute   func() error
	Interrupt func(error)
}

type ActorOut struct {
	fx.Out

	Actor Actor `group:"runner.actors"`
}

type GroupIn struct {
	fx.In

	Actors []Actor `group:"runner.actors"`
}

func NewGroup(in GroupIn) *run.Group {
	g := &run.Group{}
	for _, actor := range in.Actors {
		g.Add(actor.Execute, actor.Interrupt)
	}
	return g
}

func NewHeartbeatActor() ActorOut {
	return ActorOut{
		Actor: Actor{
			Execute: func() error { return nil },
			Interrupt: func(error) {},
		},
	}
}

var Module = fx.Module(
	"runner",
	fx.Provide(NewHeartbeatActor),
	fx.Provide(NewGroup),
)
```

### 7.7 分组命名统一表

| group 名 | 用途 |
|---|---|
| `rpc.handlers` | jrpc2 方法处理器 |
| `runner.actors` | oklog/run actor |
| `bus.subscribers` | 事件订阅器 |
| `tool.handlers` | 动态工具处理器 |
| `provider.adapters` | provider 适配器 |
| `state.machines` | `stateless` 状态机工厂 |

---

## 8. 配置注入范式

### 8.1 配置规则

1. 配置只能在 `platform/config` 模块解析一次。
2. 环境变量和配置文件都先汇总到一个 `AppConfig`。
3. 各模块只拿自己需要的切片配置，不拿整份全局配置。
4. 配置对象是只读值对象，不能在运行时被模块写回。
5. 需要动态偏好恢复的内容，不算启动配置，单独走运行时 store。

### 8.2 推荐拆分

- 静态配置：env / file / flags，启动时确定
- 运行时偏好：DB / shared_file / preference store，启动后可恢复

### 8.3 配置注入示例

```go
package config

import (
	"os"

	"go.uber.org/fx"
)

type AppConfig struct {
	DB  DBConfig
	RPC RPCConfig
}

type DBConfig struct {
	DSN string
}

type RPCConfig struct {
	ListenAddr string
}

type Out struct {
	fx.Out

	App AppConfig
	DB  DBConfig  `name:"db"`
	RPC RPCConfig `name:"rpc"`
}

func Load() (Out, error) {
	cfg := AppConfig{
		DB: DBConfig{DSN: os.Getenv("APP_DSN")},
		RPC: RPCConfig{ListenAddr: os.Getenv("APP_RPC_ADDR")},
	}
	if cfg.RPC.ListenAddr == "" {
		cfg.RPC.ListenAddr = "127.0.0.1:8080"
	}
	return Out{App: cfg, DB: cfg.DB, RPC: cfg.RPC}, nil
}

var Module = fx.Module(
	"config",
	fx.Provide(Load),
)
```

### 8.4 消费切片配置的示例

```go
package db

import "go.uber.org/fx"

type Config struct {
	DSN string
}

type In struct {
	fx.In

	Config Config `name:"db"`
}

type Pool struct {
	DSN string
}

func NewPool(in In) *Pool {
	return &Pool{DSN: in.Config.DSN}
}
```

### 8.5 运行时偏好恢复规则

V2 里像 `stallThresholdSec`、`showInjectedPromptInChat` 这种配置，不应跟静态配置混在一起。

V3 规定：

- 静态启动配置：`platform/config`
- 运行时偏好：`module/uistate` 或 `module/preferences`
- 恢复动作：放 `fx.Invoke(RestorePreferences)` 或 `Lifecycle.OnStart`

---

## 9. 可选依赖范式

### 9.1 关键澄清

在 `fx` `v1.24.0` 中，**没有单独的 `fx.Optional(...)` API**。

正确做法是：

- 在 `fx.In` 字段上使用 ``optional:"true"``
- 或在 `fx.Annotate(..., fx.ParamTags(...))` 里打 ``optional:"true"``

### 9.2 适用场景

- Wails bridge 可选
- IDA provider 可选
- 调试监控组件可选
- 仅桌面端存在的 UI 通知器可选

### 9.3 正确示例

```go
package notify

import "go.uber.org/fx"

type WailsBridge interface {
	Emit(event string, payload any)
}

type In struct {
	fx.In

	Bridge WailsBridge `optional:"true"`
}

type Notifier struct {
	bridge WailsBridge
}

func NewNotifier(in In) *Notifier {
	return &Notifier{bridge: in.Bridge}
}

func (n *Notifier) Publish(event string, payload any) {
	if n == nil || n.bridge == nil {
		return
	}
	n.bridge.Emit(event, payload)
}
```

### 9.4 使用规则

- 可选依赖只能用于真正可降级能力
- 不允许把核心依赖做成 optional 逃避图错误
- optional 构造函数内部必须显式处理 nil / zero value
- 不允许在 optional 缺失时 silently panic

### 9.5 `fx.Annotate` 形式

```go
package notify

import "go.uber.org/fx"

type WailsBridge interface {
	Emit(event string, payload any)
}

type Notifier struct {
	bridge WailsBridge
}

func NewNotifier(bridge WailsBridge) *Notifier {
	return &Notifier{bridge: bridge}
}

var Module = fx.Module(
	"notify",
	fx.Provide(
		fx.Annotate(
			NewNotifier,
			fx.ParamTags(`optional:"true"`),
		),
	),
)
```

---

## 10. 测试隔离范式

### 10.1 测试目标

V3 单测必须能做到：

- 只起一个模块
- 替换单个依赖
- 不构造整台应用
- 可以直接 `Populate` 被测对象

### 10.2 测试规则

1. 单测优先起最小模块，而不是整个 `AppModule`。
2. 替换接口实现优先用 `fx.Decorate`，不要滥用 `fx.Replace`。
3. 需要拿到对象时用 `fx.Populate`。
4. 生命周期测试用 `fxtest.New(t, ...)`。
5. 测试一个模块时，其他模块用 fake / stub / in-memory 实现替换。

### 10.3 示例

```go
package thread_test

import (
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

type Store interface {
	Save(threadID string) error
}

type realStore struct{}

func (realStore) Save(string) error { return nil }

type fakeStore struct {
	called bool
}

func (f *fakeStore) Save(string) error {
	f.called = true
	return nil
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) Run() error {
	return s.store.Save("t-1")
}

func TestService(t *testing.T) {
	fake := &fakeStore{}
	var svc *Service

	app := fxtest.New(t,
		fx.Provide(
			func() Store { return realStore{} },
			NewService,
		),
		fx.Decorate(func(Store) Store { return fake }),
		fx.Populate(&svc),
	)

	app.RequireStart()
	defer app.RequireStop()

	if err := svc.Run(); err != nil {
		t.Fatal(err)
	}
	if !fake.called {
		t.Fatal("expected fake store to be called")
	}
}
```

### 10.4 `fx.Replace` 的边界

`fx.Replace` 在接口场景下容易踩“按最具体类型替换”的坑。

因此 V3 规定：

- 替换具体类型实例可以用 `fx.Replace`
- 替换接口实现优先用 `fx.Decorate`
- 除非非常确定类型行为，否则测试里不推荐直接 `fx.Replace(interfaceValue)`

### 10.5 模块级测试模板

- store 模块：起 `platform/db` + 单 store 模块
- service 模块：起 fake store + fake bus + 被测模块
- rpc 模块：起 fake service + rpc 模块，只校验注册结果
- runner 模块：起 `fxtest.NewLifecycle` 或最小 `run.Group`

---

## 11. 模块边界强制

### 11.1 为什么必须强制

只靠 code review，最终一定会回到 V2：

- A 模块偷 import B 的 concrete store
- B 模块又反向拿 A 的 runtime state
- 直到某天整个图开始循环

V3 必须同时用两层机制强制边界：

- 容器层：`fx.Private`、`fx.ValidateApp`
- 代码层：架构测试检查 import 方向

### 11.2 依赖方向规则

平台层依赖方向：

- `contract` / `dto` 不依赖任何业务模块
- `platform/*` 不依赖 `module/*`
- `store/*` 只依赖 `platform/db`、`sqlcgen`、`contract`、`dto`
- `module/*` 可以依赖 `platform/*`、`store/*`、`contract`、`dto`
- `module/*` 之间不允许互相依赖 concrete package

### 11.3 `fx.ValidateApp` 示例

```go
package archtest

import (
	"testing"

	"go.uber.org/fx"
)

type Queries struct{}

type ThreadStore interface {
	Save(threadID string) error
}

type sqlStore struct {
	q *Queries
}

func NewQueries() *Queries {
	return &Queries{}
}

func NewThreadStore(q *Queries) *sqlStore {
	return &sqlStore{q: q}
}

func (s *sqlStore) Save(string) error { return nil }

var StoreModule = fx.Module(
	"store.thread",
	fx.Provide(NewQueries, fx.Private),
	fx.Provide(
		fx.Annotate(NewThreadStore, fx.As(new(ThreadStore))),
	),
)

func TestFXGraph(t *testing.T) {
	if err := fx.ValidateApp(StoreModule); err != nil {
		t.Fatal(err)
	}
}
```

### 11.4 import 方向测试示例

```go
package archtest

import (
	"os/exec"
	"strings"
	"testing"
)

func TestThreadModuleMustNotDependOnRPCModule(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "./internal/module/thread")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list failed: %v\n%s", err, out)
	}

	deps := string(out)
	if strings.Contains(deps, "/internal/module/rpc") {
		t.Fatal("thread module must not depend on rpc module")
	}
}
```

### 11.5 额外强制点

- `sqlcgen` 只允许被 `store/*` import
- `provider` concrete driver 只允许被 `internal/provider/*` 内部包 import
- `tool/*` concrete family 只允许被 `internal/tool/*` 与 `internal/mcpserver/*` 组装层引用
- `module/*` 对外暴露接口，不暴露实现
- 不允许在 `cmd/` 里临时手工 new service 绕过 `fx`

---

## 12. God Object 拆解指南

### 12.1 拆解原则

V2 的 `Server` 不是“一个对象太大”，而是“多个子系统共享了错误的宿主”。

V3 的拆法不是把 `Server` 切成几个更小的 `ServerXxx`，而是把字段归还给拥有它的模块。

### 12.2 V2 `Server` 字段归属建议

| V2 字段/状态组 | V3 归属模块 | 对外暴露 |
|---|---|---|
| `mgr` | `platform/runner` 或 `module/orchestration` | `runner.Manager` 接口 |
| `lsp`、`lspTools` | `tool/lsp` / `mcpserver/lsp` / `module/coderun` | `LSPToolSet` 接口 |
| `cfg` | `platform/config` | 配置切片 |
| `db` | `platform/db` | `DBTX` / `Pool` |
| `codeRunner` | `module/coderun` | `CodeRunner` 接口 |
| `providerAdapter` | `provider/unified` / `provider/claudecli` / `provider/codexapp` | `ProviderAdapter` 接口 |
| `methods` | `platform/rpc` | `[]RPCHandler` group |
| `dynTools` | `tool/registry` / `tool/*` | `[]ToolHandler` group |
| `storeBundle` | `store/*` 各模块 | 各自 store interface |
| `workspaceMgr` | `module/workspace` | `WorkspaceService` |
| `prefManager` | `module/uistate` / `ui/runtime` | `PreferenceStore` / `PreferenceService` |
| `uiRuntime` | `ui/runtime` | `UIRuntime` 接口 |
| `projectScopeState` | `ui/runtime` / `module/workspace` | `ProjectScope` |
| `threadAliasState` | `module/thread` | `AliasRegistry` |
| `connManagerState`、`sseClients`、`transportState` | `platform/rpc` / `ui/dashboard` | `TransportHub` |
| `diagnosticsCacheState` | `module/coderun` 或 `tool/lsp` | `DiagnosticsCache` |
| `toolCallState` | `provider/unified` / `tool/registry` | `ToolCallCounter` |
| `codeRunState` | `module/coderun` | `WorkDirRegistry`、`RunRegistry` |
| `turnTrackingState` | `module/thread` / `module/orchestration` / `provider/unified` | `TurnTracker` |
| `notifyHookState` | `platform/bus` / `platform/rpc` | `Notifier` |
| `uiThrottleState`、`legacyMirrorDelayState` | `ui/runtime` | 不外露 |
| `threadPatch` | `ui/runtime` | `PatchProjector` |
| `runtimeServiceState` | `module/orchestration` | 明确接口切分 |

### 12.3 拆解顺序

1. 先把所有 store 从 `Server` 拆出去，每个 store 单独成模块。
2. 再把 runtime state 按职责拆成 `uistate`、`coderun`、`transport`、`orchestration`。
3. 再把方法表和动态工具表改成 group 聚合。
4. 最后让顶层应用只保留 `fx.New(...)` 和极少量 `Populate` 出来的根对象。

### 12.4 顶层根对象只允许保留什么

V3 顶层如果还需要一个聚合根，只能保留：

- 已经组装好的少量高阶接口
- 进程入口必须持有的生命周期对象
- 运行主循环时要用到的少量 facade

禁止再出现一个承载 50+ 字段的根对象。

### 12.5 拆解后的示例

```go
package app

import "go.uber.org/fx"

type RuntimeState struct{}

type TransportHub struct{}

type ToolRegistry struct{}

type In struct {
	fx.In

	State *RuntimeState
	Hub   *TransportHub
	Tools *ToolRegistry
}

type App struct {
	State *RuntimeState
	Hub   *TransportHub
	Tools *ToolRegistry
}

func NewRuntimeState() *RuntimeState { return &RuntimeState{} }
func NewTransportHub() *TransportHub { return &TransportHub{} }
func NewToolRegistry() *ToolRegistry { return &ToolRegistry{} }

func NewApp(in In) *App {
	return &App{State: in.State, Hub: in.Hub, Tools: in.Tools}
}

var Module = fx.Options(
	fx.Module("uistate", fx.Provide(NewRuntimeState)),
	fx.Module("transport", fx.Provide(NewTransportHub)),
	fx.Module("tools", fx.Provide(NewToolRegistry)),
	fx.Module("app", fx.Provide(NewApp)),
)
```

这个 `App` 只是聚合少量根对象，不再承担构造职责。

---

## 13. 反模式

下面这些做法在 V3 一律禁止。

### 13.1 反模式：mutation 注入

```go
package anti

type DB struct{}

type Store struct {
	db *DB
}

type Server struct {
	Store *Store
}

func NewStore(db *DB) *Store {
	return &Store{db: db}
}

func initStores(s *Server, db *DB) {
	s.Store = NewStore(db)
}
```

禁止原因：

- 依赖关系不在类型系统里
- 无法用 `fx.ValidateApp` 校验
- 单测必须手工模拟装配顺序

### 13.2 反模式：服务定位器

```go
package anti

type Container struct {
	deps map[string]any
}

func (c *Container) Get(name string) any {
	return c.deps[name]
}

type Service struct {
	c *Container
}

func NewService(c *Container) *Service {
	return &Service{c: c}
}
```

禁止原因：

- 类型信息丢失
- 运行时字符串查依赖
- 图错误拖到线上才爆

### 13.3 反模式：构造函数里启动 goroutine

```go
package anti

import "time"

type Worker struct{}

func NewWorker() *Worker {
	go func() {
		for range time.Tick(time.Second) {
		}
	}()
	return &Worker{}
}
```

禁止原因：

- 资源启动不可控
- 测试无法可靠停止
- 会绕开 `fx.Lifecycle`

### 13.4 正确替代

```go
package good

import (
	"context"
	"time"

	"go.uber.org/fx"
)

type Worker struct{}

func NewWorker(lc fx.Lifecycle) *Worker {
	w := &Worker{}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for range ticker.C {
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			return nil
		},
	})
	return w
}
```

### 13.5 反模式总表

| 反模式 | 禁止原因 | V3 替代 |
|---|---|---|
| God Object 持有所有依赖 | 边界失效 | 每模块自有状态 + 顶层聚合 |
| `initXxx(s, dep)` 赋字段 | mutation 注入 | `fx.Provide` |
| nil-guard 包装器满天飞 | 依赖未建模 | 用类型系统表达依赖 |
| Store 暴露具体类型 | concrete 泄漏 | `fx.As(new(Store))` |
| 构造函数里启动 goroutine | 生命周期失控 | `fx.Lifecycle` |
| `Invoke` 偷做构造 | 图不透明 | 把对象创建移回 `Provide` |
| 到处 optional | 掩盖缺依赖 | 只对可降级能力标 optional |
| 测试起整台应用 | 单测成本高 | 最小模块 + `fxtest` |

---

## 14. V2 → V3 对照表

下面的对照表以 V2 `go-agent-v2/internal/apiserver.New()` 的真实装配链路为基准。

V2 当前主链路是：

- `initStores(s, deps.DB)`
- `initRuntimeWiring(s)`
- `initSkills(s, deps.SkillsDir)`
- `s.registerMethods()`
- `applyStallConfig(s, deps.Config)`
- `ensureStallPreferenceFromDB(s)`
- `initCodeRunner(s)`
- `initIDAClient(s)`
- `recoverSubAgents(s)`
- `applyInjectedPromptVisibilityPreference(s, context.Background())`
- `registerDynamicTools(s)`
- `setNotifyHookState(...)`
- `startMemoryStatsTicker(s)`

### 14.1 对照表

| V2 步骤 | V2 语义 | V3 模块 | V3 `fx` 形式 |
|---|---|---|---|
| `initStores` | 构造所有 store 并塞到 `Server` | `store/*` + `platform/db` | 多个 `fx.Provide` |
| `initRuntimeWiring` | provider adapter、LSP tool、busy checker、phase1 controller | `provider/unified`、`tool/lsp`、`platform/rpc`、`platform/runner` | `fx.Provide` + 局部 `fx.Invoke` |
| `initSkills` | 选择 skills dir、创建 skill service / manager | `module/skill` | `fx.Provide` |
| `registerMethods` | RPC 方法表注册 | `platform/rpc` + 各业务模块 | handler group `fx.Out` + `fx.Invoke(RegisterHandlers)` |
| `applyStallConfig` | 静态配置作用到 provider adapter | `platform/config` + `provider/unified` | `fx.Invoke(ApplyStallPolicy)` |
| `ensureStallPreferenceFromDB` | 从偏好 store 恢复 runtime timeout | `module/uistate`、`ui/runtime` 或 `module/preferences` | `fx.Invoke(RestoreRuntimePreferences)` |
| `initCodeRunner` | 创建 code runner | `module/coderun` | `fx.Provide(NewCodeRunner)` |
| `initIDAClient` | 启动 IDA gateway | `module/ida` + `tool/ida` | `fx.Provide(NewGateway)` + `Lifecycle` |
| `recoverSubAgents` | 启动期恢复子 agent | `module/orchestration` | `fx.Invoke(RecoverSubAgents)` |
| `applyInjectedPromptVisibilityPreference` | 恢复 UI 注入提示词可见性 | `module/uistate` + `ui/runtime` | `fx.Invoke(ApplyUIPreferences)` |
| `registerDynamicTools` | 动态工具注册表聚合 | `tool/registry` + `tool/*` | tool group `fx.Out` + `fx.Invoke(BuildToolRegistry)` |
| `setNotifyHookState` | 统一通知桥接 | `platform/bus` + `platform/rpc` + `ui/dashboard` | `fx.Invoke(BindNotifierBridge)` |
| `startMemoryStatsTicker` | 内存状态监控后台任务 | `platform/monitor` | `fx.Provide` + `Lifecycle` |

### 14.2 V2 store 分解到 V3

| V2 字段 | V3 模块 |
|---|---|
| `dagStore` | `internal/store/dag` |
| `cmdStore` | `internal/store/commandcard` |
| `promptStore` | `internal/store/prompt` |
| `fileStore` | `internal/store/sharedfile` |
| `workspaceRunStore` | `internal/store/workspace` |
| `sysLogStore` | `internal/store/systemlog` |
| `agentStatusStore` | `internal/store/agentstatus` |
| `auditLogStore` | `internal/store/auditlog` |
| `aiLogStore` | `internal/store/ailog` |
| `busLogStore` | `internal/store/buslog` |
| `taskAckStore` | `internal/store/taskack` |
| `taskTraceStore` | `internal/store/tasktrace` |
| `bindingStore` | `internal/store/binding` |
| `agentThreadStore` | `internal/store/agentthread` |

### 14.3 V2 `go-agent-v2/internal/apiserver/server_context.go` 的替代思路

V2 的 50+ nil-guard wrapper 本质是在补救“依赖没有进入类型系统”。

V3 替代规则：

- 若状态归属于某模块：把方法移回该模块对象
- 若是跨模块只读能力：抽成 interface
- 若是共享 map 状态：封成拥有锁的私有对象，并由 owning module 暴露接口
- 若是通知桥接：进入 `bus` / `rpc` 模块，不再挂在全局上下文函数上

### 14.4 V3 顶层组装模板

```go
package app

import "go.uber.org/fx"

var Module = fx.Options(
	ConfigModule,
	DBModule,
	StoreModule,
	BusModule,
	RunnerModule,
	RPCModule,
	ProviderModule,
	ToolModule,
	ThreadModule,
	SkillModule,
	WorkspaceModule,
	UIStateModule,
	UIRuntimeModule,
	UIDashboardModule,
	CodeRunModule,
	OrchestrationModule,
	IDAModule,
	MonitorModule,
)
```

### 14.5 `main.go` 模板

```go
package main

import (
	"go.uber.org/fx"

	"super-agent-v3/internal/app"
)

func main() {
	fx.New(
		app.Module,
	).Run()
}
```

`cmd/mcp-lsp`、`cmd/mcp-orch`、`cmd/mcp-ida` 也遵循同一入口原则，但它们组装的是 `internal/mcpserver/common` 加对应家族模块，而不是桌面 UI 模块。

---

## 落地检查清单

每次新增模块时，必须同时满足下面 12 条：

- 包内有且只有一个 `Module`
- 对外只暴露接口和 DTO
- 具体实现是 unexported type
- 构造函数不接收 `*Server` 或服务定位器
- 构造函数不做副作用启动
- 启动副作用只放 `Lifecycle` 或 `Invoke`
- 超过 3 个参数时改用 `fx.In`
- 多结果或 group 输出时改用 `fx.Out`
- 需要可选依赖时使用 ``optional:"true"``
- 内部实现如 `sqlc Queries` 默认 `fx.Private`
- 模块有最小 `fxtest` 单测
- 架构测试覆盖 import 方向

---

## 迁移建议顺序

建议按下面顺序从 V2 迁到 V3：

1. 先引入 `platform/config`、`platform/db`、`store/*`，把 `initStores` 全部替换成 `fx.Provide`。
2. 再把 `provider/unified`、`tool/registry`、`codeRunner`、`workspaceMgr`、`prefManager`、`uiRuntime` 单独模块化。
3. 再把 `registerMethods` 改成 `rpc.handlers` group 聚合。
4. 再把 `registerDynamicTools` 改成 `tool.handlers` group 聚合，并拆到 `internal/tool/*` 家族。
5. 再把 `cmd/mcp-lsp`、`cmd/mcp-orch`、`cmd/mcp-ida` 从桌面主入口中剥离成独立 `mcpserver/*` 组合。
6. 再把 `recoverSubAgents`、timeout 恢复、UI 偏好恢复改成 `Invoke`。
7. 最后删除 `Server` 上残存的状态聚合职责，让顶层只剩 `fx.App` 入口。

---

## 结论

V3 的核心不是“用了 `fx`”，而是把依赖关系、生命周期、模块边界重新拉回类型系统和模块系统。

判断一个改动是否符合 V3 契约，只看三件事：

- 依赖是不是通过 `fx` 明确声明了
- 模块是不是只暴露接口而不是具体实现
- 启动副作用是不是进入了 `Invoke` / `Lifecycle`

只要这三条被持续执行，V2 的 God Object、nil-guard 壳层、mutation 注入、整机单测四个问题会一起消失。

---

## 参考

- `fx` 包文档：<https://pkg.go.dev/go.uber.org/fx>
- `fxtest` 包文档：<https://pkg.go.dev/go.uber.org/fx/fxtest>
- `fx` 版本常量文档（`Version = "1.24.0"`）：<https://pkg.go.dev/go.uber.org/fx>
