# V3 六框架使用指南

> 依据：
> - `docs/契约/fx-convention.md`
> - `docs/契约/rungroup-convention.md`
> - `docs/契约/sqlc-convention.md`
> - `docs/契约/jrpc2-convention.md`
> - `docs/契约/statemachine-event-convention.md`
> - 当前仓库前置骨架：`internal/app/module.go`、`internal/rpc/*`、`internal/bus/*`、`internal/runner/*`、`internal/store/module.go`
>
> 矫正：
> - `stateless` / `event` 的正式契约在 `statemachine-event-convention.md`，不是单独文件。
> - 当前 `internal/bus/event_bus.go` 使用包级 `event.Emit` / `event.On` 只适合 demo，正式 V3 必须改成注入的 `*event.Dispatcher`。
> - 当前 `internal/store/module.go` 把 DB 和 store 混在一起，只能视为迁移前置骨架，不是最终形态。

## 1. fx 使用指南

### DO
- 用 `fx.Module("<boundary>", ...)` 按边界建模块，不要按技术小函数随意拼。
- constructor 只创建对象和返回错误；初始化与释放放 `fx.Lifecycle`。
- 用 `fx.In` / `fx.Out` 管理多依赖、多输出。
- 长跑组件统一输出到 `group:"runners"`，交给 `run.Group`。
- 用 `fx.Annotate(..., fx.As(...))` 暴露接口，而不是泄漏 concrete type。
- 图校验优先 `fx.ValidateApp`；生命周期 smoke 优先 `fxtest.New(...)`。

### DON'T
- 不要在 constructor 里启动 goroutine、打开 listener 后永久阻塞。
- 不要用 `fx.Invoke` 承担业务编排。
- 不要把 `*App` / `*Server` 当 service locator 到处传。
- 不要在业务实现文件里 import `fx`；`fx` 只出现在 `module.go` 或入口层。
- 不要用 `fx.Replace(interfaceValue)` 替换接口实现；优先 `fx.Decorate`。

### 示例

```go
package thread

import (
	"context"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

type Store interface {
	Save(context.Context, string) error
}

type Service interface {
	Start(context.Context, string) error
}

type service struct{ store Store }

type fakeStore struct{ called bool }

func (f *fakeStore) Save(context.Context, string) error {
	f.called = true
	return nil
}

func NewService(store Store) *service {
	return &service{store: store}
}

func (s *service) Start(ctx context.Context, threadID string) error {
	return s.store.Save(ctx, threadID)
}

var Module = fx.Module(
	"thread",
	fx.Provide(
		fx.Annotate(NewService, fx.As(new(Service))),
	),
)

func TestModule(t *testing.T) {
	fake := &fakeStore{}
	var svc Service

	app := fxtest.New(t,
		Module,
		fx.Provide(func() Store { return fake }),
		fx.Populate(&svc),
	)
	app.RequireStart()
	defer app.RequireStop()

	_ = svc.Start(context.Background(), "thread-1")
}
```

### V2 -> V3
- V2：`initStores(s, db)`、`s.xxx = newXxx(...)`、构造后再 mutation 注入。
- V3：`fx.Module` + interface constructor + `fx.ValidateApp`。
- V2：`Server` 同时持有 LSP、DB、runner、UI、tool registry。
- V3：每个边界单独导出 `Module`，入口层只做组合。

### 测试要求
- 每个模块至少有一个 `fx.ValidateApp(...)` 图校验。
- 每个带生命周期 hook 的模块至少有一个 `fxtest.New(...)` 启停测试。
- 替身注入优先 `fx.Decorate`，不要测试时起整台应用。
- `module/*` 单测只起最小模块，不起完整 `AppModule`。

#### 守卫与测试门槛
- `fx` 只允许出现在 `internal/app`、`cmd/*`、`platform/*/module.go`、`store/*/module.go`、`module/*/module.go` 以及少数装配入口。
- `fx_graph_test.go` 必须对每个模块组合运行 `fx.ValidateApp(...)`；带 lifecycle 的边界还要有最小 `fxtest.New(...)` smoke。
- 业务实现文件一旦 import `fx` 直接失败；`fx.Invoke` 只做装配，不承接业务编排。

