# P16 UI Diff 渲染 + 工具调用记录 执行计划 (v3 终版)

> 生成时间：2026-04-11
> 版本：v3（合并 8 Agent 两轮审查，共 14 份报告）
> 前置依赖：P15 dynamicTools 注入（✅ 已完成）
> 目标：修复 V3 UI 右侧 Diff/Markdown 面板 + 右下角工具调用记录面板

---

## 0. 问题定义

### 0.1 症状

| # | 症状 | 位置 |
|---|------|------|
| 1 | 右侧 Diff 面板始终空白 | DiffPanel 组件 |
| 2 | 右侧 Markdown 预览无内容 | DiffPanel markdown 模式 |
| 3 | 右下角工具调用记录无数据 | ActivityPanel + ChatTimeline |
| 4 | 右下角活动统计全零 | ActivityPanel stat bar |

### 0.2 根因（6 项）

| # | 根因 | 分类 |
|---|------|------|
| R1 | V3 完全没有 diff 数据源 | 代码没写 |
| R2 | UIThreadPatch 缺少 diffText/diffRevision 字段 | 未接线 |
| R3 | Timeline item Kind 映射漂移（item/tool_call vs command/tool） | Schema 不匹配 |
| R4 | ActivityStats 结构体不存在 | 代码没写 |
| R5 | UIThreadPatch 缺少 timelineItems/activityStats 字段 | 未接线 |
| R6 | 前端 getThreadTimeline 过滤器过激 | 设计回归 |

### 0.3 核心结论

> 前端组件全部就绪（DiffPanel / ActivityPanel / ChatTimeline 与 V2 字节级一致），问题 100% 在后端 DTO + 数据源。
> ※ Phase 2 包含前端过滤器修正（selector/useThreadCards），这不矛盾——前端组件本身没问题，但后端 Kind 变了需要适配。

---

## 1. 架构设计

### 1.1 Diff 方案：Hooks 优先 + Git 兜底

```
toolbridge.routeToolCall(ctx, req)
  │
  ├─ lsp_edit(replace_range) ────────────────────────────┐
  │   从 tool call result.Content[i].Text 提取 patch     │ Hooks 路径
  │   → 零 IO，零外部依赖                                │ （仅 replace_range）
  │   → ~90% 的代码改动                                  │
  ├──────────────────────────────────────────────────────┘
  │
  ├─ code_run / lsp_edit(rename/format/code_action) ─────┐
  │   调用前: git snapshot dirty files (轻量)            │ Git 兜底路径
  │   调用后: git diff + go-difflib 生成 unified diff    │
  │   → 非 git 目录自动跳过                               │
  ├──────────────────────────────────────────────────────┘
  │
  └─ DiffAggregator.Merge(ctx, agentID, callID, ...)
        ├── CallID 幂等去重（防 hooks+git 双重计入）
        ├── 按 agentID 隔离 session（见 §1.2）
        ├── 输出统一 unified diff 文本
        └── bus.Publish(ToolDiffUpdated{agentID, threadID, ...})
```

> **审查修正 H4**：hooks 路径**仅覆盖 `lsp_edit(replace_range)`**，因为只有它的 patch 参数本身就是标准 diff。
> `rename/format/code_action` 返回的是 workspace edit（文件级 changes），无法直接提取行级 diff，归入 git 兜底路径。

### 1.2 agentID 隔离（防串台，核心设计约束）

```go
// agentID 格式：agent_<timestamp>_<hex>（不是 UUID，审查修正 C2）
// 示例：agent-1775850581409-1775850581404858000

type DiffAggregator struct {
    mu       sync.Mutex                         // 保护 sessions map
    sessions map[string]*agentDiffSession        // key = agentID
}

type agentDiffSession struct {
    mu           sync.Mutex                      // per-session lock（审查修正 H8）
    agentID      string
    threadID     string
    repoRoot     string                          // 可空（非 git 目录）
    files        map[string]*fileDiff            // 按文件路径累积
    revision     int64
    processedIDs map[string]bool                 // CallID 幂等去重（审查修正 H9）
    lastActivity time.Time                       // TTL 兜底用（审查修正 H3）
}
```

