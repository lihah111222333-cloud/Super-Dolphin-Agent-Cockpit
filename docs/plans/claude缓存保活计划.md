# Claude 缓存保活计划 (Cache Keepalive Plan)

> **文档状态**: 已修订 v5（v4 + 二审修订：涡到 turn 核心修复 / API校正 / 类型统一 / 并发闭环）
> **修订日期**: 2026-04-14
> **可行性**: ⚠️ 功能可实现，仓库契约需同步治理（`claudecli` 包文件数已超限）

## 1. 背景与痛点分析

在 `super-agent-v3` 的多层代理架构中，系统常遇到以下情况：
当主 Agent (Claude Code CLI) 委派给后台的子 Agent 一项耗时极长的任务（如全局扫描、环境配置、编译等，可能耗时超过 1 小时）时，主 Agent 进入长期的挂机空闲状态等待。

根据 Anthropic 的官方策略，Prompt Caching 的 TTL 为 **5 分钟**（标准）或 **1 小时**（extended）。一旦闲置时间超出 TTL，原先几十万 Token 的缓存将被销毁。当子 Agent 完成任务后，主 Agent 的苏醒会导致严重的 **Cache Miss（缓存全量重写）**，带来：
1. **高昂的费用消耗：** 1h TTL 缓存写入的单价约为基础 Input 的 2 倍。
2. **极高的首字延迟 (TTFT)：** 重写几十万长文本需要数秒至十几秒的静默处理时间。

## 2. 核心解决思路

利用 Event Bus 事件总线（`kelindar/event` Dispatcher + `platformbus.ResilientSubscribe`）监听 Agent 生命周期状态变化，当检测到 Claude agent 空闲超过 55 分钟时，通过专用的静默 Turn 通路向 CLI Stdin 注入一条微型保活消息。

### 保护范围

- **仅保护**：主界面上活跃的、未进入回收站的 Claude agent
- **排除**：`binding.Archived == true` 的已归档 agent
- **排除**：已停止（`transport == nil`）的 agent

---

## 3. 架构规范

### 3.1 DI 框架

项目使用 `uber/fx` 依赖注入。所有新模块必须遵循标准模式：

```go
// 标准 fx.Module 模板（参考 hooks/module.go）
var Module = fx.Module("platform.cachekeepalive",
    fx.Provide(NewManager),
    fx.Invoke(registerKeepaliveLifecycle),
)
```

### 3.2 生命周期管理

通过 `fx.Lifecycle` 的 `OnStart` / `OnStop` Hook 管理启停，与 `hooks/module.go:76-93` 的 `registerEventRelayLifecycle` 模式完全对齐：

```go
func registerKeepaliveLifecycle(lc fx.Lifecycle, in keepaliveIn) {
    var cancel func()
    lc.Append(fx.Hook{
        OnStart: func(context.Context) error {
            cancel = startKeepaliveRelay(in.Dispatcher, in.Manager, in.Logger)
            return nil
        },
        OnStop: func(context.Context) error {
            if cancel != nil {
                cancel()
            }
            in.Manager.Shutdown()
            return nil
        },
    })
}
```

### 3.3 接口策略

`SendKeepalive` **不加入** `contract.Session` 接口（codex provider 不需要此能力）。使用接口断言，与 `AllowedModels` / `ReadConfig` / `RuntimeConfigSnapshot` 等可选方法一致：

```go
// cachekeepalive.Manager 中
type KeepaliveCapable interface {
    SendKeepalive(ctx context.Context) error
}

// 调用时
if kc, ok := sess.(KeepaliveCapable); ok {
    return kc.SendKeepalive(ctx)
}
```

### 3.4 Session 获取

通过 `contract.SessionResolver.ResolveSession(ctx, threadID)` 获取 session 实例。Manager 持有 `SessionResolver` 依赖。

### 3.5 事件订阅

复用 `platformbus.ResilientSubscribe` 模式（与 `event_relay.go` 一致），不使用 `HookManager.Subscribe` MCP 通路。

---

## 4. 实施方案

### Phase 1: 事件驱动的空闲计时器

