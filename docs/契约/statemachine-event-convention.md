# stateless 与 kelindar/event 契约文档

本文是 V3 的状态机与事件总线契约，不是泛泛的库介绍。
目标是把 V2 里分散、隐式、可变异的流程，收敛为两类清晰资产：

- 一份声明式状态转换表
- 一组类型安全事件定义

本文调研与契约均以以下版本为准：

- `github.com/qmuntal/stateless@v1.8.0`
- `github.com/kelindar/event@v1.5.2`

调研时间点：

- `stateless@v1.8.0` 本机模块元数据时间为 `2026-02-10`
- `event@v1.5.2` 本机模块元数据时间为 `2025-05-29`
- 对比项 `looplab/fsm@v1.0.3` 与 `asaskevich/EventBus@v0.0.0-20200907212545-49d423059eef` 按 `2026-03-19` 本机拉取结果核对

V2 历史参照核对结论：

- `AgentManager` 生命周期逻辑分散在 `go-agent-v2/internal/runner/manager.go`、`go-agent-v2/internal/runner/manager_event.go`、`go-agent-v2/internal/runner/manager_recover.go`、`go-agent-v2/internal/runner/manager_lifecycle.go`
- `effectiveState()` 在 `manager.go` 里把 `proc.State` 再加工成“外部可见状态”，形成双重状态表示
- `allowlistWindow` 叠加在主状态机之外，形成隐式侧状态机
- `triggerRecoverAsync()` 在 `manager_event.go` 里直接 `go func()` 触发恢复副作用
- 历史 `go-agent-v2/internal/apiserver/server_event_handler.go` 为 `557` 行
- 历史 `go-agent-v2/internal/guards/state_matrix_snapshot.json` 已覆盖 `5` 个状态、`81` 个事件夹具，说明“矩阵”已经存在，但作者权威不是状态表，而是散落代码
- 历史 `go-agent-v2/internal/bus/bus.go` 暴露了 `46` 个 `Msg*` 常量和 `16` 个 `Topic*` 常量；即使你此前口径是“35 个 topic 常量”，本质问题仍然是命名空间膨胀和未收敛

## 先纠正 4 个 API 口径

这 4 点必须先钉住，否则设计会从一开始就建立在错误 API 之上。

1. `stateless@v1.8.0` 没有 `PermitIf` 方法。
   Guard 写法是 `Permit(trigger, dest, guard...)` 或 `PermitDynamic(trigger, selector, guard...)`。
2. `stateless@v1.8.0` 原生只提供 `ToGraph()`，输出是 DOT，不是 Mermaid。
   Mermaid 需要我们自己从同一份 transition spec 生成。
3. `kelindar/event@v1.5.2` 的具名 dispatcher API 是 `event.Publish(bus, ev)` 和 `event.Subscribe(bus, handler)`。
   `event.Emit(ev)` 与 `event.On(handler)` 只操作包级全局 `event.Default`。
4. `stateless@v1.8.0` 对“未处理触发器”默认返回 `error`，不是默认 panic。
   panic 主要来自配置错误、参数不匹配、或 guard 不互斥等情况。

## 公共脚手架

除非特别说明，后续所有代码块都默认和下面这个脚手架放在同一个 `contracts_examples_test.go` 中编译。
我已经用精确版本依赖在本机临时模块里编译通过后才写本文。

