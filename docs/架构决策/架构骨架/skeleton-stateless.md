# skeleton-stateless.md — github.com/qmuntal/stateless v1.8.0 状态机

> **版本**: v1.8.0 | **模块**: `github.com/qmuntal/stateless`
> **定位**: V3 显式状态机

---

## 0. 一句话定位

stateless = V3 的显式状态机。所有状态转移声明式定义，自动生成转移矩阵，替代 V2 隐式 switch/case 链和 `effectiveState` 双重状态。

---

## 1. 状态与触发器定义范式

### 1.1 状态定义

```go
// internal/statemachine/states.go
package statemachine

// Agent 状态——用 string 常量而非 iota，便于调试和日志
const (
    StateIdle     = "idle"      // 空闲，等待用户输入
    StateThinking = "thinking"  // AI 正在思考/生成
    StateRunning  = "running"   // 工具正在执行
    StateStopped  = "stopped"   // 用户主动停止
    StateError    = "error"     // 出错
)
```

### 1.2 触发器定义

```go
// internal/statemachine/triggers.go
package statemachine

// 触发器对应 provider 事件
const (
    TriggerMessageDelta   = "message_delta"    // AI 开始生成文本
    TriggerCommandStart   = "command_start"    // 工具开始执行
    TriggerCommandEnd     = "command_end"      // 工具执行完成
    TriggerTurnComplete   = "turn_complete"    // 一轮完成
    TriggerError          = "error"            // 发生错误
    TriggerStop           = "stop"             // 用户停止
    TriggerRecover        = "recover"          // 从错误恢复
    TriggerReset          = "reset"            // 重置到初始状态
)
```

---

## 2. 转移表定义范式

### 2.1 声明式转移配置

```go
// internal/statemachine/agent_sm.go
package statemachine

import (
    "context"
    "github.com/qmuntal/stateless"
)

// NewAgentStateMachine 创建 Agent 状态机——所有合法转移一目了然
func NewAgentStateMachine(agentID string, hooks Hooks) *stateless.StateMachine {
    sm := stateless.NewStateMachine(StateIdle)

    // --- idle: 等待输入 ---
    sm.Configure(StateIdle).
        Permit(TriggerMessageDelta, StateThinking).
        Permit(TriggerCommandStart, StateRunning).
        Permit(TriggerError, StateError)

    // --- thinking: AI 正在生成 ---
    sm.Configure(StateThinking).
        Permit(TriggerCommandStart, StateRunning).
        Permit(TriggerTurnComplete, StateIdle).
        Permit(TriggerError, StateError).
        Permit(TriggerStop, StateStopped).
        Ignore(TriggerMessageDelta) // 重复的 delta 忽略

    // --- running: 工具正在执行 ---
    sm.Configure(StateRunning).
        Permit(TriggerMessageDelta, StateThinking).
        Permit(TriggerCommandEnd, StateThinking).
        Permit(TriggerTurnComplete, StateIdle).
        Permit(TriggerError, StateError).
        Permit(TriggerStop, StateStopped)

    // --- error: 出错 ---
    sm.Configure(StateError).
        Permit(TriggerRecover, StateIdle).
        Permit(TriggerReset, StateIdle).
        Permit(TriggerStop, StateStopped)

    // --- stopped: 已停止 ---
    sm.Configure(StateStopped).
        Permit(TriggerReset, StateIdle)

    return sm
}
```

### 2.2 转移矩阵可视化

```
         │ msg_delta │ cmd_start │ cmd_end │ turn_done │ error   │ stop    │ recover │ reset
─────────┼───────────┼───────────┼─────────┼───────────┼─────────┼─────────┼─────────┼──────
idle     │ thinking  │ running   │    -    │     -     │ error   │    -    │    -    │  -
thinking │ (ignore)  │ running   │    -    │   idle    │ error   │ stopped │    -    │  -
running  │ thinking  │     -     │thinking │   idle    │ error   │ stopped │    -    │  -
error    │     -     │     -     │    -    │     -     │    -    │ stopped │  idle   │ idle
stopped  │     -     │     -     │    -    │     -     │    -    │    -    │    -    │ idle
```

---

## 3. Guard 条件范式

### 3.1 PermitIf — 带条件的转移

```go
// PermitIf 替代 V2 的 allowlistWindow
sm.Configure(StateRunning).
    PermitIf(TriggerStop, StateStopped, func(ctx context.Context, args ...any) bool {
        // 只有当工具不在执行关键操作时才允许停止
        return !isToolInCriticalSection(agentID)
    }).
    Permit(TriggerError, StateError)
```

### 3.2 带名称的 Guard（便于调试）

```go
sm.Configure(StateThinking).
    PermitIf(TriggerStop, StateStopped,
        stateless.WithGuard(func(ctx context.Context, args ...any) bool {
            return canStopSafely(agentID)
        }),
        stateless.WithGuardDescription("canStopSafely"),
    )
```