**锁策略（审查修正 H8）**：
- 全局 `mu` 只保护 `sessions` map 的读写（获取/创建/删除 session）
- per-session `mu` 保护 session 内部状态（files/revision/processedIDs）
- 同一 agent 的并发 tool call 在 session 锁内串行处理 diff 合并

**隔离规则**：
1. session key = agentID（不是 threadID，因为同 thread 可有多 agent）
2. **uistate 存储改为双 key（审查修正 C1）**：
   - `DiffTextByAgent map[string]string`（key = agentID）
   - `DiffRevisionByAgent map[string]int64`（key = agentID）
   - 前端展示时按当前 active agent 过滤
3. repoRoot 不同的调用自动 reset session
4. agent 停止/失败时清理 session
5. TTL 兜底：超过 30 分钟无活动的 session 自动清理

**清理触发（审查修正 H3）**：
- 订阅 `AgentStopped` 事件 → CleanupAgent
- 订阅 `AgentFailed` 事件 → CleanupAgent
- 订阅 `AgentError` 事件 → CleanupAgent
- TTL 兜底：aggregator 内置 sweeper goroutine，30 分钟无活动清理
- CleanupAgent 挂在 **toolbridge 层**（不是 uistate，审查修正 arch-v2）

### 1.3 工具调用记录链路（修复接线）

```
provider event → bus → timeline projector handler
  ├── Kind 映射（V2 兼容）:
  │     ItemStarted(item_type=command) → "command"
  │     ItemStarted(item_type=file)    → "file"
  │     ToolCallBegin                  → "tool"
  │     ApprovalRequested              → "approval"
  ├── 字段对齐: tool_name → tool, elapsed_ms → elapsedMs
  ├── 合并逻辑: 同 tool+callID 的连续事件合并更新（不重复追加）
  ├── preview producer: 从 ToolCallEnd 的 result 截取前 200 字符
  └── ActivityStats 累积（按 threadID 分组）:
        Commands++ / FileEdits++ / ToolCalls[name]++ / LSPCalls++
```

### 1.4 UIThreadPatch 完整字段（审查修正 C5 + H5 + H6）

```go
// internal/dto/ui/event.go
type UIThreadPatch struct {
    // --- 已有字段 ---
    ThreadID         string `json:"threadId"`
    Source           string `json:"source,omitempty"`
    Sequence         int64  `json:"sequence,omitempty"`
    // Thread/Status/Overlay/TokenUsage/ActiveThread/MainAgent/Partial ...（省略已有）

    // --- Phase 1 新增：Diff ---
    DiffText         string `json:"diffText,omitempty"`
    DiffRevision     int64  `json:"diffRevision,omitempty"`

    // --- Phase 2 新增：Timeline + Stats ---
    Interruptible    *bool               `json:"interruptible,omitempty"`
    AgentMeta        map[string]any      `json:"agentMeta,omitempty"`        // 审查修正 H5
    ActivityStats    *PatchActivityStats `json:"activityStats,omitempty"`
    Alerts           []PatchAlert        `json:"alerts,omitempty"`
    TimelineItems    []PatchTimelineItem `json:"timelineItems,omitempty"`
    RemovedItemIds   []string            `json:"removedItemIds,omitempty"`
    TimelineOrder    []string            `json:"timelineOrder,omitempty"`

    // --- Phase 1 新增：降级 ---
    Recover          bool   `json:"recover,omitempty"`
    RefreshRequired  bool   `json:"refreshRequired,omitempty"`
    FallbackReason   string `json:"fallbackReason,omitempty"`
}
```

**payload_too_large 完整 contract（审查修正 H6）**：
```
当 patch JSON > 64KB 时触发降级：
  保留字段：ThreadID, Source, Sequence, Status, StatusHeader, StatusDetails,
            Interruptible, Recover=true, RefreshRequired=true,
            FallbackReason="payload_too_large"
  丢弃字段：DiffText, TimelineItems, ActivityStats, Alerts, AgentMeta
  前端行为：收到 RefreshRequired=true 后调用 ui/state/get 补拉全量
```

**Bridge 事件 method**：固定为 **`ui/thread/patch`**（审查修正 D7）

### 1.5 Patch DTO 类型定义（dto/ui 内，避免 import cycle）

