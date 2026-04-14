# P18 记忆系统 + 系统提示词架构集成

> 基于 Claude Code 官方源码逆向文档
> 创建时间：2026-04-14 | 状态：**审查修订后**
> 源码基线：Claude restored-src 本地镜像（示例路径：`/Users/mac/Desktop/agnet/claude/claude-code-sourcemap/restored-src/`，**仅作个人环境示例，不是仓库契约**）
> 当前实施口径以本目录下 `README.md`、`phase-0`~`phase-8`、`review-summary.md` 为准；[`../p18-memory-prompt-system-plan.md`](../p18-memory-prompt-system-plan.md) 仅保留历史背景/对照，不再作为最终实施口径。

---

## 目标

将 Claude Code 的**记忆系统**和**系统提示词三层注入架构**移植到 Super-Dolphin V3。

## 实施边界（给开工同学）

- P18 默认落地路径是：**单用户 + Standard 主链**
- Team Memory / KAIROS / nested_memory 不在本轮实施范围
- 推荐执行顺序：`0 → 1 → 2 → 3 → 4.5 → 4 → 5/6/7 → 8`
- 第一步先读 `phase-0-infrastructure.md`，再对照 `source-refs-appendix.md` 的 V3 锚点进入实现

## Phase 索引

| Phase | 文件 | 内容 | 预计 |
|-------|------|------|------|
| 0 | [phase-0-infrastructure.md](phase-0-infrastructure.md) | 模块骨架 + fx 注册 | 1 天 |
| 1 | [phase-1-memory-storage.md](phase-1-memory-storage.md) | 磁盘记忆存储层 | 2 天 |
| 2 | [phase-2-memory-prompt-rules.md](phase-2-memory-prompt-rules.md) | 记忆行为规则注入 | 1 天 |
| 3 | [phase-3-prompt-registry.md](phase-3-prompt-registry.md) | Section 注册表（7/7 静态 + 5/13 动态 = 12/20） | 2 天 |
| 4.5 | [phase-4.5-provider-unification.md](phase-4.5-provider-unification.md) | Provider 归一前置解耦 | 5-7 天 |
| 4 | [phase-4-provider-injection.md](phase-4-provider-injection.md) | Provider 链路注入（依赖 Phase 3 / 4.5） | 1 天 |
| 5 | [phase-5-agent-memory.md](phase-5-agent-memory.md) | Agent 记忆隔离 | 1 天 |
| 6 | [phase-6-memory-retrieval.md](phase-6-memory-retrieval.md) | 记忆检索 | 2 天 |
| 7 | [phase-7-migration-compat.md](phase-7-migration-compat.md) | Hook 注入 + 记忆读取工具 + 迁移兼容 | 0.5-1 天 |
| 8 | [phase-8-testing.md](phase-8-testing.md) | 测试 + 守护 | 1-2 天 |
| **合计** | | | **16.5-20 天** |

## Phase 依赖图（文本）

```text
Phase 0
├─> Phase 1 ─┬─> Phase 5
│            ├─> Phase 6
│            └─> Phase 7
├─> Phase 2 ─┬─> Phase 3 ─┬─> Phase 4.5 ─> Phase 4
│            │            └──────────────> Phase 4
│            └─> Phase 7
└─> Phase 8

Phase 1 ─> Phase 8
Phase 2 ─┬─> Phase 3
         ├─> Phase 7
         └─> Phase 8
Phase 3 ─┬─> Phase 4.5
         ├─> Phase 4
         └─> Phase 8
Phase 4.5 ─┬─> Phase 4
           └─> Phase 8
Phase 4 ─> Phase 8
Phase 5 ─> Phase 8
Phase 6 ─> Phase 8
Phase 7 ─> Phase 8
```

> 直接依赖口径：`0→1/2/8`，`1→5/6/7/8`，`2→3/7/8`，`3→4.5/4/8`，`4.5→4/8`，`4→8`，`5→8`，`6→8`，`7→8`。

## 实施前先看

- [source-refs-appendix.md](source-refs-appendix.md) 的“V3 当前实现锚点”
- `internal/module/thread/start_session.go:18-20`
- `internal/module/thread/lifecycle.go:56,80,116,141,192,219,245,275`
- `internal/dto/provider/session.go:5-24`
- `internal/provider/codexapp/support.go:248-261`
- `internal/provider/codexapp/session_turn.go:37-49,76-85`
- `internal/provider/claudecli/transport_config.go:99-147`
- `internal/provider/claudecli/session_turn.go:14-42,173-202`

## 审查状态

