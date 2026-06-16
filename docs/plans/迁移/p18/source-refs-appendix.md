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
| MAX_ENTRYPOINT_LINES / MAX_ENTRYPOINT_BYTES | src/memdir/memdir.ts | L35-38 |
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
| startRelevantMemoryPrefetch 调用点 | src/query.ts | L301-304 |
| pendingMemoryPrefetch consume block | src/query.ts | L1599-1613 |

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
| internal/module/thread/contract.go | 96-103 | LaunchAgentRequest |
| internal/module/thread/start_session.go | 18-20 | FirstNonEmpty 污染源 |
| internal/module/thread/start_session.go | 136-154 | (*service).startSession（provider DTO 注入点位于 146-152） |
| internal/module/thread/start_session_helpers.go | 9-30 | buildStartSessionConfig（DeveloperInstructions → Config） |
| internal/module/thread/lifecycle.go | 56-80 | Start: Prompt → launch/persist |
| internal/module/thread/lifecycle.go | 116-116 | Resume: state.Prompt → launchAgent |
| internal/module/thread/lifecycle.go | 340-372 | launchAgent / buildLaunchRequest |
| internal/module/thread/lifecycle_helpers.go | 146-152 | toRef display name fallback |
| internal/module/thread/service.go | 135-166 | SetName 仍把展示名写进 Prompt 槽位 |
| internal/module/thread/rpc.go | 51-53 | thread/name/set handler |
| internal/module/thread/rpc_types.go | 54-91 | legacy 字段兼容 |
| internal/ui/wails/binding.go | 60-66 | (*App).LaunchAgent（旧入口只传 `baseInstructions`） |
| internal/provider/codexapp/support.go | 248-261 | buildThreadStartParams |
| internal/provider/codexapp/session_turn.go | 37-49 | buildTurnStartParams |
| internal/provider/codexapp/session_turn.go | 76-85 | turnInputsFromRequest |
| internal/provider/codexapp/module.go | 231-248 | buildSkillPromptInput（skill prompt 先例） |
| internal/provider/claudecli/session.go | 129-160 | (*session).RuntimeConfigSnapshot（字段写入见 136-152） |
| internal/provider/claudecli/transport_config.go | 129-147 | composeLaunchSystemPrompt（claude `--system-prompt` 拼装） |
| internal/provider/claudecli/session_turn.go | 167-196 | (*session).prepareTurnLocked（claude turn） |
| internal/provider/claudecli/session_log_watcher_integration.go | 230-253 | (*session).prepareSessionRestartLocked（claude restart instructions 恢复点见 235） |
| internal/provider/claudecli/driver.go | 106-127 | (*driver).StartSession（claude launch 入口） |
| internal/provider/claudecli/driver.go | 149-164 | (*driver).start |
| internal/provider/claudecli/driver.go | 166-191 | (*driver).prepareSessionStart |
| internal/provider/unified/client.go | 30-37 | (*Client).StartSession |
| internal/provider/unified/client.go | 48-68 | (*Client).open（统一 driver 分发） |
| internal/contract/provider.go | 10-39 | provider 接口定义 |
| internal/contract/orchestration.go | 46-55 | orchestration LaunchRequest |
| internal/sidecar/orch/orchestration/rpc.go | 131-142 | launchRequestFromParams |
| internal/sidecar/orch/orchestration/launcher.go | 141-178 | (*remoteLauncher).Launch → thread/start RPC payload |
| internal/sidecar/orch/tools/orchestration_tools.go | 33-53 | HandleLaunchAgent（MCP 工具入口） |
| internal/sidecar/orch/tools/orchestration_tools.go | 127-150 | launchRequestFromExecutable（子 agent launch request builder） |
| internal/module/memory/config.go | 17-33 | `memory.Config` + `ENABLE_MEMORY_SYSTEM` / `ENABLE_MEMORY_TOOLS` |
| internal/module/memory/service.go | 17-53 | `NewService` / `EnsureRoot`（memory root 骨架） |
| internal/module/prompt/config.go | 15-24 | `prompt.Config` + `ENABLE_PROMPT_REGISTRY` / `ENABLE_PROMPT_ASSEMBLY` |
| internal/module/prompt/types.go | 45-102 | `SnapshotVersion` / `StartInput` / `TurnInput` / `StartAssembly` / `TurnAssembly` / `PromptAssemblySnapshot` |
| internal/module/prompt/service.go | 20-24 | `PromptAssemblyService` interface |
| internal/module/prompt/service.go | 34-58 | `service` / `NewService()`（registry/cache/dynamic provider wiring） |
| internal/module/turn/assembler.go | 12-15 | `maxTurnInputTextBytes=64*1024` / `maxTurnInputPathBytes=4096` |
| internal/module/turn/assembler.go | 48-71 | `inputAssembler.Assemble()` 现有输入去重 / clamp 入口 |
| internal/module/turn/assembler.go | 251-258 | `inputKey(type+content+path+url)` |
| cmd/mcp-orch/runtime.go | 111-125 | `newRegistry()` 注入 orchestration / workspace / prompt / command / sharedFile |
| internal/sidecar/orch/tools/registry.go | 24-35 | `tools.NewRegistry()` 汇总 19 个 MCP tools |
| internal/sidecar/orch/tools/shared_file_tools.go | 50-60 | `shared_file_read/write` tool definition |
| internal/sidecar/orch/tools/shared_file_tools.go | 62-101 | `shared_file_read/write` handler：path normalize / actor=agent / 10 MiB limit |
| internal/sidecar/orch/store/sharedfile/contract.go | 13-24 | `Store` 扩展 `sf.Reader`，补 `Upsert/Delete` |
| internal/sidecar/orch/store/sharedfile/store.go | 18-61 | `Upsert/Get/List/Delete` |
| internal/store/sharedfile/contract.go | 10-27 | `shared_files` 读侧契约 |
| internal/store/sharedfile/store.go | 16-38 | `Get/List` 读侧实现 |

