# V3 迁移会话摘要

> 更新时间：2026-04-14
> 会话范围：**P18 第二轮：20 Agent 审查+修订** + **source-refs / Phase 8 / session-summary 刷新**
> 本次验证：`git diff --check -- docs/plans/迁移/session-summary.md` 通过

---

## 1. 当前结论

- **P18 全 13 个文档已完成第二轮 20 Agent 审查+修订**：术语、工期、注入链路、ACL、源码锚点与测试口径再次收口
- **Phase 0 骨架已落地**：`memory + prompt` 模块、fx 注册和基础测试已到位
- **13% 对齐差距已精准定位**：`README.md` 已显式登记 6 个关键缺失功能，并按风险口径管理延期
- **Phase 4.5 / source-refs / Phase 8 已补强**：Prompt 污染 16 位置地图、0-7 关键路径源码锚点、测试矩阵都已写回文档
- **本轮文档校验通过**：`git diff --check -- docs/plans/迁移/session-summary.md` 已通过

---

## 2. 本会话核心成果（2026-04-14）

### 2.1 Baseline PK 修复
- **根因**：`001_baseline.sql`（pg_dump 快照）丢失所有 PRIMARY KEY，后续 migration 用 `CREATE TABLE IF NOT EXISTS` 跳过
- **修复**：
  - `001_baseline.sql` 补全 10 个表的 PK/UNIQUE
  - `0029_agent_provider_binding_schema_repair.sql` 幂等修复 binding 表
  - `0030_baseline_schema_repair.sql` 幂等修复其余 9 个表
  - 守护测试防回归
- **commit**：`5c2c15b`

### 2.2 P18 记忆系统+系统提示词架构
- **基于**：Claude Code 官方源码逆向文档（4 份 mapping + source_refs）
- **产出**：`docs/plans/迁移/p18/` 下 13 个文档（README + Phase 0-8 + Phase 4.5 + source-refs + review-summary）
- **审查历程**：
  - 第 1-6 轮：30 agent × 6 = 180 轮次 → 7.0 → 8.2
  - 第 7 轮：30 agent 归一审查 → 新增 Phase 4.5
  - 第 8-13 轮：30 agent 多维度/交叉 → 8.6 → 9.0
  - 第 14 轮：50 agent 深度 + 50 agent 交叉 = 100 轮次
- **关键设计决策**：
  - 记忆存储：磁盘为主（对齐 Claude Code），DB shared_files 保留用于 agent 间协作
  - 四种记忆类型：user/feedback/project/reference（Individual 版本）
  - 提示词：7 静态 + 5 动态 section，按 name 缓存
  - Provider 归一：PromptAssemblyService 放 internal/module/prompt，不放 provider 内部
  - 解耦方案 C：Name 贯通 lifecycle，BaseInstructions 不污染 Prompt
  - 工期：15-18 天
  - Claude 源码对齐度：~87%

### 2.3 额外修复
- prompt_templates 表 description 列缺失（0027 migration 已覆盖）
- codex agent 启动失败排查（根因同 baseline PK）

---

## 3. 2026-04-14 第二轮：20 Agent 审查+修订

- **审查范围**：P18 全部 13 个文档（README + Phase 0-8 + Phase 4.5 + source-refs + review-summary）
- **统一收口**：
  - 术语：`PromptAssemblyService / StartAssembly / TurnAssembly / relevant memories / displayName` 全面对齐
  - 工期：P18 总工期继续收口为 **15-18 天**，Phase 8 拆成 **1-2 天（P0 / P1）**
  - 注入链路：thread/start、turn/start、codex/claude、orchestration bridge 的职责边界重新收口
  - ACL / 安全：`@agent` 可见性、memory ACL、`degraded=true`、secret redaction 等条目补齐
- **核心成果**：
  - P18 全部 13 个文档经 20 Agent 审查修订
  - 术语 / 工期 / 注入链路 / ACL 已全面对齐
  - Phase 0 代码骨架已落地：`memory + prompt` 模块 + fx + 测试绿
  - `README.md` 已写入 **13% 对齐差距**：6 个关键缺失功能 + 风险评级/延后口径
  - Phase 4.5 已写入 **Prompt 污染完整地图（16 位置）**
  - `source-refs-appendix.md` 已扩充覆盖 Phase 0-7 关键路径
  - Phase 8 测试条目已大幅扩充，覆盖 provider 注入 / retrieval / compat / rollback drill / roundtrip
- **当前结论**：P18 已从“方案审查”进入“按 checklist 分阶段实施”阶段

---

## 4. P18 Phase 索引

| Phase | 内容 | 预计 |
|-------|------|------|
| 0 | 模块骨架 + fx 注册 | 1 天 |
| 1 | 磁盘记忆存储层 | 2 天 |
| 2 | 记忆行为规则注入 | 1 天 |
| 3 | Section 注册表（静态+动态） | 2 天 |
| 4.5 | Provider 归一前置解耦 | 3-5 天 |
| 4 | Provider 链路注入 | 1 天 |
| 5 | Agent 记忆隔离 | 1 天 |
| 6 | 记忆检索 | 2 天 |
| 7 | 迁移工具 + 兼容层 | 1 天 |
| 8 | 测试 + 守护 | 1-2 天 |

---

## 5. 已知限制

1. Claude `compact` 暂不支持，仍需摘要+重启方案
2. nested_memory 排期到 P19
3. KAIROS daily log / Team Memory 暂不实现
4. 前端 E2E 桥接测试仍不稳定（历史已知）

---

## 6. 下一会话建议关注

1. **Phase 1 存储层实现**（已有 Agent 在做）：优先落磁盘 CRUD / `MEMORY.md` / `canonical_name` / rebuild / `skipIndex`
2. **Phase 3 Section Registry 实现**：先打通 12 个 section 注册、cache/invalidate 与 `PromptAssemblyService`
3. **Phase 4.5 解耦实施**（已有 Checklist）：按 Prompt 污染 16 位置地图和 `source-refs` 锚点逐点清理

---

## 7. 交接结论

- P18 第二轮 20 Agent 收口已完成，当前文档口径可直接作为实施基线
- Phase 0 已落地，下一重点转到 **Phase 1 / Phase 3 / Phase 4.5**
- 本轮已完成 `session-summary.md` 刷新，`git diff --check` 通过
