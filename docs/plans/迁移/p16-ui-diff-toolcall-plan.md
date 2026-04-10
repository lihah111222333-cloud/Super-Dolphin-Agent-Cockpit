# P16 UI Diff 渲染 + 工具调用记录 执行计划

> 生成时间：2026-04-11
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

### 0.2 根因分析（3 Agent 调研结论）

| # | 根因 | 分类 | 影响 |
|---|------|------|------|
| R1 | V3 完全没有 difftracker 数据源 | 代码没写 | Diff 面板永远空 |
| R2 | UIThreadPatch 缺少 diffText/diffRevision 字段 | 未接线 | Diff 实时推送断开 |
| R3 | Timeline item Kind 映射漂移（item/tool_call vs command/tool） | Schema 不匹配 | 前端不认后端数据 |
| R4 | ActivityStats 结构体不存在 | 代码没写 | 活动统计全零 |
| R5 | UIThreadPatch 缺少 timelineItems/activityStats 字段 | 未接线 | Timeline 实时推送断开 |
| R6 | 前端 getThreadTimeline 过滤器过激 | 设计回归 | 即使后端修好也看不到 |

### 0.3 核心结论

> **前端组件全部就绪（DiffPanel / ActivityPanel / ChatTimeline 与 V2 字节级一致），问题 100% 在后端 DTO + 数据源。**

---

## 1. 架构设计

### 1.1 Diff 链路（新建）

```
toolbridge.routeToolCall
  ├── 调用前: difftracker.BeginTracker(agentID, toolName, args, workDir)
  │     └── 快照 git dirty files + HEAD 内容
  ├── peer.Callback("tools/call", ...) — 实际执行工具
  └── 调用后: tracker.EmitDiffUpdate(threadID, providerThreadID, toolName, emitter)
        └── emitter: bus.Publish(ToolDiffUpdated{threadID, diffText, files})
              └── uistate projector_diff handler:
                    ├── state.DiffTextByThread[threadID] = diffText
                    ├── state.DiffRevisionByThread[threadID]++
                    └── emitThreadPatch(UIThreadPatch{diffText, diffRevision})
                          └── eventsurface → bridge-event → 前端 DiffPanel
```

### 1.2 工具调用记录链路（修复接线）

```
provider event (ToolCallBegin/ToolCallEnd/ItemStarted/ItemCompleted)
  └── bus → timeline projector handler
        ├── Kind 映射: item(command) → "command", item(file) → "file",
        │              tool_call → "tool", approval_request → "approval"
        ├── 字段对齐: tool_name → tool, elapsed_ms → elapsedMs, 补 preview/done
        └── ActivityStats 累积: Commands++/FileEdits++/ToolCalls[name]++/LSPCalls++
              └── emitThreadPatch(UIThreadPatch{timelineItems, activityStats})
                    └── eventsurface → bridge-event → 前端 ActivityPanel + ChatTimeline
```

### 1.3 UIThreadPatch 字段补全

```
当前 V3 UIThreadPatch（偏瘦）          补全后（对齐 V2 前端 contract）
─────────────────────────            ────────────────────────────────
threadId                             threadId
source/sequence                      source/sequence
thread{id,name,state}                thread{id,name,state}
status/statusHeader/statusDetails    status/statusHeader/statusDetails
overlayText/overlayType/...          overlayText/overlayType/...
tokenUsage                           tokenUsage
activeThreadId/activeCmdThreadId     activeThreadId/activeCmdThreadId
mainAgentId/mainAgentState           mainAgentId/mainAgentState
partial                              partial
                                   + interruptible          ← 新增
                                   + diffText               ← 新增
                                   + diffRevision           ← 新增
                                   + agentMeta              ← 新增
                                   + activityStats          ← 新增
                                   + alerts                 ← 新增
                                   + timelineItems          ← 新增
                                   + removedItemIds         ← 新增
                                   + timelineOrder          ← 新增
                                   + recover                ← 新增
                                   + refreshRequired        ← 新增
```

---

## 2. 分 Phase 执行计划

### Phase 1: Diff 数据源（R1 + R2）

> 目标：让右侧 Diff 面板有数据

#### Task 1-1: 移植 difftracker 包

- **新建** `internal/platform/difftracker/` 目录
- 从 V2 `pkg/diffsdk/difftracker/` 移植以下核心文件：
  - `doc.go` — 包文档
  - `args.go` — 工具名/参数/repo root/目标文件解析
  - `emitter.go` — `DiffResult` / `DiffEmitter` / `WorkDirResolver` 类型定义
  - `git_ops.go` — git root / dirty paths / HEAD 内容读取
  - `unified_diff.go` — before/after 快照 + unified diff 生成（依赖 `go-difflib`）
  - `tracker.go` — `Tracker` / `BeginTracker` / `EmitDiffUpdate` / session 累积
  - `persist.go` — `ThreadDiffSnapshot` 持久化（可选，Phase 1 可跳过）