```go
// internal/dto/ui/patch_types.go（新建）

type PatchActivityStats struct {
    LSPCalls  int64            `json:"lspCalls"`
    Commands  int64            `json:"commands"`
    FileEdits int64            `json:"fileEdits"`
    ToolCalls map[string]int64 `json:"toolCalls"`
}

// PatchTimelineItem — 完整字段对齐前端 contract（审查修正 C5）
type PatchTimelineItem struct {
    ID          string `json:"id"`
    Ts          string `json:"ts"`
    Kind        string `json:"kind"`                    // command/tool/file/approval/thinking/assistant/plan/error/user
    Tool        string `json:"tool,omitempty"`           // 工具名
    Text        string `json:"text,omitempty"`           // 文本内容
    Command     string `json:"command,omitempty"`        // 命令内容
    File        string `json:"file,omitempty"`           // 关联文件
    Status      string `json:"status,omitempty"`         // ok/failed/running
    CallID      string `json:"callId,omitempty"`
    RequestID   int64  `json:"requestId,omitempty"`
    ElapsedMS   *int   `json:"elapsedMs,omitempty"`
    Preview     string `json:"preview,omitempty"`        // 结果预览（截取前 200 字符）
    Output      string `json:"output,omitempty"`         // 命令输出
    ExitCode    *int   `json:"exitCode,omitempty"`
    Done        bool   `json:"done,omitempty"`
    Internal    bool   `json:"internal,omitempty"`       // 内部工具标记
    Attachments []any  `json:"attachments,omitempty"`    // 附件
}

type PatchAlert struct {
    ID      string `json:"id"`
    Time    string `json:"time"`
    Level   string `json:"level"`     // info/warn/error
    Message string `json:"message"`
}
```

> **import 方向**：`dto/ui` 不引用 `module/uistate` 或 `timeline`。
> `timeline.Item` → `PatchTimelineItem` 转换在 `uistate/patch.go` 内完成。

---

## 2. 审查问题修正记录（两轮共 22 项）

| # | 问题 | 来源 | 修正 |
|---|------|------|------|
| S1 | uistate 已 15 文件 | arch-r1 | ✅ 不新建文件 |
| S2 | dto/ui 引用 timeline.Item cycle | arch-r1 | ✅ dto/ui 独立 Patch* 类型 |
| S3 | cwd 查询不可行 | risk-r1 | ✅ WorkDirResolver 接口 |
| S4 | TurnID 缺失 | impl-r1 | ✅ 不嵌入 ToolCallHeader |
| C1 | diff 按 threadID 存会串台 | risk-v2 | ✅ 改为 DiffTextByAgent（agentID 维度） |
| C2 | agentID 不是 UUID | concur | ✅ 文档修正为 agent_\<ts\>_\<hex\> |
| C3 | Merge() 签名不对 | impl-v2 | ✅ 解析 Content[i].Text |
| C4 | timeline 事件名写错 | risk-v2 | ✅ 修正为 AgentError/TurnInputReceived/PlanDelta |
| C5 | PatchTimelineItem 字段不全 | frontend | ✅ 补全 ts/text/command/file/requestId/internal/attachments |
| H1 | WorkDirResolver 缺 ctx | arch-v2+impl-v2 | ✅ 加 context.Context |
| H2 | ToolDiffUpdated 缺 Type() | impl-v2 | ✅ 实现 shared.Event 接口 |
| H3 | CleanupAgent 只订阅 Stopped | risk-v2+concur | ✅ +AgentFailed+AgentError+TTL |
| H4 | hooks 只有 replace_range 稳定 | risk-v2 | ✅ 明确只覆盖 replace_range |
| H5 | UIThreadPatch.agentMeta 遗漏 | v2parity | ✅ 已补充 |
| H6 | payload_too_large contract 不全 | v2parity | ✅ 写明保留/丢弃字段 |
| H7 | timeline merge + preview producer | v2parity | ✅ 描述合并逻辑 + preview 来源 |
| H8 | 全局锁粒度过粗 | concur | ✅ map lock + per-session lock |
| H9 | CallID 幂等去重 | concur | ✅ processedIDs map |
| D1 | 缺守卫标准 | doc-qual | ✅ 见 §6 |
| D4 | difftracker 文件数不一致 | doc-qual | ✅ 统一为 8 文件 |
| D7 | bridge method 未显式写死 | frontend | ✅ 固定 ui/thread/patch |
| D8 | 现有测试会先红 | test | ✅ 见 §3 Phase 2 Task 2-1 注意事项 |

