# P18 记忆系统 + 系统提示词架构集成

> 基于 Claude Code 官方源码逆向文档
> 创建时间：2026-04-14 | 状态：**审查修订后**
> 源码基线：`/Users/mac/Desktop/agnet/cluade/claude-code-sourcemap/restored-src/`

---

## 目标

将 Claude Code 的**记忆系统**和**系统提示词三层注入架构**移植到 Super-Dolphin V3。

## Phase 索引

| Phase | 文件 | 内容 | 预计 |
|-------|------|------|------|
| 0 | [phase-0-infrastructure.md](phase-0-infrastructure.md) | 模块骨架 + fx 注册 | 1 天 |
| 1 | [phase-1-memory-storage.md](phase-1-memory-storage.md) | 磁盘记忆存储层 | 2 天 |
| 2 | [phase-2-memory-prompt-rules.md](phase-2-memory-prompt-rules.md) | 记忆行为规则注入 | 1 天 |
| 3 | [phase-3-prompt-registry.md](phase-3-prompt-registry.md) | Section 注册表（静态+动态） | 2 天 |
| 4 | [phase-4-provider-injection.md](phase-4-provider-injection.md) | Provider 链路注入 | 1 天 |
| 5 | [phase-5-agent-memory.md](phase-5-agent-memory.md) | Agent 记忆隔离 | 1 天 |
| 6 | [phase-6-memory-retrieval.md](phase-6-memory-retrieval.md) | 记忆检索 | 2 天 |
| 7 | [phase-7-migration-compat.md](phase-7-migration-compat.md) | 迁移工具 + 兼容层 | 1 天 |
| 8 | [phase-8-testing.md](phase-8-testing.md) | 测试 + 守护 | 1 天 |
| **合计** | | | **12 天** |

## 审查状态

10 个 codex agent 已完成交叉审查，审查意见已合并到各 phase 文档。详见 [review-summary.md](review-summary.md)。

## 暂不实现

| 功能 | 原因 |
|------|------|
| KAIROS daily log 模式 | V3 非 long-lived autonomous agent |
| Team Memory 双目录 | V3 当前单用户 |
| Global cache scope | V3 走 codex 缓存策略 |
| Token budget section | Claude 的 user token target / auto-continue 机制，V3 暂不支持该交互模型 |
| Output Style section | 不迁移 Claude 的 outputStyle 配置/插件通道，统一由 CLAUDE.md 承载风格约束 |
| ant_model_override | V3 无 ant 内部模型覆写 |
| numeric_length_anchors | ant-only，V3 不需要 |
| scratchpad | V3 暂不引入 Claude 式 session scratchpad 目录；若后续需无权限临时目录/跨 worker 暂存，再单列实现 |
| frc (function result clearing) | 依赖 codex CACHED_MICROCOMPACT，V3 暂无需求 |
| summarize_tool_results | 源码独立注入无 gate，但 V3 延后 |
| brief | 依赖 KAIROS/KAIROS_BRIEF + briefToolModule + proactive 去重 |
| nested_memory | 复杂度高，单独排期到 P19 |

## 参考文档

- `claude_memory_system_mapping.md` — 记忆系统运行模式、四种类型、注入链路
- `claude_memory_system_source_refs.md` — 记忆系统源码锚点
- `claude_system_prompts_mapping.md` — 系统提示词三层架构
- `claude_system_prompts_source_refs.md` — 提示词源码锚点
- [source-refs-appendix.md](source-refs-appendix.md) — 全量源码锚点附录
