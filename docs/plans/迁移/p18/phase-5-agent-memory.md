# P18 Phase 5：Agent 记忆隔离

> 预计：1 天 | 依赖：Phase 1

## 目标
实现 Agent 专用记忆，支持三种 scope，与主线程记忆隔离。

## 三种 Scope 目录结构（审查修订 Agent 8）

| scope | 目录 | 语义 |
|-------|------|------|
| user | `<memoryBase>/agent-memory/<sanitized-agentType>/` | 全局，跨项目 |
| project | `<cwd>/.claude/agent-memory/<sanitized-agentType>/` | 项目级，可 git 管理 |
| local | `<cwd>/.claude/agent-memory-local/<sanitized-agentType>/` | 持久但不进版本控制 |

> **关键修订**：`local` **不是会话级**，是持久化但不版本控制。
> 若设置 `CLAUDE_CODE_REMOTE_MEMORY_DIR`，local 改写到：
> `<remote>/projects/<sanitizePath(canonicalGitRoot ?? projectRoot)>/agent-memory-local/<sanitized-agentType>/`
> 注：项目根路径会先走 `sanitizePath()`（`sessionStoragePortable.ts:311-319`）

## Agent Type 隔离

`sanitizeAgentTypeForPath(agentType)` 清洗规则：
- 当前实现最小规则：`:` → `-`
- 源码中未见其他清洗，V3 实现时可按需扩展

> **来源**：`restored-src/src/tools/AgentTool/agentMemory.ts:20-22`

## Agent Memory 特例路径

Agent 记忆**不走主线程 `claudeMd` 注入**，而是专用路径：

```
loadAgentMemoryPrompt()
  → void ensureMemoryDirExists(memoryDir)   // fire-and-forget 预热
  → buildMemoryPrompt()
    → readFileSync(MEMORY.md)               // 同步读取
    → truncateEntrypointContent()            // 先按 200 行截，再按 25KB 截，截断后追加 warning
    → 直接内联到 prompt: "## MEMORY.md\n{content}"
```

空态处理：
- MEMORY.md 缺失/不可读/trim 后为空 → 写入空态占位 `## MEMORY.md` + `Your MEMORY.md is currently empty...`

Scope 注释（每种 scope 会追加说明）：
- user: 通用跨项目
- project: 面向项目且可经 VCS 共享
- local: 面向项目+机器
- 可能追加 `CLAUDE_COWORK_MEMORY_EXTRA_GUIDELINES`

## 加载顺序与冲突规则

- 默认加载顺序：`user → project → local`
- 同名 memory 冲突时，按**更具体 scope 覆盖更宽 scope**：`local > project > user`
- 子 agent 默认仍加载三种 scope；仅在显式禁用某 scope 时裁剪，不做隐式只读某一层
- render 时保留 scope 注释，避免“内容相同但来源不同”造成串味

> **来源**：`restored-src/src/memdir/memdir.ts:272-316` (buildMemoryPrompt)
> **来源**：`restored-src/src/tools/AgentTool/agentMemory.ts:138-176` (loadAgentMemoryPrompt)

## 任务清单
- [ ] `memory/agent_memory.go`：GetAgentMemoryDir(scope, agentType) / LoadAgentMemoryPrompt()
- [ ] `memory/sanitize.go`：SanitizeAgentTypeForPath()
- [ ] 子 Agent 提示词自动注入记忆规则 + MEMORY.md 内容
- [ ] fire-and-forget 目录创建

## 验收
- 不同 agentType 隔离到不同目录
- MEMORY.md 内容正确内联到 prompt
- 空 MEMORY.md 不报错
- 截断测试