30 个 agent 多轮交叉审查已完成，审查意见已持续合并到各 phase 文档；第 10 轮是首次整体通过，第 13 轮是当前最新收官结论。当前 authoritative source 为 [review-summary.md](review-summary.md) 的“第 13 轮：收官统一复核”。

## 关键对齐提醒

- Claude 完整 memory 体系不只是 `loadMemoryPrompt()` 行为规则；还包含磁盘 memory 文件、主线程 `MEMORY.md` / TeamMem entrypoint 注入链、agent memory 特例、relevant memories、nested memory，以及 stop-hook 后台 extract / auto-dream / team sync 服务层。
- **主线程常规路径**里：`loadMemoryPrompt()` 只负责行为规则；`MEMORY.md` / TeamMem 内容实际走 Phase 4 的 `claudeMd` source-resolver + filter + render + UserContext prepend 链进入模型，**不是**像 agent memory 那样直接内联。
- **agent memory** 是单独特例：`LoadAgentMemoryPrompt()` 直接读取并内联 agent 自己的 `MEMORY.md`，因此不能把主线程与 agent 路径混写成同一种注入方式。
- 截至 **2026-04-14**，若按仓库内 `internal/` 实码逐项复核，当前状态已不适合继续沿用 README 早先的“~87% 对齐”口径；最新 authoritative status 以本页下方“功能对齐矩阵”为准。

## 功能对齐矩阵

> 统计口径：按“**功能族 / 导出函数组**”统计；同一行内的 helper 函数属于同一条 Claude 功能链，因此合并展示，但函数名全部保留。
>
> 状态口径：
> - ✅ 已实现：`internal/` 已有对应实现，且从符号/测试可见主行为已落地
> - 🔄 进行中：已有模块、slot、contract 或测试骨架，但未全量接线或语义仍有缺口
> - ❌ 未实现：`internal/` 未找到对应实现
> - ⏳ P18未实现：已归入 P18 范围但尚未实现，详见 `p18-unimplemented.md`