| 事件 | Bus 类型 | 触发逻辑 |
|:-----|:---------|:---------|
| Agent 进入空闲 | `agentdto.StateChanged` | `NewState == "idle"` → 启动/重置 55 分钟倒计时 |
| Turn 完成 | `turndto.TurnCompleted` | 重置 55 分钟倒计时 |
| Agent 启动 | `agentdto.AgentLaunched` | 注册 SessionUUID → agentID/threadID 映射 |
| 停止/归档/删除 | `threaddto.Stopped` | Agent 停止、归档或删除时清理该 agent 的计时器（该事件由 `stop.go:40`、`archive.go:25`、`service.go:196` 三条路径发布） |

Timer map 的 key 使用 **SessionUUID**（CLI 会话的唯一标识），而不是 agentID。UUID 跨 CLI 重启持久化（`restartResumeIDLocked()` 用同一 UUID 做 `--resume`），因此 timer 在 CLI 重启后仍然有效，实现了对同一会话的**连续保护**。

> **注意**：restart 重置时需同步清理 `silentTurnIDs`（`make(map[string]silentTurnState)`），与现有 `activeTurn`/`suppressedTurns` 清理逻辑对齐。

```go
func startKeepaliveRelay(
    dispatcher *event.Dispatcher,
    manager    *Manager,
    logger     *pkglogger.Logger,
) func() {
    // 注册 SessionUUID 映射（从 AgentLaunched 的 SessionID 获取）
    // 注意：ResilientSubscribe 回调签名是 func(ev T)，没有 ctx，
    // 因此回填逻辑封装在 Manager.HandleAgentLaunched 内部，
    // 内部使用 context.Background()。
    launchCancel := platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentLaunched) {
        manager.HandleAgentLaunched(ev)
    }, logger)

    stateCancel := platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.StateChanged) {
        if strings.EqualFold(strings.TrimSpace(ev.NewState), "idle") {
            manager.ResetTimerByAgent(ev.AgentID)
        }
    }, logger)

    turnCancel := platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
        manager.ResetTimerByAgent(ev.AgentID)
    }, logger)

    exitCancel := platformbus.ResilientSubscribe(dispatcher, func(ev threaddto.Stopped) {
        manager.StopTimerByAgent(ev.AgentID)
    }, logger)

    return func() { launchCancel(); stateCancel(); turnCancel(); exitCancel() }
}
```

```go
// manager.go — HandleAgentLaunched 封装回填逻辑
//
// 依赖：bindingStore (binding.Store) + threadStore (thread.Store)
// 回填策略参考 thread/events.go:65-76 的 resolveBindingForEvent 模式：
// 先尝试 GetByAgentID，失败时通过 threadID → threadStore.GetByThreadID → binding 回填
func (m *Manager) HandleAgentLaunched(ev agentdto.AgentLaunched) {
    ctx := context.Background()
    agentID := strings.TrimSpace(ev.AgentID)
    sid := strings.TrimSpace(ev.SessionID)
    if sid == "" {
        return
    }
    // agentID 回填：先直接用，不可靠时通过 threadID 解析
    if agentID == "" {
        if t, err := m.threadStore.GetByThreadID(ctx, ev.ThreadID); err == nil && t != nil {
            agentID = t.AgentID
        }
    }
    // 二次回填：即使 agentID 非空，也验证其有效性
    if agentID != "" {
        if _, err := m.bindingStore.GetByAgentID(ctx, agentID); err != nil {
            agentID = "" // 无效 agentID，放弃注册
        }
    }
    if agentID != "" {
        m.Register(sid, agentID, ev.ThreadID)
    }
}
```

```go
// manager.go — DI 依赖定义
type keepaliveIn struct {
    fx.In
    Dispatcher   *event.Dispatcher
    Manager      *Manager
    Logger       *pkglogger.Logger
}

// NewManager 构造函数——显式列出所有依赖
func NewManager(
    resolver     contract.SessionResolver,
    bindingStore binding.Store,
    threadStore  thread.Store,
    logger       *pkglogger.Logger,
) *Manager {
    return &Manager{
        resolver:     resolver,
        bindingStore: bindingStore,
        threadStore:  threadStore,
        logger:       logger,
        timers:       make(map[string]*agentTimer),
    }
}
```