```go
package contractexamples

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/kelindar/event"
	"github.com/qmuntal/stateless"
	"go.uber.org/fx"
)

type AgentState string

const (
	StateIdle       AgentState = "idle"
	StateThinking   AgentState = "thinking"
	StateRunning    AgentState = "running"
	StateRecovering AgentState = "recovering"
	StateStopped    AgentState = "stopped"
	StateError      AgentState = "error"
)

type AgentTrigger string

const (
	TriggerUserPrompt       AgentTrigger = "user_prompt"
	TriggerToolStarted      AgentTrigger = "tool_started"
	TriggerToolFinished     AgentTrigger = "tool_finished"
	TriggerTurnCompleted    AgentTrigger = "turn_completed"
	TriggerRecoverRequested AgentTrigger = "recover_requested"
	TriggerRecoverSucceeded AgentTrigger = "recover_succeeded"
	TriggerRecoverFailed    AgentTrigger = "recover_failed"
	TriggerStopRequested    AgentTrigger = "stop_requested"
)

func configureLifecycleMachine(sm *stateless.StateMachine) *stateless.StateMachine {
	sm.Configure(StateIdle).
		Permit(TriggerUserPrompt, StateThinking).
		Permit(TriggerStopRequested, StateStopped)

	sm.Configure(StateThinking).
		Permit(TriggerToolStarted, StateRunning).
		Permit(TriggerTurnCompleted, StateIdle).
		Permit(TriggerRecoverRequested, StateRecovering)

	sm.Configure(StateRunning).
		Permit(TriggerToolFinished, StateThinking).
		Permit(TriggerTurnCompleted, StateIdle).
		Permit(TriggerRecoverRequested, StateRecovering)

	sm.Configure(StateRecovering).
		Permit(TriggerRecoverSucceeded, StateIdle).
		Permit(TriggerRecoverFailed, StateError)

	sm.Configure(StateError).
		Permit(TriggerRecoverRequested, StateRecovering)

	sm.Configure(StateStopped)
	return sm
}

func newLifecycleMachine() *stateless.StateMachine {
	return configureLifecycleMachine(
		stateless.NewStateMachineWithMode(StateIdle, stateless.FiringQueued),
	)
}

type TransitionSpec struct {
	From    string
	To      string
	Trigger string
}

func BuildMermaid(specs []TransitionSpec) string {
	var b strings.Builder
	b.WriteString("stateDiagram-v2\n")
	for _, spec := range specs {
		b.WriteString(fmt.Sprintf("    %s --> %s: %s\n", spec.From, spec.To, spec.Trigger))
	}
	return b.String()
}

const (
	EventTypeAgentStarted uint32 = 0x0100_0001
	EventTypeTurnStarted  uint32 = 0x0100_0002
	EventTypeTurnEnded    uint32 = 0x0100_0003
)

type AgentStarted struct {
	AgentID string
}

func (AgentStarted) Type() uint32 { return EventTypeAgentStarted }

type TurnStarted struct {
	AgentID string
	Prompt  string
}

func (TurnStarted) Type() uint32 { return EventTypeTurnStarted }

type TurnEnded struct {
	AgentID string
	Reason  string
}

func (TurnEnded) Type() uint32 { return EventTypeTurnEnded }

type Routed interface {
	event.Event
	Route() string
}

type RoutedTurnEnded struct {
	AgentID string
	Reason  string
}

func (RoutedTurnEnded) Type() uint32 { return EventTypeTurnEnded }
func (RoutedTurnEnded) Route() string { return "agent.turn.ended" }

type Recorder[T event.Event] struct {
	mu     sync.Mutex
	events []T
}

func (r *Recorder[T]) Handler(ev T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *Recorder[T]) Snapshot() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]T, len(r.events))
	copy(out, r.events)
	return out
}

type RPCNotifier interface {
	Notify(context.Context, string, any) error
}
```

---

## Part A: stateless 状态机

### A1. 框架概述

选 `qmuntal/stateless`，不是因为它“更流行”，而是因为它与 V3 目标最贴合：

- 它支持层级状态 `SubstateOf`，这对拆解 V2 的“主状态机 + 叠加侧状态机”很关键
- 它支持参数化触发器 `SetTriggerParameters`，适合把“事件 + payload”显式带进转换
- 它支持外部状态存储，便于把状态放到 runtime 对象、数据库字段或 actor 状态里
- 它支持 `FiringQueued`，提供 run-to-completion 语义，能替代 V2 里散落的“先改状态，再异步副作用”
- 它支持 DOT 导出，便于把代码与图同步

为什么不选 `looplab/fsm`：

- `looplab/fsm` 的状态与事件是字符串主导，回调也是字符串键名回调表
- 它没有 `SubstateOf` 这样的层级状态能力
- 它更接近“事件-状态-回调”的平面 FSM，而不是更适合复杂 Agent 生命周期的 statechart 风格

为什么不继续手写 `switch/case`：

- V2 已经证明，手写逻辑最终会分裂到 4 个文件和多个副作用点
- 图、测试矩阵、审查口径都无法自动从实现里稳定导出
- `proc.State` 与 `effectiveState()` 这种“逻辑状态 + 显示状态”二次推导极易漂移

代码示例：

```go
func ExampleA01_BasicMachine() error {
	sm := newLifecycleMachine()
	return sm.Fire(TriggerUserPrompt)
}
```

### A2. 状态/触发器定义范式

契约结论：

- 默认使用“命名类型 + 字符串常量”
- 不直接用裸 `string`
- 不默认用 `iota`

原因：

- 日志、DOT 图、Mermaid 图、JSON、数据库字段都天然可读
- 与 V2 现有状态值 `idle/thinking/running/stopped/error` 平滑衔接
- 避免 `iota` 方案里必须额外维护 `String()`、序列化、跨进程兼容

允许的例外：

- 极热路径、纯内存、绝不序列化、绝不外露的内部状态可以用 `iota`
- 一旦进入日志、观测、数据库、RPC、图导出，就必须回到字符串常量

