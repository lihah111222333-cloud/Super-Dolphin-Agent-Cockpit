# P18 审查汇总

> 30 个 agent 多轮交叉审查结果 | 2026-04-14
> 当前 authoritative source：第 14 轮“20 Agent 自动化审查”；第 10 轮为首次整体通过，第 13 轮为最后一次完整综合评分结论，第 14 轮为最新增量收口结论。

## 审查矩阵（首批专项审查 10 agent）

> 该矩阵只覆盖首批 1-10 号专项审查；第 7-13 轮综合收敛结果见下文。

| # | Agent | 范围 | 结论 | 已合并到 |
|---|-------|------|------|---------|
| 1 | p18-review-1-memory-modes | 三种运行模式 | ⚠️ | [Phase 2](phase-2-memory-prompt-rules.md) |
| 2 | p18-review-2-memory-types | 四种记忆类型 | ⚠️ | [Phase 0](phase-0-infrastructure.md), [Phase 1](phase-1-memory-storage.md), [Phase 2](phase-2-memory-prompt-rules.md) |
| 3 | p18-review-3-memory-storage | 记忆存储层 | ⚠️ | [Phase 1](phase-1-memory-storage.md) |
| 4 | p18-review-4-memory-retrieval | 记忆检索 | ⚠️ | [Phase 6](phase-6-memory-retrieval.md) |
| 5 | p18-review-5-static-sections | 静态 Sections | ⚠️ | [Phase 3](phase-3-prompt-registry.md) |
| 6 | p18-review-6-dynamic-sections | 动态 Sections | ⚠️ | [Phase 3](phase-3-prompt-registry.md), [README](README.md) |
| 7 | p18-review-7-injection-pipeline | 注入管线 | ⚠️ | [Phase 4](phase-4-provider-injection.md) |
| 8 | p18-review-8-agent-memory | Agent 记忆 | ⚠️ | [Phase 5](phase-5-agent-memory.md) |
| 9 | p18-review-9-cache-boundary | 缓存策略 | ⚠️ | [Phase 3](phase-3-prompt-registry.md), [README](README.md) |
| 10 | p18-review-10-source-refs | 源码锚点 | ✅ | [source-refs-appendix.md](source-refs-appendix.md) |

## 截至第 14 轮已修订清单

### 已修订 ✅
1. MemoryEntry 区分 frontmatter 持久化字段 vs 运行时元数据（Agent 2）
2. 目录结构改用 canonical git root（Agent 3）
3. team/ 放置位置修正（Agent 3）
4. 路径安全校验补全：相对路径、根路径、null byte（Agent 3）
5. 截断顺序写死：trim → 200行 → 25KB → warning（Agent 3）
6. MEMORY.md 索引约束补全（Agent 3）
7. 运行模式分派优先级写明（Agent 1）
8. Individual vs Combined taxonomy 区分（Agent 2）
9. 排除列表补全（Agent 2）
10. 异步预取完整语义：per-turn start + per-iteration consume（Agent 4）
11. manifest 精确构建规则（Agent 4）
12. 双层去重机制（Agent 4）
13. 7 个静态 section 内容规格补全（Agent 5）
14. 13 个动态 section 全量对照表（Agent 6）
15. 6 个未决 section 显式排除（Agent 6）
16. UserContext 从 thread/start 移到 turn/start（Agent 7）
17. 两阶段装配架构（Agent 7）
18. Agent memory 三种 scope 完整目录（Agent 8）
19. local scope 明确为持久但不进版本控制（Agent 8）
20. 缓存改用 Region + Volatile 模型（Agent 9）
21. 缓存语义补全：name-only / nil 也缓存（Agent 6/9）
22. boundary marker 降级为非核心（Agent 9）
23. nested_memory 转记为 P18 未实现部分（Agent 4）
24. 全量源码锚点附录（Agent 10）
25. `PromptAssemblyService` / `StartAssembly` / `TurnAssembly` 术语统一（第 14 轮，7 文件）
26. P18 总工期口径修正为 15-18 天（第 14 轮）
27. Phase 5 local scope 生命周期 + ACL / 可见性边界补齐（第 14 轮）
28. guarded wrapper 全量替换：构建/测试/静态检查命令统一收口到 `./scripts/go_with_guard.sh`（第 14 轮）
29. Phase 4 ↔ 4.5 `UserContextText` 字段口径、依赖图与职责边界同步（第 14 轮）
30. README 补入 13% 对齐差距分析（第 14 轮）
31. Phase 4.5 补 Prompt 污染 16 位置完整地图（第 14 轮）
32. `source-refs-appendix.md` 补完 `SetName` / `LaunchAgent` / Phase 0-7 关键路径锚点（第 14 轮）
33. Phase 8 测试条目扩充覆盖（第 14 轮）