## 2. run.Group 使用指南

### DO
- 所有长跑组件统一实现 `Run(ctx context.Context) error`。
- 每个 actor 必须同时定义 execute 和 interrupt 语义。
- 信号处理单独作为 actor。
- 顶层退出语义统一为“一处返回，全部收敛”。
- actor 中占用的 listener/ticker/subscription 必须在 interrupt 里显式关闭。

### DON'T
- 不要在 `execute` 内再偷偷起匿名 goroutine。
- 不要把一次性初始化塞到 `run.Group`。
- 不要在 `interrupt` 里做重型数据库清理或复杂网络调用。
- 不要让 `fx.OnStart` 替代 `run.Group`。

### 示例

```go
package runnerhost

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/oklog/run"
)

type Runner interface {
	Run(context.Context) error
}

func RunAll(parent context.Context, runners []Runner) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	var g run.Group
	for _, r := range runners {
		r := r
		g.Add(
			func() error { return r.Run(ctx) },
			func(error) { cancel() },
		)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	g.Add(
		func() error {
			sig := <-signals
			return fmt.Errorf("received %s", sig)
		},
		func(error) {
			signal.Stop(signals)
			cancel()
		},
	)

	return g.Run()
}
```

### V2 -> V3
- V2：`sync.WaitGroup`、`SafeGo`、`StopAll`、`KillAll` 散落在 `internal/runner`。
- V3：`platform/runner` 统一托管 actor，模块只输出 `Runner`。
- V2：recover / watcher / server 各自起 goroutine。
- V3：recover、watcher、RPC server 都是显式 actor。

### 测试要求
- `run.Group` 宿主需要 smoke test，验证一个 actor 返回错误时其他 actor 会收到取消。
- 每个 actor 至少有一个 interrupt 路径测试。
- 信号 actor 要有可替代 channel 的测试接口，不直接绑真实 OS 信号。

#### 守卫与测试门槛
- 长跑组件只能通过 `group:"runners"` 注入；禁止在 `fx.OnStart` 里裸起无监管 goroutine。
- orchestration、ida、RPC server 这类 actor 必须验证“一处返回、全部收敛”和 interrupt 路径。
- 并发与错误路径守卫要锁定取消传播、资源释放和退出原因记录，不接受“最终能停下来”这种弱断言。

## 3. sqlc 使用指南

### DO
- 把静态查询全部落到 `sql/queries/*.sql`。
- 根目录固定 `sqlc.yaml`，schema 指向顶层 `migrations/`。
- service/store 默认依赖 `sqlc.Querier`，不是自己 `sqlc.New(pool)`。
- 事务内统一走 `Queries.WithTx(tx)`。
- 生成目录固定 `internal/store/sqlc/`，并视为只读。
- 手写 repo 只保留领域语义聚合，不重复 scan/placeholder/column list。

### DON'T
- 不要把普通 CRUD 留在手写 Store 里。
- 不要在生成目录里放手写 helper。
- 不要为 `sqlc` 再手写一份同名大接口。
- 不要把动态 SQL 的 `DBQueryStore` 强行迁进 `sqlc`。

### 示例

```sql
-- sql/queries/thread.sql

-- name: GetThread :one
SELECT thread_id, title, archived, updated_at
FROM agent_threads
WHERE thread_id = sqlc.arg(thread_id)
LIMIT 1;

-- name: UpsertThread :one
INSERT INTO agent_threads (thread_id, title, archived)
VALUES (sqlc.arg(thread_id), sqlc.arg(title), sqlc.arg(archived))
ON CONFLICT (thread_id) DO UPDATE SET
  title = EXCLUDED.title,
  archived = EXCLUDED.archived,
  updated_at = NOW()
RETURNING thread_id, title, archived, updated_at;
```

```go
package threadstore

import (
	"context"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type Store struct {
	q storedb.Querier
}

func NewStore(q storedb.Querier) *Store {
	return &Store{q: q}
}

func (s *Store) Get(ctx context.Context, threadID string) (storedb.AgentThread, error) {
	return s.q.GetThread(ctx, storedb.GetThreadParams{ThreadID: threadID})
}
```