---

## 3. 分 Phase 执行计划

### Phase 1: Diff 数据源（R1 + R2）

#### Task 1-1: 新建 difftracker 包

**新建** `internal/platform/difftracker/` — leaf infra，不依赖 module 层

| 文件 | 职责 | 预估行数 |
|------|------|---------|
| `doc.go` | 包文档 | ~20 |
| `types.go` | DiffResult / DiffEmitter / WorkDirResolver / 常量 | ~70 |
| `aggregator.go` | DiffAggregator — agentID 隔离 + CallID 去重 + TTL sweeper | ~150 |
| `hooks_extract.go` | 从 replace_range result.Content[i].Text 提取 patch | ~80 |
| `git_ops.go` | git root / dirty paths / HEAD 内容 | ~100 |
| `git_diff.go` | BeginSnapshot / EmitGitDiff | ~120 |
| `unified.go` | 多源 diff 合并为统一 unified diff | ~80 |
| `resolver.go` | WorkDirResolver 接口定义 | ~30 |

预估：**~650 行 / 8 文件**（< 4500 行 / < 15 文件）

**WorkDirResolver 接口（审查修正 H1）**：
```go
type WorkDirResolver interface {
    ResolveAgentCWD(ctx context.Context, agentID string) (string, error)
}
```

**性能护栏**：
```go
const (
    MaxTrackedFiles      = 200
    MaxFileSizeBytes     = 1 << 20  // 1MB
    MaxTotalDiffBytes    = 5 << 20  // 5MB
)
var SkipBinaryExts = map[string]bool{".png":true, ".jpg":true, ...}
```

#### Task 1-2: 定义 Diff 事件

- **修改** `internal/dto/shared/event.go` L36 后：`EventTypeToolDiffUpdated = 1204`
- **新建或修改** `internal/dto/tool/event.go`：
  ```go
  type ToolDiffUpdated struct {
      Timestamp time.Time `json:"timestamp"`
      ThreadID  string    `json:"threadId"`
      AgentID   string    `json:"agentId"`
      CallID    string    `json:"callId,omitempty"`
      ToolName  string    `json:"toolName,omitempty"`
      DiffText  string    `json:"diffText"`
      Files     []string  `json:"files"`
  }
  func (e ToolDiffUpdated) Type() uint32 { return EventTypeToolDiffUpdated }  // 审查修正 H2
  ```

#### Task 1-3: toolbridge 集成

- **修改** `internal/platform/toolbridge/handler.go`：
  - Handler struct 新增 `aggregator *difftracker.DiffAggregator`
  - `routeToolCall(ctx, req)` L50 之前：
    - 判断 toolName 是否 `code_run` 系列 → 触发 git snapshot
    - 判断 toolName 是否 `lsp_edit` 且 action=`replace_range` → 标记 hooks 路径
  - Callback 返回后（需先拆内联 callback 为 `err := Callback(...)`）：
    - `aggregator.Merge(ctx, req.AgentID, req.CallID, req.Name, result, resolver)`
    - Merge 失败只记 WARN，**不阻断工具调用**
  - 订阅 AgentStopped/AgentFailed/AgentError → `aggregator.CleanupAgent(agentID)`
- **修改** `internal/platform/toolbridge/module.go` L8-16：
  - `NewHandler` 改为 fx.In 注入 aggregator + resolver
  - resolver 实现：`bindingStore.GetByAgentID(agentID).Cwd`

#### Task 1-4: uistate 订阅 Diff 事件（不新建文件）

- **修改** `internal/module/uistate/projector.go`：+1 行订阅 `ToolDiffUpdated`
- **修改** `internal/module/uistate/diff_state.go` L34-60：
  - `DiffTextByAgent` / `DiffRevisionByAgent`（agentID 维度，审查修正 C1）
  - `applyToolDiffUpdated(agentID, threadID, diffText)` 写真实 diff
  - unchanged 语义：仅在内容变化时递增 revision
- **修改** `internal/module/uistate/state.go`：
  - 替换 `DiffTextByThread` → `DiffTextByAgent`
  - 替换 `DiffRevisionByThread` → `DiffRevisionByAgent`

