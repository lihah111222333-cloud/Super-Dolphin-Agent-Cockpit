# 根治 "session stopped" 5s 死锁（multi-layer 封口版）

> **背景：** 重启 Super-Dolphin 后端后给 claude provider 发 hi，前端等 28s 显示 "session stopped"。日志确认：claudecli driver `awaitResolvedThreadID` 等 5s（`InitialThreadIDTimeout`）后超时 → `s.stop(true)` → `agent:failed: session stopped`。
>
> **根因：** 数据脏值（binding.CodexThreadID = `agent_xxx`）经 resolver fallback 串到 claude driver；driver 内部 `sanitizeResumeID` 与 `markThreadReady` 对 placeholder 决策不一致 → 一边 drop CLI --resume 参数，一边阻塞等 system:init → 5s 超时 ForceStop。
>
> **根治范围：** L2 binding 数据卫生 + L3 resolver 入口校验 + L4 claude driver 谓词归一 + Background resume 候选过滤 + Event 路由按 provider 隔离。8 个 Phase，每 Phase 独立 commit。

## 不在范围

- agent_id / provider_thread_id / public_thread_id 类型隔离重构（L1）：1-2 天横扫式重构，作为长线技术债，不在本次。
- frontend 错误提示改进：本次只保证后端正确路径，前端展示策略另议。

## 双 provider 覆盖对账

| Phase | claude 路径 | codex 路径 | 备注 |
|---|---|---|---|
| 0 util/identifier | `IsClaudeCLISessionUUID`（严格）+ `LooksLikeUUID`（宽松） | `LooksLikeUUID`（宽松） | claude 严格谓词专用 |
| 1 L4 driver 谓词归一 | claudecli 内部 sanitize/markThreadReady 二元矛盾，**仅 claude** | codex 同步 RPC 立即失败，无此矛盾 | codex 由 Phase 3 自清承接 |
| 2 L3 resolver | 删 fallback + 入口校验 | 同上 | 双 provider 共享 |
| 3 L2 写入校验 + 失败自清 | claude `driver.go` ForceStop 路径 | codex `driver.go:222-224` resume 失败 | **双 provider 对称** |
| 4 L2 启动清扫 | 按字段过滤 | 按字段过滤 | 不分 provider |
| 5 Background resume 收紧 | thread 模块层 | 同 | 双 provider 共享 |
| 6 Event 路由 Provider 隔离 | claude translator 注册 `"claude"` | codex translator 注册 `"codex"` | 双 provider 各自命名空间 |
| 7 测试 & 验收 | 用例覆盖 | 用例覆盖 | 双 provider 集成测试 |

---

## Phase 0：抽公共 UUID helper 到 `internal/util/identifier`

**目标：** 把 thread 模块的宽松 `looksLikeUUID` 与 claudecli 的严格 `claudeSessionUUIDRE` 提到中性公共包，作为后续所有 Phase 的统一权威。

### 新建

`internal/util/identifier/uuid.go`（与 `internal/util/ctxutil` 同级；UUID 谓词是小工具，不暴露为公开包）：
```go
package identifier

import (
    "regexp"
    "strings"
)

// LooksLikeUUID 接受 hex+dash, hex>=32（宽松）。
// 用于 binding 数据卫生 / resolver 入口 / background resume / 启动清扫。
// 同时覆盖 codex 的不规则 thread id 形式。
func LooksLikeUUID(s string) bool {
    s = strings.TrimSpace(s)
    if len(s) < 32 {
        return false
    }
    hex := 0
    for _, c := range s {
        switch {
        case (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'):
            hex++
        case c == '-':
        default:
            return false
        }
    }
    return hex >= 32
}

var claudeCLIUUIDRE = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsClaudeCLISessionUUID 严格 v4 UUID dash 格式（claude CLI --resume 接受的形式）。
// 专用于 claudecli driver 内部 sanitize/markThreadReady/restart 决策。
func IsClaudeCLISessionUUID(s string) bool {
    return claudeCLIUUIDRE.MatchString(strings.TrimSpace(s))
}
```

