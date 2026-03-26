# A4-γ Timeline 投影 + D1 离线 Model 补全 实现计划

> **给 Claude:** 必须使用 @执行计划 逐任务实现此计划。
>
> **修订版 (2026-03-27):** 根据 2 Codex + 1 Claude 审查反馈修复 8 个 blocking 项 (B1-B8) + 4 个 non-blocking 改进 (N1-N4)。

**目标:** 实现 UI timeline 投影层（将碎片化 provider 事件聚合为可渲染的 timeline items）+ 补全离线配置中缺失的 model 字段

**架构:** Timeline 投影拆为独立子包 `internal/module/uistate/timeline/`，暴露 `timeline.Service` 接口 + `timeline.RegisterSubscriptions` 注册函数。子包内部使用 bounded timeline buffer 累积 items，通过 `UITimelineAppended` 事件通知前端。uistate 主包持有 `timeline.Service` 字段，在 projector 注册时合并 cancel funcs。D1 仅在 `buildOfflineRuntimeConfig` 中从 thread 记录取 model。

**技术栈:** Go / kelindar/event bus / platformbus.ResilientSubscribe / internal DTO / uistate projector

---

## 前置知识

### 仓库守卫
```
单文件 ≤400 行 | 单函数 ≤80 行 | CC ≤10 | 包非测试文件 ≤15 | 包总行数 ≤4500
验证: go test -run TestCodeSizeGuard ./internal/archtest/...
```

### uistate 当前非测试文件清单（15 个，已达上限）

| # | 文件 |
|---|------|
| 1 | `config_rpc.go` |
| 2 | `contract.go` |
| 3 | `diff_state.go` |
| 4 | `known_diff.go` |
| 5 | `module.go` |
| 6 | `patch.go` |
| 7 | `preferences.go` |
| 8 | `projector.go` |
| 9 | `projector_handlers.go` |
| 10 | `projects.go` |
| 11 | `rpc.go` |
| 12 | `service.go` |
| 13 | `sidebar_compat.go` |
| 14 | `snapshot_helpers.go` |
| 15 | `state.go` |

> **⚠️ 已达 15 文件上限。** 新增 timeline 文件必须拆到子包 `internal/module/uistate/timeline/`，否则 archtest 失败。

### 关键文件速查

| 文件 | 作用 |
|------|------|
| `internal/module/uistate/state.go` | UIState 结构体定义 |
| `internal/module/uistate/service.go` | service 结构体 + 初始化 |
| `internal/module/uistate/projector.go` | 事件订阅注册（返回 `[]context.CancelFunc`） |
| `internal/module/uistate/projector_handlers.go` | turn/agent/workspace handler |
| `internal/module/uistate/patch.go` | patch 发射 + emitter 初始化（`bindDispatcher`） |
| `internal/module/uistate/module.go` | fx wire + dispatcher binding |
| `internal/module/uistate/snapshot_helpers.go` | threadActivity 结构体 + helper |
| `internal/module/uistate/contract.go` | Service 接口 |
| `internal/dto/ui/event.go:11-18` | UITimelineAppended DTO（已存在） |
| `internal/dto/shared/event.go:53` | EventTypeUITimelineAppended = 1501（已存在） |
| `internal/dto/turn/event.go` | TurnStarted/Completed/Interrupted 等 |
| `internal/dto/turn/progress.go` | ItemStarted/ItemCompleted |
| `internal/dto/tool/event.go` | ToolCallBegin/End, ToolApprovalRequested/Resolved |
| `internal/platform/bus/sink.go:98-102` | UITimelineAppended 已接入 bus |
| `internal/platform/bus/resilient.go` | ResilientSubscribe 泛型函数 |
| `internal/provider/unified/event_map.go:60` | UITimelineAppended 已注册 event_map |
| `internal/module/thread/lifecycle_helpers.go` | buildOfflineConfig / buildOfflineRuntimeConfig / offlineThreadModel |
| `internal/module/thread/history.go` | ReadRuntimeConfig / mergeRuntimeConfig |
| `internal/store/thread/contract.go:50-67` | Thread 记录（含 Model 字段） |

### DTO 嵌套层级（经 LSP 验证）

```
EventHeader { Timestamp }
  └─ ThreadHeader { EventHeader; ThreadID }
       └─ AgentHeader { ThreadHeader; AgentID }
            └─ TurnHeader { AgentHeader; TurnIDHeader{TurnID} }
                 └─ ToolCallHeader { TurnHeader; CallID; ToolName }
                      └─ ToolApprovalHeader { ToolCallHeader; ApprovalID }
```

> **⚠️ 构造复合字面量时必须逐级嵌套**，不可跳级。正确写法：
> ```go
> shared.TurnHeader{
>     AgentHeader: shared.AgentHeader{
>         ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
>         AgentID:      "agent-1",
>     },
>     TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
> }
> ```

### 现有 projector 模式（必须遵循）

```go
// 1. 在 projector.go 的 registerProjectionSubscriptions 中注册订阅
//    ⚠️ 使用 platformbus.ResilientSubscribe（不是 event.On）
func registerProjectionSubscriptions(dispatcher *event.Dispatcher, svc *service) []context.CancelFunc {
    return []context.CancelFunc{
        platformbus.ResilientSubscribe(dispatcher, svc.applyAgentStateChanged, svc.logger),
        // ... 共 26 个订阅
    }
}

// 2. handler 模式：
func (s *service) applyXxxEvent(ev SomeEvent) {
    s.mu.Lock()
    // ... 修改 state ...
    patch := s.refreshThreadPatchLocked(threadID, agentID, "source")
    s.mu.Unlock()
    s.emitThreadPatchEvent(patch)
}

// 3. emitter 初始化（patch.go bindDispatcher）：
//    bus.NewEmitter[uidto.UIProjectionUpdated](dispatcher)
```

### ResilientSubscribe 签名（`internal/platform/bus/resilient.go`）

```go
func ResilientSubscribe[T event.Event](
    dispatcher *event.Dispatcher,
    fn func(T),
    logger *slog.Logger,
) context.CancelFunc
```

封装 `event.Subscribe` + panic recovery + 错误日志。

---

## Part A: D1 离线 Model 补全（1 个任务）

### 任务 1: buildOfflineRuntimeConfig 补全 model 字段

**文件:**
- 修改: `internal/module/thread/lifecycle_helpers.go`（buildOfflineRuntimeConfig 函数，行 129-149）
- 修改: `internal/module/thread/lifecycle_helpers.go`（buildOfflineConfig 调用点，约行 107）
- 测试: `internal/module/thread/config_offline_test.go`

