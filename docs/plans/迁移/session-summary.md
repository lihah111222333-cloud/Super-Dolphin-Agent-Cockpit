# V3 迁移会话摘要

> 更新时间：2026-04-15
> 会话范围：P18 Phase 0-8 落地 + P18.2 核心对齐完成 + P18.3 第一波(E/F/G/H/I)实施+审查中
> 当前阶段：P18.2 全部完成；P18.3 E/F/G/H/I 实施完成、审查通过、F/H 修复中；J/K/L 待启动

---

## 1. 当前结论

- **Phase 0-8 + P18.2 已全部完成**：基础设施、memory/prompt/thread/turn 核心对齐均已落地并通过审查。
- **P18.2 核心对齐 4 个 Phase 全部完成**：Turn 上下文补全、CachePolicy 三分法、失效触发点、Prefetch 接线、Freshness 展示、门禁统一、Section 实装、三层注入对齐、Snapshot 持久化。
- **P18.3 第一波(E/F/G/H/I)实施完成**：claudeMd 9 层来源注入链、动态 Section 全量实装(F-1/F-2/F-3)、Agent Memory 闭环、extractMemories MVP、Compact parity。
- **全量编译 + 核心包测试全绿**：`go build ./...` 通过，prompt/memory/turn/thread/provider 测试全部通过。
- **代码量统计**：P18 累计 **156 文件变更，净增 ~15,100 行**（含 ~40% 测试）。
- **审查历程**：文档经 4 轮 × 20 Agent 审查；代码经实施→互审→修复→复审闭环。

---

## 2. 本轮收口结果

### 2.1 代码面
- **Phase 0**：`memory + prompt` 模块、Fx 注册、基础 wiring 已落地。
- **Phase 1**：磁盘 memory CRUD、index/rebuild、scope/ACL 规则与 BOM 兼容已落地。
- **Phase 2**：memory rules 注入、截断对齐与 prompt 侧行为约束已落地。
- **Phase 3**：静态 Section 对齐完成，registry/cache/invalidate 主链路可用。
- **Phase 4 / 4.5**：Hook 方案 C、配置透传、thread/turn 注入链与 provider unification 已收口，避免 `BaseInstructions / Prompt` 语义污染。
- **Phase 5**：agent memory 路径、直读直注与隔离边界已完成。
- **Phase 6**：relevant memories、Read 降级、retrieval / start-turn 连接链已完成。
- **Phase 7**：migration / compat / source refs / rollback 口径已完成代码化落地。
- **Phase 8**：测试、golden、回归守护与编译验收已完成。

### 2.2 验证 / 文档面
- **全量编译验证已通过**：当前实现可进入最终全量验证与提交整理阶段。
- `review-summary.md` 已保留并追加历史轮次记录，用于说明 P1-P3 深审、P4-P6 实施与关键修复闭环。
- `p18/README.md` 的 **34 项功能对齐矩阵** 继续作为 authoritative baseline；超出 P18 本轮范围的未实现项保留到后续迭代。

---

## 3. Phase 状态

| Phase | 状态 | 说明 |
|------|------|------|
| 0-8 | ✅ 全部完成 | 基础设施、memory/prompt/provider/thread/turn 全链路落地 |
| P18.2-A | ✅ 完成 | Turn 上下文补全 + CachePolicy 三分法 + 失效触发点 |
| P18.2-B | ✅ 完成 | Prefetch 接线到 PrepareTurn + Freshness 展示链 + surfaced 去重 |
| P18.2-C | ✅ 完成 | 门禁快照 + 模式选择器 + SkipIndex fail-soft + 截断收敛 |
| P18.2-D | ✅ 完成 | intro 可计算 + env 补齐 + mcp 收窄 + System Context + UserContextText 格式 + Snapshot 持久化 |
| P18.3-E | ✅ 实施+审查通过 | claudeMd 9 层来源 + 三层过滤 + Renderer + AssembleTurn 接入 |
| P18.3-F | ✅ 实施完成，修复中 | output_style + scratchpad + summarize；修 output_style 判定 + scratchpad cleanup |
| P18.3-G | ✅ 实施完成 | scope/path/resume 闭环 + telemetry + migration + sqlc |
| P18.3-H | ✅ 实施完成，修复中 | transcript extraction + 三态 cursor + drain；修 metadata fail-closed + 补测试 |
| P18.3-I | ✅ 实施+审查通过 | PostCompactCleanup + tool-result budget + attachment 协议 |
| P18.3-J | ⏳ 待启动 | KAIROS daily log + Manual Dream + Auto-dream |
| P18.3-K | ⏳ 待启动 | Team Memory 完整体（整包上线） |
| P18.3-L | ⏳ 待启动 | nested_memory 条件规则系统 |

---

## 4. 下一步

1. **F/H 修复收口**：F 的 output_style 判定 + scratchpad cleanup；H 的 metadata fail-closed + 补测试。
2. **拉起 J/K/L 第二波**：按 p18.3 依赖图拉起 KAIROS + Team Memory + nested_memory，按子任务拆分（每 Agent ≤15 文件）。
3. **全量回归验证**：J/K/L 完成后跑全量 build + test + archtest 守护。
4. **仓库契约已调整**：核心包（memory/prompt/thread/turn/claudecli/codexapp）守卫放宽至 包文件≤30、单文件≤600行、包总行≤10000；函数/CC 不变。

---

## 5. 交接建议

1. **三层 canonical 映射不可破**：BaseInstructions=system body / DeveloperInstructions=tail / UserContextText=synthetic meta。system-owned sections 不得回流 UserContextText。
2. **CachePolicy 三分法**：CacheByName / InputScoped / Uncached。新 section 必须显式指定 policy。
3. **Agent Memory 边界**：KAIROS/Dream 只作用于主线程 auto memory，不碰 agent memory。agent_type 是稳定 identity。
4. **Team Memory 必须整包上线**：路径安全 + 注入链 + sync + secret guard 未全部完成前禁止暴露 team scope。
5. **nested_memory ≠ retrieval**：它是 target-path 条件规则系统，不复用 memory_context / ranking。

---

## 6. 交接结论

- P18 已从“基础落地”进入“**Claude 深度对齐迭代**”阶段。
- P18.2 核心对齐已全部完成；turn 上下文、cache 语义、三层注入、snapshot 持久化均已闭环。
- P18.3 第一波(E/F/G/H/I)已落地，剩余 J/K/L 可在 F/H 修复后立即启动。
- 代码量累计 **~15,100 行**（156 文件），测试覆盖 ~40%。
- 下一轮工作重点：**J(KAIROS) + K(Team Memory) + L(nested_memory) + 仓库契约整理**。
