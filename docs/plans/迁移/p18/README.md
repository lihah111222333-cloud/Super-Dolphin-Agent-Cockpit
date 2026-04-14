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
| 7 | [phase-7-migration-compat.md](phase-7-migration-compat.md) | 迁移工具 + 兼容层 | 1 天 |
| 8 | [phase-8-testing.md](phase-8-testing.md) | 测试 + 守护 | 1-2 天 |
| **合计** | | | **17-20 天** |

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
- 当前 P18 的 ~87% 对齐度，主要覆盖 **单用户 + Standard 主链 + agent memory + relevant memories + prompt/provider 注入重构**；剩余差距主要集中在 **KAIROS / Team Memory / nested memory / 后台服务层 / exact compact-adjacent parity**。

## 暂不实现 / 明确延后

| 功能 | 当前状态 | 延后原因 / 风险 |
|------|----------|----------------|
| KAIROS daily log 模式 | P18 不实现 | V3 当前不是 long-lived autonomous agent；但若后续引入长寿命 assistant，会缺少 append-only daily logs、`date_change` 跨日切换，以及 `/dream` 蒸馏回 topic files + `MEMORY.md` 的闭环 |
| Team Memory 双目录 | P18 不实现 | V3 当前单用户；**必须与 Team sync service 一起延后**，不能只做 prompt/目录壳，否则会落入“看似支持 team scope、实际没有同步/冲突处理/共享一致性”的半成品状态 |
| Team Memory sync service（pull/push/watcher/conflict resolution） | README 先前未显式登记，现补记为随 Team Memory 一并延后 | Claude 原生 team memory 不是只多一个 `team/` 目录，还包含 OAuth、repo-scoped API、watcher、ETag optimistic locking、partial success、suppression 与 shutdown flush；若未来单独放出 team 写入但没有这层服务，会有共享不一致与数据回滚风险 |
| Team Memory secret guard（write/edit-time + pre-push scan） | 随 Team Memory 一并延后 | Claude 对 shared team memory 有双层敏感信息防护；P18 只做通用 memory 写入敏感信息拦截，**不等价**于 team-sync 前的二次扫描与 skip-upload 语义 |
| Background extract memories（stop-hook 补漏提取） | README 先前未显式登记，P18 不实现 | Claude 会在 stop-hook 后 fork agent 做补漏提取，并把 manifest 预注入避免重复；P18 缺少这条被动补漏链，意味着 Standard 模式更依赖主 agent 显式 remember |
| Auto-dream / consolidation（stop-hook 蒸馏） | README 先前未显式登记，P18 不实现 | Claude 会在 gate 满足时做 consolidation，把会话沉淀回长期记忆；P18 当前只做主链 CRUD / retrieval，不做后台蒸馏，长期运行时记忆噪声控制会弱于 Claude |
| exact `tengu_moth_copse` parity | 不复刻同名 feature flag，只吸收必要语义 | P18 会吸收 `filterInjectedMemoryFiles()` / `skipIndex` / relevant memories 的关键语义，但**不承诺**一比一复刻 Claude 的“移除 entrypoint index、更多依赖 retrieval backfill”的完整开关模式 |
| nested_memory | 复杂度高，单独排期到 P19 | 这是按目标文件路径补充 nested `CLAUDE.md` / `.claude/rules` 的机制，不是 AutoMem retrieval；延后本身可控，但会让复杂多目录项目缺少 file-targeted instruction recall |
| Global cache scope | P18 不实现 | Claude first-party/provider 级 global cache scope / boundary 机制，V3 当前 provider 链路不实现 |
| Token budget section | P18 不实现 | Claude 的 user token target / auto-continue 机制，V3 暂不支持该交互模型 |
| Output Style section | P18 不实现 | 不迁移 Claude 的 outputStyle 配置/插件通道，统一由 CLAUDE.md 承载风格约束 |
| ant_model_override | P18 不实现 | ant-only，V3 无 ant 内部模型覆写 |
| numeric_length_anchors | P18 不实现 | ant-only，V3 不需要 |
| scratchpad | P18 不实现 | V3 暂不引入 Claude 式 session scratchpad 目录；若后续需无权限临时目录/跨 worker 暂存，再单列实现 |
| frc (function result clearing) | P18 不实现 | 依赖 Claude 侧 `CACHED_MICROCOMPACT` + FRC 配置 |
| summarize_tool_results | P18 不实现 | 源码独立注入无 gate，但 V3 延后 |
| brief | P18 不实现 | 依赖 KAIROS/KAIROS_BRIEF + briefToolModule + proactive 去重 |

## 延后风险结论（给排期决策人）

- **KAIROS daily log**：在“非长寿命 autonomous session”前提下可安全延后；一旦 V3 引入长期后台助手，应优先补 `/dream` + daily log，而不是继续沿用 Standard 目录写法硬撑。
- **Team Memory**：只有在 **完全不暴露 team scope** 时才安全延后；若未来要开放 team scope，必须把双目录、sync service、secret guard、team `MEMORY.md` entrypoint 注入一起打包上线。
- **nested_memory**：可延后，但要明确这不是 retrieval 的小补丁，而是 target-path 条件规则体系；复杂单仓/多目录项目的命中率会明显弱于 Claude。
- **compact 机制对比**：Claude 的 compact 不只是“压缩消息”，还会联动 section cache、`getUserContext()` / entrypoint 重取、`loadedNestedMemoryPaths` 生命周期，以及 transcript 中 `relevant_memories` 的保留/清空语义。P18 当前的替代方案是 **PromptAssembly invalidate + retrieval generation/cancel + attachment replay roundtrip**，可覆盖主链，但不追求 Claude 的所有边缘 compact 行为一比一同构。

## 参考文档

- [source-refs-appendix.md](source-refs-appendix.md) — 全量源码锚点附录
- [../p18-memory-prompt-system-plan.md](../p18-memory-prompt-system-plan.md) — 历史总纲/背景对照（**非当前实施口径**）
- `claude_memory_system_mapping.md` / `claude_memory_system_source_refs.md` / `claude_system_prompts_mapping.md` / `claude_system_prompts_source_refs.md` 的历史内容已并入 `source-refs-appendix.md`，仓库内不再单独维护同名文件