| Claude 函数 | 文件 | 功能 | V3 对应 | 状态 | 所属 Phase |
|---|---|---|---|---|---|
| `parseMemoryType` | `src/memdir/memoryTypes.ts` | 解析四类 memory type frontmatter | `internal/module/memory/types.go:ParseMemoryType` | ✅已实现 | P0 / P2 |
| `isAutoMemoryEnabled` | `src/memdir/paths.ts` | auto memory 总开关与门禁判定 | `internal/module/memory/config.go:NewConfig`<br>`internal/module/memory/rules_provider.go:NewRulesProvider` | 🔄进行中 | P0 / P1 |
| `getMemoryBaseDir`<br>`getAutoMemPath`<br>`getAutoMemEntrypoint` | `src/memdir/paths.ts` | 解析 memory root、project-scoped auto memory 目录与 `MEMORY.md` 入口 | `internal/module/memory/config.go:defaultRootDir`<br>`internal/module/memory/path.go:GetAutoMemPath`<br>`internal/module/memory/index.go:WriteMemoryIndex` | ✅已实现 | P1 |
| `hasAutoMemPathOverride`<br>`isAutoMemPath` | `src/memdir/paths.ts` | override 探测与 auto-memory 路径归属判断 | `internal/module/memory/config.go:envMemoryRoot`<br>`internal/module/memory/path.go:ValidateMemoryWritePath` | 🔄进行中 | P1 |
| `truncateEntrypointContent` | `src/memdir/memdir.ts` | 对 `MEMORY.md` 做行数 / 字节双阈值截断并附告警 | 无 | ❌未实现 | P1 |
| `buildMemoryLines` | `src/memdir/memdir.ts` | 渲染 Standard memory behavioral rules | `internal/module/memory/rules.go:BuildMemoryLines` | ✅已实现 | P2 |
| `loadMemoryPrompt` | `src/memdir/memdir.ts` | 按模式 / gate 分派 memory prompt，并挂入 system prompt | `internal/module/memory/rules.go:LoadMemoryPrompt`<br>`internal/module/memory/rules_provider.go:Resolve` | 🔄进行中 | P2 → P4.5 |
| `buildSearchingPastContextSection` | `src/memdir/memdir.ts` | 提示模型如何搜索 memory dir / transcript | 无（Phase 2 文档显式 deferred） | ❌未实现 | P6 |
| `isExtractModeActive`<br>`formatMemoryManifest` | `src/memdir/paths.ts`<br>`src/memdir/memoryScan.ts` | stop-hook extractMemories gate 与 manifest 格式化 | 无 | ⏳P18未实现 | P18 未实现 |
| `buildAssistantDailyLogPrompt`<br>`getAutoMemDailyLogPath`<br>`getDateChangeAttachments` | `src/memdir/memdir.ts`<br>`src/memdir/paths.ts`<br>`src/utils/attachments.ts` | KAIROS daily log、跨午夜 rollover、`date_change` 提醒 | 无 | ⏳P18未实现 | P18 未实现 |
| `buildCombinedMemoryPrompt`<br>`isTeamMemoryEnabled`<br>`getTeamMemPath`<br>`getTeamMemEntrypoint` | `src/memdir/teamMemPrompts.ts`<br>`src/memdir/teamMemPaths.ts` | private + team 双目录 prompt 与 team entrypoint | 无 | ⏳P18未实现 | P18 未实现 |
| `isTeamMemPath`<br>`isTeamMemFile`<br>`validateTeamMemWritePath`<br>`validateTeamMemKey` | `src/memdir/teamMemPaths.ts` | Team memory 归属判断与 anti-traversal / symlink escape 校验 | 无 | ⏳P18未实现 | P18 未实现 |
| `memoryAgeDays`<br>`memoryAge`<br>`memoryFreshnessText`<br>`memoryFreshnessNote`<br>`memoryHeader` | `src/memdir/memoryAge.ts`<br>`src/utils/attachments.ts` | surfaced relevant memories 的 freshness/header 文案 | 无 | ❌未实现 | P6 |
| `startRelevantMemoryPrefetch`<br>`collectSurfacedMemories`<br>`filterDuplicateMemoryAttachments` | `src/utils/attachments.ts` | relevant memory 异步预取、历史去重、read-state 去重 | 无 | ❌未实现 | P6 |
| `getDirectoriesToProcess`<br>`memoryFilesToAttachments` | `src/utils/attachments.ts` | nested memory 目录遍历与 attachment 物化 | 无 | ⏳P18未实现 | P18 未实现 |
| `buildMemoryPrompt`<br>`loadAgentMemoryPrompt` | `src/memdir/memdir.ts`<br>`src/tools/AgentTool/agentMemory.ts` | agent 特例：直接内联 agent `MEMORY.md` | 无 | ❌未实现 | P5 |
| `getAgentMemoryDir`<br>`isAgentMemoryPath`<br>`getAgentMemoryEntrypoint`<br>`getMemoryScopeDisplay` | `src/tools/AgentTool/agentMemory.ts` | agent memory scope/path 隔离与展示 | 无 | ❌未实现 | P5 |
| `systemPromptSection`<br>`DANGEROUS_uncachedSystemPromptSection` | `src/constants/systemPromptSections.ts` | 定义 cached / volatile section 描述符 | `internal/module/prompt/section.go:PromptSection`<br>`internal/module/prompt/dynamic.go:dynamicSlotSection` | ✅已实现 | P3 |
| `resolveSystemPromptSections`<br>`clearSystemPromptSections` | `src/constants/systemPromptSections.ts` | section 解析、缓存命中、clear/compact invalidation | `internal/module/prompt/assembler.go:resolveSections`<br>`internal/module/prompt/assembler.go:Invalidate`<br>`internal/module/prompt/cache.go` | ✅已实现 | P3 |
| `getSystemPrompt` | `src/constants/prompts.ts` | 组装 static + dynamic system prompt 主树 | `internal/module/prompt/assembler.go:AssembleStart`<br>`internal/module/prompt/assembler.go:AssembleTurn` | 🔄进行中 | P3 → P4.5 |
| `getSimpleIntroSection` | `src/constants/prompts.ts` | identity / baseline / no-guess-URL intro | `internal/module/prompt/section.go:sectionIdentityText` | 🔄进行中 | P3 |
| `getSimpleSystemSection` | `src/constants/prompts.ts` | 系统约束、deny 语义、注入防御提醒 | `internal/module/prompt/section.go:sectionSystemConstraintsText` | ✅已实现 | P3 |
| `getSimpleDoingTasksSection` | `src/constants/prompts.ts` | 工程纪律、避免过度设计、完成前验证 | `internal/module/prompt/section.go:sectionEngineeringText` | ✅已实现 | P3 |
| `getActionsSection` | `src/constants/prompts.ts` | 高风险动作确认策略 | `internal/module/prompt/section.go:sectionActionsText` | ✅已实现 | P3 |
| `getUsingYourToolsSection` | `src/constants/prompts.ts` | 专用工具优先、并行独立调用、避免 shell 绕过 | `internal/module/prompt/section.go:sectionToolPreferencesText` | ✅已实现 | P3 |
| `getSimpleToneAndStyleSection` | `src/constants/prompts.ts` | no emoji、`file:line`、无冒号 tool-call 前缀 | `internal/module/prompt/section.go:sectionStyleText` | ✅已实现 | P3 |
| `getOutputEfficiencySection` | `src/constants/prompts.ts` | lead with answer / concise / milestone-only updates | `internal/module/prompt/section.go:sectionOutputEfficiencyText` | ✅已实现 | P3 |
| `getLanguageSection` | `src/constants/prompts.ts` | language 动态 section | `internal/module/prompt/dynamic.go:DynamicSectionLanguage` slot | 🔄进行中 | P3 |
| `getMcpInstructionsSection` | `src/constants/prompts.ts` | MCP server instructions 动态 section | `internal/module/prompt/dynamic.go:DynamicSectionMCPInstructions` slot | 🔄进行中 | P3 |
| `computeSimpleEnvInfo`<br>`computeEnvInfo`<br>`getUnameSR` | `src/constants/prompts.ts` | 汇总 CWD / git / shell / OS / model 环境信息 | `internal/contract/prompt.go:BuildCtx`<br>`internal/module/prompt/dynamic.go:DynamicSectionEnvInfoSimple` slot | 🔄进行中 | P3 → P4 |
| `prependBullets` | `src/constants/prompts.ts` | prompt bullet 渲染 helper | `internal/module/memory/rules.go:renderBullets` | ✅已实现 | P3 |
| `getScratchpadInstructions` | `src/constants/prompts.ts` | session scratchpad 目录说明 | 无 | ⏳P18未实现 | P18 未实现 |
| `getFunctionResultClearingSection` | `src/constants/prompts.ts` | FRC / microcompact 提示 | 无 | ⏳P18未实现 | P18 未实现 |
| `getBriefSection` | `src/constants/prompts.ts` | KAIROS brief / proactive 去重提示 | 无 | ⏳P18未实现 | P18 未实现 |