## 第 7 轮：Claude/Codex 归一审查（30 个 agent）

### 关键发现
- Prompt/BaseInstructions 语义污染需先解耦（方案 C：Name 贯通 lifecycle）
- PromptAssemblyService 应放 internal/module/prompt，不放 provider 内部
- 注入点统一在 start_session.go provider DTO 前
- codex wire: baseInstructions + developerInstructions（无 userContext）
- claude wire: 全拼 --system-prompt + turn 单消息
- 子 Agent 不继承 instructions，需归一
- Provider 切换时 cache 必须清空
- 需新增 Phase 4.5 前置解耦（额外 3-5 天）

### 综合评分：7.5/10 → 需拆独立 Phase

## 第 8+9 轮：多维度深度审查（30 个 agent × 2）

### 审查维度
安全 / 性能 / 一致性 / 可测试性 / 向后兼容 / 仓库契约 / 错误处理 / 并发安全 / 迁移回滚 / 可观测性 / 用户体验 / 文档质量 / 国际化 / 磁盘IO / fx集成 / 并行可行性 / 代码规模 / V2 parity

### 关键修订
- Phase 4.5 Claude turn 顺序修正
- Phase 8 补 4.5 回归测试项
- Phase 3 TodoWrite 改为真实表述
- README 暂不实现理由微调
- Phase 7 种子迁移原则补充
- Phase 2 补敏感信息禁止规则
- Phase 6 验收"双层"改"三段式"

### 综合评分：8.9/10 → 通过

## 第 10 轮：最终多视角收敛审查（30 个 agent）

### 关键结论
- 文档总体已达到实施门槛，可进入开发（最终压轴终评通过）
- README 补齐实施边界/新人入口，并将执行顺序收敛为 `0 → 1 → 2 → 3 → 4.5 → 4 → 5/6/7 → 8`
- Phase 0 补模块职责全景、fx 装配落点、集中配置骨架
- Phase 1 补 topic file 命名、索引全量重写、unknown type 处理与服务端敏感信息校验
- Phase 4.5 补 legacy/Wails 兼容迁移策略、`StartInput/TurnInput/PromptAssemblySnapshot`、职责边界、provider 切换触发点
- Phase 7/8 补工具协议、rollout flags、kill switch、rollback drill、可观测性要求
- `source-refs-appendix.md` 补齐 Phase 4.5 缺失锚点

### 特别关注
- Agent 20（严格终评复核）：已确认第 8/9 轮文档 blocker 基本收敛；剩余问题主要是代码基座尚未实现，不属于本轮文档修订阻塞项
- Agent 30（最终压轴终评）：**9.0/10**，结论 **通过，可开工**

### 综合评分：9.0/10 → 文档通过，可进入实施

## 第 11 轮：交叉终评收敛（30 个 agent）

### 关键修订
- Phase 4 补全 claudeMd 来源：补 `filterInjectedMemoryFiles()` 过滤语义、`getClaudeMds()` 包装链、未来 team-memory-content 包装说明
- Phase 8 补 provider-specific 回归、PromptAssembly 契约断言、Restart/Recovery snapshot 用例、thread/provider 验证命令
- Phase 4.5 收紧任务 5/6：去掉无效的 thread config/set provider 切换表述，子 Agent 归一改为单一路径
- `source-refs-appendix.md` 补 `query.ts` 与 `claudemd.ts` 锚点
- 修正 `review-summary.md` 顶部旧口径（不再写“10 个 codex agent”）

### 重点独立评分
- Agent 1：**8.8/10** → 通过
- Agent 20：**8.9/10** → 通过
- Agent 27：**8.0/10** → 有条件通过
- Agent 28：**9.0/10** → 通过
- Agent 29：**6.0/10** → 不通过（主要保留异议：Phase 4.5 任务 5/6 仍需收口）
- Agent 30：**9.0/10** → 通过