代码示例：

```go
func ExampleA02_StateTriggerDefinition() (AgentState, AgentTrigger) {
	return StateIdle, TriggerUserPrompt
}
```

### A3. 声明式转换表范式

契约结论：

- 所有主状态迁移必须集中在一个构建函数里
- 一个状态机一个 authoritative builder，例如 `configureLifecycleMachine`
- 禁止把状态迁移分散到 `if`、`switch`、回调注册、异步 goroutine 里

建议结构：

- `types.go`: 状态、触发器、参数类型
- `machine.go`: 只放 `Configure(...).Permit(...)`
- `runtime.go`: 只放状态存储与副作用依赖

代码示例：

```go
func ExampleA03_DeclarativeTable() *stateless.StateMachine {
	return newLifecycleMachine()
}
```

### A4. Guard 条件范式

重要更正：

- v1.8.0 没有 `PermitIf`
- 正确写法是 `Permit(trigger, dest, guardFunc)`

契约结论：

- Guard 只做纯判断，不做副作用
- 同一状态下同一触发器的多个 Guard 必须互斥
- 如果 Guard 依赖 runtime 数据，数据读取必须稳定、只读、无锁竞争

这是强约束，因为 `stateless` 源码会在“同一 trigger 有多个 guard 同时命中”时 panic。

代码示例：

```go
func ExampleA04_Guard() error {
	sm := stateless.NewStateMachineWithMode(StateIdle, stateless.FiringQueued)
	hasBudget := true

	sm.Configure(StateIdle).
		Permit(TriggerUserPrompt, StateThinking, func(_ context.Context, _ ...any) bool {
			return hasBudget
		}).
		Permit(TriggerUserPrompt, StateError, func(_ context.Context, _ ...any) bool {
			return !hasBudget
		})

	return sm.Fire(TriggerUserPrompt)
}
```

### A5. Entry/Exit Action 范式

契约结论：

- Entry/Exit Action 负责状态相关副作用
- 业务状态修改仍由状态机本身负责，不在 Action 里手改主状态
- Action 可以发事件、记录日志、刷新指标、触发外部 worker
- Action 失败即 `Fire(...)` 失败，调用方必须处理 error

V3 推荐做法：

- 把“发通知”“更新观测”“提交恢复任务”放在 Entry/Exit
- 不再像 V2 那样在多个 `switch/case` 分支里零散插副作用

代码示例：

```go
func ExampleA05_EntryExitAction() error {
	sm := stateless.NewStateMachineWithMode(StateIdle, stateless.FiringQueued)
	var mu sync.Mutex
	logs := make([]string, 0, 4)
	record := func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, msg)
	}

	sm.Configure(StateIdle).
		OnExit(func(_ context.Context, _ ...any) error {
			record("exit idle")
			return nil
		}).
		Permit(TriggerUserPrompt, StateThinking)

	sm.Configure(StateThinking).
		OnEntry(func(_ context.Context, args ...any) error {
			record(fmt.Sprintf("enter thinking:%v", args[0]))
			return nil
		})

	if err := sm.Fire(TriggerUserPrompt, "hello"); err != nil {
		return err
	}
	_ = logs
	return nil
}
```

### A6. 带参数的触发器范式

契约结论：

- 只要 trigger 需要 payload，就必须先 `SetTriggerParameters`
- `Fire(trigger, args...)` 的参数顺序要写进契约，不允许“靠调用方默契”
- 参数类型变更必须视为契约变更

这能替代 V2 里“事件类型在一个地方，payload shape 在另一个地方”的隐式耦合。

代码示例：

```go
func ExampleA06_TriggerArgs() error {
	sm := stateless.NewStateMachineWithMode(StateIdle, stateless.FiringQueued)

	sm.SetTriggerParameters(
		TriggerUserPrompt,
		reflect.TypeOf(""),
		reflect.TypeOf(false),
	)

	sm.Configure(StateIdle).
		Permit(TriggerUserPrompt, StateThinking)

	sm.Configure(StateThinking).
		OnEntryFrom(TriggerUserPrompt, func(_ context.Context, args ...any) error {
			_ = args[0].(string)
			_ = args[1].(bool)
			return nil
		})

	return sm.Fire(TriggerUserPrompt, "ping", false)
}
```

### A7. 子状态范式

契约结论：

- 层级状态只用来表达“仍属于父状态，但有更细分阶段”
- 子状态不能代替正交状态
- 像 V2 的 `allowlistWindow` 这种“带时间窗的恢复许可”不应该硬塞成主状态层级；它更适合单独 policy 对象或 guard 输入