### Phase 2: 三重安全检查

```go
// agentTimer 存储每个 CLI 进程实例的保活信息
type agentTimer struct {
    sessionUUID string         // CLI 进程实例唯一标识（key）
    agentID     string         // agent 标识（用于 binding 查询）
    threadID    string         // thread 标识（用于 session 解析）
    timer       *time.Timer
}

func (m *Manager) canPing(ctx context.Context, t *agentTimer) bool {
    // 1. 回收站排除 → 通过 bindingStore.GetByAgentID 查询 Archived 字段
    //    注意：不存在 IsArchived() API，直接查 binding 结构体
    b, err := m.bindingStore.GetByAgentID(ctx, t.agentID)
    if err != nil || b == nil || b.Archived {
        return false
    }
    // 2. session 存活 → SessionResolver.ResolveSession（按 threadID 解析）
    sess, err := m.resolver.ResolveSession(ctx, t.threadID)
    if err != nil || sess == nil {
        return false
    }
    // 3. 无活跃 Turn → 接口断言
    kc, ok := sess.(KeepaliveCapable)
    return ok && kc != nil
}
```

### Phase 3: 心跳发送 — 专用静默 Turn

> **关键设计**：不使用 `StartTurn()`，原因见 §5 三个致命问题。

```go
// session_silent_turn.go — 静默 Turn 状态类型
type silentTurnState int
const (
    silentActive   silentTurnState = iota // keepalive 进行中
    silentDraining                        // 超时，等待晚到事件被拦截
)

const keepaliveTimeout = 30 * time.Second

func (s *session) SendKeepalive(ctx context.Context) error {
    s.mu.Lock()
    // 先回收残留的 draining silent turn（如果有）
    s.reclaimStaleSilentTurnLocked()
    if err := ensureTurnAvailable(s.activeTurn); err != nil {
        s.mu.Unlock()
        return err
    }
    if s.transport == nil || !s.transport.readyForSend() {
        s.mu.Unlock()
        return errors.New("claudecli: transport not ready for keepalive")
    }

    payload, err := marshalTurnPayload(
        "[CACHE-KEEPALIVE] Automated cache maintenance. Reply with only: OK",
    )
    if err != nil {
        s.mu.Unlock()
        return err
    }

    localID := "keepalive_" + shared.NewID("ping")
    handle := newTurnHandle(localID, localID)
    s.activeTurn = handle
    s.silentTurnIDs[localID] = silentActive
    s.mu.Unlock()

    // 底层发送，不触发 turn:started / turn:input_received
    if err := s.transport.Send(payload); err != nil {
        s.mu.Lock()
        s.takeActiveTurnLocked()
        delete(s.silentTurnIDs, localID)
        s.mu.Unlock()
        handle.finish(err)
        return err
    }

    // 带超时等待：防止 CLI 挂起导致 goroutine 泄漏
    timer := time.NewTimer(keepaliveTimeout)
    defer timer.Stop()
    select {
    case <-handle.Done():
        return handle.Err()
    case <-timer.C:
        // ❗ 核心设计：超时后 **不清 activeTurn**
        //
        // 原因：rawBase() 用 currentTurnID(s.activeTurn) 生成 turn_id。
        // 若超时后清除 activeTurn，currentTurnID(nil) 返回 ""(空)，
        // 晚到事件的 turn_id 会变成空或被打上后续新 turn 的 ID，
        // isSilentTurn 无法匹配，导致泄漯。
        //
        // 方案：保留 activeTurn 在位，标记为 silentDraining。
        // 晚到事件仍会被 rawBase() 打上正确的 silent turn_id，
        // 由 isSilentTurn 拦截丢弃。
        // 用户下次 StartTurn 时，prepareTurnLocked 调用
        // reclaimStaleSilentTurnLocked() 自动回收这个死 handle。
        s.mu.Lock()
        s.silentTurnIDs[localID] = silentDraining
        // 不调用 takeActiveTurnLocked()！保留 activeTurn 在位
        s.mu.Unlock()
        handle.finish(errors.New("claudecli: keepalive timeout"))
        return handle.Err()
    }
}

// reclaimStaleSilentTurnLocked 回收已完成的静默 turn，释放 activeTurn 槽位。
// 调用时机：StartTurn / SendKeepalive 前。必须持锁调用。
func (s *session) reclaimStaleSilentTurnLocked() {
    if s.activeTurn == nil {
        return
    }
    select {
    case <-s.activeTurn.Done():
        lid := s.activeTurn.LocalID()
        if _, ok := s.silentTurnIDs[lid]; ok {
            s.takeActiveTurnLocked()
            // 不删除 silentTurnIDs[lid]，保留用于拦截后续晚到事件。
            // 最终由 finishSilentTurn / handleReceiveExit / restart 清理。
        }
    default:
        // activeTurn 仍在运行，不回收
    }
}
```