**背景:** `buildOfflineRuntimeConfig` 当前只输出 `approvalPolicy`、`personality`、`toolRouting`。Thread 记录的 `Model` 字段已持久化但未被传入 runtime map。`buildOfflineConfig` 已在行 102 使用 `firstNonEmpty(stored.Model, offlineThreadModel(thread))` 设置 `Config.Model`，但 Runtime map 中无对应字段。

**步骤 1: 写失败的测试**

在 `config_offline_test.go` 末尾新增。

> **⚠️ 不使用 `mustNewThreadService`（不存在）。** 使用现有 stub 模式：直接调用 `NewService(...)` + stub store（定义在 `resume_test.go`）。

```go
func TestBuildOfflineRuntimeConfigIncludesModel(t *testing.T) {
	threads := &stubThreadStore{
		thread: &threadstore.Thread{
			ThreadID: "thread-model-offline",
			Model:    "claude-sonnet-4-20250514",
			Status:   "running",
		},
	}
	svc := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{}},
		&stubSessionProvider{session: nil}, // 无 session → 走 offline 路径
		nil, // starter
		nil, // turns
		nil, // orchestration
		nil, // threadEvents
	)

	ctx := context.Background()
	runtime, err := svc.ReadRuntimeConfig(ctx, "thread-model-offline")
	require.NoError(t, err)

	model, ok := runtime["model"]
	require.True(t, ok, "offline runtime should contain model field")
	assert.Equal(t, "claude-sonnet-4-20250514", model)
}
```

**步骤 2: 运行测试确认失败**

运行: `go test ./internal/module/thread/... -run TestBuildOfflineRuntimeConfigIncludesModel -v`
预期: FAIL — `runtime["model"]` 不存在

**步骤 3: 写最小实现**

修改 `lifecycle_helpers.go`：

1. 修改 `buildOfflineRuntimeConfig` 签名，增加 `thread` 参数：

```go
// 旧签名（行 129）
func buildOfflineRuntimeConfig(stored storedThreadConfig) map[string]any

// 新签名
func buildOfflineRuntimeConfig(stored storedThreadConfig, thread *threadstore.Thread) map[string]any
```

2. 在函数体内，`toolRouting` 构建之后添加 model（与 `buildOfflineConfig` 的 `Config.Model` 保持相同优先级）：

```go
	// model — 复用 offlineThreadModel (行 162) + firstNonEmpty (行 45)
	if m := firstNonEmpty(stored.Model, offlineThreadModel(thread)); m != "" {
		rt["model"] = m
	}
```

3. 修改 `buildOfflineConfig` 中的调用点（约第 107 行）：

```go
// 旧
Runtime: buildOfflineRuntimeConfig(stored),
// 新
Runtime: buildOfflineRuntimeConfig(stored, thread),
```

> **⚠️ 删除计划中的 `threadModel(thread, binding)` helper。** 直接复用已有的 `offlineThreadModel(thread *threadstore.Thread) string`（行 162-167），配合 `firstNonEmpty` 保持与 `Config.Model`（行 102 `firstNonEmpty(stored.Model, offlineThreadModel(thread))`）相同的优先级语义。

**步骤 4: 运行测试确认通过**

运行: `go test ./internal/module/thread/... -run TestBuildOfflineRuntimeConfigIncludesModel -v`
预期: PASS

**步骤 5: 全量回归**

运行: `go test ./internal/module/thread/... -v`
预期: 全 PASS（已有的 4 个 offline 测试不应受影响）

**步骤 6: 守卫验证**

运行: `go test -run TestCodeSizeGuard ./internal/archtest/... -v`
预期: PASS

**步骤 7: 死代码清理**

1. 用 `lsp_xref(references)` 确认旧 `buildOfflineRuntimeConfig(stored)` 单参数调用点已全部更新为双参数
2. 确认无未使用的 import

**步骤 8: 提交**

```bash
git add internal/module/thread/lifecycle_helpers.go internal/module/thread/config_offline_test.go
git commit -m "fix(thread): include model field in offline runtime config (D1 收口)"
```

---

## Part B: A4-γ Timeline 投影（4 个任务）

### 设计概览

```
Provider 事件流
  ├─ TurnStarted        → Item{kind:"turn_start"}
  ├─ ItemStarted        → Item{kind:"item", item_type:classify(item)}
  ├─ ItemCompleted      → 更新对应 item 的 completed 状态 + emit
  ├─ ToolCallBegin      → Item{kind:"tool_call"}
  ├─ ToolCallEnd        → 更新对应 tool_call 的结果 + emit
  ├─ ToolApprovalReq    → Item{kind:"approval_request"}
  ├─ ToolApprovalRes    → 更新对应 approval 的决定 + emit
  ├─ TurnCompleted      → Item{kind:"turn_end"}
  └─ TurnInterrupted    → Item{kind:"turn_interrupted"}
        ↓
  UITimelineAppended 事件 → 前端渲染
```

> **⚠️ TurnOutputDelta 不在本轮追踪。** 见文档末尾“已知限制/defer”章节。

**子包拆分（B1 — 包文件数守卫）：**

因 uistate 已达 15 个非测试文件上限，timeline 功能拆为独立子包：

| 新文件 | 包 | 职责 | 估算行数 |
|--------|-------|------|---------|
| `timeline/timeline.go` | `timeline` | Item 定义 + Service 接口 + bounded timeline buffer + 去重逻辑 | ~170 |
| `timeline/projector.go` | `timeline` | RegisterSubscriptions + 9 个事件 handler 闭包 | ~200 |
| `timeline/timeline_test.go` | `timeline_test` | 单元测试 + 集成测试 | ~350 |

子包对外暴露：
- `timeline.Service` 接口 — `Append`, `UpdateByCallID`, `GetByThread`, `Snapshot`, `SetEmitter`
- `timeline.Item` 结构体（公开）
- `timeline.AppendedEmitter` 类型（`func(uidto.UITimelineAppended)`）
- `timeline.New(logger, emitter, capacity) Service` 构造函数
- `timeline.RegisterSubscriptions(dispatcher, svc, logger, onUpdated) []context.CancelFunc`

