# P18 审查汇总

> 30 agent 多轮交叉审查结果 | 2026-04-14

## 审查矩阵

| # | Agent | 范围 | 结论 | 已合并到 |
|---|-------|------|------|---------|
| 1 | p18-review-1-memory-modes | 三种运行模式 | ⚠️ | Phase 2 |
| 2 | p18-review-2-memory-types | 四种记忆类型 | ⚠️ | Phase 0,1,2 |
| 3 | p18-review-3-memory-storage | 记忆存储层 | ⚠️ | Phase 1 |
| 4 | p18-review-4-memory-retrieval | 记忆检索 | ⚠️ | Phase 6 |
| 5 | p18-review-5-static-sections | 静态 Sections | ⚠️ | Phase 3 |
| 6 | p18-review-6-dynamic-sections | 动态 Sections | ⚠️ | Phase 3, README |
| 7 | p18-review-7-injection-pipeline | 注入管线 | ⚠️ | Phase 4 |
| 8 | p18-review-8-agent-memory | Agent 记忆 | ⚠️ | Phase 5 |
| 9 | p18-review-9-cache-boundary | 缓存策略 | ⚠️ | Phase 3, README |
| 10 | p18-review-10-source-refs | 源码锚点 | ✅ | source-refs-appendix |

## 关键修订清单

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
19. local ≠ 会话级，是持久非版本控制（Agent 8）
20. 缓存改用 Region + Volatile 模型（Agent 9）
21. 缓存语义补全：name-only / nil 也缓存（Agent 6/9）
22. boundary marker 降级为非核心（Agent 9）
23. nested_memory 排期到 P19（Agent 4）
24. 全量源码锚点附录（Agent 10）

## 第 7 轮：Claude/Codex 归一审查（30 agent）

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

## 第 8+9 轮：多维度深度审查（30 agent × 2）

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

## 第 10 轮：最终多视角收敛审查（30 agent）

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

## 第 11 轮：交叉终评收敛（30 agent）

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

## 第 12 轮：最终统一评分（30 agent）

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