新增 `internal/util/identifier/uuid_test.go`：覆盖 v4 严格 / hex32 宽松 / `agent_xxx` placeholder / 空字符串 / 各类杂质字符 各组合。

### 迁移现有 caller（行为不变）

- `internal/module/thread/start_session_helpers.go:350 looksLikeUUID` 删除
- `internal/module/thread/events.go:124` `looksLikeUUID(sessionID)` → `identifier.LooksLikeUUID(sessionID)`（import `"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"`）
- `internal/module/thread/lifecycle.go:517` `looksLikeUUID(id)` → `identifier.LooksLikeUUID(id)`
- `internal/module/thread/start_session.go:570` `looksLikeUUID(state.SessionUUID)` → `identifier.LooksLikeUUID(state.SessionUUID)`
- `internal/provider/claudecli/transport_config.go:23 claudeSessionUUIDRE` 删除
- `internal/provider/claudecli/transport_config.go:35 sanitizeResumeID` 内部判断改用 `identifier.IsClaudeCLISessionUUID`

### 验证

- `go test ./internal/util/identifier/...`
- `go test ./internal/module/thread/...`
- `go test ./internal/provider/claudecli/...`

### Commit

```
refactor(identifier): extract UUID predicates to internal/util/identifier
```

---

## Phase 1：L4 claudecli driver 谓词归一

**目标：** 删除 `requiresResolvedThreadID` / `shouldMarkThreadReady` 两个补丁产物，所有「能否 resume」决策统一走 `IsClaudeCLISessionUUID`。`isPlaceholderThreadID` 仅保留给显示语义。

### 改动

`internal/provider/claudecli/thread_identity.go`：
- 删除 `:11 requiresResolvedThreadID`（alias）
- 删除 `:17 shouldMarkThreadReady`（被取代）
- 保留 `:21 isPlaceholderThreadID`（仅 `session_events.go:139` 显示语义用）

`internal/provider/claudecli/driver.go`：
- `:289` markThreadReady 闸改为：
  ```go
  if !identifier.IsClaudeCLISessionUUID(spec.threadID) {
      s.markThreadReady()
  }
  ```
  语义：spec.threadID 不是合法 claude UUID → 视同 fresh start，立即 markThreadReady，不阻塞等 system:init。
- `:412 restartResumeIDLocked`：`requiresResolvedThreadID(resumeID)` → `!identifier.IsClaudeCLISessionUUID(resumeID)`

`internal/provider/claudecli/session_log_watcher_integration.go`：
- `:85` `requiresResolvedThreadID(identity.sessionID)` → `!identifier.IsClaudeCLISessionUUID(identity.sessionID)`
- `:273` `shouldMarkThreadReady(resumeID, s.publicThreadID)` → `identifier.IsClaudeCLISessionUUID(resumeID)`

### 测试

`internal/provider/claudecli/thread_identity_test.go` 同步迁移。新增用例：
- `spec.threadID = "agent_xxx"` + `publicThreadID = "agent_xxx"` → markThreadReady 闸条件满足，立即 ready
- `spec.threadID = ""` → 立即 ready（fresh start）
- `spec.threadID = <真 v4 UUID>` → 不立即 ready（等 system:init 走正常 resume）
- `restartResumeIDLocked` 在 placeholder 输入下返回 ""

### 验证

- `go test ./internal/provider/claudecli/...`
- 集成手动验证：制造脏 binding（`provider_thread_id="agent_xxx"`）→ 启动 → 发 hi → driver 立即 fresh start，不再等 5s 超时

### Commit

```
fix(claudecli): consolidate threadID predicates and align resume decision
```

---

## Phase 2：L3 resolver 删 fallback + 入口校验

**目标：** 切断 resolver 把 `binding.CodexThreadID`（routing key）当 `req.ThreadID` 兜底喂给 driver 的脏路径；autoResume 入口拒绝 binding 不可恢复的请求。