uistate 主包集成：
- `service` struct 增加 `timeline timeline.Service` 字段
- `registerProjectionSubscriptions` 合并 `timeline.RegisterSubscriptions` 返回的 cancel funcs
- `GetState` 通过 `svc.timeline.Snapshot()` 填充 `UIState.TimelineByThread`
- `patch.go` 的 `bindDispatcher` 中用 `bus.NewEmitter[uidto.UITimelineAppended](dispatcher)` 初始化 emitter 并通过 `svc.timeline.SetEmitter(...)` 注入

---

### 任务 2: timeline.Item 数据结构 + bounded timeline buffer

**文件:**
- 创建: `internal/module/uistate/timeline/timeline.go`
- 测试: `internal/module/uistate/timeline/timeline_test.go`

**步骤 1: 写失败的测试**

创建 `internal/module/uistate/timeline/timeline_test.go`：

```go
package timeline_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"
)

func TestAppendAndGetByThread(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	item := timeline.Item{
		ID:     "item-1",
		Kind:   "tool_call",
		CallID: "call-abc",
		Status: "running",
	}
	svc.Append("t1", "agent-1", item)

	items := svc.GetByThread("t1")
	require.Len(t, items, 1)
	assert.Equal(t, "item-1", items[0].ID)
	assert.Equal(t, "call-abc", items[0].CallID)
}

func TestAppendRespectsCapacity(t *testing.T) {
	svc := timeline.New(nil, nil, 3)
	for i := 0; i < 5; i++ {
		svc.Append("t1", "a", timeline.Item{
			ID:   fmt.Sprintf("item-%d", i),
			Kind: "msg",
		})
	}
	items := svc.GetByThread("t1")
	require.Len(t, items, 3)
	// 最早的两个被淘汰
	assert.Equal(t, "item-2", items[0].ID)
}

func TestUpdateByCallID(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	svc.Append("t1", "a", timeline.Item{
		ID: "item-1", Kind: "tool_call", CallID: "call-1", Status: "running",
	})

	updated := svc.UpdateByCallID("t1", "a", "call-1", func(item *timeline.Item) {
		item.Status = "completed"
		b := true
		item.Success = &b
	})
	assert.True(t, updated)

	items := svc.GetByThread("t1")
	assert.Equal(t, "completed", items[0].Status)
}

func TestAppendDedup_SameCallID(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	svc.Append("t1", "a", timeline.Item{
		ID: "item-1", Kind: "tool_call", CallID: "call-1", Status: "running",
	})
	// 相同 CallID 重复追加 → 应跳过
	svc.Append("t1", "a", timeline.Item{
		ID: "item-1-dup", Kind: "tool_call", CallID: "call-1", Status: "running",
	})
	items := svc.GetByThread("t1")
	require.Len(t, items, 1, "duplicate CallID should be skipped")
	assert.Equal(t, "item-1", items[0].ID)
}

func TestAppendDedup_SameTurnKind(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	svc.Append("t1", "a", timeline.Item{ID: "ts-1", Kind: "turn_start", TurnID: "turn-1"})
	svc.Append("t1", "a", timeline.Item{ID: "ts-1-dup", Kind: "turn_start", TurnID: "turn-1"})
	items := svc.GetByThread("t1")
	require.Len(t, items, 1, "duplicate turn_start for same turnID should be skipped")
}

func TestSnapshot(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	svc.Append("t1", "a", timeline.Item{ID: "i1", Kind: "turn_start"})
	svc.Append("t2", "a", timeline.Item{ID: "i2", Kind: "turn_start"})
	snap := svc.Snapshot()
	assert.Len(t, snap, 2)
	assert.Len(t, snap["t1"], 1)
	assert.Len(t, snap["t2"], 1)
}

func TestUpdateByCallID_NonExistentCallID(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	svc.Append("t1", "a", timeline.Item{ID: "i1", Kind: "tool_call", CallID: "c1"})

	updated := svc.UpdateByCallID("t1", "a", "non-existent", func(it *timeline.Item) {
		it.Status = "completed"
	})
	assert.False(t, updated, "should return false for non-existent CallID")

	// 原 item 不受影响
	items := svc.GetByThread("t1")
	assert.Equal(t, "", items[0].Status)
}

func TestAppendRespectsCapacity_IndexConsistency(t *testing.T) {
	svc := timeline.New(nil, nil, 3)
	for i := 0; i < 5; i++ {
		svc.Append("t1", "a", timeline.Item{
			ID:     fmt.Sprintf("item-%d", i),
			Kind:   "tool_call",
			CallID: fmt.Sprintf("call-%d", i),
		})
	}

	items := svc.GetByThread("t1")
	require.Len(t, items, 3)
	// 淀汰后 index 应与 items 一致：call-2, call-3, call-4 可查
	for _, it := range items {
		updated := svc.UpdateByCallID("t1", "a", it.CallID, func(item *timeline.Item) {
			item.Status = "checked"
		})
		assert.True(t, updated, "index should be consistent for %s", it.CallID)
	}
	// 已淀汰的 call-0, call-1 不可查
	assert.False(t, svc.UpdateByCallID("t1", "a", "call-0", func(it *timeline.Item) {}),
		"evicted call-0 should not be findable")
}
```

**步骤 2: 运行测试确认失败**

运行: `go test ./internal/module/uistate/timeline/... -v`
预期: FAIL — 包/类型不存在

**步骤 3: 写最小实现**

创建 `internal/module/uistate/timeline/timeline.go`：

```go
package timeline

import (
	"log/slog"
	"strings"
	"sync"

	shared "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
)

// Item represents a single renderable entry in the thread timeline.
type Item struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`                 // turn_start, item, tool_call, approval_request, turn_end, turn_interrupted
	Status    string `json:"status"`               // running, completed, interrupted, pending, approved, rejected
	CallID    string `json:"call_id,omitempty"`
	RequestID int64  `json:"request_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	ItemType  string `json:"item_type,omitempty"`
	Command   string `json:"command,omitempty"`
	File      string `json:"file,omitempty"`
	Error     string `json:"error,omitempty"`
	Success   *bool  `json:"success,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
}

// AppendedEmitter emits UITimelineAppended events to the bus.
type AppendedEmitter func(uidto.UITimelineAppended)

// Service provides timeline operations for uistate.
type Service interface {
	Append(threadID, agentID string, item Item)
	UpdateByCallID(threadID, agentID, callID string, fn func(*Item)) bool
	GetByThread(threadID string) []Item
	Snapshot() map[string][]Item
	SetEmitter(AppendedEmitter)
}

const defaultCapacity = 200

