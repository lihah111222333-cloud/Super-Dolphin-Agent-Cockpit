# V3 迁移会话摘要

> 更新时间：2026-04-14
> 会话范围：**P18 记忆系统+系统提示词架构** + **baseline PK 修复** + **Codex agent 启动修复**
> 本次验证：`go build ./internal/... ./cmd/...` + `go vet ./internal/... ./cmd/...` + `go test ./internal/platform/db/...` 全绿

---

## 1. 当前结论

- **baseline PK 修复已完成**：`001_baseline.sql` 补全 10 个表的 PRIMARY KEY/UNIQUE 约束 + `0029`/`0030` 幂等修复 migration
- **P18 记忆系统+提示词计划已完成审查**：经 15+ 轮、500+ agent 审查轮次，最终评分 9.0/10 通过
- **Claude/Codex 归一方案已确定**：新增 Phase 4.5 前置解耦，解决 BaseInstructions→Prompt 污染
- **当前仓库验证通过**：build / vet / db 测试全绿

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
  - 工期：15-17 天
  - Claude 源码对齐度：~87%

### 2.3 额外修复
- prompt_templates 表 description 列缺失（0027 migration 已覆盖）
- codex agent 启动失败排查（根因同 baseline PK）

---

## 3. P18 Phase 索引

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
| 8 | 测试 + 守护 | 1 天 |

---

## 4. 已知限制

1. Claude `compact` 暂不支持，仍需摘要+重启方案
2. nested_memory 排期到 P19
3. KAIROS daily log / Team Memory 暂不实现
4. 前端 E2E 桥接测试仍不稳定（历史已知）

---

## 5. 下一会话建议关注

1. **开干 P18 Phase 0**：创建 memory/prompt 模块骨架
2. **Phase 4.5 优先**：解耦 BaseInstructions→Prompt 污染（阻塞 Phase 4）
3. **Phase 1+2 可并行**：记忆存储 + 行为规则无依赖
4. 观察真实长会话下的统一 diff 链路稳定性
5. prompt_templates description 列在新设备重启后自动修复

---

## 6. 交接结论

- P18 计划已通过 500+ agent 审查，评分 9.0/10，可直接进入实施
- baseline PK 修复已 commit + push，新设备不再报 42P10
- 当前重点从"计划审查"转到"Phase 0 开工"