V3 推荐拆法：

- 主状态机：`idle / thinking / running / recovering / stopped / error`
- 子状态：例如 `running.tooling`、`running.awaiting_approval`
- 侧策略：恢复 allowlist、重试 budget、submission queue 状态，独立建模

代码示例：

```go
func ExampleA07_SubstateOf() error {
	type RunState string

	const (
		RunConnected RunState = "connected"
		RunTooling   RunState = "tooling"
	)

	sm := stateless.NewStateMachineWithMode(RunTooling, stateless.FiringQueued)
	sm.Configure(RunConnected).
		Permit("recover", "recovering")
	sm.Configure(RunTooling).
		SubstateOf(RunConnected)

	ok, err := sm.IsInState(RunConnected)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("expected tooling to be included in connected")
	}
	return nil
}
```

### A8. 并发安全范式

源码口径非常重要：

- `StateMachine` 运行时并发使用是安全的
- 但 callback、action、外部状态存取函数不受库本身保护
- 推荐 `FiringQueued`，因为它提供 run-to-completion 语义
- 配置阶段仍应视为单线程初始化阶段，构建完成后再暴露给外部 goroutine

契约结论：

- 运行态统一 `FiringQueued`
- 配置态禁止并发 `Configure`
- Action 内如果写共享内存，自己加锁
- 使用 `NewStateMachineWithExternalStorage*` 时，外部 accessor/mutator 自己负责线程安全

代码示例：

```go
func ExampleA08_ConcurrentFire() error {
	sm := stateless.NewStateMachineWithMode(StateIdle, stateless.FiringQueued)
	sm.Configure(StateIdle).PermitReentry(TriggerUserPrompt)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sm.Fire(TriggerUserPrompt)
		}()
	}
	wg.Wait()
	return nil
}
```

### A9. 状态机可视化

契约结论：

- DOT 图使用 `sm.ToGraph()` 原生导出
- Mermaid 图不伪装成库原生能力，而是从同一份 transition spec 额外生成
- 任何图都必须从代码或 transition spec 自动生成，禁止人工维护图

推荐实践：

- `stateless` 负责执行态
- `[]TransitionSpec` 负责可视化与测试矩阵输入
- 如果两者并存，则 `TransitionSpec` 生成 `Configure`，不要反过来人工维护两套表

代码示例：

```go
func ExampleA09_Graphs() (string, string) {
	sm := newLifecycleMachine()

	dot := sm.ToGraph()
	mermaid := BuildMermaid([]TransitionSpec{
		{From: "idle", To: "thinking", Trigger: "user_prompt"},
		{From: "thinking", To: "running", Trigger: "tool_started"},
		{From: "running", To: "thinking", Trigger: "tool_finished"},
	})

	return dot, mermaid
}
```

### A10. 全矩阵测试范式

契约结论：

- 测试不要只测 happy path
- 至少要覆盖 `State × Trigger -> CanFire`
- 对关键路径再覆盖 `State × Trigger -> NextState`
- 失败态、停止态、恢复态必须进矩阵，不要只测 `idle -> thinking -> running -> idle`

V2 已经有 `81` 个事件夹具快照，这说明矩阵测试是刚需。
V3 要做的是把“矩阵测试”建立在声明式表上，而不是建立在隐式 `switch/case` 上。

代码示例：

```go
func TestA10_Matrix(t *testing.T) {
	cases := []struct {
		from    AgentState
		trigger AgentTrigger
		wantCan bool
	}{
		{from: StateIdle, trigger: TriggerUserPrompt, wantCan: true},
		{from: StateIdle, trigger: TriggerToolFinished, wantCan: false},
		{from: StateRunning, trigger: TriggerToolFinished, wantCan: true},
	}

	for _, tc := range cases {
		current := tc.from
		sm := stateless.NewStateMachineWithExternalStorage(
			func(_ context.Context) (stateless.State, error) { return current, nil },
			func(_ context.Context, state stateless.State) error {
				current = state.(AgentState)
				return nil
			},
			stateless.FiringQueued,
		)
		configureLifecycleMachine(sm)

		can, err := sm.CanFire(tc.trigger)
		if err != nil {
			t.Fatalf("CanFire(%s): %v", tc.trigger, err)
		}
		if can != tc.wantCan {
			t.Fatalf("from=%s trigger=%s can=%v want=%v", tc.from, tc.trigger, can, tc.wantCan)
		}
	}
}
```

### A11. 与 fx 集成

契约结论：