#### Task 1-5: UIThreadPatch Diff 字段 + payload 降级

- **修改** `internal/dto/ui/event.go` L71 后：+DiffText/DiffRevision/Recover/RefreshRequired/FallbackReason
- **新建** `internal/dto/ui/patch_types.go`：PatchTimelineItem/PatchActivityStats/PatchAlert（§1.5 完整定义）
- **修改** `internal/module/uistate/patch.go` L130 后：
  - threadPatchLocked 填充 DiffText/DiffRevision
  - payload_too_large 降级（>64KB 触发，保留 cheap fields）

#### Task 1-6: 验证

- `go build ./internal/... ./cmd/...` — 0 errors
- `go vet ./...` — 0 warnings
- `go test -run TestCodeSizeGuard ./internal/archtest/...` — PASS
- `go test -run TestDependencyDirection ./internal/archtest/...` — PASS

---

### Phase 2: 工具调用记录（R3 + R4 + R5 + R6）

#### Task 2-1: Timeline Kind 映射对齐

- **修改** `internal/module/uistate/timeline/projector.go`：
  - `itemStartedHandler` L149-150：按 `ev.ItemType` 映射
    - `command` → Kind=`"command"`, `file` → Kind=`"file"`, 其他 → Kind=`"item"`
  - `toolCallBeginHandler` L193：`"tool_call"` → `"tool"`
  - `toolCallEndHandler` L212-217：补 `ElapsedMS`/`Done`/`Preview`（从 result 截取前 200 字符）
  - `approvalRequestedHandler` L234：`"approval_request"` → `"approval"`

> **⚠️ 注意（审查修正 D8）**：此改动会导致现有 `timeline_test.go` 中 hard-code 旧 Kind 的用例失败。
> Agent 必须同步更新测试中的 Kind 期望值。

#### Task 2-2: Timeline Item 补字段 + 合并逻辑

- **修改** `internal/module/uistate/timeline/timeline.go` L23 后：
  - 补 Tool/Preview/ElapsedMS/Output/ExitCode/Done/Text/Internal/Attachments
- **修改** `timeline.go` Append/UpdateByCallID：
  - 合并逻辑：同 tool+callID 的连续事件**合并更新**字段（不重复追加 item）

#### Task 2-3: Timeline parity — 补 plan/error/user

- **修改** `internal/module/uistate/timeline/projector.go`：
  - 订阅 `PlanDelta`/`PlanUpdated` → Kind=`"plan"`（审查修正 C4 — 不是 PlanDeltaEvent）
  - 从 `AgentError`/`AgentFailed`/failed `TurnCompleted` 映射 → Kind=`"error"`（审查修正 C4）
  - 订阅 `TurnInputReceived` → Kind=`"user"`（审查修正 C4 — 不是 UserMessage）

#### Task 2-4: 实现 ActivityStats

- **修改** `internal/module/uistate/state.go` L20 后：
  ```go
  ActivityStatsByThread map[string]*ActivityStats
  ```
  - cloneState L154-176 同步
- **修改** `internal/module/uistate/projector.go`（审查修正 H1 — 不是 projector_handlers.go）：
  - `applyItemStarted(command)` → `stats.Commands++`
  - `applyItemStarted(file)` → `stats.FileEdits++`
  - `applyToolCallBegin` → `stats.ToolCalls[name]++`；`lsp_*` 前缀 → `stats.LSPCalls++`

#### Task 2-5: UIThreadPatch Timeline + Stats 字段

- **修改** `internal/dto/ui/event.go`：补 Interruptible/AgentMeta/ActivityStats/Alerts/TimelineItems/RemovedItemIds/TimelineOrder
- **修改** `internal/module/uistate/patch.go` threadPatchLocked：
  - timeline **增量** delta（changed/removed/order — V2 语义）
  - `timeline.Item` → `PatchTimelineItem` 转换
  - activityStats 填充
- **修改** `internal/module/uistate/service.go` L145-159：GetState 返回 activityStats

> **注意**：此 Task 与 Task 2-4 共享 `patch.go` 和 `service.go` 写集。
> 建议由同一 Agent 执行，或 Task 2-4 先完成后 Task 2-5 再开始。

#### Task 2-6: 前端修正