## 未实现功能锚点（供 p18-unimplemented.md 引用）

> 状态口径：
> - `待实现`：已有显式保留位/结构字段，但功能主链尚未落地
> - `有部分痕迹`：当前只有通用基础设施可复用，尚无该功能专用实现
> - `完全缺失`：按当前代码关键字检索，未发现相关实现痕迹

### 1. extractMemories（stop-hook 后台补漏）
- Claude 锚点：
  - `src/query/stopHooks.ts` `L133-153`
  - `src/services/extractMemories/extractMemories.ts` `L82-148, L154-222, L251-268, L296-615`
  - `src/services/extractMemories/prompts.ts` `L26-154`
  - `src/memdir/memoryScan.ts` `L24-94`
  - `src/utils/forkedAgent.ts` `L46-141, L464-625`
- 当前 V3 痕迹（`lsp_grep`）：
  - `internal/platform/hooks/module.go` `76-93`：存在通用 lifecycle `OnStop` / event relay 底座
  - `internal/app/runner.go` `30-80`：应用 runner 存在 stop 生命周期
  - 在 `internal/` 搜索 `extractMemories`：无命中
- 实现状态：`有部分痕迹`

### 2. autoDream / consolidation（stop-hook 蒸馏）
- Claude 锚点：
  - `src/query/stopHooks.ts` `L154-156`
  - `src/services/autoDream/autoDream.ts` `L54-100, L122-129, L130-233, L235-323`
  - `src/services/autoDream/consolidationLock.ts` `L21-140`
  - `src/services/autoDream/consolidationPrompt.ts` `L1-64`
  - `src/tasks/DreamTask/DreamTask.ts` `L1-156`
- 当前 V3 痕迹（`lsp_grep`）：
  - `internal/platform/hooks/module.go` `76-93`：仅能承接 stop-hook 类逻辑
  - 在 `internal/` 搜索 `autoDream` / `dream` / `consolid`：无命中
- 实现状态：`有部分痕迹`

### 3. teamMemorySync（pull/push/watcher/ETag）
- Claude 锚点：
  - `src/memdir/teamMemPrompts.ts` `L69-74`
  - `src/setup.ts` `L330-369`
  - `src/services/teamMemorySync/watcher.ts` `L147-229, L231-305`
  - `src/services/teamMemorySync/index.ts` `L71-91, L95-136, L149-184, L188-410, L414-553, L567-755, L770-1191`
  - `src/services/teamMemorySync/types.ts` `L16-57, L74-156`
- 当前 V3 痕迹（`lsp_grep`）：
  - 在 `internal/` 搜索 `teamMemory`：无命中
  - 在 `internal/` 搜索 `If-Match`：无命中
  - 在 `internal/` 搜索 `team/`：无命中
- 实现状态：`完全缺失`

### 4. nested_memory（target-path 条件规则）
- Claude 锚点：
  - `src/tools/FileReadTool/FileReadTool.ts` `L840-848, L865-870, L1032-1038`
  - `src/utils/attachments.ts` `L1646-1689, L1709-1775, L1792-1891, L2163-2193`
  - `src/utils/claudemd.ts` `L94-227, L245-279, L537-684, L697-775, L1205-1396`
  - `src/query/stopHooks.ts` / compact-clear 相关生命周期见 `src/services/compact/compact.ts` `L517-523, L918-921`
- 当前 V3 痕迹（`lsp_grep` / `xref`）：
  - `internal/module/prompt/buildctx.go` `8-18`：仅保留 `AdditionalWorkingDirectories []string`
  - 上述字段当前 `references` 仅命中声明本身，尚无消费点
  - 在 `internal/` 搜索 `CLAUDE.local` / `.claude/rules`：无命中
- 实现状态：`待实现`

### 5. KAIROS daily log（date_change + /dream 蒸馏）
- Claude 锚点：
  - `src/memdir/memdir.ts` `L327-369, L419-438, L448-471`
  - `src/memdir/paths.ts` `L237-250`
  - `src/utils/attachments.ts` `L1402-1443`（`date_change`）
  - `src/services/autoDream/autoDream.ts` `L211-223`
  - `src/services/autoDream/consolidationPrompt.ts` `L10-64`
- 当前 V3 痕迹（`lsp_grep`）：
  - 在 `internal/` 搜索 `KAIROS`：无命中
  - 在 `internal/` 搜索 `date_change`：无命中
  - 在 `internal/` 搜索 `dream`：无命中
- 实现状态：`完全缺失`