- 状态机本身作为依赖注入组件，由 `fx.Provide` 暴露
- 构建函数只负责建表与依赖装配，不在 `fx.Provide` 里偷偷启动 goroutine
- 需要后台 worker 的，交给 `fx.Lifecycle` 或 `oklog/run` 管

代码示例：

```go
type MachineHolder struct {
	fx.Out
	SM *stateless.StateMachine
}

func NewMachineHolder() MachineHolder {
	return MachineHolder{SM: newLifecycleMachine()}
}

var StateMachineModule = fx.Module(
	"stateMachine",
	fx.Provide(NewMachineHolder),
)
```

### A12. V2 AgentManager 迁移指南

迁移目标不是“把原来的 switch/case 原样搬到 stateless”。
迁移目标是收敛职责：

- 主状态只保留一个作者权威
- `effectiveState()` 改为显式 read-model，而不是二次状态机
- `allowlistWindow` 从“隐式侧状态机”变为独立恢复策略输入
- `triggerRecoverAsync()` 从裸 `go func()` 改成受 `oklog/run`/worker 管控的副作用执行器

推荐步骤：

1. 提炼 `AgentState` 与 `AgentTrigger`
2. 把所有 `event -> trigger` 适配集中到一个 adapter
3. 把所有 `state -> side effect` 放到 Entry/Exit Action
4. 把恢复 allowlist、重试 budget、queue 深度改成 runtime policy，不再污染主状态
5. 用 `state_matrix` 自动测试替代分散断言

代码示例：

```go
type LegacyUIEvent struct {
	UIType      string
	Recoverable bool
}

type AgentRuntime struct {
	mu    sync.Mutex
	state AgentState
}

func NewAgentStateMachine(rt *AgentRuntime) *stateless.StateMachine {
	sm := stateless.NewStateMachineWithExternalStorage(
		func(_ context.Context) (stateless.State, error) {
			rt.mu.Lock()
			defer rt.mu.Unlock()
			return rt.state, nil
		},
		func(_ context.Context, state stateless.State) error {
			rt.mu.Lock()
			defer rt.mu.Unlock()
			rt.state = state.(AgentState)
			return nil
		},
		stateless.FiringQueued,
	)
	return configureLifecycleMachine(sm)
}

func TriggerFromLegacy(ev LegacyUIEvent) AgentTrigger {
	switch ev.UIType {
	case "turn_started", "assistant_delta", "user_message":
		return TriggerUserPrompt
	case "command_start":
		return TriggerToolStarted
	case "command_done":
		return TriggerToolFinished
	case "turn_complete":
		return TriggerTurnCompleted
	case "error":
		return TriggerRecoverRequested
	default:
		return TriggerTurnCompleted
	}
}
```

---

## Part B: kelindar/event 事件总线

### B1. 框架概述

`kelindar/event` 是进程内、高吞吐、泛型化的事件分发器。

选它的理由：

- 事件类型是具体 Go struct，而不是 `map[string]any`
- dispatcher 是 in-process、低依赖、无 broker，适合 V3 单进程组件解耦
- 每个 subscriber 自有 goroutine，天然适合“一个发布，多处消费”
- 当消费者队列打满时，源码不是 drop，而是等待，提供背压

为什么不选 `asaskevich/EventBus`：

- 它以字符串 topic + `interface{}` + reflection 调用为核心
- 类型安全要靠运行时
- 回调签名错误、参数顺序错误、payload shape 漂移都会晚发现
- 它甚至还包含 cross-process rpc 方向，这不是 V3 需要的职责

为什么不继续 V2 手写 bus / channel：

- V2 `MessageBus` 的 `Payload` 是 `json.RawMessage` / `map[string]any` 风格的松散 envelope
- channel 自己写广播、回放、背压、订阅生命周期，最终只会长成又一个定制总线
- `drop-on-full` 在 Agent 场景会把诊断问题变成“偶现丢事件”

代码示例：

```go
func ExampleB01_BasicDispatcher() {
	bus := event.NewDispatcher()
	cancel := event.Subscribe(bus, func(ev AgentStarted) {
		_ = ev.AgentID
	})
	defer cancel()

	event.Publish(bus, AgentStarted{AgentID: "agent-1"})
}
```

### B2. 类型安全事件定义范式

契约结论：

- 一个事件一个 struct
- 事件 struct 只承载该事件最小必要字段
- `Type() uint32` 由常量稳定返回
- 禁止以 `map[string]any` 作为内部总线事件体

推荐补充：

- 若需要可读层级，额外实现 `Route() string`
- 若需要 tracing，字段直接放强类型 `TraceID string`

代码示例：

```go
func ExampleB02_TypedEventDefinition() event.Event {
	ev := AgentStarted{AgentID: "agent-1"}
	return ev
}
```

