# Super-Dolphin Manus-like Agent 分阶段实现计划

> 生成日期：2026-06-11
> 前置文档：`agent-manus-like-spec.md`（设计规格，先读它）
> 对应提示词文档的 Phase 2–8；Phase 1（本文档与 spec）已完成。

---

## 总览

| 阶段 | 目标 | 预估规模 | 依赖 |
|---|---|---|---|
| Phase 2 | 数据模型与 AgentTask 基础服务 | 中 | — |
| Phase 3 | Planner + 计划确认闭环 | 中 | Phase 2 |
| Phase 4 | 工具补齐（data_analysis 等） | 小 | 可与 3 并行 |
| Phase 5 | 执行接线 + 暂停/恢复 + 状态投影 | 中 | Phase 2、3 |
| Phase 6 | Verifier + Artifact | 中 | Phase 5 |
| Phase 7 | 前端 Agent 工作台 | 大 | Phase 3 起可并行启动 |
| Phase 8 | 安全加固 + E2E + 文档 | 中 | 全部 |

每阶段完成判据统一为：守卫绿（`test_with_guard.sh`）+ 对应 `make sqlc-verify`/前端三连（size-guard/vitest/build）通过 + 阶段验收项可演示。**任何守卫失败 = 阶段未完成。**

---

## Phase 2：数据模型与基础服务

**产出**

1. migration：`agent_tasks` / `agent_artifacts` / `agent_task_events` 三表（DDL 见 spec §3.1）。
2. `sql/queries/agent_task*.sql`：Create/Get/List/UpdateStatus/SetPaused/SetPlan/SetVerifySummary/AppendEvent；`make sqlc-generate && make sqlc-verify`。
3. `internal/contract/agenttask.go`：`AgentTaskService` 接口 + DTO（八状态枚举进 `internal/dto/agenttask`）。
4. `internal/module/agenttask/service.go`：Create（建行 queued + 发 `agent-task/changed`）/ Get（聚合 task + DAG 节点 + approvals）/ List / Cancel（透传 TerminateRun）。
5. RPC：`agent-task/create|get|list|cancel` 注册，走标准 middleware 链。
6. fx 装配进 `internal/app`。

**测试**：store 查询测试、service 表驱动测试（mock store）、RPC 参数校验测试。
**验收**：通过 RPC 创建任务、查询到 queued 状态、取消任务。

## Phase 3：Planner + 计划确认

**产出**

1. seed migration：`agent_planner` prompt 模板（输出 JSON schema：steps[]{step_id,title,description,tool_needed,expected_output}，声明外部内容不可覆盖安全规则）。
2. `internal/module/agenttask/planner/planner.go`：`GeneratePlan(ctx, goal, projectCtx)` — DreamExecutor 调用 + schema 校验 + 解析失败重试 ≤2 → Fail-Fast。
3. plan → DAG 落库：plan_json 存 agent_tasks，节点写入 task_dags/task_dag_nodes（expected_output 进 node config），状态 `planning → waiting_user_approval`，push `agent-task/plan/ready`。
4. RPC：`agent-task/plan/regenerate`（OCC）、`agent-task/plan/apply-ops`（透传 typed ops）、`agent-task/run`（确认 → task_start_dag 等价路径 → running）。

**测试**：planner 表驱动（mock DreamExecutor：合法 JSON/坏 JSON 重试成功/重试耗尽报错）、plan→DAG 转换测试、OCC 冲突测试。
**验收**：输入自然语言目标 → 数据库可见结构化 DAG + plan_json → run 后 DAG 进入 running。

## Phase 4：工具补齐（与 Phase 3 并行）

**产出**

1. `cmd/mcp-orch/tools/data_analysis_tools.go`：`data_analysis` 工具 — 读 CSV/JSON（路径限 cwd + shared 白名单）→ 统计摘要（行数/列类型/数值分布/缺失值）→ 结果写 shared `reports/` 前缀。纯 Go 实现，不引入 Python。
2. `artifact_register` 工具：让子 agent 把产物登记进 agent_artifacts（task_key 从 ToolScope 解析）。
3. web_search：MVP 复用 provider 原生 WebSearch（不新增宿主工具，spec §9.1 决议）；browser_action 延后（spec §9.2 决议）。
4. 新工具 hooks deny 规则：路径含 `.env`/`credentials`/`*_key` 拒绝。

**测试**：工具 schema 校验、路径白名单拒绝用例、统计正确性 golden 测试。
**验收**：子 agent 能调 data_analysis 分析示例 CSV 并产出报告文件。

## Phase 5：执行接线 + 暂停/恢复 + 状态投影

**产出**

