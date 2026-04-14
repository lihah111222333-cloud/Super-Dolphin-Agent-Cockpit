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
| User Context | 前置 synthetic user message | `TurnAssembly.UserContextText` → codex 前置 synthetic text input / claude provider-local prepend block |

> **关键修订**：User Context **不放 thread/start**，而是放 **turn 级 assemble 后的 provider-specific 前置位**。
> 这与 Claude Code 的真实架构一致：`prependUserContext()` 在 `query.ts:660` 每次调模型前临时装配；V3 再由 thread/turn adapter 分别映射到 codex synthetic text input 与 claude provider-local prepend block。

## 两阶段装配

> **硬 gate**：Phase 4.5 未完成前，Phase 4 不得启动实现；本 Phase 的 provider 只消费 assembly 产物，不重新计算 prompt。

### 合规前置
- `internal/provider/*` 不得 import `internal/module/prompt`
- `PromptAssemblyService` 这类跨 layer 接口放 `internal/contract/*`
- `StartInput` / `TurnInput` / `StartAssembly` / `TurnAssembly` / `PromptAssemblySnapshot` 这类跨 layer 载荷放 `internal/dto/*`
- `internal/module/prompt` 只保留实现、registry、context 等模块内部细节；provider / `cmd/mcp-*` 只消费 contract + dto 产物

### 阶段 A：会话启动（thread/start）
```text
PromptAssemblyService.AssembleStart()
  → PromptRegistry.BuildSystemPrompt()  → BaseInstructions
  → PromptContext.BuildSystemContext()  → DeveloperInstructions (gitStatus 等)
  → StartAssembly{BaseInstructions, DeveloperInstructions, Snapshot}
```

> **❗实施警告**（Agent 24 终审发现）：
> V3 现有代码中 `req.Prompt = FirstNonEmpty(req.Prompt, req.BaseInstructions)`（`start_session.go:18-20`），
> 导致 BaseInstructions 会污染 `Prompt` 字段，进而流入 launch name / thread store / resume metadata。
> **实施前必须先解耦 System Prompt 与 Prompt 元数据语义**，不能让它流进 thread lifecycle。

### 阶段 B：每轮 turn（turn/start 前）
```text
PromptAssemblyService.AssembleTurn()
  → BuildBaseUserContext()          → cached base payload
    - claudeMd（由 SourceResolver + renderer 产出）
    - currentDate（固定句式：Today's date is YYYY-MM-DD.）
  → MergeRuntimeUserContext()       → merge turn-scoped extras
    - (未来) coordinator workerToolsContext
    - (未来) terminalFocus
  → FormatUserContextMessage()      → TurnAssembly.UserContextText
    - hidden/meta
    - <system-reminder>...</system-reminder>
    - # key 分节 + relevance disclaimer
```

补充约束：
- 基础 UserContext 只有 `claudeMd + currentDate`；turn 级 extras 不得误缓存进跨 turn 复用的 base payload
- `claudeMd` 被禁用、payload 为空、测试 bypass 场景下，不注入 synthetic message
- turn 注入语义按 **per-attempt / per-turn assemble** 实现，不能固化进 thread/start lifecycle
- `TurnAssembly.UserContextText` 是唯一交接载体；thread/turn 层负责把它交给 provider adapter，**禁止** provider 自己再从 runtime 状态二次拼装同内容
- `codex` 路径：`TurnAssembly.UserContextText` 由 thread/turn adapter 消费为**前置 synthetic text input**（在 skill prompt 之后），同时覆盖 `turn/start` 与 `turn/steer`
- `claude` 路径：`TurnAssembly.UserContextText` 由 `prepareTurnLocked()` 消费为 **provider-local prepend block**；**不回写**普通 `dto.TurnRequest.Inputs`，普通 turn / steer 必须复用同一 helper，避免顺序漂移
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
- 发现顺序按 **Managed → User → root→cwd Project/Local → additional dirs → AutoMem → TeamMem(未来)** 收敛；解析层负责 normalized-path / symlink 去重，并保留 nested worktree 特判
- `.claude/rules` 必须区分 **unconditional rules** 与 **conditional rules**：只有 unconditional rules 进入基础 `claudeMd`，带 frontmatter/globs 的 conditional rules 保留给后续 attachment/target-path 链
- 过滤语义分两段：先按 `filterInjectedMemoryFiles()` 过滤 AutoMem/TeamMem 注入文件，再按 Project/Local source gate 决定是否跳过
- external include / additional dirs / bare mode / disable gates 属于 SourceResolver 责任；普通 load 与 warning path 必须区分，不把审批前探测与正常装配混成一条链
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
> 用一个共享 `PromptAssemblyService` 统一产出 `StartAssembly / TurnAssembly`（内部再调用 `PromptRegistry + PromptContext`）。
> Provider 只传不同 policy（是否 custom prompt、是否 coordinator）。
> cache boundary 也应收口到 `PromptAssemblyService`：provider 只消费 assembly 产物，不各自维护一套 user-context/system-tail 拼装缓存。

## 任务清单
- [ ] 先落 `internal/contract/*` 中的 `PromptAssemblyService` 契约，以及 `internal/dto/*` 中的 `StartInput / TurnInput / StartAssembly / TurnAssembly / PromptAssemblySnapshot`
- [ ] `prompt/context.go`：`BuildBaseUserContext()` + `MergeRuntimeUserContext()` + `FormatUserContextMessage()` + `BuildSystemContext()`
- [ ] 修改 `internal/provider/codexapp/support.go:248-261`：thread/start 接入 `PromptAssemblyService.AssembleStart()`，消费 `StartAssembly.BaseInstructions / DeveloperInstructions / Snapshot`（通过 start_session.go / thread adapter 注入，不在 provider 内部，也不新增对 `internal/module/prompt` 的 import）
- [ ] 修改 `internal/provider/codexapp/session_turn.go:37-49,76-85`：`turn/start` + `turn/steer` 消费 `TurnAssembly.UserContextText`，映射为前置 synthetic text input（在 skill prompt 之后）
- [ ] 修改 `internal/provider/claudecli/transport_config.go:129`：Claude CLI launch prompt 接入 `StartAssembly`
- [ ] 修改 Claude turn helper：普通 turn / steer 共用 `TurnAssembly.UserContextText` prepend helper，消费为 provider-local prepend block，且不把 UserContext 塞回普通 user inputs
- [ ] 明确 turn 层与 provider adapter 的 `TurnAssembly` 交接契约，不允许再走 `FirstNonEmpty(req.BaseInstructions, req.Prompt)` 兜底
- [ ] 缓存失效注册：`/clear`、`/compact`、worktree、`/resume`、provider switch、auto-compact、partial compact

## 验收
- thread/start 消费 `PromptAssemblyService.AssembleStart()` 返回的 `StartAssembly{BaseInstructions, DeveloperInstructions, Snapshot}`
- `start_session.go:146-152` 不再用 `Prompt` 兜底 provider `Instructions`
- Codex `turn/start` 与 `turn/steer` 都把 `TurnAssembly.UserContextText` 映射为前置 synthetic text input（在 skill prompt 之后）
- Claude 普通 turn / steer 顺序一致，且 `TurnAssembly.UserContextText` 只作为 provider-local prepend block 消费，不回写普通用户输入，provider 也不二次构造 UserContext
- 缓存失效测试