// New creates a new timeline service backed by a bounded timeline buffer.
func New(logger *slog.Logger, emitter AppendedEmitter, capacity int) Service {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &service{
		timelines: make(map[string]*threadTimeline),
		logger:    logger,
		emitter:   emitter,
		capacity:  capacity,
	}
}

type service struct {
	mu        sync.RWMutex
	timelines map[string]*threadTimeline
	logger    *slog.Logger
	emitter   AppendedEmitter
	capacity  int
}

func (s *service) SetEmitter(emitter AppendedEmitter) {
	s.mu.Lock()
	s.emitter = emitter
	s.mu.Unlock()
}

func (s *service) Append(threadID, agentID string, item Item) {
	s.mu.Lock()
	tl := s.timelineLocked(threadID)
	if tl.isDuplicate(item) {
		s.mu.Unlock()
		return
	}
	tl.append(item)
	s.mu.Unlock()
	s.emitAppended(threadID, item)
}

func (s *service) UpdateByCallID(threadID, agentID, callID string, fn func(*Item)) bool {
	cid := strings.TrimSpace(callID)
	if cid == "" {
		return false
	}
	s.mu.Lock()
	tl := s.timelineLocked(threadID)
	updated := tl.updateByCallID(cid, fn)
	s.mu.Unlock()
	return updated
	// 注意：update 不 emit UITimelineAppended（语义是"新增"）。
	// 由 RegisterSubscriptions 的 handler 通过 onUpdated 回调通知主包
	// emit UIProjectionUpdated{Projection:"timeline"}（语义是"刷新"）。
}

func (s *service) GetByThread(threadID string) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tl, ok := s.timelines[threadID]
	if !ok {
		return nil
	}
	return tl.snapshot()
}

func (s *service) Snapshot() map[string][]Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.timelines) == 0 {
		return nil
	}
	out := make(map[string][]Item, len(s.timelines))
	for k, tl := range s.timelines {
		if tl.len() > 0 {
			out[k] = tl.snapshot()
		}
	}
	return out
}

func (s *service) timelineLocked(threadID string) *threadTimeline {
	tl, ok := s.timelines[threadID]
	if !ok {
		tl = newThreadTimeline(s.capacity)
		s.timelines[threadID] = tl
	}
	return tl
}

func (s *service) emitAppended(threadID string, item Item) {
	if s.emitter == nil {
		return
	}
	s.emitter(uidto.UITimelineAppended{
		UITurnHeader: shared.UITurnHeader{
			UIProjectionHeader: shared.UIProjectionHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: threadID},
				Projection:   "timeline",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: item.TurnID},
		},
		ItemID:    item.ID,
		ItemKind:  item.Kind,
		RequestID: item.RequestID,
		CallID:    item.CallID,
	})
}

// --- bounded timeline buffer (非导出) ---

type threadTimeline struct {
	items    []Item
	cap      int
	index    map[string]int    // callID → slice index
	turnKind map[string]string // "turnID:kind" → exists (去重用)
}

func newThreadTimeline(capacity int) *threadTimeline {
	return &threadTimeline{
		items:    make([]Item, 0, capacity),
		cap:      capacity,
		index:    make(map[string]int),
		turnKind: make(map[string]string),
	}
}

// isDuplicate 检查是否应跳过追加（B7 去重）。
func (tl *threadTimeline) isDuplicate(item Item) bool {
	// 规则 1: CallID 非空且 index 中已存在 → 跳过
	if cid := strings.TrimSpace(item.CallID); cid != "" {
		if _, exists := tl.index[cid]; exists {
			return true
		}
	}
	// 规则 2: turn_start/turn_end/turn_interrupted 且相同 turnID+kind 已存在 → 跳过
	if isTurnBoundaryKind(item.Kind) {
		if tid := strings.TrimSpace(item.TurnID); tid != "" {
			key := tid + ":" + item.Kind
			if _, exists := tl.turnKind[key]; exists {
				return true
			}
		}
	}
	return false
}

func (tl *threadTimeline) append(item Item) {
	if len(tl.items) >= tl.cap {
		tl.evictOldest()
	}
	tl.items = append(tl.items, item)
	idx := len(tl.items) - 1
	if cid := strings.TrimSpace(item.CallID); cid != "" {
		tl.index[cid] = idx
	}
	if isTurnBoundaryKind(item.Kind) {
		if tid := strings.TrimSpace(item.TurnID); tid != "" {
			tl.turnKind[tid+":"+item.Kind] = ""
		}
	}
}

func (tl *threadTimeline) findByCallID(callID string) (Item, bool) {
	idx, ok := tl.index[callID]
	if !ok || idx >= len(tl.items) {
		return Item{}, false
	}
	return tl.items[idx], true
}

func (tl *threadTimeline) updateByCallID(callID string, fn func(*Item)) bool {
	idx, ok := tl.index[callID]
	if !ok || idx >= len(tl.items) {
		return false
	}
	fn(&tl.items[idx])
	return true
}

func (tl *threadTimeline) len() int { return len(tl.items) }

func (tl *threadTimeline) snapshot() []Item {
	out := make([]Item, len(tl.items))
	copy(out, tl.items)
	return out
}

func (tl *threadTimeline) evictOldest() {
	if len(tl.items) == 0 {
		return
	}
	evicted := tl.items[0]
	tl.items = tl.items[1:]
	if cid := strings.TrimSpace(evicted.CallID); cid != "" {
		delete(tl.index, cid)
	}
	if isTurnBoundaryKind(evicted.Kind) {
		if tid := strings.TrimSpace(evicted.TurnID); tid != "" {
			delete(tl.turnKind, tid+":"+evicted.Kind)
		}
	}
	tl.rebuildCallIDIndex()
}

func (tl *threadTimeline) rebuildCallIDIndex() {
	for k := range tl.index {
		delete(tl.index, k)
	}
	for i, item := range tl.items {
		if cid := strings.TrimSpace(item.CallID); cid != "" {
			tl.index[cid] = i
		}
	}
}