### Phase 4: 静默 Turn 事件全量抑制

在 `applyRaw` 中新增 `isSilentTurn` 拦截，位于 `isCurrentTransport` 校验之后、`shouldSuppressTurn` 之前：

```go
// session_events.go — applyRaw 修改
if s.isSilentTurn(raw) {
    if shouldFinishTurnRaw(raw) {
        s.finishSilentTurn(raw)
    }
    return
}
```

```go
// session_silent_turn.go — 新文件
func (s *session) isSilentTurn(raw dto.RawProviderEvent) bool {
    turnID := dataString(raw.Data, "turn_id")
    if turnID == "" { return false }
    s.mu.Lock()
    _, silent := s.silentTurnIDs[turnID]
    s.mu.Unlock()
    return silent
}

func (s *session) finishSilentTurn(raw dto.RawProviderEvent) {
    turnID := dataString(raw.Data, "turn_id")
    // 复用现有 takeActiveTurn(turnID)（session_config.go:204-211），
    // 它在锁内按 turnID 比对后再 takeActiveTurnLocked()，不会盲取。
    handle := s.takeActiveTurn(turnID)
    s.mu.Lock()
    delete(s.silentTurnIDs, turnID)
    s.mu.Unlock()
    if handle == nil { return }
    if dataBool(raw.Data, "success") {
        handle.finish(nil)
    } else {
        handle.finish(errors.New(dataString(raw.Data, "error")))
    }
}
```

### Phase 4.1: 旁路拦截 — `handleReceiveExit` 静默 Turn 识别

> **审查发现**：`handleReceiveExit` 不经过 `applyRaw`，直接调用 `finishTurnWithError` + `dispatch turn:complete`。
> 如果 keepalive 期间 CLI 崩溃，静默 turn 事件仍会泄漯到 UI。

```go
// session_events.go — handleReceiveExit 修改
// 保留原始签名 (tr *transport, err error)，不删除 tr 参数！
// tr 用于过滤旧 transport 退出（restart 安全性保证）。
func (s *session) handleReceiveExit(tr *transport, err error) {
    finishErr := err
    if finishErr == nil || errors.Is(finishErr, io.EOF) {
        finishErr = io.EOF
    }
    s.mu.Lock()
    if s.transport != tr {
        s.mu.Unlock()
        return
    }
    handle := s.takeActiveTurnLocked()
    // 检查是否为静默 turn，若是则跳过 UI dispatch
    isSilent := false
    if handle != nil {
        _, isSilent = s.silentTurnIDs[handle.LocalID()]
        if isSilent {
            delete(s.silentTurnIDs, handle.LocalID())
        }
    }
    s.mu.Unlock()

    if isSilent {
        // 静默 turn：只 finish handle，不 dispatch 任何事件到 UI
        if handle != nil { handle.finish(finishErr) }
        return
    }
    // 非静默 turn：复用现有 finishTurnWithError，
    // 它会 handle.finish(err) + dispatch turn:complete(success:false, error:...)
    s.finishTurnWithError(handle, finishErr)
}
```

### Phase 4.2: restart 时清理 `silentTurnIDs`

在现有 restart 重置逻辑（`session_log_watcher_integration.go:270-278`）旁加：

```go
// restart 重置时，与 activeTurn/suppressedTurns 对齐
s.silentTurnIDs = make(map[string]silentTurnState)
```

### Phase 4.3: prepareTurnLocked 增加静默 turn 回收

