# P18 Phase 4：Provider 链路注入

> 预计：1 天 | 依赖：Phase 3

## 目标
将三层提示词注入到 codex/claude provider 链路。

## 三层注入映射（审查修订 Agent 7）

| Claude Code 层 | API 位置 | V3 落点 |
|----------------|---------|---------|
| System Prompt | `system` 参数 | `thread/start` BaseInstructions |
| System Context | system 尾部追加 | `thread/start` DeveloperInstructions 或 system tail |
| User Context | 前置 synthetic user message | `turn/start` 前置 synthetic 输入 |

> **关键修订**：User Context **不放 thread/start**，而是放 **turn/start 前置**。
> 这与 Claude Code 的真实架构一致：`prependUserContext()` 在 `query.ts:660` 每次调模型前临时装配。

## 两阶段装配

### 阶段 A：会话启动（thread/start）
```
PromptRegistry.BuildSystemPrompt()  →  BaseInstructions
PromptContext.BuildSystemContext()   →  DeveloperInstructions (gitStatus 等)
```

> **❗实施警告**（Agent 24 终审发现）：
> V3 现有代码中 `req.Prompt = FirstNonEmpty(req.Prompt, req.BaseInstructions)`（`start_session.go:18-20`），
> 导致 BaseInstructions 会污染 `Prompt` 字段，进而流入 launch name / thread store / resume metadata。
> **实施前必须先解耦 System Prompt 与 Prompt 元数据语义**，不能让它流进 thread lifecycle。

### 阶段 B：每轮 turn（turn/start 前）
```
PromptContext.BuildUserContext()     →  前置 synthetic 输入
  - claudeMd (MEMORY.md 索引 + CLAUDE.md 指令文件)
  - currentDate
  - (未来) coordinator workerToolsContext
  - (未来) terminalFocus
  注意：基础 UserContext 只有 claudeMd+currentDate，
  最终送模前还会 merge 运行时 extras
```

## User Context 的 claudeMd 来源（完整）

不只是 CLAUDE.md，而是聚合：
1. Managed CLAUDE.md + rules
2. User CLAUDE.md + rules
3. Project CLAUDE.md / .claude/CLAUDE.md / .claude/rules
4. Local CLAUDE.local.md
5. AutoMem MEMORY.md 入口文件（受 filter 影响）

包装格式：`Contents of {path}: ...`（不是裸拼接）

> **来源**：`restored-src/src/context.ts:155-189`

## System Context 内容

```go
type SystemContext struct {
    GitStatus string // git 仓库状态摘要
    // cacheBreaker: V3 不实现（debug/cache-bust 机制）
}
```

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

## 任务清单
- [ ] `prompt/context.go`：BuildUserContext() + BuildSystemContext()
- [ ] 修改 `internal/provider/codexapp/session.go`：thread/start 接入 PromptRegistry
- [ ] 修改 `internal/provider/codexapp/session_turn.go`：turn/start 前注入 UserContext
- [ ] 修改 `internal/provider/claudecli/session.go`：同步接入
- [ ] 缓存失效注册：/clear, /compact, worktree, /resume

## 验收
- thread/start 的 instructions 来自 PromptRegistry.Build()
- turn/start 前 UserContext 被正确前置
- 缓存失效测试