### 改动

`internal/provider/unified/session_resolver.go`：
- 删除 `:180-182`：
  ```go
  if threadID == "" {
      threadID = strings.TrimSpace(binding.CodexThreadID)   // 删除
  }
  ```
- `:157 autoResumeSession` 入口加：
  ```go
  if !identifier.LooksLikeUUID(strings.TrimSpace(binding.ProviderThreadID)) {
      return nil, contract.ErrSessionNotFound
  }
  ```
  位置：在 `provider := strings.TrimSpace(binding.Provider)` 校验之后、`driver, err := r.registry.Resolve(provider)` 之前。

### 测试

`internal/provider/unified/session_resolver_test.go` 加用例：
- `binding.ProviderThreadID = ""` → ErrSessionNotFound
- `binding.ProviderThreadID = "agent_xxx"` → ErrSessionNotFound
- `binding.ProviderThreadID = <真 v4 UUID>` → 正常进入 driver.ResumeSession
- `binding.ProviderThreadID = <宽松 hex 32+>` → 正常通过（codex 兼容）

### 验证

- `go test ./internal/provider/unified/...`
- codex 路径手动验证：正常 codex 启动后 binding.ProviderThreadID 正常写入；删 fallback 不破坏 codex 恢复

### Commit

```
fix(unified): reject placeholder bindings and remove codex_thread_id fallback at resolver entry
```

---

## Phase 3：L2 binding 写入校验 + 双 provider 失败自清

**目标：** 写入侧拦截非 UUID 的 ProviderThreadID 落库；resume 失败侧主动清空脏字段，避免下次重启再撞。

### 写入校验

`internal/module/thread/binding_registration.go`：
- `:332 validateBindingProviderThread` 在已有逻辑前加：
  ```go
  if id := strings.TrimSpace(registration.ProviderThreadID); id != "" && !identifier.LooksLikeUUID(id) {
      return fmt.Errorf("binding rejects non-UUID provider_thread_id %q for agent %q", id, registration.AgentID)
  }
  ```
- `:38 normalizeThreadState` 与 `:65 normalizeBindingRegistration` 同步加同样校验（早期拒绝，避免脏值流到 Upsert）
- 不动 `PublicThreadID` / `CodexThreadID`（这两字段就是设计为 routing key，允许 `agent_xxx`）

### 新增 contract `SessionRecoveryReporter`

新建 `internal/contract/session_recovery.go`：
```go
package contract

import "context"

// SessionRecoveryReporter lets a provider session signal the thread service
// that the persisted binding has gone stale and should be reset.
//
// Today the only known stale state is provider_thread_id holding a non-UUID
// placeholder that prevents successful auto-resume; clearing it lets the
// next launch take the fresh-start path until the real session UUID
// arrives via the agent-launched event.
type SessionRecoveryReporter interface {
    ClearStaleProviderThreadID(ctx context.Context, agentID string) error
}
```

### 新增 binding store API

`sql/queries/agent_provider_binding.sql` 新增：
```sql
-- name: UpdateAgentProviderBindingProviderThreadID :exec
UPDATE agent_provider_binding
SET provider_thread_id = $1,
    updated_at = $2
WHERE agent_id = $3;
```

跑 `make sqlc` / `sqlc generate` 重新生成 `internal/store/sqlc`。

`internal/store/binding/contract.go` Store 接口加：
```go
UpdateProviderThreadID(ctx context.Context, params UpdateProviderThreadIDParams) error
```

新增 `UpdateProviderThreadIDParams`：
```go
type UpdateProviderThreadIDParams struct {
    AgentID          string
    ProviderThreadID string
    UpdatedAt        int64
}
```

`internal/store/binding/store.go` 实现：
```go
func (s *store) UpdateProviderThreadID(ctx context.Context, params UpdateProviderThreadIDParams) error {
    return wrapBindingError(s.q.UpdateAgentProviderBindingProviderThreadID(ctx, sqlc.UpdateAgentProviderBindingProviderThreadIDParams{
        ProviderThreadID: params.ProviderThreadID,
        UpdatedAt:        params.UpdatedAt,
        AgentID:          params.AgentID,
    }), "update provider thread id")
}
```