在 `StartTurn` / `prepareTurnLocked` 入口处调用 `reclaimStaleSilentTurnLocked()`，
确保 draining 状态的静默 turn 不会阻塞用户新 turn：

```go
// session_turn.go — prepareTurnLocked 修改
func (s *session) prepareTurnLocked(...) (...) {
    s.reclaimStaleSilentTurnLocked() // ← 新增
    if err := ensureTurnAvailable(s.activeTurn); err != nil {
        return ..., err
    }
    // ... 原有逻辑
}
```

> **清理责任归属**：`silentTurnIDs` 条目的最终删除由以下路径负责：
> 1. `finishSilentTurn`（applyRaw 收到 turn:complete）— 正常路径
> 2. `handleReceiveExit`（CLI 崩溃）— 异常路径
> 3. restart 重置 — 全量清理
> 不需要独立的 sweep timer，因为以上三条路径已覆盖所有场景。

---

## 5. 不可使用 `suppressedTurns` 的三个致命问题

**问题 1：`shouldSuppressTurn` 只拦截终端事件**

```go
// session_events.go:196-199
func (s *session) shouldSuppressTurn(raw dto.RawProviderEvent) bool {
    return (raw.EventType == "turn:complete" || raw.EventType == "turn:interrupted") &&
        s.consumeSuppressedTurn(dataString(raw.Data, "turn_id"))
}
```

Claude 回复 "OK" 时产生的 `assistant:message_delta` 穿透到 UI。

**问题 2：`StartTurn` 中 `turn:started` / `turn:input_received` 绕过 `applyRaw`**

```go
// session.go:195-196
s.dispatch(started)        // 直接推送到 UI
s.dispatch(inputReceived)  // 直接推送到 UI
```

**问题 3：抑制后 `finishTurnFromRaw` 被跳过 → 永久死锁**

```go
// session_events.go:94
if s.shouldSuppressTurn(raw) {
    return  // ← finishTurnFromRaw() 永不调用 → handle.Done() 永不关闭 → Agent 瘫痪
}
```

> `ForceComplete` 不受影响，因为它在 `forceCompleteTurn()` 中先手动 `takeActiveTurn` + `handle.finish(nil)` 然后才用 `suppressedTurns` 丢弃 CLI 的延迟到达事件。

---

## 6. 代码落点与代码预算

### 新建文件

| # | 文件路径 | 职责 | 预算 (行) |
|:--|:--------|:-----|:---------|
| 1 | `internal/platform/cachekeepalive/manager.go` | Manager 结构体、Timer map、`HandleAgentLaunched`、`ResetTimer`/`StopTimer`/`Shutdown`/`executePing`/`canPing`、bindingStore+threadStore 依赖、`NewManager`、`keepaliveIn` | ~180 |
| 2 | `internal/platform/cachekeepalive/module.go` | fx.Module 定义、`registerKeepaliveLifecycle` | ~40 |
| 3 | `internal/platform/cachekeepalive/relay.go` | `startKeepaliveRelay` 事件订阅（回调委托给 Manager 方法） | ~35 |
| 4 | `internal/platform/cachekeepalive/manager_test.go` | Timer 重置、Archived 排除、ActiveTurn 跳过、Shutdown 清理、agentID 回填 | ~130 |
| 5 | `internal/provider/claudecli/session_silent_turn.go` | `silentTurnState`、`SendKeepalive`、`reclaimStaleSilentTurnLocked`、`isSilentTurn`、`finishSilentTurn` | ~115 |
| 6 | `internal/provider/claudecli/session_silent_turn_test.go` | 静默 Turn 生命周期、死锁回归、超时保留 activeTurn 回归、draining 回收回归 | ~120 |

**新增合计：~620 行**

### 修改文件