### V2 -> V3
- V2：`BaseStore` + `squirrel` + 手写 query builder + 手写 scan。
- V3：SQL 文件 + `sqlc generate` + repo adapter。
- V2：每个 Store 自己管 tx、占位符和列名清洗。
- V3：`pgx.Tx` + `Queries.WithTx(tx)`；技术细节收敛到 `platform/db`。

### 测试要求
- SQL 语义、索引、锁、事务隔离必须跑真实 PostgreSQL 集成测试。
- 单元测试只 mock `Querier` 窄接口，不 mock SQL 结果集实现。
- CI 流程至少包含：migration -> `sqlc generate` -> `sqlc vet` -> `go test`。
- `DBQueryStore` 例外要单独测只读校验和 sandbox，不混入普通 store 测试。

#### 守卫与测试门槛
- `internal/store/sqlc/` 或 `sqlcgen` 只允许被 `store/*` import；生成目录视为只读，禁止手写 helper 混入。
- 每个手写 store 包都必须保留 D2/D5/D6/D7 守卫：副作用、协议面、并发事务、错误路径缺一不可。
- CI 至少执行 `migration -> sqlc generate -> sqlc vet -> go test`，否则 schema 漂移不会被及时拦住。

## 4. stateless 使用指南

### DO
- 状态、触发器统一定义成命名字符串常量。
- 所有主迁移集中在一个 builder 文件。
- 运行模式统一 `stateless.FiringQueued`。
- Guard 只做纯判断；多个 guard 必须互斥。
- Entry/Exit Action 只做副作用，不手改主状态。
- Graph 导出和 matrix 测试从同一份 transition spec 生成。

### DON'T
- 不要继续保留 `proc.State` + `effectiveState()` 双真相。
- 不要把状态迁移散落在 `switch/case`、goroutine、event handler 各处。
- 不要在 Guard 里做 I/O 或加写锁。
- 不要让“任意事件”直接驱动状态机；必须经过显式 trigger adapter。

### 示例

```go
package lifecycle

import (
	"context"

	"github.com/qmuntal/stateless"
)

type State string
type Trigger string

const (
	StateIdle       State = "idle"
	StateThinking   State = "thinking"
	StateRunning    State = "running"
	StateRecovering State = "recovering"
	StateStopped    State = "stopped"
)

const (
	TriggerUserPrompt    Trigger = "user_prompt"
	TriggerToolStarted   Trigger = "tool_started"
	TriggerToolFinished  Trigger = "tool_finished"
	TriggerRecover       Trigger = "recover"
	TriggerTurnCompleted Trigger = "turn_completed"
	TriggerStop          Trigger = "stop"
)

type Actions struct {
	OnTurnStarted func(context.Context) error
	OnStopped     func(context.Context) error
}

func Build(actions Actions) *stateless.StateMachine {
	sm := stateless.NewStateMachineWithMode(StateIdle, stateless.FiringQueued)

	sm.Configure(StateIdle).
		Permit(TriggerUserPrompt, StateThinking).
		Permit(TriggerStop, StateStopped)

	sm.Configure(StateThinking).
		OnEntry(func(ctx context.Context, _ ...any) error { return actions.OnTurnStarted(ctx) }).
		Permit(TriggerToolStarted, StateRunning).
		Permit(TriggerTurnCompleted, StateIdle).
		Permit(TriggerRecover, StateRecovering).
		Permit(TriggerStop, StateStopped)

	sm.Configure(StateRunning).
		Permit(TriggerToolFinished, StateThinking).
		Permit(TriggerTurnCompleted, StateIdle).
		Permit(TriggerStop, StateStopped)

	sm.Configure(StateRecovering).
		Permit(TriggerTurnCompleted, StateIdle).
		Permit(TriggerStop, StateStopped)

	sm.Configure(StateStopped).
		OnEntry(func(ctx context.Context, _ ...any) error { return actions.OnStopped(ctx) })

	return sm
}
```