### 综合评分
- 重点独立评分中位数：**8.85/10**
- 结论：**通过（保留 Agent 29 的少量实施层异议，但不再构成文档阻塞）**

## 第 12 轮：最终统一评分（30 个 agent）

### 统计
- 已收齐 **30/30** agent 报告
- 其中 **25** 个给出显式数值评分，**5** 个给出专项通过/不通过判定
- 显式评分**均值：8.60/10**
- 显式评分**中位数：8.80/10**
- **通过率：86.7%（26/30）**（含“有条件通过 / 基本通过”计入通过）

### 关键收口
- Phase 2 补充“`BuildMemoryPrompt()` 是 V3 适配入口名”，避免与 Claude 源码函数名逐字误对齐
- Phase 1 补 `MEMORY.md` 并发写保护与临时文件原子替换
- Phase 3 补 section cache 并发语义与单 section fail-safe
- Phase 7 补 feature flag disabled / 迁移失败 / 共享写锁 的错误处理与并发安全要求
- Phase 8 补 auto-compact / partial compact invalidate、并发写、显式错误返回测试
- 采纳 Code Reviewer 提醒：`req.Instructions` fallback 的实现语义应落在 provider DTO（如 `dto.StartSessionRequest.Instructions`）映射层，而不是字面扩展 `thread.StartRequest`

### 特别关注
- Agent 29：**8.0/10**，由上轮 **6.0** 提升到“基本通过 / 可执行”
- Agent 30：**9.0/10**，结论 **通过，可直接进入实施**

### 最终结论
**P18 文档终稿通过，可按 `0 → 1 → 2 → 3 → 4.5 → 4 → 5/6/7 → 8` 顺序进入实施。**

## 第 13 轮：收官统一复核（30 个 agent）

### 统计
- 已收齐 **30/30** agent 报告
- 其中 **25** 个给出显式数值评分，**5** 个给出专项通过/不通过判定
- 显式评分**均值：8.61/10**（较第12轮 **8.60** ↑ **0.01**）
- 显式评分**中位数：8.80/10**（与第12轮持平）
- **通过率：86.7%（26/30）**（与第12轮持平）

### 本轮修订
- Phase 4.5 修正 `req.Instructions` 表述：兼容 fallback 落在 provider DTO `dto.StartSessionRequest.Instructions` 映射层
- Phase 6 补 relevant memories 的并发安全语义（generation、幂等 consume、跨 provider 隔离）
- 继续收口 Phase 1 / 3 / 7 / 8 的并发写保护、cache generation、feature-disabled 显式错误、原子替换与 race 回归口径
- 继续收口 rollout / rollback / kill switch / QA 厚度相关文档要求

### 特别关注
- Agent 29：**8.0/10**，较更早轮次的 6.0 已明显回升，本轮维持“基本通过 / 可执行”
- Agent 30：**9.0/10**，结论 **通过，可直接进入实施**

### 结论
**第13轮收官后，文档结论维持通过；剩余分歧主要集中在实施期风险偏好，不再构成文档阻塞。**

## 第 14 轮（2026-04-14 20 Agent 自动化审查）

### 审查方式
- 本轮采用 **20 Agent 自动化复核**，基于仓库当前 `git diff --stat` 对 P18 全量文档做术语、一致性、依赖关系、验证口径与源码锚点回归审查。
- 本轮以收口修订为主，不重复进行第 13 轮那类综合打分；重点确认本轮增量修改是否消除新的文档歧义与实施偏差。

### git diff --stat（追加本记录前）
- `README.md`
- `docs/plans/迁移/p18-memory-prompt-system-plan.md`
- `docs/plans/迁移/p18/README.md`
- `docs/plans/迁移/p18/phase-0-infrastructure.md`
- `docs/plans/迁移/p18/phase-1-memory-storage.md`
- `docs/plans/迁移/p18/phase-2-memory-prompt-rules.md`
- `docs/plans/迁移/p18/phase-3-prompt-registry.md`
- `docs/plans/迁移/p18/phase-4-provider-injection.md`
- `docs/plans/迁移/p18/phase-4.5-provider-unification.md`
- `docs/plans/迁移/p18/phase-5-agent-memory.md`
- `docs/plans/迁移/p18/phase-6-memory-retrieval.md`
- `docs/plans/迁移/p18/phase-7-migration-compat.md`
- `docs/plans/迁移/p18/phase-8-testing.md`
- `docs/plans/迁移/p18/source-refs-appendix.md`
- `docs/plans/迁移/session-summary.md`
- `internal/app/modules.go`
- 统计：**16 files changed, 361 insertions(+), 127 deletions(-)**