| # | 文件路径 | 修改内容 | 变更量 |
|:--|:--------|:---------|:------|
| 7 | `internal/provider/claudecli/session.go` | `session` 结构体新增 `silentTurnIDs map[string]silentTurnState` | +1 行 |
| 8 | `internal/provider/claudecli/driver.go` | `newStartedSession()` 中初始化 `silentTurnIDs`（真实构造点，非 `newSession`） | +1 行 |
| 9 | `internal/provider/claudecli/session_events.go` | `applyRaw` 中插入 `isSilentTurn` 检查（`isCurrentTransport` 之后、`shouldSuppressTurn` 之前） | +5 行 |
| 10 | `internal/provider/claudecli/session_events.go` | `handleReceiveExit` 增加静默 turn 识别（保留 `tr *transport` 参数） | +10 行 |
| 11 | `internal/provider/claudecli/session_turn.go` | `prepareTurnLocked` 入口增加 `reclaimStaleSilentTurnLocked()` 调用 | +1 行 |
| 12 | `internal/provider/claudecli/session_log_watcher_integration.go` | restart 重置时清理 `silentTurnIDs` | +1 行 |
| 13 | `internal/app/modules.go` | `Module` 列表新增 `cachekeepalive.Module`（放在 platform 模块段，靠近 `hooks.Module`） | +2 行 |

**修改合计：~21 行**

### 不修改的文件

| 文件 | 原因 |
|:----|:-----|
| `internal/contract/provider.go` | `SendKeepalive` 不加入 `Session` 接口，用接口断言 |
| `internal/contract/session_resolver.go` | 已有 `ResolveSession`，直接复用 |
| `internal/platform/hooks/event_relay.go` | 不嵌入 keepalive relay，独立模块 |
| `internal/provider/codexapp/*.go` | 无需实现 `KeepaliveCapable` |

**总预算：~620 行新代码 + ~21 行修改**

### ℹ️ 仓库契约影响说明

| 契约项 | 当前状态 | 本方案影响 | 建议 |
|:------|:---------|:---------|:-----|
| `session_events.go` ≤400 行 | 397 行 | +15 行 → ~412 行 **超限** | 先抽取 `handleReceiveExit`（14 行）到独立文件，原文件降至 ~383，加回 +5 约 388 行 ✅ |
| `claudecli` 包非测试文件 ≤15 | 21 个 | +1（silent_turn）+1（receive_exit 抽取）→ 23 个 **已超限**（历史债务） | 同步规划 `claudecli` 包拆分，非本方案阻断项 |

---

## 7. 架构一览

```
┌──────────────────────────────────────────────────────────────┐
│                    Event Bus (kelindar/event)                 │
│                                                              │
│  agentdto.AgentLaunched ───┐ Register(sessionUUID)           │
│  agentdto.StateChanged ────┤                                 │
│  turndto.TurnCompleted ────┼──→ cachekeepalive.Manager       │
│  threaddto.Stopped ────────┘        │                        │
│                                     ▼                        │
│  fx.Module("platform.cachekeepalive")                        │
│  ├─ relay.go:  platformbus.ResilientSubscribe x4             │
│  ├─ module.go: fx.Invoke(registerKeepaliveLifecycle)         │
│  └─ manager.go                                               │
│        │                                                     │
│        │  Timer map: sessionUUID → agentTimer                │
│        │  55 分钟到期                                         │
│        ▼                                                     │
│  ┌─────────────────────────────────────────┐                 │
│  │ canPing 三重检查                         │                 │
│  │ 1. bindingStore.GetByAgentID(agentID)  │                 │
│  │    → b == nil || b.Archived → false   │                 │
│  │ 2. resolver.ResolveSession(threadID)    │                 │
│  │ 3. sess.(KeepaliveCapable) → ok         │                 │
│  └────────────┬────────────────────────────┘                 │
│               │                                              │
│               ▼                                              │
│  ┌─────────────────────────────────────────┐                 │
│  │ claudecli.session.SendKeepalive()       │                 │
│  │                                         │                 │
│  │ 1. mu.Lock + ensureTurnAvailable        │                 │
│  │ 2. marshalTurnPayload (NDJSON)          │                 │
│  │ 3. activeTurn = handle                  │                 │
│  │ 4. silentTurnIDs[localID] = {}          │                 │
│  │ 5. transport.Send(payload)              │                 │
│  │ 6. <-handle.Done() 阻塞等待             │                 │
│  └────────────┬────────────────────────────┘                 │
│               │                                              │
│               ▼           Claude CLI (stdin → stdout)        │
│                                                              │
│  ┌─────────────────────────────────────────┐                 │
│  │ applyRaw 全量拦截                        │                 │
│  │                                         │                 │
│  │ isSilentTurn(raw)?                      │                 │
│  │ ├─ assistant:message_delta → 丢弃       │                 │
│  │ ├─ turn:complete →                      │                 │
│  │ │   finishSilentTurn()                  │                 │
│  │ │   ├─ takeActiveTurn(turnID) 按ID校验  │                 │
│  │ │   ├─ delete(silentTurnIDs)            │                 │
│  │ │   └─ handle.finish(nil) ← 解除阻塞   │                 │
│  │ └─ return (不 dispatch 到 bus)          │                 │
│  └─────────────────────────────────────────┘                 │
└──────────────────────────────────────────────────────────────┘
```