- **外部依赖**：`github.com/pmezard/go-difflib/difflib`（需 go get）
- **注意**：V2 用 git CLI（`rev-parse`/`diff --name-only`/`ls-files`/`show HEAD:path`），非 git 目录自动禁用
- **守卫**：每文件 ≤400 行，每函数 ≤80 行，CC ≤10

#### Task 1-2: 定义 Diff 事件

- **修改** `internal/dto/shared/event.go` — 新增 `EventTypeToolDiffUpdated`
- **修改** `internal/dto/tool/event.go`（或新建 `diff_event.go`）— 新增 `ToolDiffUpdated` struct：
  ```go
  type ToolDiffUpdated struct {
      shared.ToolCallHeader
      ProviderThreadID string   `json:"providerThreadId"`
      DiffText         string   `json:"diffText"`
      Files            []string `json:"files"`
  }
  ```

#### Task 1-3: toolbridge 集成 difftracker

- **修改** `internal/platform/toolbridge/handler.go`
  - `routeToolCall()` (L38-67) 前后包裹 `BeginTracker` / `EmitDiffUpdate`
  - 已有 `AgentID/ThreadID/CallID`（L149-182 `decodeToolCallRequest` 已解出）
- **修改** `internal/platform/toolbridge/module.go` (L8-16) — 注入 bus dispatcher + WorkDirResolver
- **注意**：需要 agent → cwd 查询能力，用于定位 repo root

#### Task 1-4: uistate 订阅 Diff 事件

- **新建** `internal/module/uistate/projector_diff.go` — 订阅 `ToolDiffUpdated` 事件
  - 写入 `state.DiffTextByThread[threadID] = diffText`
  - 递增 `state.DiffRevisionByThread[threadID]`（仅在内容变化时）
  - 触发 `emitThreadPatch(UIThreadPatch{DiffText, DiffRevision})`
- **修改** `internal/module/uistate/diff_state.go` (L34-60)
  - 返回真实 diff 正文，不再返回空字符串占位
  - 实现真实 unchanged 语义（knownRevision == currentRevision → skip）
- **修改** `internal/module/uistate/patch.go` (L113-147)
  - `threadPatchLocked()` 填充 `DiffText` / `DiffRevision`

#### Task 1-5: UIThreadPatch 补 Diff 字段

- **修改** `internal/dto/ui/event.go` (L60-77) — UIThreadPatch 新增：
  ```go
  DiffText     string `json:"diffText,omitempty"`
  DiffRevision int64  `json:"diffRevision,omitempty"`
  ```

#### Task 1-6: 验证

- 构建验证：`go build ./internal/... ./cmd/...`
- Archtest：`go test -run TestCodeSizeGuard ./internal/archtest/...`
- 手动验证：启动应用 → 让 agent 执行工具 → 右侧面板出现 diff

---

### Phase 2: 工具调用记录（R3 + R4 + R5 + R6）

> 目标：让右下角 ActivityPanel + ChatTimeline 有数据

#### Task 2-1: Timeline Kind 映射对齐

- **修改** `internal/module/uistate/timeline/projector.go`
  - `itemStartedHandler` / `itemCompletedHandler`：按 `item_type` 映射 Kind
    - `item_type=command` → Kind = `"command"`
    - `item_type=file` → Kind = `"file"`
    - 其他 → Kind = `"item"`（兜底）
  - `toolCallBeginHandler` / `toolCallEndHandler`：Kind 从 `"tool_call"` 改为 `"tool"`
  - `approvalRequestedHandler` / `approvalResolvedHandler`：Kind 从 `"approval_request"` 改为 `"approval"`
- **修改** `internal/module/uistate/timeline/timeline.go` (L14-31) — Item 补字段：
  ```go
  Tool      string `json:"tool,omitempty"`      // 工具名（从 tool_name 映射）
  Preview   string `json:"preview,omitempty"`   // 结果预览
  ElapsedMS *int   `json:"elapsedMs,omitempty"` // 耗时
  Output    string `json:"output,omitempty"`    // 命令输出
  ExitCode  *int   `json:"exitCode,omitempty"`  // 退出码
  Done      bool   `json:"done,omitempty"`      // 是否完成
  ```

#### Task 2-2: 实现 ActivityStats

- **修改** `internal/module/uistate/state.go` (L12-32) — 新增：
  ```go
  type ActivityStats struct {
      LSPCalls  int64            `json:"lspCalls"`
      Commands  int64            `json:"commands"`
      FileEdits int64            `json:"fileEdits"`
      ToolCalls map[string]int64 `json:"toolCalls"`
  }
  ```
  - `UIState` 新增 `ActivityStatsByThread map[string]*ActivityStats`
  - 新增 `AlertsByThread map[string][]Alert`（可选）
