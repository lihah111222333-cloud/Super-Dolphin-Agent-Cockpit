# P18 源码锚点附录

> 源码基线：`/Users/mac/Desktop/agnet/cluade/claude-code-sourcemap/restored-src/`

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
| MEMORY_FRONTMATTER_EXAMPLE | src/memdir/memoryTypes.ts | L261-270 |
| getAutoMemPath | src/memdir/paths.ts | L223-235 |
| getMemoryBaseDir | src/memdir/paths.ts | L85-90 |
| validateMemoryPath | src/memdir/paths.ts | L109-150 |
| findCanonicalGitRoot | src/utils/git.ts | L195-210 |
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
| formatMemoryManifest | src/memdir/memoryScan.ts | L84-93 |
| MAX_MEMORY_FILES | src/memdir/memoryScan.ts | L21 |
| FRONTMATTER_MAX_LINES | src/memdir/memoryScan.ts | L22 |
| findRelevantMemories | src/memdir/findRelevantMemories.ts | L39-75 |
| selectRelevantMemories | src/memdir/findRelevantMemories.ts | L77-141 |
| startRelevantMemoryPrefetch | src/utils/attachments.ts | L2361-2424 |
| getRelevantMemoryAttachments | src/utils/attachments.ts | L2196-2242 |
| collectSurfacedMemories | src/utils/attachments.ts | L2251-2266 |

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
| internal/provider/claudecli/session.go | 127-158 | RuntimeConfigSnapshot (137-145 为字段写入) |
