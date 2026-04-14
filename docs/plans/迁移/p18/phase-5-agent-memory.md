# P18 Phase 5：Agent 记忆隔离

> 预计：1 天 | 依赖：Phase 1、Phase 4.5（thread/start prompt assembly + snapshot 落点）

## 目标
实现 Agent 专用记忆，支持三种 scope，与主线程记忆隔离。

## 三种 Scope 目录结构（审查修订 Agent 8）

| scope | 目录 | 语义 |
|-------|------|------|
| user | `<memoryBase>/agent-memory/<sanitized-agentType>/` | 全局，跨项目 |
| project | `<projectRoot>/.claude/agent-memory/<sanitized-agentType>/` | 项目级，可 git 管理 |
| local | `<projectRoot>/.claude/agent-memory-local/<sanitized-agentType>/` | 持久但不进版本控制 |

> **关键修订**：`local` **是持久但不进版本控制**。
> `projectRoot` 指 **canonical git root，fallback 当前 launch/resolved project root**；`project/local` 两种 scope 必须锚定同一个解析结果，不能随子目录 cwd 漂移。
> 若设置 `CLAUDE_CODE_REMOTE_MEMORY_DIR`，local 改写到：
> `<remote>/projects/<sanitizePath(canonicalGitRoot ?? projectRoot)>/agent-memory-local/<sanitized-agentType>/`
> 注：项目根路径会先走 `sanitizePath()`（`sessionStoragePortable.ts:311-319`）

## Agent Type 隔离

`sanitizeAgentTypeForPath(agentType)` 清洗规则：
- Claude parity 最小规则：`:` → `-`
- V3 安全增强：先做 NFC 归一化，再拒绝空串、`.`、`..`、`/`、`\\`、控制字符、bidi 字符、Windows 保留名、末尾空格/点等跨平台危险段
- **默认保留原始可读拼写与大小写**；安全 ASCII 段至少允许 `[A-Za-z0-9._-]`，`Writer` 与 `writer` 这类合法 agentType **不能**因为 sanitize 被无条件折叠到同一路径
- 不得直接复用 project path slug 那类全量 lower-case 规则；agentType 需要独立 sanitizer，避免把“可读标识”误处理成“路径 slug”
- 仅在**危险或冲突**场景退化为 `readable-prefix + short hash`：如规范化后无法安全表达、在大小写不敏感文件系统上与现有 segment 冲突、或截断后前缀冲突
- 超长时截断并追加短 hash
- 清洗后若为空，直接拒绝，不创建目录

> **来源**：`restored-src/src/tools/AgentTool/agentMemory.ts:20-22`
> **审查补充**：不要直接复用 `internal/module/memory/path.go:88-115` 这类 project path `SanitizePath()` 的 lower-case slug 行为；AgentType 目录名需要单独规则。

## 实现前置门禁（审查补充）

- 当前 V3 虽已定义 `PromptAssemblyService.AssembleStart()` / `StartAssembly.Snapshot`（`internal/module/prompt/service.go:104-126`，`internal/module/prompt/types.go:54-72`），但 `thread/start` 仍在 `internal/module/thread/start_session.go:136-154` 直接把 `req.BaseInstructions/req.Prompt` 送入 provider DTO。
- `internal/module/thread/lifecycle.go:50-102,104-170` 当前持久化/恢复的仍是 `req.Prompt` / `state.Prompt`，不是 `StartAssembly.Snapshot`；若不同步打通，agent memory 很容易出现 **preview 有、runtime 缺** 或 **start 有、resume 丢** 的漂移。
- 因此本 phase 不能只实现 memory builder；必须与 Phase 4.5 的 start/resume prompt assembly 落点同批闭环，或明确标记 blocked。

## Agent Memory 特例路径