- **修改** `internal/module/uistate/projector_handlers.go` — 在相关 handler 中累积：
  - `applyItemStarted(item_type=command)` → `stats.Commands++`
  - `applyItemStarted(item_type=file)` → `stats.FileEdits++`
  - `applyToolCallBegin` → `stats.ToolCalls[toolName]++`；若 `lsp_*` 前缀则 `stats.LSPCalls++`
- **修改** `internal/module/uistate/service.go` — `GetState()` 返回 `ActivityStatsByThread`

#### Task 2-3: UIThreadPatch 补 Timeline + Stats 字段

- **修改** `internal/dto/ui/event.go` (L60-77) — UIThreadPatch 新增：
  ```go
  Interruptible  *bool                   `json:"interruptible,omitempty"`
  AgentMeta      map[string]interface{}  `json:"agentMeta,omitempty"`
  ActivityStats  *ActivityStats          `json:"activityStats,omitempty"`
  Alerts         []Alert                 `json:"alerts,omitempty"`
  TimelineItems  []timeline.Item         `json:"timelineItems,omitempty"`
  RemovedItemIds []string                `json:"removedItemIds,omitempty"`
  TimelineOrder  []string                `json:"timelineOrder,omitempty"`
  Recover        bool                    `json:"recover,omitempty"`
  RefreshRequired bool                   `json:"refreshRequired,omitempty"`
  ```
- **修改** `internal/module/uistate/patch.go` (L113-147) — `threadPatchLocked()` 扩展：
  - 携带 timeline 增量（新增/更新的 items）
  - 携带 activityStats
  - 参考 V2 `server_thread_patch.go:120-167`

#### Task 2-4: 前端过滤器修正

- **修改** `cmd/agent-terminal/frontend/vue-app/stores/thread-sync-selectors.js` (L5-18)
  - `STRUCTURAL_TIMELINE_KINDS` 移除 `item`/`tool_call`/`approval_request`
  - 只保留 `turn_start`/`turn_end`/`turn_interrupted` 作为结构性过滤
- **修改** `cmd/agent-terminal/frontend/vue-app/composables/useThreadCards.js` (L91-153)
  - `toProcessActivityItem` 增加对 `tool`/`approval` kind 的处理
- **可选** `components/timeline/useTimelineHelpers.js` — `roleLabel()` 兼容新 kind

#### Task 2-5: 验证

- 构建验证：`go build ./internal/... ./cmd/...`
- Archtest：`go test -run TestCodeSizeGuard ./internal/archtest/...`
- 前端功能验证：启动应用 → 让 agent 执行工具 → 右下角出现活动统计 + timeline

---

### Phase 3: 集成验证 + 收尾

#### Task 3-1: E2E 验证

- 启动应用
- 创建 thread → 让 agent 执行多种工具（LSP/命令/文件编辑）
- 验证：
  - [ ] 右侧 Diff 面板实时显示文件变更
  - [ ] Diff 面板按文件分组、逐行着色
  - [ ] Markdown 预览正常工作
  - [ ] 右下角 stat bar 显示 LSP/命令/文件/工具 计数
  - [ ] 展开后显示每个工具的调用次数
  - [ ] ChatTimeline 中工具调用条目正常渲染（工具名 + 耗时 + 文件 + 预览）
  - [ ] ToolTickerBar 滚动显示工具调用摘要

#### Task 3-2: payload 降级

- 大 diff 文本超过阈值时降级（参考 V2 `payload_too_large` 逻辑）
- 前端通过 `ui/state/get(includeDiff, knownDiffRevision)` 补拉完整 diff

#### Task 3-3: 持久化（可延后）

- difftracker session 持久化（`persist.go`）
- 线程停止/归档时 `ClearThreadState`
- 应用重启后恢复 diff 快照

---

## 3. 文件变更矩阵

### 3.1 新建文件

| 文件 | Phase | 说明 |
|------|-------|------|
| `internal/platform/difftracker/doc.go` | P1 | 包文档 |
| `internal/platform/difftracker/args.go` | P1 | 工具参数解析 |
| `internal/platform/difftracker/emitter.go` | P1 | DiffResult/DiffEmitter 类型 |
| `internal/platform/difftracker/git_ops.go` | P1 | git CLI 操作 |
| `internal/platform/difftracker/unified_diff.go` | P1 | unified diff 生成 |
| `internal/platform/difftracker/tracker.go` | P1 | Tracker 核心逻辑 |
| `internal/module/uistate/projector_diff.go` | P1 | Diff 事件订阅 handler |

### 3.2 修改文件

