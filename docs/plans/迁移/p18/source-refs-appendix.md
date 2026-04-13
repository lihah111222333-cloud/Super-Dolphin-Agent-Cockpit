# P18 源码锚点附录

> 源码基线：Claude restored-src 本地镜像（示例路径：`/Users/mac/Desktop/agnet/claude/claude-code-sourcemap/restored-src/`，**仅作个人环境示例，不是仓库契约**）
> 锚点维护说明：当前行号对应 2026-04-14 审查快照；若 restored-src 镜像更新，需同步刷新本附录并在 README / review-summary 中更新基线说明。

## 记忆系统

| 函数/类型 | 文件 | 行范围 |
|-----------|------|--------|
| loadMemoryPrompt | src/memdir/memdir.ts | L419-507 |
| buildMemoryLines | src/memdir/memdir.ts | L199-266 |
| buildAssistantDailyLogPrompt | src/memdir/memdir.ts | L327-370 |
| buildCombinedMemoryPrompt | src/memdir/teamMemPrompts.ts | L22-100 |
| buildMemoryPrompt (agent) | src/memdir/memdir.ts | L272-316 |
| loadAgentMemoryPrompt | src/tools/AgentTool/agentMemory.ts | L138-177 |
| sanitizeAgentTypeForPath | src/tools/AgentTool/agentMemory.ts | L20-22 |
| getAgentMemoryDir | src/tools/AgentTool/agentMemory.ts | L52-65 |
| MEMORY_TYPES | src/memdir/memoryTypes.ts | L14-19 |
| MemoryType | src/memdir/memoryTypes.ts | L21 |
| parseMemoryType | src/memdir/memoryTypes.ts | L28-31 |
| TYPES_SECTION_COMBINED | src/memdir/memoryTypes.ts | L37-106 |
| TYPES_SECTION_INDIVIDUAL | src/memdir/memoryTypes.ts | L113-178 |
| WHAT_NOT_TO_SAVE_SECTION | src/memdir/memoryTypes.ts | L183-195 |
| MEMORY_FRONTMATTER_EXAMPLE | src/memdir/memoryTypes.ts | L261-271 |
| getAutoMemPath | src/memdir/paths.ts | L223-235 |
| getMemoryBaseDir | src/memdir/paths.ts | L85-90 |
| validateMemoryPath | src/memdir/paths.ts | L109-150 |
| findCanonicalGitRoot | src/utils/git.ts | L195 (export), L197-210 (impl) |
| sanitizePath | src/utils/sessionStoragePortable.ts | L311-319 |
| isAutoMemoryEnabled | src/memdir/paths.ts | L30-55 |
| isTeamMemoryEnabled | src/memdir/teamMemPaths.ts | L73-78 |
| getTeamMemPath | src/memdir/teamMemPaths.ts | L84-86 |
| getTeamMemEntrypoint | src/memdir/teamMemPaths.ts | L92-94 |
| validateTeamMemWritePath | src/memdir/teamMemPaths.ts | L228-256 |
| validateTeamMemKey | src/memdir/teamMemPaths.ts | L265-284 |
| realpathDeepestExisting | src/memdir/teamMemPaths.ts | L109-171 |
| MAX_ENTRYPOINT_LINES/BYTES | src/memdir/memdir.ts | L34-38 |
| truncateEntrypointContent | src/memdir/memdir.ts | L57-103 |
| parseMemoryFileContent | src/utils/claudemd.ts | L343-400 |
| scanMemoryFiles | src/memdir/memoryScan.ts | L35-77 |
| formatMemoryManifest | src/memdir/memoryScan.ts | L84-94 |
| MAX_MEMORY_FILES | src/memdir/memoryScan.ts | L21 |
| FRONTMATTER_MAX_LINES | src/memdir/memoryScan.ts | L22 |
| findRelevantMemories | src/memdir/findRelevantMemories.ts | L39-75 |
| selectRelevantMemories | src/memdir/findRelevantMemories.ts | L77-141 |
| startRelevantMemoryPrefetch | src/utils/attachments.ts | L2361-2424 |
| getRelevantMemoryAttachments | src/utils/attachments.ts | L2196-2242 |
| collectSurfacedMemories | src/utils/attachments.ts | L2251-2266 |
| relevant memory prefetch start | src/query.ts | L301-304 |
| relevant memory consume in tool loop | src/query.ts | L1599-1613 |

## 系统提示词