- **修改** `stores/thread-sync-selectors.js` L6-9：
  - `STRUCTURAL_TIMELINE_KINDS` 移除 `item`/`tool_call`/`approval_request`
  - 只保留 `turn_start`/`turn_end`/`turn_interrupted`
- **修改** `composables/useThreadCards.js` L108-170：
  - `toProcessActivityItem` 新增 `tool`/`approval`/`file` kind
- **修改** `stores/thread-diff-sync.js`（已存在）：
  - 确认 knownDiffRevision 语义与后端 DiffRevisionByAgent 一致
  - 前端按 active agentID 过滤 diff（对应后端 DiffTextByAgent）

#### Task 2-7: 验证

- `go build ./internal/... ./cmd/...` — 0 errors
- `go vet ./...` — 0 warnings
- `go test -run TestCodeSizeGuard ./internal/archtest/...` — PASS
- `go test ./internal/module/uistate/timeline/...` — PASS（Kind 已更新）

---

### Phase 3: 集成验证 + 收尾

#### Task 3-1: 构建验证

- [ ] `go build ./internal/... ./cmd/...` — 0 errors
- [ ] `go vet ./...` — 0 warnings
- [ ] `go test -run TestCodeSizeGuard ./internal/archtest/...` — PASS
- [ ] `go test -run TestDependencyDirection ./internal/archtest/...` — PASS
- [ ] `lsp_file(diagnostics)` — 0 errors

#### Task 3-2: 功能验收

- [ ] **Diff-1**: lsp_edit(replace_range) 后右侧面板实时显示 unified diff（hooks 路径）
- [ ] **Diff-2**: code_run 创建/修改文件后右侧面板显示 git diff（git 路径）
- [ ] **Diff-3**: 非 git 目录下 lsp_edit 的 diff 正常，code_run 优雅跳过
- [ ] **Diff-4**: 多 agent 场景 — agent A 和 B 的 diff **不串台**
- [ ] **Diff-5**: 大 diff 降级 — patch >64KB 时前端收到 refreshRequired 并补拉
- [ ] **Diff-6**: Diff 面板按文件分组、逐行着色（add/del/ctx/hunk）
- [ ] **MD-1**: Markdown 预览正常（markdown-it + highlight.js + katex）
- [ ] **Stats-1**: 右下角 stat bar 显示 LSP/命令/文件/工具 计数
- [ ] **Stats-2**: 展开后每个工具调用次数降序排列
- [ ] **TL-1**: ChatTimeline 中 tool/command/file/approval 条目正常渲染
- [ ] **TL-2**: ToolTickerBar 滚动显示工具调用摘要（kind=tool + tool/elapsedMs/preview/file）
- [ ] **TL-3**: plan/error/user timeline item 正常显示

---

## 4. 文件变更矩阵

### 4.1 新建文件

| 文件 | Phase | 说明 | 预估行数 |
|------|-------|------|---------|
| `internal/platform/difftracker/doc.go` | P1 | 包文档 | 20 |
| `internal/platform/difftracker/types.go` | P1 | 类型定义 | 70 |
| `internal/platform/difftracker/aggregator.go` | P1 | agentID 隔离 + 去重 + TTL | 150 |
| `internal/platform/difftracker/hooks_extract.go` | P1 | replace_range patch 提取 | 80 |
| `internal/platform/difftracker/git_ops.go` | P1 | git CLI | 100 |
| `internal/platform/difftracker/git_diff.go` | P1 | git diff 生成 | 120 |
| `internal/platform/difftracker/unified.go` | P1 | diff 合并 | 80 |
| `internal/platform/difftracker/resolver.go` | P1 | WorkDirResolver 接口 | 30 |
| `internal/dto/ui/patch_types.go` | P1 | PatchTimelineItem/PatchActivityStats/PatchAlert | 80 |

### 4.2 修改文件