func isTurnBoundaryKind(kind string) bool {
	return kind == "turn_start" || kind == "turn_end" || kind == "turn_interrupted"
}
```

> **注意：** state.go / service.go 的修改在任务 5（uistate 主包集成）中完成，此处仅关注 timeline 子包本身。

**步骤 4: 运行测试确认通过**

运行: `go test ./internal/module/uistate/timeline/... -v`
预期: PASS

**步骤 5: 提交**

```bash
git add internal/module/uistate/timeline/
git commit -m "feat(timeline): add Item data structure and bounded timeline buffer with dedup (A4-γ step 1)"
```

---

### 任务 3: Timeline 事件订阅与全部 Handler

**文件:**
- 创建: `internal/module/uistate/timeline/projector.go`（RegisterSubscriptions + 9 个 handler + timelineID）
- 测试: `internal/module/uistate/timeline/timeline_test.go`（追加 Turn/Item/Tool/Approval 测试）

**步骤 1: 写失败的测试**

在 `timeline_test.go` 追加（需 import `shared`, `turndto`, `uidto`, `"github.com/kelindar/event"`）：

```go
func TestRegisterSubscriptions_TurnStarted(t *testing.T) {
	var emitted []uidto.UITimelineAppended
	emitter := func(ev uidto.UITimelineAppended) { emitted = append(emitted, ev) }

	svc := timeline.New(nil, emitter, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() { for _, c := range cancels { c() } }()

	event.Publish(dispatcher, turndto.TurnStarted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
		},
	})

	items := svc.GetByThread("t1")
	require.Len(t, items, 1)
	assert.Equal(t, "turn_start", items[0].Kind)
	assert.Equal(t, "turn-1", items[0].TurnID)
	assert.Equal(t, "running", items[0].Status)
	require.Len(t, emitted, 1)
	assert.Equal(t, "timeline", emitted[0].Projection)
}

func TestRegisterSubscriptions_TurnCompleted(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() { for _, c := range cancels { c() } }()

	event.Publish(dispatcher, turndto.TurnStarted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
		},
	})
	event.Publish(dispatcher, turndto.TurnCompleted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
		},
	})

	items := svc.GetByThread("t1")
	require.Len(t, items, 2)
	assert.Equal(t, "turn_end", items[1].Kind)
	assert.Equal(t, "completed", items[1].Status)
}

func TestRegisterSubscriptions_TurnInterrupted(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() { for _, c := range cancels { c() } }()

	event.Publish(dispatcher, turndto.TurnInterrupted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
		},
	})

	items := svc.GetByThread("t1")
	require.Len(t, items, 1)
	assert.Equal(t, "turn_interrupted", items[0].Kind)
	assert.Equal(t, "interrupted", items[0].Status)
}
```

**步骤 2: 运行测试确认失败**

运行: `go test ./internal/module/uistate/timeline/... -run TestRegisterSubscriptions -v`
预期: FAIL — RegisterSubscriptions 不存在

**步骤 3: 写最小实现**

创建 `internal/module/uistate/timeline/projector.go`：

```go
package timeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
)

// RegisterSubscriptions wires timeline projection handlers to the event bus.
// Returns cancel funcs that must be merged into the caller's subscription list.
func RegisterSubscriptions(
	dispatcher *event.Dispatcher,
	svc Service,
	logger *slog.Logger,
	onUpdated func(threadID string), // B6: update 时由主包 emit UIProjectionUpdated
) []context.CancelFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return []context.CancelFunc{
		platformbus.ResilientSubscribe(dispatcher, turnStartedHandler(svc), logger),
		platformbus.ResilientSubscribe(dispatcher, turnCompletedHandler(svc), logger),
		platformbus.ResilientSubscribe(dispatcher, turnInterruptedHandler(svc), logger),
		platformbus.ResilientSubscribe(dispatcher, itemStartedHandler(svc), logger),
		platformbus.ResilientSubscribe(dispatcher, itemCompletedHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, toolCallBeginHandler(svc), logger),
		platformbus.ResilientSubscribe(dispatcher, toolCallEndHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, approvalRequestedHandler(svc), logger),
		platformbus.ResilientSubscribe(dispatcher, approvalResolvedHandler(svc, onUpdated), logger),
	}
}

func turnStartedHandler(svc Service) func(turndto.TurnStarted) {
	return func(ev turndto.TurnStarted) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		svc.Append(threadID, ev.AgentID, Item{
			ID:      timelineID("turn", ev.TurnID),
			Kind:    "turn_start",
			TurnID:  strings.TrimSpace(ev.TurnID),
			AgentID: strings.TrimSpace(ev.AgentID),
			Status:  "running",
		})
	}
}

func turnCompletedHandler(svc Service) func(turndto.TurnCompleted) {
	return func(ev turndto.TurnCompleted) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		svc.Append(threadID, ev.AgentID, Item{
			ID:      timelineID("turn-end", ev.TurnID),
			Kind:    "turn_end",
			TurnID:  strings.TrimSpace(ev.TurnID),
			AgentID: strings.TrimSpace(ev.AgentID),
			Status:  "completed",
		})
	}
}

func turnInterruptedHandler(svc Service) func(turndto.TurnInterrupted) {
	return func(ev turndto.TurnInterrupted) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		svc.Append(threadID, ev.AgentID, Item{
			ID:      timelineID("turn-int", ev.TurnID),
			Kind:    "turn_interrupted",
			TurnID:  strings.TrimSpace(ev.TurnID),
			AgentID: strings.TrimSpace(ev.AgentID),
			Status:  "interrupted",
		})
	}
}

