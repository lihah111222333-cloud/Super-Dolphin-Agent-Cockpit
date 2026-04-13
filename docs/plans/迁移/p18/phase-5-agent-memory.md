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
- Claude parity 最小规则：`:` → `-`
- V3 安全增强：先做 NFC 归一化，再拒绝空串、`.`、`..`、`/`、`\\`、控制字符、bidi 字符、Windows 保留名
- 最终落盘段只允许安全子集 `[a-z0-9._-]`；无法安全表达时退化为 hash segment
- 超长时截断并追加短 hash
- 清洗后若为空，直接拒绝，不创建目录

> **来源**：`restored-src/src/tools/AgentTool/agentMemory.ts:20-22`

## Agent Memory 特例路径

Agent 记忆**不走主线程 `claudeMd` 注入**，而是专用路径；每个 agent 定义一次只启用**一个** scope，不做 `user/project/local` 三层 merge：

```text
loadAgentMemoryPrompt(scope, agentType)
  → void ensureMemoryDirExists(memoryDir)   // fire-and-forget 预热；失败只记 debug，不阻断 prompt 构建
  → buildMemoryPrompt(memoryDir, extraGuidelines)
    → buildMemoryLines(...)                  // 先生成通用记忆规则骨架
    → readFileSync(MEMORY.md)                // 同步读取 entrypoint
    → truncateEntrypointContent()            // 先按 200 行截，再按 25KB 截，截断后追加 warning
    → 末尾追加 "## MEMORY.md\n{content|empty-placeholder}"
```

通用骨架必须显式包含：
- 持久记忆系统说明
- what to save / what not to save
- how to save memories
- memory vs plan/tasks
- searching past context
- `extraGuidelines` 扩展位

读取结果分级：
- `not_found` / `trim 后为空` → 注入空态占位 `## MEMORY.md` + `Your MEMORY.md is currently empty...`
- `unreadable` / `corrupt` → 注入 degraded warning + 空态占位，并记录 observability；**不能伪装成正常空文件**
- 读取 entrypoint 后记录 telemetry：`content_length` / `line_count` / `was_truncated` / `was_byte_truncated` / `memory_type=agent`

Scope 注释（每种 scope 会追加说明）：
- user: 通用跨项目
- project: 面向项目且可经 VCS 共享
- local: 面向项目+机器
- 可能追加 `CLAUDE_COWORK_MEMORY_EXTRA_GUIDELINES`

## Scope 选择规则

- Claude parity：**per-agent 只使用一个 scope**；不存在 `user → project → local` 叠加加载
- 不存在 `local > project > user` 覆盖规则；若 V3 未来要支持多层 merge，必须作为增强项单列，不写成 P18 parity
- `LoadAgentMemoryPrompt()`、预览 UI、JSON/Markdown/Plugin agent 入口都必须复用同一 builder，避免 preview/runtime 漂移
- Phase 6 的 `@agent` relevant memories 必须复用同一 `GetAgentMemoryDir(scope, agentType)` 规则

> **来源**：`restored-src/src/memdir/memdir.ts:272-316` (buildMemoryPrompt)
> **来源**：`restored-src/src/tools/AgentTool/agentMemory.ts:138-176` (loadAgentMemoryPrompt)

## 任务清单
- [ ] `memory/agent_memory.go`：GetAgentMemoryDir(scope, agentType) / GetAgentMemoryEntrypoint() / LoadAgentMemoryPrompt()
- [ ] `memory/agent_memory_paths.go`：IsAgentMemoryPath()（供检索/附件链识别 auto-managed memory）
- [ ] `memory/sanitize.go`：SanitizeAgentTypeForPath()
- [ ] 子 Agent 提示词自动注入通用记忆规则 + `MEMORY.md` 内容
- [ ] fire-and-forget 目录创建（失败仅记 debug / metric，不阻断 prompt 构建）
- [ ] agent memory 读取 telemetry + 目录统计埋点
- [ ] snapshot sync 若本期不做 parity，需在 Phase 7 明确标成 out-of-scope

## 验收
- 不同 agentType 在同一 scope 下隔离到不同目录
- `LoadAgentMemoryPrompt()` 复用通用记忆规则骨架，`MEMORY.md` 内容正确内联到 prompt
- `not_found/empty` 与 `unreadable/corrupt` 两类空态被区分处理
- JSON/Markdown/Plugin/Wizard 预览入口拿到一致的 agent memory prompt
- telemetry 能区分 agent memory 截断与目录统计
- 截断测试