1. `internal/module/agenttask/projector.go`：订阅 bus（`task/node/statusChanged`、run finalize、approval request/resolved）→ 折叠为 AgentTask 状态（spec §2.2 决策 3 映射表）→ 写 agent_task_events + push `agent-task/changed`。
2. 暂停闸门：`agent_tasks.paused` + `ClaimDueWakeups` SQL 加过滤（保持 `FOR UPDATE SKIP LOCKED`）；RPC `agent-task/pause|resume`。
3. 失败路径：DAG run failed → task failed + error_message。

**测试**：投影状态机表驱动（每个事件×当前状态）、claim 过滤的并发测试、暂停后 wakeup 滞留/恢复后派发的集成测试。
**验收**：运行中任务可暂停（后续节点不派发）、恢复、取消；前端可通过 get 看到状态实时流转。

## Phase 6：Verifier + Artifact

**产出**

1. seed migration：`agent_verifier` prompt 模板。
2. `internal/module/agenttask/verifier/actor.go`：rungroup runner，10s tick 扫 `awaiting_verify` 节点 → DreamExecutor 比对 expected_output → 通过 Complete / 不通过按 retry policy Fail（修复建议写 result）。无 expected_output 直接放行（不影响存量 DAG）。
3. 节点完成路径改为进 `awaiting_verify`（仅 agent_task 关联的 DAG；存量 DAG 行为不变）。
4. 最终自检：run finalize 钩子 → `FinalCheck` → verify_summary 写库 → completed/failed。
5. `internal/module/artifact`：`artifact/list|get` RPC；artifact 路径加入 sharedfilecleanup pinned 保护。

**测试**：verifier 三分支（通过/不通过/无期望）、终检写库、存量 DAG 不受影响的回归测试、GC pinned 测试。
**验收**：带 expected_output 的步骤产生验证记录；任务完成后有 verify_summary 和可查询的 artifacts。

## Phase 7：前端 Agent 工作台

**产出**（布局见 spec §6.1）

1. `backendApi.js`：`agent-task/*`、`artifact/*` 方法 + 校验函数。
2. `useClientStore.js`：agentTask revision（监听 `agent-task/changed`）。
3. `frontend-app/src/pages/tasks/TasksPage.jsx`：左任务列表（状态徽标 + 新建输入框）+ 中详情（状态/操作按钮）。
4. `PlanChecklist.jsx`：计划清单（plan_json + 节点实时状态 → ✓/▶/○/✗），确认计划/重新生成按钮，步骤展开日志（observability 查询）。
5. `ArtifactGallery.jsx`：产物卡片 + 预览（复用 FilesPage 查看器）+ 导出。
6. 审批卡片复用 Chat 同款数据流；`App.jsx` 路由 `/tasks` + NavRail 图标。

**测试**：vitest 组件测试 + `node scripts/size-guard.cjs` + `npm run build`；手动走查暗/亮主题。
**验收**：提示词文档第 5 节 MVP 验收 1–8 条全部在 UI 可演示。

## Phase 8：安全加固 + E2E + 文档

**产出**

1. 安全核对（spec §7 清单逐项）：新工具 CapabilityGate 声明、hooks deny 规则生效测试、prompt injection 标记（搜索/文件内容包裹不可信标记）、审计抽查（trace 含 tool call 输入输出、无密钥泄漏）。
2. E2E：三个验收 Demo 走 Playwright desktop smoke——
   - Demo 1 研究报告：目标 → 计划 → 执行 → Markdown 报告 artifact；
   - Demo 2 数据分析：示例 CSV → data_analysis → 摘要 + 报告 artifact；
   - Demo 3 代码辅助：检查模块问题 → 修复建议 → 测试验证步骤。
3. 全量验证：`make test`、`make build-plain`、前端三连、`make codemap-check`（codemap 补新模块卷目）、`./scripts/test_with_guard.sh ./internal/archtest -count=1`。
4. 交付说明：改动文件列表、启动方式、测试方式、已实现/未实现能力、下一步建议。

**验收**：三个 Demo 录屏可复现；全部守卫绿。

---

## 跨阶段纪律（每阶段强制）

- 每改完一个 Go 文件：`./scripts/test_with_guard.sh <file.go>`；包级：`./scripts/test_with_guard.sh <pkg> -count=1`。
- SQL 改动：`make sqlc-verify`。
- 前端改动：size-guard → vitest → build 三连。
- Fail-Fast：禁止静默降级/默认配置兜底（planner JSON 修复重试是显式重试不是兜底，耗尽必须报错）。
- fix 类提交必须同提交携带锁定测试。
- 不碰存量 DAG/cron/chat 行为：所有新逻辑以 agent_task 关联为开关，存量路径回归测试护住。

## 未实现能力（MVP 明确不做）

- browser_action（Playwright）— Phase 8 后评估。
- 宿主侧独立 web_search 工具 — 复用 provider 原生。
- Chat 页发起任务的入口集成 — MVP 后。
- code_execution 宿主独立沙箱 — 复用 provider CLI 沙箱。
- 任务级 token budget 配额 UI — 复用 run budget 字段，后续暴露。