### V2 -> V3
- V2：`manager.go`、`manager_event.go`、`manager_auto_recover.go`、`manager_lifecycle.go` 分散持有生命周期逻辑。
- V3：builder 文件只放 `Configure(...).Permit(...)`，runtime/action 单独分层。
- V2：recover 通过裸 goroutine、allowlist window、显示状态补丁隐式完成。
- V3：recover 是显式 trigger + action + matrix test。

### 测试要求
- 每个状态的合法 trigger 集必须有 matrix 测试。
- 每个 guard 都要测 true/false 两面。
- 每个 entry/exit action 都要能被 fake/spy 断言。
- 图导出（DOT/Mermaid）和 builder 必须共源，不允许手写文档图。

#### 守卫与测试门槛
- 运行态统一 `stateless.FiringQueued`；出现第二种 firing 模式直接视为违规。
- 每个状态机都必须有全矩阵测试：合法 trigger、guard true/false、entry/exit action、非法迁移全部覆盖。
- `platform/statemachine` 只能提供技术骨架，禁止 import `module/*` 或承载业务状态枚举。

## 5. jrpc2 使用指南

### DO
- 所有公共方法最终收敛到一个 `handler.Map`。
- 对公共参数使用 typed request/response struct。
- 参数校验统一走 `Validate()` + validated binder。
- 公共方法统一包上 `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()`。
- 日志、认证、capability、scope 等横切逻辑统一做 middleware。
- 错误统一映射为 `*jrpc2.Error`，业务错误码落在自定义非保留区。
- handler 测试优先 direct handler test，集成再用 `server.NewLocal`。

### DON'T
- 不要继续手写 `typedHandler` + `withRequiredThreadID` 闭包链。
- 不要保留 `dashrpc.Register` 之类第二条注册链。
- 不要在 handler 内直接处理原始 `json.RawMessage`，除非参数结构真的是动态形状。
- 不要把 transport 细节泄漏到业务 handler。

### 示例

```go
package rpc

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

type ThreadReadRequest struct {
	ThreadID string `json:"threadId"`
}

func (r ThreadReadRequest) Validate() error {
	if r.ThreadID == "" {
		return errors.New("threadId is required")
	}
	return nil
}

type Middleware func(jrpc2.Handler) jrpc2.Handler

func RequireCapability() Middleware {
	return func(next jrpc2.Handler) jrpc2.Handler {
		return func(ctx context.Context, req *jrpc2.Request) (any, error) {
			_ = req
			if false {
				return nil, jrpc2.Errorf(1002, "capability denied")
			}
			return next(ctx, req)
		}
	}
}

func NewValidated[P interface{ Validate() error }](fn func(context.Context, P) (any, error)) jrpc2.Handler {
	return handler.New(func(ctx context.Context, req P) (any, error) {
		if err := req.Validate(); err != nil {
			return nil, jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
		}
		return fn(ctx, req)
	})
}

func Routes(svc ThreadService) handler.Map {
	return handler.Map{
		"thread/read": NewValidated(func(ctx context.Context, req ThreadReadRequest) (any, error) {
			return svc.Read(ctx, req.ThreadID)
		}),
	}
}
```

### V2 -> V3
- V2：`typedHandler(s.xxxTyped)`。
- V3：`handler.New(s.xxx)` 或 `NewValidated(...)`。
- V2：`withRequiredThreadID(...)`。
- V3：`req.Validate()`。
- V2：`capabilityGuard(...)` 分散在 handler 注册点。
- V3：统一 middleware / assigner decoration。

### 测试要求
- handler 单测直接测 `handler.New(...)` 产物和 `*jrpc2.Error`。
- 注册表测试检查 `handler.Map` 是否包含预期方法名。
- push/notify 行为用 `server.NewLocal(..., AllowPush=true)` 验证，不要默认假设 HTTP bridge 可推送。
- transport 适配测试与 handler 测试分层。

#### 守卫与测试门槛
- 所有公共方法必须使用 `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()`；`handler.New` 只用于内部宽松场景。
- `jrpc2` 只允许出现在 `platform/rpc` 和 `module/*/rpc.go`；禁止第二条注册链和旁路 handler。
- 公共方法必须有响应 shape 守卫和错误码守卫，至少覆盖 `invalid params`、业务拒绝和 transport 失败三条路径。