### 实现 SessionRecoveryReporter

新建 `internal/module/thread/binding_recovery.go`：
```go
package thread

type bindingRecoveryReporter struct {
    store  bindingstore.Store
    logger *slog.Logger
}

func NewBindingRecoveryReporter(store bindingstore.Store, logger *slog.Logger) contract.SessionRecoveryReporter {
    return &bindingRecoveryReporter{store: store, logger: logger}
}

func (r *bindingRecoveryReporter) ClearStaleProviderThreadID(ctx context.Context, agentID string) error {
    agentID = strings.TrimSpace(agentID)
    if agentID == "" {
        return nil
    }
    binding, err := r.store.GetByAgentID(ctx, agentID)
    if err != nil || binding == nil {
        return err
    }
    current := strings.TrimSpace(binding.ProviderThreadID)
    if current == "" || identifier.LooksLikeUUID(current) {
        return nil // 干净，不动
    }
    if err := r.store.UpdateProviderThreadID(ctx, bindingstore.UpdateProviderThreadIDParams{
        AgentID:          agentID,
        ProviderThreadID: "",
        UpdatedAt:        time.Now().Unix(),
    }); err != nil {
        return err
    }
    r.logger.Info("thread: cleared stale provider_thread_id", "agent_id", agentID, "old", current)
    return nil
}
```

`internal/module/thread/module.go` fx 暴露：
```go
fx.Provide(NewBindingRecoveryReporter),
```

### claude driver 失败自清

`internal/provider/claudecli/driver.go`：
- driver struct 加 `recovery contract.SessionRecoveryReporter`
- `newDriver` 接受 recovery 参数
- `module.go` fx Inputs 加 `Recovery contract.SessionRecoveryReporter`
- `awaitStartedSession`（`:296-302`）失败兜底：
  ```go
  if err := s.awaitResolvedThreadID(ctx); err != nil {
      shared.LogIgnoredError(d.logger, "stop failed on start error", s.stop(true))
      if d.recovery != nil {
          // 注意：原 ctx 此时大概率已被 timeout cancel（5s InitialThreadIDTimeout 或上游 LaunchTimeout）。
          // 必须用独立 short-lived ctx，否则 store.GetByAgentID 会立即 ctx.Err()，自清静默失败。
          cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
          if cleanErr := d.recovery.ClearStaleProviderThreadID(cleanCtx, s.agentID); cleanErr != nil {
              d.logger.Warn("claudecli: clear stale binding failed", "agent_id", s.agentID, "error", cleanErr)
          }
          cancel()
      }
      return err
  }
  ```

### codex driver 失败自清（对称）

`internal/provider/codexapp/driver.go`：
- driver struct 加 `recovery contract.SessionRecoveryReporter`
- `newDriver` 接受 recovery 参数
- `module.go` fx Inputs 加 `Recovery contract.SessionRecoveryReporter`
- `ResumeSession`（`:222-224`）失败兜底：
  ```go
  threadID, err := resumeRemoteThread(ctx, s.transport, req)
  if err != nil {
      cleanupFailedSession(s, "force stop failed on resume error")
      if d.recovery != nil {
          // 同 claude 端：resume 失败时 ctx 大概率已 cancel（RPC timeout / 上游 cancel）。
          // 必须用独立 short-lived ctx，否则自清静默失败。
          cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
          if cleanErr := d.recovery.ClearStaleProviderThreadID(cleanCtx, req.AgentID); cleanErr != nil {
              d.logger.Warn("codexapp: clear stale binding failed", "agent_id", req.AgentID, "error", cleanErr)
          }
          cancel()
      }
      return nil, err
  }
  ```

### fx wiring 改动清单