### 3.3 多条件 Guard

```go
sm.Configure(StateError).
    PermitIf(TriggerRecover, StateIdle, func(ctx context.Context, args ...any) bool {
        // 多条件组合
        errCount := getErrorCount(agentID)
        return errCount < maxRetries && isRecoverable(agentID)
    })
```

---

## 4. Entry / Exit Action 范式

### 4.1 OnEntry 动作

```go
// Hooks 封装所有副作用回调
type Hooks struct {
    OnStateChange func(agentID, from, to, trigger string)
    OnError       func(agentID string, err error)
    OnStopped     func(agentID string)
}

func NewAgentStateMachine(agentID string, hooks Hooks) *stateless.StateMachine {
    sm := stateless.NewStateMachine(StateIdle)

    // Entry/Exit 动作替代 V2 的 applyAgentEventToRuntime
    sm.Configure(StateThinking).
        OnEntry(func(ctx context.Context, args ...any) error {
            hooks.OnStateChange(agentID, "->", StateThinking, triggerName(args))
            // 发射事件通知 UI
            event.Emit(events.AgentStateChanged{
                AgentID:   agentID,
                ToState:   StateThinking,
                Trigger:   triggerName(args),
            })
            return nil
        }).
        Permit(TriggerCommandStart, StateRunning).
        Permit(TriggerTurnComplete, StateIdle)

    sm.Configure(StateError).
        OnEntry(func(ctx context.Context, args ...any) error {
            hooks.OnError(agentID, errorFromArgs(args))
            event.Emit(events.AgentStateChanged{
                AgentID: agentID,
                ToState: StateError,
            })
            return nil
        })

    sm.Configure(StateStopped).
        OnEntry(func(ctx context.Context, args ...any) error {
            hooks.OnStopped(agentID)
            event.Emit(events.AgentStopped{
                AgentID: agentID,
                Reason:  "user_stop",
            })
            return nil
        })

    return sm
}
```

### 4.2 OnExit 动作

```go
sm.Configure(StateRunning).
    OnExit(func(ctx context.Context, args ...any) error {
        // 离开 running 状态时清理工具执行上下文
        cleanupToolContext(agentID)
        return nil
    })
```

### 4.3 全局 OnTransitioned 回调

```go
// 所有转移的全局日志记录
sm.OnTransitioned(func(ctx context.Context, t stateless.Transition) {
    log.Info("state transition",
        "agent", agentID,
        "from", t.Source,
        "to", t.Destination,
        "trigger", t.Trigger,
    )
})
```

---

## 5. 自动转移矩阵生成（用于测试守卫）

### 5.1 从状态机定义生成矩阵

```go
// internal/statemachine/matrix_test.go
package statemachine_test

import (
    "encoding/json"
    "os"
    "testing"

    "github.com/qmuntal/stateless"
)

// TestGenerateTransitionMatrix 自动从状态机定义生成矩阵 JSON
// 替代 V2 手写的 state_matrix_snapshot.json
func TestGenerateTransitionMatrix(t *testing.T) {
    sm := NewAgentStateMachine("test", Hooks{})

    states := []string{StateIdle, StateThinking, StateRunning, StateError, StateStopped}
    triggers := []string{
        TriggerMessageDelta, TriggerCommandStart, TriggerCommandEnd,
        TriggerTurnComplete, TriggerError, TriggerStop,
        TriggerRecover, TriggerReset,
    }

    matrix := make(map[string]map[string]string)
    for _, state := range states {
        row := make(map[string]string)
        for _, trigger := range triggers {
            // 尝试获取目标状态
            permitted, _ := sm.PermittedTriggers(state)
            found := false
            for _, p := range permitted {
                if p == trigger {
                    row[trigger] = "permitted"
                    found = true
                    break
                }
            }
            if !found {
                row[trigger] = "denied"
            }
        }
        matrix[state] = row
    }

    data, _ := json.MarshalIndent(matrix, "", "  ")

    // Golden file 测试
    golden := "testdata/transition_matrix.golden.json"
    if os.Getenv("UPDATE_GOLDEN") != "" {
        os.WriteFile(golden, data, 0644)
    }

    expected, err := os.ReadFile(golden)
    if err != nil {
        t.Fatalf("golden file not found, run with UPDATE_GOLDEN=1: %v", err)
    }
    assert.JSONEq(t, string(expected), string(data))
}
```

### 5.2 DOT 图生成

```go
// 生成 Graphviz DOT 格式的状态图
func TestGenerateDOT(t *testing.T) {
    sm := NewAgentStateMachine("test", Hooks{})
    dot := sm.ToGraph()
    os.WriteFile("testdata/agent_states.dot", []byte(dot), 0644)
    // 可用 dot -Tpng agent_states.dot -o agent_states.png 生成图片
}
```