## 6. kelindar/event 使用指南

### DO
- 生产代码统一注入 `*event.Dispatcher`。
- 一个事件一个 typed struct，稳定实现 `Type() uint32`。
- 需要可读层级时，再额外实现 `Route() string`。
- subscriber 保存并调用取消函数。
- 把“事件 -> 状态机 trigger”做成显式 adapter。
- 用投影器模式把 typed event 变成 UI/dashboard/read-model。

### DON'T
- 不要在生产代码使用全局 `event.Default`。
- 不要把内部事件体写成 `map[string]any`。
- 不要搞一个万能 handler 再反射分发所有事件。
- 不要用 drop-on-full 思路处理背压问题。

### 示例

```go
package bus

import (
	"context"

	"github.com/kelindar/event"
	"go.uber.org/fx"
)

const (
	EventTypeTurnStarted uint32 = 0x0100_0001
)

type TurnStarted struct {
	ThreadID string
	TurnID   string
}

func (TurnStarted) Type() uint32 { return EventTypeTurnStarted }
func (TurnStarted) Route() string { return "turn.started" }

func NewDispatcher(lc fx.Lifecycle) *event.Dispatcher {
	bus := event.NewDispatcher()
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error { return bus.Close() },
	})
	return bus
}

func PublishTurnStarted(bus *event.Dispatcher, threadID, turnID string) {
	event.Publish(bus, TurnStarted{ThreadID: threadID, TurnID: turnID})
}

func BindProjector(bus *event.Dispatcher, projector func(TurnStarted)) context.CancelFunc {
	return event.Subscribe(bus, projector)
}
```

### 投影器模式

```go
type UIProjector struct {
	Append func(threadID, kind, id string)
}

func (p *UIProjector) Bind(bus *event.Dispatcher) context.CancelFunc {
	return event.Subscribe(bus, func(ev TurnStarted) {
		p.Append(ev.ThreadID, "turn_started", ev.TurnID)
	})
}
```

### Bus log sink 模式

```go
type BusLogWriter interface {
	Write(context.Context, string, map[string]any) error
}

func BindBusLogSink(bus *event.Dispatcher, w BusLogWriter) context.CancelFunc {
	return event.Subscribe(bus, func(ev TurnStarted) {
		_ = w.Write(context.Background(), ev.Route(), map[string]any{
			"threadId": ev.ThreadID,
			"turnId":   ev.TurnID,
		})
	})
}
```

### V2 -> V3
- V2：`Msg*` 常量 + `Topic*` 常量 + `json.RawMessage` / `map[string]any` payload。
- V3：typed event struct + `Type() uint32` + injected dispatcher。
- V2：bus/router/orchestration state 混写。
- V3：bus 只负责分发；状态真相回到 stateless；投影由 uistate/dashboard 消费。

### 测试要求
- 事件测试要用 recorder、channel 或 wait group 等待消费完成。
- 不要 publish 后立即断言，除非明确知道 handler 是同步路径。
- 必测取消订阅后不再消费。
- 必测高并发 publish 下的顺序/背压边界，不允许用“丢事件容忍”掩盖问题。

#### 守卫与测试门槛
- 生产事件必须是 typed struct，稳定实现 `Type() uint32`；禁止回退到 `map[string]any` 或全局 `event.Default`。
- 事件路由守卫要冻结 route、payload shape、订阅取消语义和 bridge 行为，不能只测 happy path。
- 并发门槛必须覆盖高并发 publish、慢消费者、取消订阅后不再消费三类场景。

## 执行结论

- 六个框架在 V3 里不是“可选工具箱”，而是明确的职责边界。
- `fx` 管构造，`run.Group` 管长跑，`sqlc` 管静态 SQL，`stateless` 管生命周期真相，`jrpc2` 管 RPC 契约，`kelindar/event` 管进程内 typed event。
- 任何一个框架被拿去承接它不该承接的职责，最终都会把 V2 的样板再长回来。