### 本轮已完成修订
1. **术语统一**：`PromptAssemblyService` / `StartAssembly` / `TurnAssembly` 在 **7 个文件**内完成统一，收口旧术语混用。
2. **工期修正**：P18 总工期口径统一修正为 **15-18 天**。
3. **Phase 5 local scope 修正**：补齐 local scope 的生命周期说明，并明确 ACL / 可见性边界与线程可见范围。
4. **guarded wrapper 全量替换**：P18 phase 文档中的构建/测试/静态检查命令已统一切换到 `./scripts/go_with_guard.sh`。
5. **Phase 4 ↔ 4.5 `UserContextText` 同步**：补齐跨 phase 的字段口径、职责边界与依赖图，避免 provider 适配与 assembly 设计脱节。
6. **13% 对齐差距分析写入 README**：将当前与 Claude 设计的差距分析显式写入 README，避免实施期误判“文档已 100% 对齐”。
7. **Prompt 污染 16 位置完整地图写入 Phase 4.5**：把污染来源、旧路径兼容点与收口目标集中成图，便于迁移时按点清除。
8. **source-refs 锚点补完**：补齐 `SetName` / `LaunchAgent` / Phase 0-7 关键路径源码锚点，降低实现落点歧义。
9. **Phase 8 测试条目扩充覆盖**：补强 provider 注入、assembly snapshot、orchestration bridge、guarded 命令、roundtrip 与回归场景覆盖。

### 结论
- 第 13 轮“**通过，可进入实施**”的总体判断维持不变。
- 第 14 轮完成的是一次**自动化增量收口审查**：重点修正术语、验证命令、依赖图、源码锚点与测试覆盖口径。
- 本轮未引入新的文档 blocker；P18 文档可继续按 `0 → 1 → 2 → 3 → 4.5 → 4 → 5/6/7 → 8` 顺序推进实施。

## 第 15 轮（2026-04-14 最终代码收口审查）

### 审查方式
- **P1-P3 Claude 源码深度审计**：投入 **20 agent** 对 memory / prompt / source-resolver / section cache / assembly 主链做源码级逐点复核，并完成 **16 处修复** 收口。
- **P4 / P5 / P6 代码实现收敛**：投入 **8 agent** 并行完成 provider 注入、agent memory、retrieval / start-turn 链、兼容层与测试补齐的最终闭环。

### 本轮关键修复
1. **配置透传**：补齐 provider / request config 的透传路径，避免 assembly 层和 provider DTO 之间字段丢失。
2. **turn 链接线**：补齐 start → thread → turn 的接线关系，确保 prompt / retrieval / provider 注入按同一上下文演进。
3. **Read 降级**：为 retrieval / read-path 增加失败降级与兼容兜底，避免单点读取异常放大为整链失败。
4. **BOM 兼容**：补齐 `MEMORY.md` / index / topic file 的 UTF-8 BOM 兼容处理，保证入口文件与磁盘 memory 在混合编辑器场景下可稳定解析。
5. **截断对齐**：统一 memory entrypoint / prompt 侧的截断口径，避免 Claude 对齐链路在边界长度上出现漂移。

### 验证结果
- **Phase 0-8 全部代码落地**：P18 范围内实现、兼容层、测试与 golden 已完成交付闭环。
- **Claude 三条注入链全部打通**：主线程 entrypoint 链、agent memory 链、relevant memory / retrieval 链已收口到统一实现口径。
- **87+ 测试 + golden 已纳入守护覆盖**：当前回归口径可作为后续演进基线。
- **全量编译验证通过**：当前 HEAD 可进入最终全量验证、commit 整理与未实现部分迭代阶段。

### 结论
- 第 15 轮标志着 P18 已从“文档通过、等待实施”进入“代码完成、等待最终交付验证”状态。
- 后续工作重点转为：**全量验证 + commit + P18 未实现部分迭代**。