---

## 6. 与 kelindar/event 集成

### 6.1 事件 → 状态机触发

```go
// Provider 事件映射为状态机触发器
type AgentEventHandler struct {
    sm *stateless.StateMachine
}

func (h *AgentEventHandler) Setup() {
    // Provider 事件 → 状态机转移
    event.On[events.ProviderMessageDelta](func(ev events.ProviderMessageDelta) {
        if err := h.sm.FireCtx(context.Background(), TriggerMessageDelta); err != nil {
            log.Warn("state transition rejected", "trigger", TriggerMessageDelta, "err", err)
        }
    })

    event.On[events.ProviderCommandStart](func(ev events.ProviderCommandStart) {
        _ = h.sm.FireCtx(context.Background(), TriggerCommandStart)
    })

    event.On[events.ProviderTurnComplete](func(ev events.ProviderTurnComplete) {
        _ = h.sm.FireCtx(context.Background(), TriggerTurnComplete)
    })

    event.On[events.ProviderError](func(ev events.ProviderError) {
        _ = h.sm.FireCtx(context.Background(), TriggerError, ev.Err)
    })
}
```

### 6.2 状态机 → 事件（通过 OnEntry）

```go
// 在第 4 节已展示：OnEntry 中 event.Emit(...)
// 形成完整的 事件→状态机→事件 循环
```

---

## 7. 并发安全

```go
// stateless.StateMachine 的 FireCtx 是并发安全的
// 内部使用 sync.Mutex 保护状态转移
// 可以从多个 goroutine 安全调用

// 安全的并发使用
go func() { sm.FireCtx(ctx, TriggerMessageDelta) }()
go func() { sm.FireCtx(ctx, TriggerStop) }()
// 只有一个会成功，另一个可能因为状态已变而返回错误

// 查询状态也是并发安全的
currentState, _ := sm.State(context.Background())
```

---

## 8. 多实例管理

### 8.1 AgentManager 管理多个状态机

```go
// internal/runner/manager.go
type AgentManager struct {
    mu       sync.RWMutex
    machines map[string]*AgentInstance // key: agentID
}

type AgentInstance struct {
    SM       *stateless.StateMachine
    AgentID  string
    ThreadID string
    Created  time.Time
}

func (m *AgentManager) StartAgent(ctx context.Context, agentID, threadID string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if _, exists := m.machines[agentID]; exists {
        return fmt.Errorf("agent %q already running", agentID)
    }

    hooks := Hooks{
        OnStateChange: func(id, from, to, trigger string) {
            log.Info("agent state", "agent", id, "from", from, "to", to)
        },
    }
    sm := NewAgentStateMachine(agentID, hooks)

    m.machines[agentID] = &AgentInstance{
        SM:       sm,
        AgentID:  agentID,
        ThreadID: threadID,
        Created:  time.Now(),
    }
    return nil
}

func (m *AgentManager) FireTrigger(agentID, trigger string, args ...any) error {
    m.mu.RLock()
    inst, ok := m.machines[agentID]
    m.mu.RUnlock()

    if !ok {
        return fmt.Errorf("agent %q not found", agentID)
    }
    return inst.SM.FireCtx(context.Background(), trigger, args...)
}

func (m *AgentManager) GetState(agentID string) (string, error) {
    m.mu.RLock()
    inst, ok := m.machines[agentID]
    m.mu.RUnlock()

    if !ok {
        return "", fmt.Errorf("agent %q not found", agentID)
    }
    state, err := inst.SM.State(context.Background())
    if err != nil {
        return "", err
    }
    return state.(string), nil
}
```

---

## 9. 测试范式

### 9.1 Table-Driven 转移测试

```go
func TestAgentTransitions(t *testing.T) {
    tests := []struct {
        name        string
        initial     string
        trigger     string
        wantState   string
        wantErr     bool
    }{
        {"idle -> thinking on message_delta", StateIdle, TriggerMessageDelta, StateThinking, false},
        {"idle -> running on command_start", StateIdle, TriggerCommandStart, StateRunning, false},
        {"idle -> error on error", StateIdle, TriggerError, StateError, false},
        {"idle rejects stop", StateIdle, TriggerStop, StateIdle, true},
        {"thinking -> idle on turn_complete", StateThinking, TriggerTurnComplete, StateIdle, false},
        {"thinking -> running on command_start", StateThinking, TriggerCommandStart, StateRunning, false},
        {"thinking -> stopped on stop", StateThinking, TriggerStop, StateStopped, false},
        {"running -> thinking on message_delta", StateRunning, TriggerMessageDelta, StateThinking, false},
        {"error -> idle on recover", StateError, TriggerRecover, StateIdle, false},
        {"stopped -> idle on reset", StateStopped, TriggerReset, StateIdle, false},
        {"stopped rejects message_delta", StateStopped, TriggerMessageDelta, StateStopped, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            sm := NewAgentStateMachine("test", Hooks{})
            // 设置初始状态
            setInitialState(t, sm, tt.initial)

            err := sm.FireCtx(context.Background(), tt.trigger)

            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
            }

            state, _ := sm.State(context.Background())
            assert.Equal(t, tt.wantState, state)
        })
    }
}
```