本 Phase 涉及三个 fx 模块文件，实施时按下表逐项检查，避免漏改导致注入失败：

| 文件 | 改动 |
|---|---|
| `internal/module/thread/module.go` | 加 `fx.Provide(NewBindingRecoveryReporter)`；该构造函数返回 `contract.SessionRecoveryReporter` |
| `internal/provider/claudecli/module.go` | fx Inputs struct 加 `Recovery contract.SessionRecoveryReporter`；`fx.Invoke` 链里把 Recovery 传给 `newDriver` |
| `internal/provider/codexapp/module.go` | 同 claudecli：Inputs struct 加 `Recovery`；`newDriver` 调用传入 |

### 测试 mock 影响

`newDriver(...)` 签名增 Recovery 参数后，所有调用方需要同步更新（传 nil 或 mock）：

- `internal/provider/claudecli/driver_capability_test.go`
- `internal/provider/claudecli/driver_workspace_skills_test.go`
- `internal/provider/codexapp/driver_pool_routing_test.go`
- `internal/provider/codexapp/factory_tool_result_test.go`（如有 newDriver 直调）
- 其他位置：实施前用 `grep -rn "newDriver(" --include="*.go" ./internal/provider/` 全量列出

`contract.Session` 的 stub 实现（`historyStubSession` / `stubSession` / `mockSession` 等 11+ 处）**不受影响**——它们实现的是 Session 接口，与 driver 构造无关。

`contract.SessionRecoveryReporter` 的 mock：在 `binding_recovery_test.go` 内自定义；driver 测试里大多数路径传 nil 即可（已有 nil 守卫），需要验证调用的少数测试用 mock 实现 `ClearStaleProviderThreadID`。

### 测试

- `internal/module/thread/binding_registration_test.go`：写入非 UUID ProviderThreadID 被拒绝
- `internal/module/thread/binding_recovery_test.go`（新）：脏值清空 / 干净不动 / 不存在的 agent 跳过
- `internal/provider/claudecli/driver_test.go`：失败路径调用 ClearStaleProviderThreadID
- `internal/provider/codexapp/driver_test.go`：resume 失败路径调用 ClearStaleProviderThreadID

### 验证

- `go test ./internal/store/binding/...`
- `go test ./internal/module/thread/...`
- `go test ./internal/provider/...`

### Commit

```
fix(thread,providers): validate provider_thread_id at write and self-heal on resume failure
```

---

## Phase 4：L2 启动清扫（一次性扫库）

**目标：** 清掉 DB 里现存的 `provider_thread_id != "" AND !UUID` 脏数据，避免重启后被同一根钉子绊倒。

### 改动

新建 `internal/module/thread/binding_migration.go`：
```go
package thread

func RunBindingCleanup(lc fx.Lifecycle, store bindingstore.Store, logger *slog.Logger) {
    lc.Append(fx.Hook{
        OnStart: func(ctx context.Context) error {
            ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
            defer cancel()
            // 启动清扫只是数据卫生，不是启动必备条件。
            // 失败时只记 warn 不 return err，避免阻塞 app 启动；
            // 运行时仍有 Phase 3 自清 + Phase 5 候选过滤兜底。
            if err := cleanupStaleProviderThreadIDs(ctx, store, logger); err != nil {
                logger.Warn("thread: binding cleanup failed (continuing)", "error", err)
            }
            return nil
        },
    })
}

func cleanupStaleProviderThreadIDs(ctx context.Context, store bindingstore.Store, logger *slog.Logger) error {
    bindings, err := store.ListAgentThreadBindings(ctx)
    if err != nil {
        return fmt.Errorf("list bindings for cleanup: %w", err)
    }
    cleaned := 0
    for _, b := range bindings {
        id := strings.TrimSpace(b.ProviderThreadID)
        if id == "" || identifier.LooksLikeUUID(id) {
            continue
        }
        if err := store.UpdateProviderThreadID(ctx, bindingstore.UpdateProviderThreadIDParams{
            AgentID:          b.AgentID,
            ProviderThreadID: "",
            UpdatedAt:        time.Now().Unix(),
        }); err != nil {
            logger.Warn("thread: cleanup binding failed", "agent_id", b.AgentID, "error", err)
            continue
        }
        cleaned++
    }
    logger.Info("thread: binding cleanup done", "cleaned", cleaned, "total", len(bindings))
    return nil
}
```

