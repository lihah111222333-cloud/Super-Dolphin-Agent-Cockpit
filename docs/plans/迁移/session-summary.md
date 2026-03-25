# V3 迁移会话摘要

> 生成时间：2026-03-25（P8 审查+修复+P0 安全修复+V2V3 二次核对 完成）
> 会话范围：P0-P8.5 全程 + P7.5 桥接 + V2↔V3 核对×2 + archtest 收官 + MCP 独立服务 + ctl/* 回调框架 + lifecycle hooks + P8 审查修复 + P0 安全修复
> Claude 会话 UUID：58fdd978-cc4b-41e6-bd26-d40f3ff66854
> 前序会话 UUID：ea3ad84e-7b52-422d-bc46-cff9da3ea9f9

---

## 1. 当前结论

### 编译验证：全绿
```
✅ go build ./internal/... ./cmd/mcp-orch/...  — 0 errors
✅ go vet ./...                                — 0 warnings
✅ lsp diagnostics                             — 0 errors (仅 hints)
✅ archtest TestCodeSizeGuard                  — PASS
✅ archtest TestDependencyDirection             — PASS
✅ archtest TestTimeoutLocality                 — PASS
```

### 迁移状态

| 阶段 | 状态 | 内容 |
|---|---|---|
| P0 骨架 | ✅ | 4 二进制 + platform 6 包 + contract/dto + Zone A/B 工厂 |
| P1 Store | ✅ | sqlc 96 方法 + 19 repo adapter + WrapStoreError 全覆盖 |
| P2 Event Bus | ✅ | kelindar/event + 6 族 typed event + 泛型工厂 |
| P3 状态机 | ✅ | 10 状态 + 11 触发器 + 严格模式 |
| P4 Provider | ✅ | unified + claudecli + codexapp + SessionManager lifecycle |
| P5 RPC | ✅ | 79+ handler + 全部 Blocker 清零 |
| P6 Wails | ✅ | RunDesktop + CallAPI/Dispatch + 原生绑定 + 双向 shutdown + event bridge |
| P7w1 V2 兼容 | ✅ | D1-D12 全部修复 + 互审 8 问题修复 |
| P7w2 Dashboard | ✅ | module/dashboard — agent 监控 + 系统信息 + 日志 + query |
| P7w2 UI State | ✅ | module/uistate — bus projector + 实时快照 + sidebar + preferences + projects |
| P7.5 桥接校准 | ✅ | ~24 handler 补建 + 4 事件桥接 + knownDiffRevision + 安全加固 |
| P8 前置 | ✅ | D-1 sqlc 漂移 + D-2 runtime 上报 + D-3 状态机封堵 + dbquery 执行器 |
| V2↔V3 核对 | ✅ | 21 模块 1:1 核对 + 修复 + 互审，排除 MCP 后残留归零 |
| archtest 收官 | ✅ | 35+6 项违规全部修复 + rule15(hooks→db) + _test.go 覆盖 |
| P8 编排工具 | ✅ | orchestration 迁移到 cmd/mcp-orch, 19 handler, CodeSizeGuard PASS |
| P8.5 ctl 回调框架 | ✅ | 15 方法, 三 binary 统一, bootstrap 三封装 |
| P8.5 lifecycle hooks | ✅ | 7 层 transport, 覆盖率 85.1%, 35+ 测试 |
| **P8 审查+修复** | ✅ | **五维审查 + 互审校准 + 20 项修复（P2×4+P3×3+P4×6+低优×4+P0×3）** |
| **V2↔V3 二次核对** | ✅ | **20 模块重新核对 + 1:10 互审 + 二次审修正 + 三方审计** |
| **P0 安全修复** | ✅ | **3 项 P0 安全退化全部修复（guard 链+binding id+approval auto-decline）** |
| P9 LSP 工具 | ⏳ | MCP LSP 工具独立服务（`cmd/mcp-lsp`），9 个工具，计划已出 |
| P10 工厂丰满 | ⏳ | Zone A 3.8%→目标 60%，计划已出 |

---

## 2. 本会话（2026-03-25）完成的重大任务

### 2.1 P8 五维审查+修复（本会话核心成果 1）

**审查流程**：5 初审 Agent → 4 互审 Agent → 校准 → 修复 → 互审 → 反驳 → 修复

**五维审查评分**：

| 维度 | 初审 | 互审后 |
|------|------|--------|
| 代码质量 | B+ | B+ |
| 契约边界 | B+ | A- |
| 测试健壮性 | A- | A |
| Hooks 架构一致性 | A- | A- |
| 安全健壮性 | A- | A |

**修复清单（20 项，全部交付）**：

P2 修复（4 项）：
1. IntersectTargets 单元测试（fanout_test.go）
2. resolvedReviewReader 提升到 contract（contract/hooks.go + resolver.go）
3. hooks 解耦 platform/db（ErrHookReviewNotFound sentinel）
4. resolved_by 字段全链路（DTO→contract→resolver→store→migration 0026）

P3 修复（3 项）：
5. mcpcontrol 15 个 exported 类型 doc comment
6. 设计文档 After 语义对齐（p8-lifecycle-hooks.md）
7. archtest rule15: hooks 不 import platform/db（含 _test.go）

P4 修复（6 项）：
8. 文件拆分（service→helpers, registry→registry_helpers, rpc_types→dag）
9. IntersectTargets 维度补充 + bootstrap t.Parallel
10. AfterDecision TTL（DTO + merge + manager + resolver）
11. session-summary 表数 24→19 修正
12. CodeSizeGuard 修复（service_support→helpers, orchestration 15→13 文件）
13. fanout_test thread_id 精确化 + Module doc + TTL 多 subscriber 测试 + rule15 扩展

低优修复（4 项）：
14. dispatcher 外层 panic recovery（dispatchWorkerState + markPanicResult）
15. env_test.go 环境隔离（resetBootEnvVars helper）
16. TestTimeoutLocality 8 处（WithTimeout + WithTimeoutIfNone helper）
17. orchestration 文件数 15→13（contract.go+module.go→service.go）

### 2.2 P0 安全退化修复（本会话核心成果 2）

| P0 | 问题 | 修复 |
|----|------|------|
| P0-1 | thread-start 默认值/guard 链不对齐 | resolveStartConfig 4 子函数：danger→never, enum guard(含 on-request/on-failure/untrusted), sandbox 容错, cwd fallback + Claude transport 映射 |
| P0-2 | binding/thread-id 一致性退化 | public/provider id 分层 + 首写 authoritative + mismatch 拒绝 + 幂等 backfill + orphan fail-closed + 事务保护（binding 回滚）+ Stop 用 public id |
| P0-3 | approval fail-closed/auto-decline 缺失 | 三路 auto-decline（无前端→decline, agent gone→deny, policy=never→auto-approve）+ resume 恢复 policy + 单边 nil 诊断 + requestID 去重（callID:requestID 组合键 + 并发占位 + Close 清理 + 容量上限 1000）|

### 2.3 V2↔V3 二次核对（本会话核心成果 3）

- 20 个 Codex Agent 覆盖全部模块（排除 P9 LSP MCP 和 IDA）
- 1:10 互审 + 二次审修正
- 2 个汇总 Agent（Codex + Claude）
- 3 个审计 Agent（2 Codex + 1 Claude）
- 终极报告：docs/plans/迁移/v2v3-recheck-final.md

**核对结果对比**：

| 指标 | 上次（03-21） | 本次（03-25） |
|------|-------------|-------------|
| ✅ 1:1 完成 | 0 / 21 | 0 / 20 |
| ⚠️ 部分对齐 | 0 / 21 | **12 / 20** |
| ❌ 仍不一致 | 21 / 21 | **8 / 20** |
| 已修复子项 | — | **23 个** |
| P0 安全项 | 4 skill + 3 其他 | **skill 4 项已修 ✅, 其他 3 项已修 ✅** |

---

## 3. 未完成

| 项 | 状态 | 说明 |
|---|---|---|
| TestTimeoutLocality | ✅ | 8 处全部修复，archtest 全绿 |
| P0 安全退化 | ✅ | 3 项全部修复 |
| P8 审查修复 | ✅ | 20 项全部交付 |
| V2↔V3 二次核对 | ✅ | 终极报告已落盘 |
| DAG SQL 适配 | ⏳ | cmd/mcp-orch/store/taskdag/ 10 个接口重写 |
| **P9 LSP 工具族** | ⏳ | 9 个工具，6+1 Agent，~12,500 行 |
| P10 工厂丰满 | ⏳ | Zone A 3.8%→60%，3 波次 |
| IDA 工具族 | ⏳ | 82 个工具，暂缓 |
| 前端功能测试 | ⏳ | run-debug.sh 已就绪，等手动启动验证 |
| **V2V3 P1 功能缺失（30 项）** | ⏳ | 详见 v2v3-recheck-final.md §4 |
| **V2V3 P2 协议收缩（12 项）** | ⏳ | 详见 v2v3-recheck-final.md §5 |

---

## 4. 下一步（优先级排序）

```
本周（已完成）: P0-3 approval → P0-1 thread guard → P0-2 binding id ✅
第2周: 根因B orchestration终态 → 根因C provider进程
第3周: 根因D RPC golden测试集 → 根因E approval/guard
第4周: 根因A session解耦 → dashboard + uistate
并行: P9 LSP 工具族
```

V2V3 跨模块根因：
- A. session-centric 架构（01,02,03,13）→ 恢复 thread-scoped resolver
- B. orchestration 终态闭环不完整（04,06,08）→ 扩展事件消费面
- C. provider 进程生命周期薄弱（06,15,16）→ 统一 provider 进程契约
- D. RPC 返回体未系统性迁移（01,03,04,12,17,19）→ golden 测试集
- E. approval/guard 链路分散（01,05,08）→ 补 fail-closed + guard

---

## 5. 风险/延后项

| # | 问题 | 严重度 | 归期 |
|---|---|---|---|
| 1 | V2V3 P1 功能缺失 30 项 | P1 | 第 2-4 周 |
| 2 | V2V3 P2 协议收缩 12 项 | P2 | 第 3-4 周 |
| 3 | MCP 新依赖方向 archtest 尚未落地 | P2 | P9 前置 |
| 4 | lsp/gui_structure/inspect/xref 仍是 stub | P3 | P9 |
| 5 | knownDiffRevision 仍是 no-op | P3 | 待定 |
| 6 | thread/name/set provider 未接 | P3 | 待定 |
| 7 | sidebar_test 异步投影偏重 | P3 | P10 |

---

## 6. 关键文档清单

| 文档 | 路径 |
|---|---|
| 主迁移计划 | docs/plans/迁移/v3-migration-plan.md |
| P7.5 桥接校准 | docs/plans/迁移/p7.5-bridge-calibration.md |
| P8 执行计划 | docs/plans/迁移/p8-execution-plan.md |
| P8.5 ctl 回调框架 | docs/plans/迁移/p8.5-execution-plan.md |
| P8 lifecycle hooks | docs/plans/迁移/p8-lifecycle-hooks.md |
| MCP 服务契约 | docs/契约/mcp-service-convention.md |
| P9 执行计划 | docs/plans/迁移/p9-execution-plan.md |
| P10 执行计划 | docs/plans/迁移/p10-execution-plan.md |
| V2↔V3 初次报告 | docs/plans/迁移/v2v3-final-report.md |
| **V2↔V3 二次核对终极报告** | **docs/plans/迁移/v2v3-recheck-final.md** |
| 两级工厂方案 | docs/plans/迁移/v3-two-zone-dry-enrichment.md |
| LSP 高级指南 | shared file: prompts/lsp-advanced-guide.md |
| LSP 强制前缀 | shared file: prompts/lsp-mandatory-prefix.md |
| P8.5 Hooks 审查报告 | docs/plans/迁移/p8.5-hooks-review.md |

---

## 7. Agent 使用统计

| 类型 | 数量 |
|---|---|
| **本会话（03-25）累计 Agent** | **~80+** |
| P8 五维审查（初审+互审） | 9 |
| P2/P3/P4 修复 + 互审 | ~25 |
| 低优修复 + 互审 | ~10 |
| V2↔V3 二次核对 | 20 |
| V2↔V3 互审 + 汇总 + 审计 | 25 |
| P0 修复 + 多轮互审 | ~15 |
| 前序会话累计 | ~350+ |
| **总计** | **~430+** |

---

## 8. 最近 10 条对话摘要

| # | 用户指令 | 执行内容 |
|---|---|---|
| 1 | "P8 五维审查" | 5 Agent 初审：代码质量/契约边界/测试/架构/安全 |
| 2 | "互审 1:4" | 4 Agent 校准，驳回 5 项误报，0 阻塞 |
| 3 | "拉 codex agent 修" | 4 Codex 修复 P2（IntersectTargets/resolvedReviewReader/解耦/resolved_by） |
| 4 | "P3+P4 修复" | 6 Codex 修复（doc comment/archtest/拆分/测试/TTL/文档） |
| 5 | "低优修复" | 4 项（dispatcher panic/env_test/TimeoutLocality/orchestration 精简） |
| 6 | "拉 20 agent V2V3 核对" | 20 Codex 1:1 核对全部模块 + 1:10 互审 + 二次审修正 |
| 7 | "汇总 + 审计" | 2 汇总 + 3 审计，终极报告落盘 |
| 8 | "P0 修复" | 3 Codex 修复 P0-1/P0-2/P0-3 + 多轮互审+反驳+修补 |
| 9 | "全量总审" | 3 Agent 全量验证 20 项，archtest 全绿，20/20 可交付 |
| 10 | "session-summary 更新" | 本文档更新 |

---

## 9. 子 Agent 提示词模式

### LSP 强制指令（所有 Agent 追加，硬性约束）

**拉起任何 Agent 时，初始 prompt 必须包含以下文档链接，不得省略：**

```
先执行 shared_file_read prompts/lsp-mandatory-prefix.md
再执行 shared_file_read prompts/lsp-advanced-guide.md
禁止只用 lsp_grep + lsp_file，每个任务至少 4 种 LSP 工具
```

**文档路径（必须原样下发）：**
- `prompts/lsp-mandatory-prefix.md` — LSP 强制前缀，所有 Agent 首先读取
- `prompts/lsp-advanced-guide.md` — LSP 高级工具完整指南

**违反后果：** Agent 产出的代码搜索/读取质量不可靠，审查结论不可信

### 仓库契约引用
```
守卫标准：文件 ≤400行，函数 ≤80行，CC ≤10，包非测试文件 ≤15
Zone B 模式：docs/plans/迁移/v3-two-zone-dry-enrichment.md §3
sqlc 生成代码豁免：internal/store/sqlc/ SkipDir
```

### Agent 拉起规范（硬性约束）

**所有 Agent 必须通过编排接口（`orchestration_launch_agent` / `orchestration_send_message`）拉起，禁止通过 SDK Agent tool 拉 Claude 子 agent。**

| 用户指令 | 含义 | 实现方式 |
|---------|------|----------|
| "拉 agent" / "拉 codex" | 拉 Codex Agent | `orchestration_launch_agent(provider="codex")` |
| "拉 claude" | 拉 Claude Agent | `orchestration_launch_agent(provider="claude")` |
| 未指定 provider | 默认 Codex | `orchestration_launch_agent(provider="codex")` |

**禁止行为：**
- ❌ 通过 SDK `Agent` tool 拉 claude 子 agent（绕过编排系统，无法被追踪/管理）
- ❌ 不通过编排接口直接启动后台 agent

**用途分工：**
- Codex Agent（默认）：代码实施、搜索、修复、测试
- Claude Agent：架构评审、全局视角审查、复杂推理