| 文件 | Phase | 修改点 |
|------|-------|--------|
| `internal/dto/shared/event.go` L36 | P1 | +EventTypeToolDiffUpdated=1204 |
| `internal/dto/tool/event.go` | P1 | +ToolDiffUpdated struct + Type() |
| `internal/dto/ui/event.go` L60-77 | P1+P2 | UIThreadPatch +13 字段 |
| `internal/platform/toolbridge/handler.go` L38-67 | P1 | routeToolCall 包裹 diff |
| `internal/platform/toolbridge/module.go` L8-16 | P1 | fx.In 注入 |
| `internal/module/uistate/projector.go` | P1+P2 | +ToolDiffUpdated +Stats 累积 |
| `internal/module/uistate/diff_state.go` L34-60 | P1 | DiffTextByAgent + 真实 diff |
| `internal/module/uistate/state.go` L12-32 | P1+P2 | DiffByAgent + ActivityStats |
| `internal/module/uistate/patch.go` L113-147 | P1+P2 | diff+timeline+stats 填充 |
| `internal/module/uistate/service.go` L145-159 | P2 | GetState 返回 stats |
| `uistate/timeline/projector.go` L149,193,234 | P2 | Kind 映射 + plan/error/user |
| `uistate/timeline/timeline.go` L14-31 | P2 | Item 补字段 + 合并逻辑 |
| `stores/thread-sync-selectors.js` L6-9 | P2 | 过滤器修正 |
| `composables/useThreadCards.js` L108-170 | P2 | +tool/approval/file |
| `stores/thread-diff-sync.js` | P2 | agentID 过滤适配 |

---

## 5. 代码预算

| 包 | 当前行数 | 文件数 | 变更后预估 | 状态 |
|---|---:|---:|---:|---|
| `platform/difftracker`（新建） | 0 | 0 | ~650 / 8 | ✅ |
| `platform/toolbridge` | 278 | 3 | ~380 / 3 | ✅ |
| `module/uistate` | 3538 | 15 | ~4000 / **15** | ✅ |
| `dto/ui` | 68 | 1 | ~230 / 2 | ✅ |
| `uistate/timeline` | 512 | 2 | ~680 / 2 | ✅ |

---

## 6. 守卫标准（每次 Agent 任务必须包含）

```
⚠️ 守卫红线（违反将触发 archtest 失败，导致返工）：
- 单文件 ≤400 行（超了必须拆文件）
- 单函数 ≤80 行（超了必须提取子函数）
- 圈复杂度 CC ≤10（超了必须拆分分支逻辑）
- 包非测试文件 ≤15（超了必须合并小文件）
- 包总行数 ≤4500（超了必须拆包或精简）
- 完成后必须跑 go test -run TestCodeSizeGuard ./internal/archtest/... 确认全绿
```

---

## 7. 依赖方向

| 依赖关系 | 方向 | archtest |
|----------|------|---------|
| `platform/toolbridge` → `platform/difftracker` | 同层 | ✅ |
| `module/uistate` → `dto/tool` | 下层 | ✅ 已存在 |
| `module/uistate` → `dto/ui` | 下层 | ✅ |
| `dto/ui` ←✗ `module/*` | 禁止 | ✅ 不引用 |
| `platform/difftracker` → `go-difflib` | 外部 | ✅ 已在 go.mod |

**无需更新 archtest 白名单。**

---

## 8. Agent 分配

### Phase 1（5 Agent 并行）