---

## 8. 收益预期

| 场景 | 原策略成本 (200k Token) | Ping 策略成本 (200k Token) | 节省 |
|:-----|:----------------------|:--------------------------|:-----|
| 单次挂机 2 小时后恢复 | 重写失效缓存 ($1.20) | 2 次 Cache Read ($0.12) | **90%** |
| 全天 8h 开发 (4 次挂机) | $4.80 | $0.48 | **90%** |
| 月度成本 (22 工作日) | $105.60 | $10.56 | **$95/月** |

## 9. 风险与缓解

| 风险 | 等级 | 缓解措施 |
|:-----|:-----|:---------|
| 心跳期间用户发送消息 | 中 | 正常心跳期间：`ensureTurnAvailable` 返回 `turn already running` 快速报错（非阻塞），心跳回复 < 1s。超时 draining 期间：`prepareTurnLocked` 调用 `reclaimStaleSilentTurnLocked()` 自动回收，用户 turn 正常启动 |
| 超时后晚到输出串 turn | **已修复** | 超时后不清 activeTurn，保留在位。`rawBase()` 仍生成 silent turn_id，晚到事件被 `isSilentTurn` 拦截。用户 StartTurn 时 `reclaimStaleSilentTurnLocked()` 自动回收（见 Phase 4.3） |
| `handleReceiveExit` 旁路泄漯 | **已修复** | 保留原始 `(tr *transport, err error)` 签名 + transport 守卫，增加静默 turn 识别，非静默时复用 `finishTurnWithError`（见 Phase 4.1） |
| Claude 对心跳 Prompt 发散推理 | 低 | `Reply with only: OK` 约束，预计 < 5 Output Token |
| 心跳消息污染 Claude 对话历史 | 低 | 每次 Ping 仅增加 ~20 Token（prompt + "OK"），单次挂机最多 1-2 次 |
| Timer goroutine 泄漏 | 低 | `Manager.Shutdown()` 全量清理 + fx.OnStop 保证调用 |
| CLI 挂起导致 Ping 阻塞 | 低 | 30s 超时后 activeTurn 保留为 draining，晚到事件仍被拦截；用户 turn 触发自动回收 |
| `restartIfNeededLocked` 触发重启 | 无 | `SendKeepalive` 绕过 `StartTurn`，不经过 restart 检查 |
| restart 后 `silentTurnIDs` 残留 | 低 | restart 重置时同步清理 `silentTurnIDs`（见 Phase 4.2） |
| 心跳完成后 55min 重置 | 低 | 静默 turn 被全量抑制，`turndto.TurnCompleted` 不会发到 bus；Manager.executePing 成功后应直接调用 `ResetTimer` 重置 55min 倒计时 |

## 10. 副作用说明

**心跳会写入 Claude CLI 的对话历史**。每次 Ping 在 Claude 侧留下一轮 `[CACHE-KEEPALIVE] → OK` 对话（~20 Token）。这是不可避免的代价——正是因为上下文被完整推送，才能触发 Cache Hit。影响：
- 单次 2h 挂机：1-2 次 Ping = 20-40 Token 增量 → **可忽略**
- 8h 开发日：最多 ~8 次 Ping = ~160 Token → 相比 200k 上下文可忽略（0.08%）
- 前端 UI 时间线：`silentTurnIDs` 全量拦截，**完全不可见**