| 函数/类型 | 文件 | 行范围 |
|-----------|------|--------|
| getSystemPrompt | src/constants/prompts.ts | L444-577 |
| 静态装配锚点 | src/constants/prompts.ts | L560-571 |
| 动态 slots 注册 | src/constants/prompts.ts | L491-555 |
| getSimpleIntroSection | src/constants/prompts.ts | L175-184 |
| getSimpleSystemSection | src/constants/prompts.ts | L186-197 |
| getSimpleDoingTasksSection | src/constants/prompts.ts | L199-253 |
| getActionsSection | src/constants/prompts.ts | L255-267 |
| getUsingYourToolsSection | src/constants/prompts.ts | L269-314 |
| getOutputEfficiencySection | src/constants/prompts.ts | L403-428 |
| getSimpleToneAndStyleSection | src/constants/prompts.ts | L430-442 |
| computeSimpleEnvInfo | src/constants/prompts.ts | L651-710 |
| getLanguageSection | src/constants/prompts.ts | L142-149 |
| SYSTEM_PROMPT_DYNAMIC_BOUNDARY | src/constants/prompts.ts | L114-115 |
| systemPromptSection | src/constants/systemPromptSections.ts | L20-25 |
| DANGEROUS_uncachedSystemPromptSection | src/constants/systemPromptSections.ts | L32-38 |
| resolveSystemPromptSections | src/constants/systemPromptSections.ts | L43-58 |
| clearSystemPromptSections | src/constants/systemPromptSections.ts | L65-68 |
| systemPromptSectionCache | src/bootstrap/state.ts | L203, L1641-1654 |

## Context 注入

| 函数/类型 | 文件 | 行范围 |
|-----------|------|--------|
| getUserContext | src/context.ts | L155-189 |
| getSystemContext | src/context.ts | L116-150 |
| getClaudeMds / filterInjectedMemoryFiles | src/utils/claudemd.ts | L1142-1195 |
| prependUserContext | src/utils/api.ts | L449-474 |
| appendSystemContext | src/utils/api.ts | L437-447 |
| splitSysPromptPrefix | src/utils/api.ts | L321-435 |
| buildSystemPromptBlocks | src/services/api/claude.ts | L3213-3237 |
| computeEnvInfo | src/constants/prompts.ts | L606-649 |
| getMcpInstructionsSection | src/constants/prompts.ts | L160-165 |
| buildEffectiveSystemPrompt | src/utils/systemPrompt.ts | L41-123 |
| fetchSystemPromptParts | src/utils/queryContext.ts | L44-74 |

## 缓存失效点

| 触发 | 文件 | 行范围 |
|------|------|--------|
| /clear | src/commands/clear/caches.ts | L71-84 |
| /compact | src/services/compact/postCompactCleanup.ts | L31-77 |
| enter worktree | src/tools/EnterWorktreeTool/EnterWorktreeTool.ts | L97-102 |
| exit worktree | src/tools/ExitWorktreeTool/ExitWorktreeTool.ts | L142-145 |
| /resume restore | src/utils/sessionRestore.ts | L359-389 |
| setup flip | src/setup.ts | L337-347 |

## V3 当前实现锚点

| 文件 | 行 | 内容 |
|------|-----|------|
| internal/module/thread/contract.go | 34-50 | StartRequest |
| internal/provider/codexapp/support.go | 248-261 | buildThreadStartParams |
| internal/provider/codexapp/session_turn.go | 37-49 | buildTurnStartParams |
| internal/provider/codexapp/session_turn.go | 76-85 | turnInputsFromRequest |
| internal/provider/claudecli/session.go | 128-159 | RuntimeConfigSnapshot（字段写入见 136-152） |
| internal/module/thread/start_session.go | 18-20 | FirstNonEmpty 污染源 |
| internal/module/thread/start_session_helpers.go | 9-30 | DeveloperInstructions → Config |
| internal/module/thread/lifecycle.go | 56,80,116,340-372 | Prompt 扩散链 |
| internal/module/thread/lifecycle_helpers.go | 146-152 | toRef display name fallback |
| internal/module/thread/rpc_types.go | 54-91 | legacy 字段兼容 |
| internal/provider/codexapp/module.go | 231-248 | skill prompt 先例 |
| internal/provider/claudecli/transport_config.go | 99-147 | claude --system-prompt 拼装 |
| internal/provider/claudecli/session_turn.go | 173-202 | claude turn prepareTurnLocked |
| internal/provider/claudecli/session_log_watcher_integration.go | 231-236 | claude restart instructions 恢复 |
| internal/provider/claudecli/driver.go | 106-127 | claude launch 链路 |
| internal/provider/unified/client.go | 30-68 | 统一 driver 分发 |
| internal/contract/provider.go | 10-39 | provider 接口定义 |
| cmd/mcp-orch/tools/orchestration_tools.go | 127-150 | 子 agent launch |