`internal/module/thread/module.go`：
```go
fx.Invoke(RunBindingCleanup),
```

### 测试

`internal/module/thread/binding_migration_test.go`（新）：
- 准备 mix bindings：干净 / placeholder / 空 / 真 UUID
- 跑清扫
- 断言：placeholder 被清成 ""，其他不动

### 验证

- `go test ./internal/module/thread/...`
- 启动观察日志：`thread: binding cleanup done cleaned=N total=M`

### Commit

```
feat(thread): startup cleanup for stale provider_thread_id bindings
```

---

## Phase 5：Background resume 候选过滤

**目标：** 不可 resume 的 binding 不进 `resumeInFlight` stamp，下次重启或修复后能再次尝试。

### 改动

`internal/module/thread/service.go`：
- `:434 backgroundResumeCandidate` 在 `resumeLifecycleBlockReason` 检查之前加：
  ```go
  if !identifier.LooksLikeUUID(strings.TrimSpace(binding.ProviderThreadID)) {
      return "", false
  }
  ```

### 测试

`internal/module/thread/service_test.go` 加用例：
- binding.ProviderThreadID = `""` → candidate false，不进 stamp
- binding.ProviderThreadID = `"agent_xxx"` → candidate false
- binding.ProviderThreadID = `<真 UUID>` → candidate true

### 验证

- `go test ./internal/module/thread/...`
- 启动观察日志：脏 binding 不再触发 `thread: background resume`

### Commit

```
fix(thread): skip background resume for bindings without valid provider UUID
```

---

## Phase 6：Event 路由按 provider 隔离（长期方案）

**目标：** RawProviderEvent 携带 Provider 字段；EventDispatcher 按 Provider 路由 translator，杜绝 codexapp translator 处理 claude raw event 之类的跨 provider 串。

### 改动

`internal/dto/provider/event.go`：
```go
type RawProviderEvent struct {
    Provider  string  // 新增
    EventType string
    Data      any
}
```

`internal/provider/unified/event_map.go`：
- `EventDispatcher` 内部 translators 改为：
  ```go
  type EventDispatcher struct {
      mu               sync.RWMutex
      providerTrans    map[string][]EventTranslator
      commonTrans      []EventTranslator
      bus              *event.Dispatcher
      logger           *slog.Logger
  }
  ```
- `NewEventDispatcher` 把 `translateCommonRawEvent` 加到 `commonTrans`
- `Register(provider string, t EventTranslator)`：空 provider 进 commonTrans，否则 providerTrans[provider]
- `Dispatch(raw)` 改为：
  ```go
  func (d *EventDispatcher) Dispatch(raw dto.RawProviderEvent) {
      // 既有 bus 重发逻辑保留
      d.mu.RLock()
      common := append([]EventTranslator(nil), d.commonTrans...)
      providerOnly := append([]EventTranslator(nil), d.providerTrans[raw.Provider]...)
      d.mu.RUnlock()

      for _, t := range common {
          t(raw, ...)
      }
      for _, t := range providerOnly {
          t(raw, ...)
      }
  }
  ```

### 派发 helper 填 Provider

`internal/provider/claudecli/session_events.go`：
- `:60 dispatch` helper 内：
  ```go
  func (s *session) dispatch(raw dto.RawProviderEvent) {
      if s.eventDispatcher != nil {
          raw.Provider = "claude"
          s.eventDispatcher.Dispatch(raw)
      }
  }
  ```

`internal/provider/codexapp/session_dispatch.go`：
- `:32` 调 Dispatch 前：
  ```go
  raw.Provider = "codex"
  s.dispatcher.Dispatch(raw)
  ```