| Agent | 职责 | 文件范围 | 依赖 |
|-------|------|----------|------|
| A1 | difftracker 核心（aggregator + types + resolver） | difftracker/{aggregator,types,resolver,doc}.go | 无 |
| A2 | difftracker hooks+git（extract + git_ops + git_diff + unified） | difftracker/{hooks_extract,git_ops,git_diff,unified}.go | 无 |
| A3 | 事件定义 + toolbridge 集成 | dto/shared + dto/tool + toolbridge/* | A1 类型定义 |
| A4 | uistate Diff 接线 + UIThreadPatch + patch_types | uistate/{projector,diff_state,state,patch}.go + dto/ui/* | A3 事件 |
| A5 | difftracker 单元测试 | difftracker/*_test.go | A1+A2 |

### Phase 2（5 Agent 并行）

| Agent | 职责 | 文件范围 | 依赖 |
|-------|------|----------|------|
| B1 | Timeline Kind + Item 字段 + 合并逻辑 + plan/error/user | timeline/* | 无 |
| B2 | ActivityStats + Patch Stats/Timeline 填充 | uistate/{state,projector,patch,service}.go + dto/ui | B1 完成 |
| B3 | 前端修正（selector + useThreadCards + diff-sync） | JS 文件 | B1 完成 |
| B4 | 后端测试（timeline + stats + delta patch） | *_test.go | B1+B2 |
| B5 | 前端测试 + ToolTickerBar 验证 | JS 测试 | B3 |

> **Task 依赖**：B2/B3/B4 依赖 B1 先完成 Kind 映射。B1 可独立开始。

### Phase 3（2 Agent）

| Agent | 职责 |
|-------|------|
| C1 | E2E 集成验证（§3 Task 3-1 + 3-2 全部 checklist） |
| C2 | 互审 + 收尾清理 + 死代码检查 |

---

## 9. 回滚方案

若 P16 引入回归：
1. **Phase 1 回滚**：删除 `internal/platform/difftracker/`，revert toolbridge/uistate 改动。前端不受影响（回到空 diff 状态）。
2. **Phase 2 回滚**：revert timeline/projector.go Kind 映射 + 前端 selector 改动。回到"有 timeline 但前端不显示"状态。
3. **局部回滚**：git diff 路径出问题 → 只禁用 git 路径（hooks 路径独立可用）。

---

## 10. 已知限制与决策记录

| # | 决策 | 原因 |
|---|------|------|
| 1 | MVP 不做 MCP relay | toolbridge 主路径已覆盖所有工具调用 |
| 2 | MVP 不做 diff 持久化 | 应用重启后 diff 清空可接受 |
| 3 | hooks 仅覆盖 lsp_edit(replace_range) | 其他 lsp_edit action 返回 workspace edit 非行级 diff |
| 4 | 非 git 目录 code_run 无 diff | 纯 hooks 无法感知 shell 命令 |
| 5 | timeline 推增量不推全量 | 防 patch 膨胀 |
| 6 | session key = agentID | 同 thread 多 agent 必须按 agent 隔离 |
| 7 | diff 存储用 agentID 维度 | 防同 thread 多 agent 覆盖 |
| 8 | payload_too_large 阈值 64KB | 与 V2 一致 |

---

## 11. Agent Prompt 模板

所有 Phase 1/2 Agent 初始 prompt 必须包含以下前缀：

```
先执行 shared_file_read prompts/lsp-mandatory-prefix.md
再执行 shared_file_read prompts/lsp-advanced-guide.md

⚠️ 你的所有操作不需要审批，直接执行即可。

读取计划文档：lsp_file(read_file, "docs/plans/迁移/p16-ui-diff-toolcall-plan.md")

⚠️ 守卫红线（违反将触发 archtest 失败，导致返工）：
- 单文件 ≤400 行 / 单函数 ≤80 行 / CC ≤10 / 包非测试文件 ≤15 / 包总行数 ≤4500
- 完成后必须跑 go test -run TestCodeSizeGuard ./internal/archtest/...

⚠️ 死代码清理：替换 = 删旧 + 加新，不是只加新。
```

---

## 12. 互审规范

- Phase 1 完成后：**1:4 互审**（5 Agent 交叉审查）
- Phase 2 完成后：**1:4 互审**（5 Agent 交叉审查）
- 互审标准：代码正确性 + 守卫合规 + 死代码清理 + 测试覆盖
- 修复后再审查一轮，形成闭环

---

## 13. 调研与审查报告引用

| 报告 | 路径 |
|------|------|
| Diff 链路调研 | shared:reports/p16-diff-research.md |
| 工具调用记录调研 | shared:reports/p16-toolcall-research.md |
| 前端与 Binding 调研 | shared:reports/p16-frontend-research.md |
| 架构审查 r1 | shared:reports/p16-review-arch.md |
| 实现落点审查 r1 | shared:reports/p16-review-impl.md |
| 风险审查 r1 | shared:reports/p16-review-risk.md |
| 架构审查 v2 | shared:reports/p16-review-arch-v2.md |
| 实现落点审查 v2 | shared:reports/p16-review-impl-v2.md |
| 风险审查 v2 | shared:reports/p16-review-risk-v2.md |
| V2 对齐审查 | shared:reports/p16-review-v2parity.md |
| 并发安全审查 | shared:reports/p16-review-concurrency.md |
| 前端 contract 审查 | shared:reports/p16-review-frontend-contract.md |
| 测试策略审查 | shared:reports/p16-review-test.md |
| 文档质量审查 | shared:reports/p16-review-doc-quality.md |
