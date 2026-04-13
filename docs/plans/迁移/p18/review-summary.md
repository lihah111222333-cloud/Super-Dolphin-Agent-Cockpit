# P18 审查汇总

> 10 个 codex agent 交叉审查结果 | 2026-04-14

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