`internal/provider/codexapp/recovery.go:167`、`session_approval.go:323`、`factory.go:280` 等直接构造 `RawProviderEvent` 的位置 **不用改**——它们都流经 `s.dispatch()` helper。

### Translator 注册改两参

`internal/provider/claudecli/event_map.go:22`：
```go
dispatcher.Register("claude", translateClaudeEvent)
```

`internal/provider/codexapp/event_map.go:22`：
```go
dispatcher.Register("codex", translateCodexEvent)
```

### 顺手清理

`internal/provider/codexapp/event_map.go:135` `case "thread/started", "session.configured", "agent:launched":` 删除 `"agent:launched"` alias：codex 永远不发 `agent:` 前缀事件。删掉避免误以为它支持。

**预检（实施前必须跑一次确认）：**
```bash
grep -rn '"agent:launched"' internal/provider/codexapp/
```
预期只命中 `event_map.go:135` 这条 case 字面。如果还有其他位置（例如 codexapp 自己 `s.dispatch(... "agent:launched" ...)`），则 case 不能删，需要改成「保留 case 但加注释说明 codex 在某些场景也派发」。

### 测试

`internal/provider/unified/event_dispatcher_test.go`（已存在则补，不存在则新建）：
- 注册 claude translator + codex translator
- Dispatch claude raw event → 仅 claude translator 收到
- Dispatch codex raw event → 仅 codex translator 收到
- common translator 始终收到
- 空 Provider 的 raw event → 仅 common translator 收到（兼容）

### 验证

- `go test ./internal/provider/...`
- 集成验证：claude session 失败 → 日志不再出现 `codexapp: unknown raw event raw_type=agent:failed`

### Commit

```
refactor(unified,providers): route raw provider events by provider namespace
```

---

## Phase 7：测试 & 验收

### 集成测试用例（新增到 `internal/integration/` 或对应模块的集成测试位置）

1. **claude 脏 binding 自愈**：
   - 手动写入 `provider="claude", provider_thread_id="agent_xxx"` 的 binding
   - 启动后端
   - 启动清扫日志：`cleaned >= 1`
   - 发 hi → 走 fresh start，不再 5s 死锁

2. **codex 脏 binding 自愈**：
   - 同上，provider 改为 codex
   - 启动清扫清空字段
   - 发 hi → 走 fresh start，不再撞脏 binding
   （driver 失败自清路径由 Phase 3 单元测试覆盖；集成层不再重复构造"清扫禁用"场景，避免绕过 fx OnStart hook 的复杂 setup）

3. **正常 claude 启动**：
   - fresh start → system:init 来 → binding ProviderThreadID 写入真 UUID
   - 重启 → resolver 入口校验通过 → resume 成功
   - 测试无回归

4. **正常 codex 启动**：
   - fresh start → thread/started 来 → binding ProviderThreadID 写入真 UUID
   - 重启 → resume 成功

5. **Event 路由**：
   - claude session 派发 `agent:failed` → 仅 claude translator 处理
   - 日志无 `codexapp: unknown raw event raw_type=agent:failed`

### 全量回归

- `go test ./...`
- `scripts/test_with_guard.sh`（项目惯例）
- `go vet ./...`
- 必要时 `golangci-lint run`

### 手动复现：用户原 case

1. 用 master 分支跑后端
2. 给 claude provider thread 发 hi → 复现 28s "session stopped"
3. 切到本分支跑后端
4. 启动后看日志确认清扫
5. 同一 thread 发 hi → 立即 fresh start 或正常 resume，不再死锁

### Commit

```
test: integration coverage for binding self-heal and event routing
```

---

## 范围边界：本次后仍未恢复的用户体验（Phase 8 后续）

**本次根治目标：** 不再 5s/30s 死锁、不再脏数据反复绊倒。**已达成。**

**未覆盖：** 自清把 `binding.ProviderThreadID` 设为 `""` 后，用户**再次**发 hi 仍会经历：

