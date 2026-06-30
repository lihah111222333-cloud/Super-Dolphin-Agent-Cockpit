# skeleton-stateless.md — platform/statemachine 状态机骨架

> **当前库**: `github.com/qmuntal/stateless`
> **当前平台封装**: `internal/platform/statemachine`
> **定位**: 用声明式配置创建状态机，统一外部状态存储、guard、entry/exit 回调和允许触发器查询。

---

## 0. 一句话定位

`internal/platform/statemachine` 是 stateless 的薄工厂。业务代码提供 `Config{Initial, States}`，平台层负责创建 `stateless.StateMachine`，并确保外部 accessor/mutator 要么成对使用，要么退回进程内状态，避免半持久化状态漂移。

---

## 1. 当前 API

```go
type Permit struct {
    Trigger string
    Dest    string
    Guard   func(ctx context.Context, args ...any) bool
}

type StateConfig struct {
    Name    string
    Permits []Permit
    OnEntry func(ctx context.Context, args ...any) error
    OnExit  func(ctx context.Context, args ...any) error
}

type Config struct {
    Initial string
    States  []StateConfig
}

func New(cfg Config, accessor func() string, mutator func(string)) *stateless.StateMachine
func AllowedTriggers(sm *stateless.StateMachine, ctx context.Context) ([]string, error)
```

注意：`stateless@v1.8.0` 没有本文旧版本写过的 `PermitIf` 方法。带条件转移通过 `conf.Permit(trigger, dest, guard...)` 接入，平台层的 `Permit.Guard` 就是这个入口。

---

## 2. 外部状态存储

```go
sm := statemachine.New(cfg,
    func() string { return runtime.State },
    func(next string) { runtime.State = next },
)
```

规则：

- accessor/mutator 必须成对提供。
- 任一缺失时，平台层退回进程内 `state := cfg.Initial`，避免只读或只写的半持久化状态。
- 状态值使用字符串，便于日志、数据库、JSON 和 UI 展示。
- `AllowedTriggers` 包装 `PermittedTriggersCtx`，保留底层错误上下文。

---

## 3. 当前使用面

| 使用方 | 用途 |
|---|---|
| `cmd/mcp-orch/orchestration/launch_helpers.go` | agent runtime 启动时创建状态机 |
| `cmd/mcp-orch/orchestration/persistent_runtime_rehydrate.go` | 从持久化 runtime 恢复状态机 |
| `internal/module/turn/tracker.go` | turn 运行状态跟踪 |
| `internal/platform/statemachine/factory_test.go` | 平台工厂行为、guard、entry/exit 和外部存储测试 |

`internal/platform/statemachine/module.go` 当前只保留 Fx 装配锚点：

```go
var Module = fx.Module("statemachine")
```

---

## 4. 事件集成

状态变化本身不直接推 UI。当前链路应保持：

```text
state transition
  -> business/orchestration emits typed DTO
  -> internal/platform/bus
  -> internal/platform/eventsurface
  -> rpc push
```

不要在状态机 guard 或 entry callback 里做阻塞 I/O；需要发事件时，把副作用限制为明确的 typed event 或业务层回调。

---

## 5. 测试范式

最低验证：

```bash
./scripts/test_with_guard.sh ./internal/platform/statemachine ./internal/module/turn ./cmd/mcp-orch/orchestration -count=1
./scripts/test_with_guard.sh ./internal/archtest -count=1
```

状态机相关测试应覆盖：

- 每个状态允许/拒绝的 trigger。
- guard true/false 两条路径。
- OnEntry/OnExit 错误传播。
- 外部 accessor/mutator 与进程内 fallback。
- `AllowedTriggers` 返回值和错误上下文。

---

## 6. 禁止行为

| 规则 | 原因 |
|---|---|
| 不绕过 `platform/statemachine.New` 手写分散状态表 | 状态配置会失去统一 guard/test 入口 |
| 不忽略 `FireCtx` 返回的 error | 非法转移必须可观测 |
| 不在 guard 中修改外部状态 | guard 应保持可预测 |
| 不在 entry/exit 中做阻塞 I/O | 会阻塞状态机触发链 |
| 不用 `switch/case` 二次推导主状态 | 状态机和持久化状态会漂移 |
