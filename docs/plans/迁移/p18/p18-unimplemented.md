# P18 未实现部分

> 原标注为"P19 延后"的功能，现归入 P18 范围但尚未实现。
> 更新时间：2026-04-14

---

> 说明：风险评级沿用 README 当前排期语义做归类（高 / 中 / 条件安全 / 低），工期为单人净工时估算；如 thread/provider 主链范围扩张，需整体顺延。

## 1. 未实现动态 Sections（8 个）

> Claude 主锚点来自 [source-refs-appendix.md](./source-refs-appendix.md) 的“系统提示词”章节：`getSystemPrompt (src/constants/prompts.ts:L444-577)` 与 `动态 slots 注册 (src/constants/prompts.ts:L491-555)`；下表补充 `phase-3-prompt-registry.md` 已拆开的 section 级子锚点，便于实施。

| Section | 功能 | Claude 锚点 | 风险评级 | 预估工期 | 依赖关系 | 实现建议 |
|---------|------|------------|---------|---------|---------|---------|
| output_style | 根据 `outputStyleConfig` / 插件 style 通道改写 intro、doing_tasks 与输出风格 | `src/constants/prompts.ts:L151-158, L505-507, L562-567` | 中 | 2-3 天 | `outputStyleConfig` 输入源、插件 style 通道、静态 section 装配分支 | 先补独立 style 输入 contract，再让 `identity` / `engineering` / `style` 三处共用同一 renderer，避免各自分叉。 |
| scratchpad | 暴露 session scratchpad 目录，供无权限临时写入与跨步骤暂存 | `src/constants/prompts.ts:L521, L797-819` | 中 | 2-3 天 | scratchpad 目录生命周期、权限模型、跨 worker 共享约定 | 先定义 scratchpad path contract 与清理策略，再接 prompt section；不要先暴露文案而没有目录协议。 |
| frc | 执行 function result clearing / microcompact，回收旧 tool result 占用 | `src/constants/prompts.ts:L522, L821-839` | 中 | 3-5 天 | `CACHED_MICROCOMPACT`、compact 流程、tool-result 生命周期管理 | 与 compact 机制一起设计；至少要覆盖 stale tool result 清理与 cache/invalidation 联动。 |
| summarize_tool_results | 在 system prompt 中要求优先概括工具结果，降低原始输出占用 | `src/constants/prompts.ts:L523-526, L841` | 中 | 1-2 天 | tool result 摘要层、长输出裁剪策略、turn 注入位置 | 可先落轻量版 prompt section，但最好与 tool result render/裁剪策略一并验收。 |
| numeric_length_anchors | 提供数值长度锚点，约束回答篇幅 | `src/constants/prompts.ts:L527-537` | 低 | 0.5-1 天 | ant 分支、模型家族判定、长度约束模板 | 仅在 ant family 真接入时补；当前保持低优先级即可。 |
| token_budget | 注入 user token target / auto-continue 预算约束 | `src/constants/prompts.ts:L538-550` | 中 | 2-4 天 | token target 配置、auto-continue 交互模型、预算消耗观测 | 先定预算 contract，再补 prompt section；否则只写文案没有运行时约束意义不大。 |
| brief | 注入 KAIROS brief / proactive dedupe 规则，驱动阶段性 recap 压缩 | `src/constants/prompts.ts:L552-554, L843-858` | 中 | 3-4 天 | `KAIROS` / `KAIROS_BRIEF`、brief module、proactive 去重链路 | 应与 KAIROS daily log / consolidation 一起排期，否则会出现 brief 文案存在但无数据来源。 |
| ant_model_override | ant 模型分支下覆写 system prompt 细节 | `src/constants/prompts.ts:L136-140, L496-498` | 低 | 0.5-1 天 | ant family provider 分支、模型选择注入点 | 等 ant family 真进入主链再补，不建议为当前主链提前分叉。 |

## 2. Team Memory 完整体

- 状态：骨架已实现（Go 侧 `TeamMemoryManager` 已预留并默认 hard-disabled）；sync service / secret guard / team entrypoint prompt 注入仍未实现。
- 范围：sync service（pull/push/watcher/ETag/shutdown flush）+ team secret guard 双层防护 + team entrypoint/prompt 注入。
- Claude 锚点：
  - `src/memdir/teamMemPrompts.ts:L22-100`
  - `src/memdir/teamMemPaths.ts:L73-78, L84-86, L92-94, L228-256, L265-284`
  - `src/services/teamMemorySync/watcher.ts:L147-229, L231-305`
  - `src/services/teamMemorySync/index.ts:L71-91, L95-136, L149-184, L188-410, L414-553, L567-755, L770-1191`
  - `src/services/teamMemorySync/types.ts:L16-57, L74-156`
- 风险：高（README 已明确：**只有在完全不暴露 team scope 时才安全延后**；一旦提前开放 team scope，没有 sync/conflict/secret guard 就会直接失真）。
- 预估工期：8-12 天。
- 依赖关系：team scope 产品开关、repo-scoped sync contract、watcher、ETag optimistic locking、partial success/suppression、shutdown flush、shared-memory secret scan。
- 实现建议：必须整包上线，至少拆成「team 目录与 entrypoint → sync service → secret guard → 冲突/回滚/关停 flush 验收」四段，不接受只上 prompt/目录壳的半成品。

## 3. KAIROS Daily Log