// Item/Tool/Approval handlers + timelineID — 同文件实现，见下方
```

**步骤 4: 在 `projector.go` 追加 Item/Tool/Approval handler**

在 `turnInterruptedHandler` 之后追加 `itemStartedHandler`, `itemCompletedHandler`, `toolCallBeginHandler`, `toolCallEndHandler`, `approvalRequestedHandler`, `approvalResolvedHandler` 和 `timelineID` helper。所有 handler 已在步骤 3 的 `RegisterSubscriptions` 中注册。

> **B6 修复说明：** `UpdateByCallID` 本身不 emit 任何事件（它只返回 `bool`）。做 update 的 3 个 handler（`itemCompletedHandler`、`toolCallEndHandler`、`approvalResolvedHandler`）接收 `onUpdated func(string)` 回调，在 `UpdateByCallID` 返回 `true` 后调用 `onUpdated(threadID)`。主包在 wiring 时传入的回调会 emit `UIProjectionUpdated{Projection:"timeline"}`，通知前端刷新 timeline。这样 timeline 子包不依赖 `UIProjectionUpdated` DTO，保持单向依赖。
>
> 示例 handler 模式（`toolCallEndHandler` 为例）：
> ```go
> func toolCallEndHandler(svc Service, onUpdated func(string)) func(tooldto.ToolCallEnd) {
> 	return func(ev tooldto.ToolCallEnd) {
> 		threadID := strings.TrimSpace(ev.ThreadID)
> 		if threadID == "" { return }
> 		success := ev.Success
> 		updated := svc.UpdateByCallID(threadID, ev.AgentID, ev.CallID, func(it *Item) {
> 			it.Status = "completed"
> 			it.Success = &success
> 		})
> 		if updated && onUpdated != nil { onUpdated(threadID) }
> 	}
> }
> ```
> `itemCompletedHandler` 和 `approvalResolvedHandler` 遵循相同模式。

**步骤 5: 写 Item/Tool/Approval 测试**

在 `timeline_test.go` 追加（需 import `tooldto`）：

```go
func TestRegisterSubscriptions_ToolCallBeginAndEnd(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil) // onUpdated=nil: 本测试不关注 projection 通知
	defer func() { for _, c := range cancels { c() } }()

	event.Publish(dispatcher, tooldto.ToolCallBegin{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: shared.TurnHeader{
				AgentHeader: shared.AgentHeader{
					ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
					AgentID:      "agent-1",
				},
				TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
			},
			CallID:   "call-1",
			ToolName: "bash",
		},
		RequestID: 42,
	})

	items := svc.GetByThread("t1")
	require.Len(t, items, 1)
	assert.Equal(t, "tool_call", items[0].Kind)
	assert.Equal(t, "bash", items[0].ToolName)
	assert.Equal(t, "call-1", items[0].CallID)
	assert.Equal(t, int64(42), items[0].RequestID)

	// End → UpdateByCallID → onUpdated 回调（本测试 onUpdated=nil，仅验证 item 状态更新）
	event.Publish(dispatcher, tooldto.ToolCallEnd{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: shared.TurnHeader{
				AgentHeader: shared.AgentHeader{
					ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
					AgentID:      "agent-1",
				},
			},
			CallID: "call-1",
		},
		Success: true,
	})

	items = svc.GetByThread("t1")
	assert.Equal(t, "completed", items[0].Status)
	require.NotNil(t, items[0].Success)
	assert.True(t, *items[0].Success)
}

func TestRegisterSubscriptions_ApprovalRequestAndResolve(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() { for _, c := range cancels { c() } }()

	event.Publish(dispatcher, tooldto.ToolApprovalRequested{
		ToolApprovalHeader: shared.ToolApprovalHeader{
			ToolCallHeader: shared.ToolCallHeader{
				TurnHeader: shared.TurnHeader{
					AgentHeader: shared.AgentHeader{
						ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
						AgentID:      "agent-1",
					},
				},
				CallID:   "call-2",
				ToolName: "file_edit",
			},
		},
		Kind: "tool",
	})

	items := svc.GetByThread("t1")
	require.Len(t, items, 1)
	assert.Equal(t, "approval_request", items[0].Kind)
	assert.Equal(t, "pending", items[0].Status)

	event.Publish(dispatcher, tooldto.ToolApprovalResolved{
		ToolApprovalHeader: shared.ToolApprovalHeader{
			ToolCallHeader: shared.ToolCallHeader{
				TurnHeader: shared.TurnHeader{
					AgentHeader: shared.AgentHeader{
						ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
						AgentID:      "agent-1",
					},
				},
				CallID: "call-2",
			},
		},
		Approved: true,
	})

	items = svc.GetByThread("t1")
	assert.Equal(t, "approved", items[0].Status)
}

func TestRegisterSubscriptions_ItemStartedAndCompleted(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() { for _, c := range cancels { c() } }()

	event.Publish(dispatcher, turndto.ItemStarted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
		},
		ItemType: "command",
		Command:  "ls -la",
		CallID:   "call-3",
	})

	items := svc.GetByThread("t1")
	require.Len(t, items, 1)
	assert.Equal(t, "item", items[0].Kind)
	assert.Equal(t, "command", items[0].ItemType)

	event.Publish(dispatcher, turndto.ItemCompleted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
		},
		CallID:  "call-3",
		Success: true,
	})

	items = svc.GetByThread("t1")
	assert.Equal(t, "completed", items[0].Status)
	require.NotNil(t, items[0].Success)
	assert.True(t, *items[0].Success)
}
```

**步骤 2: 在 projector.go 追加 handler 实现**

在 `projector.go` 的 `turnInterruptedHandler` 之后追加所有 6 个 handler（`itemStartedHandler`, `itemCompletedHandler`, `toolCallBeginHandler`, `toolCallEndHandler`, `approvalRequestedHandler`, `approvalResolvedHandler`）和 `timelineID` helper。代码见任务 3 步骤 3 的完整 projector.go 已包含全部 handler。

> **B6 修复说明：** `UpdateByCallID` 不 emit 事件，只返回 `bool`。做 update 的 handler 在成功后调 `onUpdated(threadID)` 回调，由主包 emit `UIProjectionUpdated{Projection:"timeline"}`。详见任务 3 步骤 4 的 handler 模式示例。

**步骤 6: 运行测试确认通过**

运行: `go test ./internal/module/uistate/timeline/... -v`
预期: 全 PASS

**步骤 7: 提交**

```bash
git add internal/module/uistate/timeline/projector.go internal/module/uistate/timeline/timeline_test.go
git commit -m "feat(timeline): RegisterSubscriptions + all 9 handlers + tests (A4-γ step 2)"
```

---

### 任务 4: uistate 主包集成 + emitter wiring + GetState

**文件:**
- 修改: `internal/module/uistate/service.go`（新增 timeline 字段 + 初始化）
- 修改: `internal/module/uistate/patch.go`（bindDispatcher 中初始化 timeline emitter + SetEmitter 注入）
- 修改: `internal/module/uistate/projector.go`（注册 timeline 订阅 + 合并 cancel funcs）
- 修改: `internal/module/uistate/state.go`（UIState 加 TimelineByThread 字段）
- 修改: `internal/module/uistate/service.go`（GetState 约行 127，集成 timeline snapshot）
- 测试: `internal/module/uistate/timeline/timeline_test.go`（追加 emitter 集成测试）

**步骤 1: 修改 state.go**

在 `UIState` 结构体中新增字段（在 `DiffRevisionByThread` 之后）：

```go
TimelineByThread map[string][]timeline.Item `json:"timelineByThread,omitempty"`
```

需要 import: `"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"`

**步骤 2: 修改 service.go**

在 service 结构体中新增字段：

```go
timeline timeline.Service
```

在构造函数中初始化（emitter 先传 nil，后续在 bindDispatcher 中注入）：

```go
timeline: timeline.New(logger, nil, 0),
```

**步骤 3: 修改 patch.go（bindDispatcher）**

在 `bindDispatcher` 方法中新增 emitter 初始化和注入：

```go
// N2: timeline emitter — SetEmitter 仅在 init 阶段调用一次
emitTimelineAppend := bus.NewEmitter[uidto.UITimelineAppended](dispatcher)
if s.timeline != nil { // C4: 现有测试（如 preferences_event_test.go）直接 &service{} 构造，timeline 为 nil
	s.timeline.SetEmitter(timeline.AppendedEmitter(emitTimelineAppend))
}
```

> **C4 说明：** `NewService` 中 timeline 始终通过 `timeline.New(logger, nil, 0)` 初始化为非 nil。但部分现有测试直接 `&service{}` 构造跳过 `NewService`，此时 `timeline` 字段为 nil。`bindDispatcher` 中必须加 `if s.timeline != nil` 守卫，否则这些测试 panic。

**步骤 4: 修改 projector.go**

在 `registerProjectionSubscriptions` 中合并 timeline 子包的订阅。将原有的 `return []context.CancelFunc{...}` 改为变量模式：

```go
func registerProjectionSubscriptions(dispatcher *event.Dispatcher, svc *service) []context.CancelFunc {
	cancels := []context.CancelFunc{
		platformbus.ResilientSubscribe(dispatcher, svc.applyAgentStateChanged, svc.logger),
		// ... 现有 26 个订阅（保持不变）...
	}
	// 合并 timeline 子包的订阅（C4: nil 守卫）
	// B6: update 时 emit UIProjectionUpdated{Projection:"timeline"} 通知前端刷新
	onTimelineUpdated := func(threadID string) {
		svc.emitProjectionUpdatedEvents(uidto.UIProjectionUpdated{
			UIProjectionHeader: shared.UIProjectionHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: threadID},
				Projection:   "timeline",
			},
		})
	}
	if svc.timeline != nil {
		timelineCancels := timeline.RegisterSubscriptions(dispatcher, svc.timeline, svc.logger, onTimelineUpdated)
		cancels = append(cancels, timelineCancels...)
	}
	return cancels
}
```

**步骤 5: 修改 GetState 集成**

在 `service.go` 的 `GetState` 方法（约行 127）中填充 TimelineByThread：

```go
// timeline.Service 内部有自己的 sync.RWMutex，Snapshot() 是并发安全的
if s.timeline != nil { // C4: nil 守卫
	state.TimelineByThread = s.timeline.Snapshot()
}
```

> **注意：** 此调用应在 `s.mu.RUnlock()` 之后、return 之前，因为 timeline.Service 有独立锁，不需要 uistate 的 mutex 保护。

**步骤 6: 写 emitter 集成测试**

在 `timeline_test.go` 追加：

```go
func TestEmitterCalledOnAppend(t *testing.T) {
	var emitted []uidto.UITimelineAppended
	emitter := func(ev uidto.UITimelineAppended) { emitted = append(emitted, ev) }
	svc := timeline.New(nil, emitter, 50)

	svc.Append("t1", "agent-1", timeline.Item{
		ID: "item-1", Kind: "turn_start", TurnID: "turn-1",
	})

	require.Len(t, emitted, 1)
	assert.Equal(t, "item-1", emitted[0].ItemID)
	assert.Equal(t, "turn_start", emitted[0].ItemKind)
	assert.Equal(t, "timeline", emitted[0].Projection)
}