Agent 记忆**不走主线程 `claudeMd` 注入**，也**不进入** `TurnAssembly.UserContextText` / relevant-memory attachment-hint 链；而是由专用 builder 产物在 agent 启动时被 `PromptAssemblyService.AssembleStart()` 吸收到该 agent 的 `StartAssembly.BaseInstructions`。每个 agent 定义一次只启用**一个** scope，不做 `user/project/local` 三层 merge：

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

## 可见性 / ACL 边界

- `GetAgentMemoryDir(scope, agentType)` 只负责在 canonical root 上解析候选目录；**读写/检索/预览前必须再过统一 authorizer**，不能把“能算出路径”当成“允许访问”
- `user` scope：仅 root agent / 当前线程授权链可读写；未进入当前线程可见集的 agentType 即使被 `@` 提及，也不能解析到其目录
- `project` scope：仅同 project / 同 workspace 授权链可访问；canonical git root 不匹配时直接 deny，不得回退到别的 memory root
- `local` scope：仅当前机器 + 当前 project 授权链可访问；跨机器 replay / restore 必须视为 unavailable，而不是静默降级成 project/user scope
- Phase 6 的 `@agent` relevant memory 检索与 Phase 7 的 Memory 工具必须共享同一 `sanitize + resolve + authorize` 流程；未命中或未授权时返回空结果/显式 deny reason，不得偷偷 fallback 到主 memory 根目录
- preview/runtime/tooling 共用同一 access contract，避免 UI 预览能看见、运行时却读取失败（或反过来）

## 任务清单
- [ ] `memory/agent_memory.go`：GetAgentMemoryDir(scope, agentType) / GetAgentMemoryEntrypoint() / LoadAgentMemoryPrompt()
- [ ] `memory/agent_memory_paths.go`：IsAgentMemoryPath()（供检索/附件链识别 auto-managed memory）
- [ ] `memory/sanitize.go`：SanitizeAgentTypeForPath()（保留可读拼写与大小写，不复用 project path 的 lower-case slug 规则；仅在危险/冲突场景追加 hash）
- [ ] `memory/agent_memory_access.go`：统一 `sanitize + resolve + authorize`，供 `@agent` 检索 / 预览 / Memory 工具复用
- [ ] 与 Phase 4.5 同步打通 `thread/start` / `resume` → `PromptAssemblyService.AssembleStart()` → `StartAssembly.Snapshot` 持久化/恢复，避免 agent memory 只在单次 start 生效
- [ ] 子 Agent 提示词自动注入通用记忆规则 + `MEMORY.md` 内容，并通过 `PromptAssemblyService.AssembleStart()` 并入子 agent `StartAssembly.BaseInstructions`
- [ ] fire-and-forget 目录创建（失败仅记 debug / metric，不阻断 prompt 构建）
- [ ] agent memory 读取 telemetry + 目录统计埋点
- [ ] access deny / not visible / cross-machine local-unavailable 结构化日志与 metric
- [ ] snapshot sync 若本期不做 parity，需在 Phase 7 明确标成 out-of-scope

## 验收
- 不同 agentType 在同一 scope 下隔离到不同目录
- 大小写不同但均合法的 agentType 不会被 sanitize 意外并桶；只有危险/冲突场景才退化为 `readable-prefix + short hash`
- `LoadAgentMemoryPrompt()` 复用通用记忆规则骨架，`MEMORY.md` 内容正确内联到 agent prompt，并稳定并入 `StartAssembly.BaseInstructions`
- `start → persist → resume` 全链路保持同一份 agent memory prompt / snapshot，不出现 preview/runtime 或 start/resume 漂移
- `not_found/empty` 与 `unreadable/corrupt` 两类空态被区分处理
- 未授权或当前线程不可见的 agent memory 不会被 `@agent` 检索、预览或 Memory 工具误读
- JSON/Markdown/Plugin/Wizard 预览入口拿到一致的 agent memory prompt
- telemetry 能区分 agent memory 截断、access deny 与目录统计
- 截断测试