### B3. 发布范式

必须纠正 API：

- 具名 dispatcher 用 `event.Publish(bus, ev)`
- 包级全局 dispatcher 才用 `event.Emit(ev)`

契约结论：

- 生产代码统一注入 `*event.Dispatcher`
- `event.Default` 只允许在非常小的 demo 或一次性测试里使用
- 生产模块不要直接依赖包级全局总线

代码示例：

```go
func ExampleB03_Publish(bus *event.Dispatcher) {
	event.Publish(bus, AgentStarted{AgentID: "agent-1"})

	// 只建议用于小型测试或 demo。
	event.Emit(AgentStarted{AgentID: "global-agent"})
}
```

### B4. 订阅范式

必须纠正 API：

- 具名 dispatcher 用 `event.Subscribe(bus, handler)`
- 包级全局 dispatcher 用 `event.On(handler)`

契约结论：

- 订阅函数返回 `context.CancelFunc`，调用方必须保存并在生命周期结束时取消
- 一个模块只订阅自己真正消费的事件
- 不要用一个“万能 handler”接住所有事件再做二次反射分发

代码示例：

```go
func ExampleB04_Subscribe(bus *event.Dispatcher) context.CancelFunc {
	return event.Subscribe(bus, func(ev TurnStarted) {
		_ = ev.Prompt
	})
}
```

### B5. 事件层级范式

`kelindar/event` 的匹配键是 `Type() uint32`，不是字符串 topic。
所以 V3 的“层级”要分两层：

- 分发层：`Type() uint32`
- 可读层：`Route() string`

推荐编码方式：

- 高位表示 domain
- 低位表示该 domain 下的事件号
- 领域路由用 `Route() string` 提供观测友好名

代码示例：

```go
func ExampleB05_EventHierarchy() string {
	var ev Routed = RoutedTurnEnded{
		AgentID: "agent-1",
		Reason:  "completed",
	}
	return ev.Route()
}
```

### B6. 并发安全

从源码与测试看，v1.5.2 的关键语义是：

- `Subscribe`/`Publish` 可并发使用
- 每个 subscriber 在自己的 goroutine 顺序消费自己的队列
- 单个 subscriber 内事件顺序可保持
- 多个 subscriber 之间完成顺序不保证一致
- 队列达到容量时不是丢弃，而是等待消费者处理，形成背压

这对 V3 很重要：

- “不会无声掉事件”是比“绝对不阻塞”更重要的契约
- 若某类事件可能长期慢消费，应该拆 dispatcher 或拆 handler，而不是重新引入 drop-on-full

代码示例：

```go
func ExampleB06_ConcurrentPublish() {
	bus := event.NewDispatcher()
	cancel := event.Subscribe(bus, func(ev TurnEnded) {})
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event.Publish(bus, TurnEnded{
				AgentID: "agent-1",
				Reason:  "ok",
			})
		}()
	}
	wg.Wait()
}
```

### B7. 与状态机集成

契约结论：

- 状态机 Action 可以发布 typed event
- typed event subscriber 可以驱动 `sm.Fire(...)`
- 但不要让“任意事件都能直接改状态”，必须通过显式 trigger adapter

推荐结构：

- `state machine`: 主生命周期
- `event adapter`: `event -> trigger`
- `event projector`: `state/action -> typed event`

代码示例：

```go
type StateEngine struct {
	Bus *event.Dispatcher
	SM  *stateless.StateMachine
}

func NewStateEngine() *StateEngine {
	return &StateEngine{
		Bus: event.NewDispatcher(),
		SM:  newLifecycleMachine(),
	}
}

func (e *StateEngine) Bind() context.CancelFunc {
	return event.Subscribe(e.Bus, func(ev TurnStarted) {
		_ = e.SM.Fire(TriggerUserPrompt, ev.Prompt)
	})
}

func (e *StateEngine) OnTurnCompleted() error {
	if err := e.SM.Fire(TriggerTurnCompleted); err != nil {
		return err
	}
	event.Publish(e.Bus, TurnEnded{
		AgentID: "agent-1",
		Reason:  "completed",
	})
	return nil
}
```

### B8. 与 fx 集成

契约结论：

- dispatcher 作为 `fx.Provide` 的单例组件
- `OnStop` 里调用 `Close()`
- 不要在模块里偷偷使用 `event.Default`

代码示例：

```go
func NewManagedDispatcher(lc fx.Lifecycle) *event.Dispatcher {
	bus := event.NewDispatcher()
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			return bus.Close()
		},
	})
	return bus
}

var EventModule = fx.Module(
	"eventBus",
	fx.Provide(NewManagedDispatcher),
)
```