### 9.2 Guard 条件测试

```go
func TestGuardConditions(t *testing.T) {
    criticalSection := false
    sm := NewAgentStateMachineWithGuards("test", Hooks{}, func() bool {
        return !criticalSection
    })

    // 设置到 running 状态
    sm.FireCtx(ctx, TriggerCommandStart)

    // Guard 允许: 非关键段可以停止
    criticalSection = false
    err := sm.FireCtx(ctx, TriggerStop)
    assert.NoError(t, err)

    // 重置
    sm.FireCtx(ctx, TriggerReset)
    sm.FireCtx(ctx, TriggerCommandStart)

    // Guard 拒绝: 关键段不能停止
    criticalSection = true
    err = sm.FireCtx(ctx, TriggerStop)
    assert.Error(t, err)
}
```

### 9.3 完整矩阵覆盖测试

```go
func TestFullTransitionMatrix(t *testing.T) {
    states := []string{StateIdle, StateThinking, StateRunning, StateError, StateStopped}
    triggers := []string{
        TriggerMessageDelta, TriggerCommandStart, TriggerCommandEnd,
        TriggerTurnComplete, TriggerError, TriggerStop,
        TriggerRecover, TriggerReset,
    }

    sm := NewAgentStateMachine("test", Hooks{})

    for _, state := range states {
        for _, trigger := range triggers {
            t.Run(fmt.Sprintf("%s+%s", state, trigger), func(t *testing.T) {
                testSM := NewAgentStateMachine("test", Hooks{})
                setInitialState(t, testSM, state)

                err := testSM.FireCtx(context.Background(), trigger)
                // 只验证不 panic——具体转移结果由 table test 覆盖
                _ = err
            })
        }
    }
}
```

---

## 10. 对比 V2 改进

| 维度 | V2 (隐式状态机) | V3 (stateless) |
|---|---|---|
| 转移定义 | switch/case 分散在 4 个文件 | 声明式转移表，1 文件集中 |
| 状态表示 | `effectiveState` 双重状态 | 单一 `stateless.State` |
| 条件转移 | `allowlistWindow` 自定义实现 | `PermitIf` + guard function |
| 副作用 | `applyAgentEventToRuntime` 大函数 | `OnEntry`/`OnExit` 声明式 |
| 矩阵测试 | 手写 `state_matrix_snapshot.json` | 自动从定义生成 |
| 并发安全 | 手写 `sync.Mutex` | 内置并发安全 |
| 可视化 | 无 | `sm.ToGraph()` 生成 DOT |
| 非法转移 | 静默忽略或 panic | 返回明确错误 |
| 新增状态 | 全文搜索修改 | 在转移表中添加 |

---

## 11. 与其他 5 个框架的集成

| 框架 | 集成点 | 说明 |
|---|---|---|
| **kelindar/event** (`skeleton-event.md`) | 双向桥接 | 事件 → 触发器，OnEntry → 发射事件 |
| **fx** (`skeleton-fx.md`) | `RunnerModule` | fx 注入 `AgentManager`，管理状态机实例 |
| **oklog/run** (`skeleton-rungroup.md`) | `AgentManager.Run()` | 状态机管理器作为 Actor |
| **jrpc2** (`skeleton-jrpc2.md`) | handler 查询状态 | RPC handler 读取当前状态用于响应 |
| **sqlc** (`skeleton-sqlc.md`) | 状态持久化 | 状态变更时写入数据库（通过事件） |

---

## 12. 禁止行为（红线）

| 规则 | 原因 |
|---|---|
| ❌ 在 state machine 定义之外用 switch/case 做状态转移 | 所有转移必须声明式 |
| ❌ 直接修改状态机内部状态 | 只能通过 `FireCtx` 触发转移 |
| ❌ 在 OnEntry/OnExit 中做阻塞 I/O | 会阻塞状态机的内部 mutex |
| ❌ 创建多个同 agent 的状态机实例 | 一个 agent 对应一个状态机 |
| ❌ 用 iota 定义状态 | 用 string 常量便于调试和日志 |
| ❌ 忽略 `FireCtx` 返回的 error | error 表示非法转移，必须记录日志 |
| ❌ 在 Guard 函数中修改外部状态 | Guard 必须是纯函数 |
