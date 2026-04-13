# P18 Phase 4：Provider 链路注入

> 预计：1 天 | 依赖：Phase 3, **Phase 4.5**

## 目标
将三层提示词注入到 codex/claude provider 链路。

> **启动条件**：本 Phase 在 Phase 4.5 解耦完成后才能启动。

## 三层注入映射（审查修订 Agent 7）

| Claude Code 层 | API 位置 | V3 落点 |
|----------------|---------|---------|
| System Prompt | `system` 参数 | `thread/start` BaseInstructions |
| System Context | system 尾部追加 | `thread/start` DeveloperInstructions 或 system tail |
| User Context | 前置 synthetic user message | `turn/start` 前置 synthetic 输入 |

> **关键修订**：User Context **不放 thread/start**，而是放 **turn/start 前置**。
> 这与 Claude Code 的真实架构一致：`prependUserContext()` 在 `query.ts:660` 每次调模型前临时装配。

## 两阶段装配

> **硬 gate**：Phase 4.5 未完成前，Phase 4 不得启动实现；本 Phase 的 provider 只消费 assembly 产物，不重新计算 prompt。

### 阶段 A：会话启动（thread/start）
```text
PromptRegistry.BuildSystemPrompt()  →  BaseInstructions
PromptContext.BuildSystemContext()   →  DeveloperInstructions (gitStatus 等)
```

> **❗实施警告**（Agent 24 终审发现）：
> V3 现有代码中 `req.Prompt = FirstNonEmpty(req.Prompt, req.BaseInstructions)`（`start_session.go:18-20`），
> 导致 BaseInstructions 会污染 `Prompt` 字段，进而流入 launch name / thread store / resume metadata。
> **实施前必须先解耦 System Prompt 与 Prompt 元数据语义**，不能让它流进 thread lifecycle。

### 阶段 B：每轮 turn（turn/start 前）
```text
BuildBaseUserContext()          → cached base payload
  - claudeMd（由 SourceResolver + renderer 产出）
  - currentDate（固定句式：Today's date is YYYY-MM-DD.）
MergeRuntimeUserContext()       → merge turn-scoped extras
  - (未来) coordinator workerToolsContext
  - (未来) terminalFocus
FormatUserContextMessage()      → 前置 synthetic user message
  - hidden/meta
  - <system-reminder>...</system-reminder>
  - # key 分节 + relevance disclaimer
```

补充约束：
- 基础 UserContext 只有 `claudeMd + currentDate`；turn 级 extras 不得误缓存进会话级 base payload
- `claudeMd` 被禁用、payload 为空、测试 bypass 场景下，不注入 synthetic message
- turn 注入语义按 **per-attempt / per-turn assemble** 实现，不能固化进 thread/start lifecycle
- Codex 必须同时覆盖 `turn/start` 与 `turn/steer`
- Claude 必须让普通 turn / steer 复用同一 prepend helper，避免顺序漂移
- provider 只消费 assembly 产物，不在 provider 内部二次构造 UserContext

## User Context 的 claudeMd 来源（完整）

不只是 CLAUDE.md，而是聚合：
1. Managed CLAUDE.md + rules
2. User CLAUDE.md + rules
3. Project CLAUDE.md / .claude/CLAUDE.md / .claude/rules
4. Local CLAUDE.local.md
5. AutoMem MEMORY.md 入口文件（受 `filterInjectedMemoryFiles()` 过滤语义影响）
6. 若未来恢复 Team Memory，还包含 team memory 注入片段；源码会以 `team-memory-content` 包装，但 **P18 本轮不实现该分支**

实现约束：
- 先 `ResolveClaudeMdSources() ([]ClaudeMdSource)`，再统一 render；不要把来源解析压扁成“只返回一个字符串”
- `.claude/rules` 必须区分 **unconditional rules** 与 **conditional rules**：只有 unconditional rules 进入基础 `claudeMd`，带 frontmatter/globs 的 conditional rules 保留给后续 attachment/target-path 链
- 过滤语义分两段：先按 `filterInjectedMemoryFiles()` 过滤 AutoMem/TeamMem 注入文件，再按 Project/Local settingSources 决定是否跳过
- 普通注入文件使用 `Contents of {path}: ...` 形式，不是裸拼接
- `getClaudeMds()` / filter 链会先做注入文件筛选，再做最终包装

> **来源**：`restored-src/src/context.ts:155-189`
> **来源**：`restored-src/src/utils/claudemd.ts:1142-1195`

## System Context 内容

```go
type SystemContext struct {
    GitStatus string // git 仓库状态摘要
    // cacheBreaker: V3 不实现（debug/cache-bust 机制）
}
```

补充约束：
- System Context 最终渲染为**一个** system tail block，不拆成多段 runtime metadata
- `approvalPolicy` / `sandbox` / `summary` / `effort` / `personality` 继续走 runtime flags / config，不在本 Phase 混入 `SystemContext`

> **来源**：`restored-src/src/context.ts:116-149`

## V3 当前实现锚点

| 文件 | 行 | 内容 |
|------|-----|------|
| `internal/module/thread/contract.go` | 34-50 | StartRequest (BaseInstructions/DeveloperInstructions) |
| `internal/provider/codexapp/support.go` | 248-255 | buildThreadStartParams() |
| `internal/provider/codexapp/session_turn.go` | 37-84 | buildTurnStartParams() + turn 输入拼装 |
| `internal/provider/claudecli/session.go` | 137-145 | runtime snapshot |

## 统一 Builder（不复制两套）

> **审查修订**（Agent 7）：不复制 Claude 的 REPL/SDK 两套组装。
> 用一个共享 `PromptAssemblyService` 统一产出 SystemPromptParts + SystemContext + UserContext。
> Provider 只传不同 policy（是否 custom prompt、是否 coordinator）。
> cache boundary 也应收口到 `PromptAssemblyService`：provider 只消费 assembly 产物，不各自维护一套 user-context/system-tail 拼装缓存。

## 任务清单
- [ ] `prompt/context.go`：`BuildBaseUserContext()` + `MergeRuntimeUserContext()` + `FormatUserContextMessage()` + `BuildSystemContext()`
- [ ] 修改 `internal/provider/codexapp/support.go:248-261`：thread/start 接入 PromptAssemblyService（通过 start_session.go 注入，不在 provider 内部）
- [ ] 修改 `internal/provider/codexapp/session_turn.go:37-49,76-85`：`turn/start` + `turn/steer` 前注入 UserContext
- [ ] 修改 `internal/provider/claudecli/transport_config.go:129`：Claude CLI launch prompt 接入
- [ ] 修改 Claude turn helper：普通 turn / steer 共用 UserContext prepend 逻辑
- [ ] 缓存失效注册：`/clear`、`/compact`、worktree、`/resume`、provider switch、auto-compact、partial compact

## 验收
- thread/start 的 instructions 来自 PromptAssemblyService.AssembleStart()
- Codex `turn/start` 与 `turn/steer` 都正确前置 UserContext
- Claude 普通 turn / steer 顺序一致，且 provider 未二次构造 UserContext
- 缓存失效测试