### B9. 与 jrpc2 集成

V3 里不建议把 `*jrpc2.Client` 散落进各个 handler。
推荐先抽一个窄接口，再把具体实现绑定到 `jrpc2.Client.Notify`。

这样做的原因：

- 测试时可以 fake
- 事件层不与传输实现耦死
- 后续若从 push 通知切到别的 transport，不必重写总线消费端

代码示例：

```go
type EventPushBridge struct {
	bus *event.Dispatcher
	rpc RPCNotifier
}

func (b *EventPushBridge) Bind() context.CancelFunc {
	return event.Subscribe(b.bus, func(ev TurnEnded) {
		_ = b.rpc.Notify(context.Background(), "agent.turn.ended", ev)
	})
}
```

### B10. 测试范式

契约结论：

- 事件测试要么等 channel，要么用 recorder + wait
- 不能 publish 完立刻断言，除非你明确知道当前 handler 是同步消费
- 录制器本身要线程安全

代码示例：

```go
func ExampleB10_Recorder() []TurnEnded {
	bus := event.NewDispatcher()
	rec := &Recorder[TurnEnded]{}
	done := make(chan struct{}, 1)

	cancel := event.Subscribe(bus, func(ev TurnEnded) {
		rec.Handler(ev)
		select {
		case done <- struct{}{}:
		default:
		}
	})
	defer cancel()

	event.Publish(bus, TurnEnded{
		AgentID: "agent-1",
		Reason:  "done",
	})
	<-done

	return rec.Snapshot()
}
```

### B11. 反模式

禁止的用法：

- 在生产代码里使用 `event.Default`
- 用一个 `map[string]any` 包住所有内部事件
- 把一个大而全 handler 当变异管道，内部再反射二次分派
- 在 handler 里阻塞很久却不拆分 dispatcher 或 worker
- 把 dispatcher 关闭后继续 publish

其中最后一点需要额外说明：

- `Close()` 会关闭内部 `done`，并阻止新的 `Subscribe`
- 源码没有把“关闭后 publish”做成强错误
- 所以 V3 契约上必须把“已关闭 dispatcher”视为不可再用的终态

代码示例：

```go
func BadUseDefaultBus() {
	event.Emit(AgentStarted{AgentID: "bad"})
}

func GoodUseInjectedBus(bus *event.Dispatcher) {
	event.Publish(bus, AgentStarted{AgentID: "good"})
}
```

### B12. V2→V3 迁移指南

迁移重点不是“继续发字符串 topic，只是换个库”。
真正的迁移点是：

- `map[string]any` -> 强类型事件 struct
- `MessageBus.Publish(Message{...})` -> `event.Publish(bus, TypedEvent{...})`
- 大型 `AgentEventHandler` 变异链 -> 多个小订阅器，各自只消费一种 typed event

推荐迁移分层：

1. `legacy adapter`
   负责把旧事件 envelope 转成 typed event
2. `domain event`
   只保留强类型 struct
3. `projector/consumer`
   各模块独立订阅自己要的事件

代码示例：

```go
type LegacyEnvelope struct {
	Type    string
	AgentID string
	Payload map[string]any
}

func PublishLegacy(bus *event.Dispatcher, env LegacyEnvelope) {
	switch env.Type {
	case "turn_started":
		event.Publish(bus, TurnStarted{
			AgentID: env.AgentID,
			Prompt:  fmt.Sprint(env.Payload["prompt"]),
		})
	case "turn_complete":
		event.Publish(bus, TurnEnded{
			AgentID: env.AgentID,
			Reason:  "completed",
		})
	case "agent_started":
		event.Publish(bus, AgentStarted{
			AgentID: env.AgentID,
		})
	}
}
```

---

## V3 最终契约

把上面两部分压缩成 V3 可执行约束，就是下面这些：

### 状态机契约

- 主生命周期状态机统一使用 `stateless@v1.8.0`
- 主状态只保留一个作者权威，不允许 `proc.State` / `effectiveState()` 双写双读
- 所有主转换统一集中到一个 builder 文件
- 运行态统一 `FiringQueued`
- 所有 Guard 必须纯函数且互斥
- Entry/Exit Action 负责副作用，不手改主状态
- DOT 图从 `ToGraph()` 自动导出
- Mermaid 图从同一份 transition spec 自动导出
- 所有关键状态与触发器必须进入矩阵测试

### 事件契约

- 进程内事件总线统一使用 `kelindar/event@v1.5.2`
- 生产代码统一使用注入的 `*event.Dispatcher`
- 内部事件统一使用 typed struct，不允许 `map[string]any`
- 事件分发键用 `Type() uint32`
- 人类可读层级用可选的 `Route() string`
- 订阅函数必须保留并正确调用取消函数
- 背压问题通过拆 handler / 拆 dispatcher / worker 化处理，不重新引入 drop-on-full