| 文件 | Phase | 修改点 |
|------|-------|--------|
| `internal/dto/shared/event.go` | P1 | +EventTypeToolDiffUpdated |
| `internal/dto/tool/event.go` | P1 | +ToolDiffUpdated struct |
| `internal/platform/toolbridge/handler.go` | P1 | routeToolCall 包裹 diff tracking |
| `internal/platform/toolbridge/module.go` | P1 | 注入 bus + WorkDirResolver |
| `internal/module/uistate/diff_state.go` | P1 | 返回真实 diff（非空字符串） |
| `internal/module/uistate/patch.go` | P1+P2 | threadPatchLocked 扩展 |
| `internal/dto/ui/event.go` | P1+P2 | UIThreadPatch 补 11 个字段 |
| `internal/module/uistate/timeline/timeline.go` | P2 | Item 补 6 个字段 |
| `internal/module/uistate/timeline/projector.go` | P2 | Kind 映射修正 |
| `internal/module/uistate/state.go` | P2 | +ActivityStats +AlertsByThread |
| `internal/module/uistate/projector_handlers.go` | P2 | Stats 累积逻辑 |
| `internal/module/uistate/service.go` | P2 | GetState 返回 stats |
| `stores/thread-sync-selectors.js` | P2 | 过滤器修正 |
| `composables/useThreadCards.js` | P2 | Kind 兼容 |

---

## 4. 依赖与风险

### 4.1 外部依赖

| 依赖 | 用途 | 风险 |
|------|------|------|
| `github.com/pmezard/go-difflib/difflib` | unified diff 生成 | 低 — 成熟库 |
| `git` CLI | repo root / dirty files / HEAD 内容 | 中 — 非 git 目录需自动禁用 |

### 4.2 风险项

| # | 风险 | 缓解措施 |
|---|------|----------|
| 1 | agent cwd 查询 — difftracker 需要知道 agent 的工作目录 | toolbridge 已有 AgentID，从 agent registry 查 cwd |
| 2 | 大 diff 撑爆 WebSocket | 参考 V2 payload_too_large 降级，前端 fallback 到 ui/state/get 补拉 |
| 3 | 非 git 目录 | tracker.BeginTracker 检测 git root 失败时返回 nil tracker，skip diff |
| 4 | timeline Kind 改动影响现有消费者 | 用 lsp_xref references 全量扫描 Kind 引用，确保无遗漏 |
| 5 | UIThreadPatch 字段膨胀 | 所有新字段用 omitempty，无变化时不发 |

### 4.3 守卫标准

```
⚠️ 守卫红线（违反将触发 archtest 失败）：
- 单文件 ≤400 行
- 单函数 ≤80 行
- 圈复杂度 CC ≤10
- 包非测试文件 ≤15
- 包总行数 ≤4500
- 完成后必须跑 go test -run TestCodeSizeGuard ./internal/archtest/...
```

---

## 5. Agent 分配建议

### Phase 1（5 Agent 并行）

| Agent | 职责 | 文件范围 |
|-------|------|----------|
| A1 | difftracker 包移植 | internal/platform/difftracker/* |
| A2 | Diff 事件定义 + toolbridge 集成 | dto/shared + dto/tool + toolbridge/* |
| A3 | uistate Diff 订阅 + diff_state 修复 | uistate/projector_diff.go + diff_state.go |
| A4 | UIThreadPatch Diff 字段 + patch.go 扩展 | dto/ui/event.go + uistate/patch.go |
| A5 | 单元测试（difftracker + Diff 事件） | *_test.go |

### Phase 2（5 Agent 并行）

| Agent | 职责 | 文件范围 |
|-------|------|----------|
| B1 | Timeline Kind 映射 + Item 字段补全 | timeline/projector.go + timeline.go |
| B2 | ActivityStats 实现 | uistate/state.go + projector_handlers.go |
| B3 | UIThreadPatch Timeline+Stats 字段 | dto/ui/event.go + uistate/patch.go + service.go |
| B4 | 前端过滤器 + Kind 兼容修正 | thread-sync-selectors.js + useThreadCards.js |
| B5 | 单元测试（Timeline + ActivityStats） | *_test.go |

### Phase 3（2 Agent）

| Agent | 职责 |
|-------|------|
| C1 | E2E 集成验证 + payload 降级 |
| C2 | 互审 + 收尾清理 |

---

## 6. 调研报告引用

| 报告 | 路径 | 作者 |
|------|------|------|
| Diff 链路调研 | shared:reports/p16-diff-research.md | p16-diff-researcher |
| 工具调用记录调研 | shared:reports/p16-toolcall-research.md | p16-toolcall-researcher |
| 前端与 Binding 调研 | shared:reports/p16-frontend-research.md | p16-frontend-researcher |