func TestOnUpdatedCalledAfterUpdate(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	var notifiedThreads []string
	onUpdated := func(threadID string) { notifiedThreads = append(notifiedThreads, threadID) }
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, onUpdated)
	defer func() { for _, c := range cancels { c() } }()

	// Append tool_call — onUpdated 不应被调用
	event.Publish(dispatcher, tooldto.ToolCallBegin{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: shared.TurnHeader{
				AgentHeader: shared.AgentHeader{
					ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
					AgentID:      "agent-1",
				},
			},
			CallID:   "call-1",
			ToolName: "bash",
		},
	})
	require.Empty(t, notifiedThreads, "append should not call onUpdated")

	// End → UpdateByCallID → onUpdated
	event.Publish(dispatcher, tooldto.ToolCallEnd{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: shared.TurnHeader{
				AgentHeader: shared.AgentHeader{
					ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
					AgentID:      "agent-1",
				},
			},
			CallID: "call-1",
		},
		Success: true,
	})
	require.Len(t, notifiedThreads, 1, "update should call onUpdated")
	assert.Equal(t, "t1", notifiedThreads[0])

	// Verify item state actually updated
	items := svc.GetByThread("t1")
	assert.Equal(t, "completed", items[0].Status)
}

func TestSetEmitterLateInjection(t *testing.T) {
	svc := timeline.New(nil, nil, 50) // 无 emitter
	svc.Append("t1", "a", timeline.Item{ID: "i1", Kind: "turn_start"})

	var emitted []uidto.UITimelineAppended
	svc.SetEmitter(func(ev uidto.UITimelineAppended) { emitted = append(emitted, ev) })

	svc.Append("t1", "a", timeline.Item{ID: "i2", Kind: "turn_end", TurnID: "turn-x"})
	require.Len(t, emitted, 1, "should emit after SetEmitter")
	assert.Equal(t, "i2", emitted[0].ItemID)
}
```

> **N1 说明：** UITimelineAppended DTO 当前字段足够（ItemID, ItemKind, RequestID, CallID）。如需 Status/ToolName/Error，可后续扩展 DTO（标注为 optional，前端也可从 GetState 获取完整 timeline snapshot）。

**步骤 7: 运行测试确认通过**

运行: `go test ./internal/module/uistate/... ./internal/module/uistate/timeline/... -v`
预期: 全 PASS

**步骤 8: 确认文件数守卫**

```bash
find internal/module/uistate -maxdepth 1 -name '*.go' ! -name '*_test.go' | wc -l
# 预期: 15（未新增文件）
find internal/module/uistate/timeline -maxdepth 1 -name '*.go' ! -name '*_test.go' | wc -l
# 预期: 2（timeline.go + projector.go）
```

**步骤 9: 提交**

```bash
git add internal/module/uistate/ internal/module/uistate/timeline/
git commit -m "feat(uistate): integrate timeline subpackage + emitter wiring + GetState (A4-γ step 3)"
```

---

### 任务 5: 全量验证 + 守卫 + 文档更新

**文件:**
- 修改: `docs/plans/迁移/session-summary.md`（状态更新）

**步骤 1: 全量测试**

运行: `go test ./internal/module/uistate/... ./internal/module/uistate/timeline/... -v`
预期: 全 PASS

运行: `go test ./internal/module/thread/... -v`
预期: 全 PASS

**步骤 2: 编译验证**

运行: `go build ./internal/... ./cmd/...`
预期: 0 errors

运行: `go vet ./internal/... ./cmd/...`
预期: 0 warnings

**步骤 3: 守卫验证**

运行: `go test -run TestCodeSizeGuard ./internal/archtest/... -v`
预期: PASS

验证文件行数：
- `timeline/timeline.go` ≤ 400 行（估算 ~170 行）
- `timeline/projector.go` ≤ 400 行（估算 ~200 行）
- `timeline/timeline_test.go` 不受限（测试文件）
- uistate 主包非测试文件数 = 15（未新增）
- timeline 子包非测试文件数 = 2（timeline.go + projector.go）

**步骤 4: LSP diagnostics**

运行 `lsp_file(diagnostics)` 对以下文件确认无新增 error：
- `internal/module/uistate/timeline/timeline.go`
- `internal/module/uistate/timeline/projector.go`
- `internal/module/uistate/service.go`
- `internal/module/uistate/projector.go`
- `internal/module/uistate/state.go`
- `internal/module/uistate/patch.go`
- `internal/module/thread/lifecycle_helpers.go`

**步骤 5: 死代码清理验证**

1. `lsp_xref(references)` 确认旧 `buildOfflineRuntimeConfig(stored)` 单参数签名已无引用
2. `lsp_grep(text_search)` 确认 `event.On(dispatcher` 不在 uistate 和 timeline 包中出现
3. 确认无未使用的 import
4. 确认无 TODO/FIXME 遗留

**步骤 6: 更新 session-summary.md**

将 A4-γ 从 `⏸️` 改为 `✅`，D1 更新说明。

**步骤 7: 最终提交**

```bash
git add -A
git commit -m "feat: complete A4-γ timeline projection + D1 offline model (closes A4-γ, D1)"
```

---

## 已知限制 / defer

| 项目 | 说明 | 优先级 |
|------|------|--------|
| **TurnOutputDelta 流式输出** | `TurnOutputDelta` 事件（assistant 实时流式输出 delta）不在本轮 timeline 中追踪。当前 `applyTurnOutputDelta`（`projector_handlers.go:336-348`）仅更新 thread/agent 的 lastMessage 状态。后续 P2 可将 delta 累积为 `assistant` 类型的 timeline item，但需考虑高频率事件的性能影响（每次 delta 都 append 会导致 timeline 快速填满，需合并/节流策略）。 | P2 |
| **UITimelineAppended DTO 扩展字段** | 当前 DTO 仅含 `ItemID`/`ItemKind`/`RequestID`/`CallID`。`Status`/`ToolName`/`Error` 等字段可按需扩展（N1），前端也可通过 `GetState` 获取完整 snapshot。 | P3 |
| **thinking/assistant 消息合并** | 多次 TurnOutputDelta 的 assistant 消息应合并为单个 timeline item，而非每次 delta 都追加。需设计合并/节流策略。 | P2 |
| **backfill（断线重连后从历史重建 timeline）** | 当前 timeline 仅基于实时事件流，断线重连后无法恢复已错过的事件。需从 turn history 重建。 | P2 |
| **timeline 持久化到 DB** | 当前 timeline 仅内存缓存，进程重启后丢失。后续可持久化到 SQLite/thread store。 | P3 |

---

## Blocking 项修复追踪

| ID | 问题 | 修复方式 | 涉及任务 |
|----|------|----------|---------|
| B1 | 包文件数超限 | 拆 `timeline/` 子包，主包文件数不变（15） | 任务 2-4 |
| B2 | 事件订阅 API 错误 | `event.On` → `platformbus.ResilientSubscribe`，返回 `[]context.CancelFunc` | 任务 3, 4 |
| B3 | threadModel helper 设计错误 | 删除 `threadModel`，直接用 `offlineThreadModel(thread)` + `firstNonEmpty` | 任务 1 |
| B4 | mustNewThreadService 不存在 | 改用 `NewService(silentLogger(), stubs...)` 现有 stub 模式 | 任务 1 |
| B5 | DTO 复合字面量不合法 | 修正嵌套：`TurnHeader{AgentHeader{ThreadHeader{...}}, TurnIDHeader{...}}` | 任务 3 |
| B6 | update 事件缺失 | `UpdateByCallID` 不 emit，handler 通过 `onUpdated` 回调由主包 emit `UIProjectionUpdated{Projection:"timeline"}` | 任务 3, 4 |
| B7 | 去重逻辑缺失 | `isDuplicate`: CallID 索引去重 + turnID:kind 去重 | 任务 2 |
| B8 | TurnOutputDelta 无任务承接 | 从设计概览删除，移至"已知限制/defer" | 设计概览 |

## Non-blocking 改进追踪

| ID | 改进 | 涉及位置 |
|----|------|---------|
| N1 | UITimelineAppended DTO 扩字段 | "已知限制/defer"标注为 P3 optional |
| N2 | emitter 命名统一 | 任务 4：局部变量 `emitTimelineAppend`，`SetEmitter` 注入 |
| N3 | ring buffer 改名 | 全文改为"bounded timeline buffer" |
| N4 | 行号/文件归属修正 | `buildOfflineConfig` 调用点约行 107；emitter wiring 在 `patch.go` + `module.go` |

---

## 风险与注意事项

| 风险 | 缓解 |
|------|------|
| uistate 包文件数已达 15 上限 | timeline 功能已拆到 `timeline/` 子包，主包文件数不变 |
| timeline 子包文件数 | 当前 2 个非测试文件（timeline.go + projector.go），远低于 15 上限 |
| bounded timeline buffer 内存 | 每 thread 默认 200 条上限，自动淘汰最旧 item |
| 并发安全 | timeline.Service 内部持有独立 `sync.RWMutex`，与 uistate 主锁无嵌套 |
| emitter 生命周期 | emitter 通过 `SetEmitter` 延迟注入，在 `bindDispatcher` 中通过 `bus.NewEmitter` 构造 |
| 去重覆盖度 | CallID 去重 + turnID:kind 去重覆盖所有 append 场景；update 不涉及去重 |
| handler 模式差异 | timeline 子包使用闭包 handler（因 `RegisterSubscriptions` 是包级函数）；逻辑等价，均经 `ResilientSubscribe` 包装 |
| 死代码风险 | 任务 5 步骤 5 强制验证旧签名无引用、无 `event.On` 残留 |