- 范围：`date_change` 跨日切换 + `/dream` 蒸馏回 `MEMORY.md` / topic files。
- Claude 锚点：
  - `src/memdir/memdir.ts:L327-369, L419-438, L448-471`
  - `src/memdir/paths.ts:L237-250`
  - `src/utils/attachments.ts:L1402-1443`
  - `src/services/autoDream/autoDream.ts:L211-223`
  - `src/services/autoDream/consolidationPrompt.ts:L10-64`
- 风险：条件安全（README 已限定在“非长寿命 autonomous session”前提下可暂缓）。
- 预估工期：4-6 天。
- 依赖关系：long-lived session 形态、daily log path、`date_change` attachment 注入、`/dream` 任务调度、topic file 回写策略。
- 实现建议：若后续要做长期后台助手，应优先补 daily log 与 `/dream` 闭环，再考虑 brief/proactive 之类的上层能力。
- 实现状态：骨架已实现。

## 4. nested_memory

- 范围：target-path 条件规则体系，而非普通 retrieval 补丁。
- Claude 锚点：
  - `src/tools/FileReadTool/FileReadTool.ts:L840-848, L865-870, L1032-1038`
  - `src/utils/attachments.ts:L1646-1689, L1709-1775, L1792-1891, L2163-2193`
  - `src/utils/claudemd.ts:L94-227, L245-279, L537-684, L697-775, L1205-1396`
  - `src/services/compact/compact.ts:L517-523, L918-921`
- 风险：中。
- 预估工期：4-5 天。
- 依赖关系：target-file → nested rules 匹配器、`loadedNestedMemoryPaths` 生命周期、compact-clear 后重建、attachment 注入链路。
- 实现建议：先定义 target-path 命中规则与生命周期清理，再接 `CLAUDE.local.md` / `.claude/rules` 物化；不要把它误当成 retrieval 小补丁来做。
- 实现状态：骨架已实现。

## 5. Background extractMemories

- 范围：stop-hook 后台补漏提取，降低主 agent 漏记风险。
- Claude 锚点：
  - `src/query/stopHooks.ts:L133-153`
  - `src/services/extractMemories/extractMemories.ts:L82-148, L154-222, L251-268, L296-615`
  - `src/services/extractMemories/prompts.ts:L26-154`
  - `src/memdir/memoryScan.ts:L24-94`
  - `src/utils/forkedAgent.ts:L46-141, L464-625`
- 风险：中。
- 预估工期：3-4 天。
- 依赖关系：stop-hook 生命周期、forked agent 执行器、manifest 去重、memory scan/写入 gate。
- 实现建议：优先保证幂等与“失败不污染主链”；manifest 预注入和重复提取抑制要一起交付。
- 当前 V3 进展：
  - `internal/module/memory/module.go` 已订阅 `thread.Stopped`，并异步触发 `ExtractAndSave`
  - `internal/module/memory/service.go` 已补 stop-hook 注册落点与 `Config.ExtractOnStop` 开关
- 实现状态：骨架已实现（mock）。

## 6. Auto-dream / Consolidation

- 范围：stop-hook 蒸馏服务，把会话沉淀回长期记忆。
- Claude 锚点：
  - `src/query/stopHooks.ts:L154-156`
  - `src/services/autoDream/autoDream.ts:L54-100, L122-129, L130-233, L235-323`
  - `src/services/autoDream/consolidationLock.ts:L21-140`
  - `src/services/autoDream/consolidationPrompt.ts:L1-64`
  - `src/tasks/DreamTask/DreamTask.ts:L1-156`
- 风险：中。
- 预估工期：4-6 天。
- 依赖关系：stop-hook 调度、consolidation lock、dream prompt、长期记忆写回协议、与 KAIROS daily log 的输入衔接。
- 实现建议：与 daily log 共排更稳；若只做 consolidation 不做日志来源，蒸馏质量和可解释性都会偏弱。
- 当前 V3 进展：
  - `internal/module/memory/auto_dream.go` 已补 `AutoDreamConsolidator.Consolidate(ctx, memoryRoot, extractFn)`，可扫描 memory 文件、清理重复/空内容项并重建 `MEMORY.md`
  - 当前 `ExtractFunc` 仍为 mock 骨架，尚未补齐 Claude parity 的 consolidation lock、dream prompt 与 daily log 输入
- 实现状态：骨架已实现（mock）。

## 7. Compact 完整 Parity

- 范围：section cache 联动、`getUserContext()` / entrypoint 重取、`loadedNestedMemoryPaths` 生命周期、`relevant_memories` 保留/清空语义。
- Claude 锚点：
  - `src/context.ts:L155-189`
  - `src/utils/claudemd.ts:L1142-1195`
  - `src/utils/api.ts:L321-435, L437-447, L449-474`
  - `src/services/api/claude.ts:L3213-3237`
  - `src/services/compact/postCompactCleanup.ts:L31-77`
- 风险：中（README 已明确：主链够用，但边缘行为与 Claude 不等价）。
- 预估工期：5-7 天。
- 依赖关系：thread/provider 主链接入、prompt cache invalidation、nested-memory 生命周期管理、attachment replay、`relevant_memories` transcript 语义整理。
- 实现建议：先补失效触发与主链接线，再追边缘 parity；当前最大缺口不是 cache 数据结构，而是运行态没有把 invalidate / reload 真正串起来。
