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
| 3 | [phase-3-prompt-registry.md](phase-3-prompt-registry.md) | Section 注册表（静态+动态） | 2 天 |
| 4.5 | [phase-4.5-provider-unification.md](phase-4.5-provider-unification.md) | Provider 归一前置解耦 | 3-5 天 |
| 4 | [phase-4-provider-injection.md](phase-4-provider-injection.md) | Provider 链路注入（依赖 Phase 4.5） | 1 天 |
| 5 | [phase-5-agent-memory.md](phase-5-agent-memory.md) | Agent 记忆隔离 | 1 天 |
| 6 | [phase-6-memory-retrieval.md](phase-6-memory-retrieval.md) | 记忆检索 | 2 天 |
| 7 | [phase-7-migration-compat.md](phase-7-migration-compat.md) | 迁移工具 + 兼容层 | 1 天 |
| 8 | [phase-8-testing.md](phase-8-testing.md) | 测试 + 守护 | 1 天 |
| **合计** | | | **15-17 天** |

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

## 暂不实现

| 功能 | 原因 |
|------|------|
| KAIROS daily log 模式 | V3 非 long-lived autonomous agent |
| Team Memory 双目录 | V3 当前单用户 |
| Global cache scope | Claude first-party/provider 级 global cache scope / boundary 机制，V3 当前 provider 链路不实现 |
| Token budget section | Claude 的 user token target / auto-continue 机制，V3 暂不支持该交互模型 |
| Output Style section | 不迁移 Claude 的 outputStyle 配置/插件通道，统一由 CLAUDE.md 承载风格约束 |
| ant_model_override | V3 无 ant 内部模型覆写 |
| numeric_length_anchors | ant-only，V3 不需要 |
| scratchpad | V3 暂不引入 Claude 式 session scratchpad 目录；若后续需无权限临时目录/跨 worker 暂存，再单列实现 |
| frc (function result clearing) | 依赖 Claude 侧 CACHED_MICROCOMPACT + FRC 配置 |
| summarize_tool_results | 源码独立注入无 gate，但 V3 延后 |
| brief | 依赖 KAIROS/KAIROS_BRIEF + briefToolModule + proactive 去重 |
| nested_memory | 复杂度高，单独排期到 P19 |

## 参考文档

- [source-refs-appendix.md](source-refs-appendix.md) — 全量源码锚点附录
- [../p18-memory-prompt-system-plan.md](../p18-memory-prompt-system-plan.md) — 历史总纲/背景对照（**非当前实施口径**）
- `claude_memory_system_mapping.md` / `claude_memory_system_source_refs.md` / `claude_system_prompts_mapping.md` / `claude_system_prompts_source_refs.md` 的历史内容已并入 `source-refs-appendix.md`，仓库内不再单独维护同名文件