### 集成契约

- 状态机 Action 可以发布 typed event
- typed event 只能通过显式 adapter 驱动状态机 trigger
- `fx` 负责注入与生命周期管理
- `oklog/run` 负责后台 worker 和副作用编排
- `jrpc2` 推送通过窄接口桥接，不把 transport 细节泄漏到总线消费者

### 对 V2 痛点的直接回应

- 4 文件分散状态机：收敛为 1 个 builder + 1 个 adapter + 1 个 runtime
- `switch/case` 链：收敛为声明式 `Configure(...).Permit(...)`
- `proc.State` vs `effectiveState()`：收敛为单一主状态 + 明确的只读投影
- `allowlistWindow`：从隐式侧状态机改为独立恢复策略输入
- `triggerRecoverAsync` 裸 goroutine：改为受控 worker / runner
- `MessageBus` + `map[string]any`：改为 typed event
- `557` 行 `AgentEventHandler`：拆为多个 typed consumer
- drop-on-full：改为背压显式化

---

## 推荐目录布局

建议在 V3 中落成如下结构：

```text
internal/agentflow/
  state_types.go
  event_types.go
  lifecycle_machine.go
  lifecycle_adapter.go
  lifecycle_actions.go
  lifecycle_matrix_test.go
  graph_export.go
  fx_module.go
```

其中职责划分如下：

- `state_types.go`
  只放 `AgentState`、`AgentTrigger`、trigger payload 类型
- `event_types.go`
  只放 typed event struct 与 `Type() uint32`
- `lifecycle_machine.go`
  只放 `Configure(...).Permit(...)`
- `lifecycle_adapter.go`
  只放 `legacy/raw event -> trigger`
- `lifecycle_actions.go`
  只放 entry/exit side effects
- `lifecycle_matrix_test.go`
  只放矩阵测试
- `graph_export.go`
  只放 DOT / Mermaid 导出
- `fx_module.go`
  只放依赖装配

---

## 调研依据

官方与源码依据：

- `stateless` README: <https://github.com/qmuntal/stateless/blob/v1.8.0/README.md>
- `stateless` 状态机实现: <https://github.com/qmuntal/stateless/blob/v1.8.0/statemachine.go>
- `stateless` 配置 API: <https://github.com/qmuntal/stateless/blob/v1.8.0/config.go>
- `stateless` 运行模式: <https://github.com/qmuntal/stateless/blob/v1.8.0/modes.go>
- `stateless` 状态表示与 guard 互斥检查: <https://github.com/qmuntal/stateless/blob/v1.8.0/states.go>
- `stateless` DOT 导出: <https://github.com/qmuntal/stateless/blob/v1.8.0/graph.go>
- `event` README: <https://github.com/kelindar/event/blob/v1.5.2/README.md>
- `event` dispatcher 实现: <https://github.com/kelindar/event/blob/v1.5.2/event.go>
- `event` 包级默认 dispatcher: <https://github.com/kelindar/event/blob/v1.5.2/default.go>
- `looplab/fsm` README: <https://github.com/looplab/fsm/blob/v1.0.3/README.md>
- `looplab/fsm` 实现: <https://github.com/looplab/fsm/blob/v1.0.3/fsm.go>
- `asaskevich/EventBus` README: <https://github.com/asaskevich/EventBus/blob/49d423059eef/README.md>
- `asaskevich/EventBus` 实现: <https://github.com/asaskevich/EventBus/blob/49d423059eef/event_bus.go>

历史 V2 参照文件：

- `go-agent-v2/internal/runner/manager.go`
- `go-agent-v2/internal/runner/manager_event.go`
- `go-agent-v2/internal/runner/manager_recover.go`
- `go-agent-v2/internal/runner/manager_lifecycle.go`
- `go-agent-v2/internal/runner/manager_auto_recover.go`
- `go-agent-v2/internal/uistate/event_normalizer.go`
- `go-agent-v2/internal/apiserver/server_event_handler.go`
- `go-agent-v2/internal/bus/bus.go`
- `go-agent-v2/internal/guards/state_matrix_snapshot.json`

---

## 一句话结论

V3 应当采用“`stateless` 负责生命周期真相，`kelindar/event` 负责类型安全广播，`fx` 负责装配，`oklog/run` 负责副作用编排”的组合。
不要把 V2 的隐式状态与松散 payload 原样搬迁到新库上，否则只是把旧问题换一个 API 外壳继续保留。