### 统计（截至 2026-04-14）

- 总功能数：**34**
- ✅ 已实现：**7 / 34 = 20.6%**
- 🔄 进行中：**12 / 34 = 35.3%**
- ❌ 未实现：**7 / 34 = 20.6%**
- ⏳ P18未实现：**8 / 34 = 23.5%**

> 结论：当前 `internal/` 实码已经把 **Phase 1/2 的 memory store / rules** 与 **Phase 3 的 prompt registry 骨架**搭起来了，但 **Phase 4.5 的 thread/provider 接线、Phase 5 agent memory、Phase 6 relevant memories** 仍是主缺口；Team Memory / KAIROS / nested memory / scratchpad / FRC / brief 已归档为 **P18 未实现部分**，详见 `p18-unimplemented.md`。

## P18 未实现部分

> 原先标注为“P19 延后”的条目，现统一纳入 P18 未实现范围。
> 详见 [p18-unimplemented.md](p18-unimplemented.md)。


## 未实现风险结论（给排期决策人）

- **KAIROS daily log**：在“非长寿命 autonomous session”前提下可安全延后；一旦 V3 引入长期后台助手，应优先补 `/dream` + daily log，而不是继续沿用 Standard 目录写法硬撑。
- **Team Memory**：只有在 **完全不暴露 team scope** 时才安全延后；若未来要开放 team scope，必须把双目录、sync service、secret guard、team `MEMORY.md` entrypoint 注入一起打包上线。
- **nested_memory**：可延后，但要明确这不是 retrieval 的小补丁，而是 target-path 条件规则体系；复杂单仓/多目录项目的命中率会明显弱于 Claude。
- **compact 机制对比**：Claude 的 compact 不只是“压缩消息”，还会联动 section cache、`getUserContext()` / entrypoint 重取、`loadedNestedMemoryPaths` 生命周期，以及 transcript 中 `relevant_memories` 的保留/清空语义。P18 当前的替代方案是 **PromptAssembly invalidate + retrieval generation/cancel + attachment replay roundtrip**，可覆盖主链，但不追求 Claude 的所有边缘 compact 行为一比一同构。

## 参考文档

- [p18-unimplemented.md](p18-unimplemented.md) — P18 未实现部分汇总（原 P19 延后项）
- [source-refs-appendix.md](source-refs-appendix.md) — 全量源码锚点附录
- [../p18-memory-prompt-system-plan.md](../p18-memory-prompt-system-plan.md) — 历史总纲/背景对照（**非当前实施口径**）
- `claude_memory_system_mapping.md` / `claude_memory_system_source_refs.md` / `claude_system_prompts_mapping.md` / `claude_system_prompts_source_refs.md` 的历史内容已并入 `source-refs-appendix.md`，仓库内不再单独维护同名文件