1. `waitForReadyTurnSession` → `ResolveSession`
2. resolver 入口校验 `LooksLikeUUID("") = false` → `ErrSessionNotFound`
3. `waitForReadyTurnSession` 30s 超时 → 返回 `ErrInvalidState("thread session is not available; start or resume the thread first")`

用户从「28s 后看到 session stopped」改善为「30s 后看到 thread session is not available」。**仍是 30s 等待 + 显式错误**，不是无感知 fresh start。

**Phase 8 待定方案（不在本次范围）：**

- 选项 A：turn flow 在 `ErrSessionNotFound` 时自动调 `StartSession`（半天～一天，触面广，需考虑 race）
- 选项 B：前端拿到 `ErrInvalidState` 后自动调 `thread/start`（前端改动，更轻）
- 选项 C：维持当前——用户看到错误信息后主动重发 / 切换 thread

实施时机：本次 8 个 Phase 上线后，根据用户反馈决定走哪条；此前不预先实现。

---

## 提交策略（按"小步提交"）

| # | Phase | Commit 主语 |
|---|---|---|
| 1 | 0 | `refactor(identifier): extract UUID predicates to internal/util/identifier` |
| 2 | 1 | `fix(claudecli): consolidate threadID predicates and align resume decision` |
| 3 | 2 | `fix(unified): reject placeholder bindings and remove codex_thread_id fallback at resolver entry` |
| 4 | 3 | `fix(thread,providers): validate provider_thread_id at write and self-heal on resume failure` |
| 5 | 4 | `feat(thread): startup cleanup for stale provider_thread_id bindings` |
| 6 | 5 | `fix(thread): skip background resume for bindings without valid provider UUID` |
| 7 | 6 | `refactor(unified,providers): route raw provider events by provider namespace` |
| 8 | 7 | `test: integration coverage for binding self-heal and event routing` |

每个 commit 独立可上线。1-3 是修主线 5s 死锁的最小集；4-5 是数据卫生；6-7 是相邻设计 bug 清理；8 是验收。

## 验收标准

- 重启后给 claude provider 发 hi 不再出现 "session stopped" 5s 死锁
- 启动清扫日志 `thread: binding cleanup done cleaned=N total=M` 出现在 OnStart 阶段
- claude session 失败时日志包含 `thread: cleared stale provider_thread_id`（自愈）
- codex session resume 失败时同样自愈
- 日志不再出现 `codexapp: unknown raw event raw_type=agent:failed`
- 全量 `go test ./...` 通过
- 现有 thread / unified / claudecli / codexapp 测试不出现回归

## 风险与回滚

| 风险 | 概率 | 缓解 |
|---|---|---|
| `LooksLikeUUID` 宽松判断仍误判某些 codex 真 thread id | 低 | Phase 0 单独 commit，发现误判就单独回滚 helper 行为 |
| sqlc 生成代码冲突 | 低 | 跑 `make sqlc` 提前重新生成 |
| Event 路由改动破坏现有 translator | 中 | Phase 6 独立 commit；测试覆盖共享 / per-provider 两路；可 revert |
| 启动清扫误清正常 binding | 极低 | 清扫前后日志可见，且只清非 UUID 字段，UUID 不动 |
| fx 注入循环 | 低 | `SessionRecoveryReporter` 由 thread 模块实现，provider 模块消费，方向单一 |
| 自清后用户再发 hi 仍经历 30s `ErrInvalidState` | 中 | 已在「范围边界（Phase 8 后续）」章节明列；按用户反馈推进 A/B/C 任选 |

每个 Phase 独立 commit，单点回滚不影响其他 Phase 修复效果。

## 时间估计

| Phase | 估时 |
|---|---|
| 0 | 1-2h |
| 1 | 2-3h |
| 2 | 1h |
| 3 | 半天 |
| 4 | 2h |
| 5 | 1h |
| 6 | 半天 |
| 7 | 半天 |

合计约 1.5-2 天。
