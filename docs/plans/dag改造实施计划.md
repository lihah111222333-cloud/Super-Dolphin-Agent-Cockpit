# Super-Dolphin DAG 改造实施计划

> 配套文档：`docs/plans/dag改造蓝图v2.md`（决策与设计）
> 本文是执行级清单：每行一个可独立提交的任务，含触动文件、验收、依赖、size、可并行性
> Size 直觉：S = 半天以内 / M = 1-2 天 / L = 3 天以上（仅相对量级，非时间承诺）
> 修订历史：2026-05-10 初稿 / 4 处小补 / 2-pass 审查 / S2.4 推迟 F / **骨架阶段封板**；**2026-05-10 T 阶段快车道完成**：T0.4/5/6/8 + T1.1 + T2.1+T2.2 + T4.1+T4.4 全部 done（6 commit）；T 阶段二次审查 4 轻度 findings 全推迟到 T0.1 / F4.1 / F 阶段

---

## 0. 总览

| 阶段 | 任务数 | 状态 | 总 size | 关键产出 |
|---|---|---|---|---|
| **S 骨架** | **24** | **✅ 封板：17 done / 1 推迟 F (S2.4) / 2 推迟 T5 (S6.1+S6.2) / 4 转 T0 作业** | ~25% | 14 处补丁 + ADR 0001 + 删死代码；行为完全不变 |
| **T 工具** | 18 + 9 T0 | **🟡 快车道 done：4 T0 done / 5 T0 推迟 + T1.1 / T2.1+T2.2 / T4.1+T4.4 done。5 task 入 PG/前端阔。** | ~35% | MCP 工具 9/9 就位（含 registry）；UI/AI 设计师待外部依赖 |
| F 功能 | 26 | ⛔ 未开动 | ~30% | 行为兑现：cron 真跑、AI 设计师上岗、智能重试落地 |
| H 加固 | 按需 | ⛔ 未开动 | ~10% | 生产问题驱动，不预排 |

里程碑：
- **M1（S 完成）**：删除 `auto_handoff_phase1` 全代码 0 命中；旧 DAG 100% 兼容
- **M2（T 完成）**：UI 上能看到 DAG → 点 Start → 节点状态变化
- **M3（F 完成）**：每日 cron + AI 帮你设计流程 + 智能重试 三大需求端到端通

---

## 1. 阶段 S 骨架（24 任务，已封板）

状态图例：✅ done / ⏸ 推迟 / ⛔ 未做

| ID | 状态 | 标题 | 主要触动文件 | commit |
|---|---|---|---|---|
| **S0.1** | ✅ | 在 p23/README.md 顶部加 deprecation 提示 | `docs/plans/迁移/p23/README.md` | 66c42c82 |
| **S1.1** | ✅ | NodeExecutor interface + NodeOutcome / RetryHint | `nodeexec/types.go` | 5e1c731e |
| **S1.2** | ✅ | FailureClass 7 / OnFailureStrategy 7 / HookPoint 4 / HookHandler interface | `nodeexec/types.go` | 5e1c731e |
| **S1.3** | ✅ | AgentExecutor stub | `nodeexec/stubs.go` | 5de5dd44 |
| **S1.4** | ✅ | AutomationExecutor stub | `nodeexec/stubs.go` | 5de5dd44 |
| **S1.5** | ✅ | HybridExecutor stub | `nodeexec/stubs.go` | 5de5dd44 |
| (重构) | ✅ | NodeExecutor 抽到 nodeexec 子包 | `nodeexec/` | 9dda3a41 |
| **S2.1** | ✅ | service.StartDAG / TerminateDAG stub + ErrLifecycleNotImplemented | `orchestration/dag.go` | c504441e |
| **S2.2** | ✅ | service.ApplyOps stub | `orchestration/dag.go` | da79df11 |
| **S2.3** | ✅ | Scheduler interface + noopScheduler stub | `orchestration/scheduler.go` | c504441e |
| ~~S2.4~~ | ⏸ | 节点完成时自动 promote 下游 status pending→ready (B-14) | **推迟到 F 阶段**跨 SQL/sqlc/dispatcher 三层 | 84c5b0da 说明 |
| **S3.1** | ✅ | migration: task_dags 加 5 列 (trigger/owner_id/cron_expr/next_run_at/version) | `migrations/0072_dag_v2_dag_columns.sql` | 9130f601 |
| **S3.2** | ✅ | migration: task_dag_nodes 加 run_id/reads/writes | `migrations/0073_dag_v2_node_columns.sql` | 9130f601 |
| **S3.3** | ✅ | migration: task_dag_runs 表 + 3 索引 | `migrations/0074_dag_v2_runs.sql` | 9130f601 |
| **S3.4** | ✅ | migration: auto_handoff_phase1 一次性映射 → trigger='auto' | `migrations/0075_dag_v2_compat.sql` | 9130f601 |
| **S3.5** | ✅ | store contract: Run / RunStore / CreateRunInput / ListRunsFilter | `store/taskdag/contract.go` | 9130f601 |
| **S4.1** | ✅ | typed ops payload (4 动词 + Op interface + custom (Un)Marshal) | `nodeexec/ops.go` | 89073074 |
| **S4.2** | ✅ | OpsRequest / OpsResponse | `nodeexec/ops.go` | 89073074 |
| **S5.1** | ✅ | typed node.config schema (3 种 node_type + 共享 Inputs/Outputs) | `nodeexec/config.go` | 0883254b |
| **S5.2** | ✅ | ParseNodeConfig dispatcher | `nodeexec/config.go` | 0883254b |
| **S5.3** | ✅ | OnFailureConfig 解码 + by_class lookup + EscalationModelFor | `nodeexec/on_failure.go` | 61bff08b |
| **S6.1** | ⏸ | UI `DagDetailModal` 真实组件结构 (推迟 T5) | `components/DagDetailModal.js` | T 阶段 |
| **S6.2** | ⏸ | UI 状态色 token (推迟 T5) | `styles/dag-tokens.css` | T 阶段 |
| **S7.1** | ✅ | 9 态 NodeStatus + ValidateTransition + IsTerminal | `nodeexec/status.go` | af542629 |
| **S7.2** | ✅ | service.UpdateNodeStatus 接通 ValidateTransition + dispatcher fast-lane 说明 | `orchestration/dag.go` | c972b3f1 |
| **S15.1** | ✅ | 删 auto_handoff_phase1 全部写入点 (grep 代码 0 命中) | `tools/task_tools.go` | 4c355d5e |
| **S16.1** | ✅ | ADR 0001 骨架阶段契约 (276 行) | `docs/adr/0001-dag-v2-contracts.md` | 83d83ea0 |

## 1.1 T0 启动作业（骨架阶段二次审查后转入）

骨架阶段二次 DAG 审查（`dag_skeleton_audit_20260510`）产出 8 个非阻塞型 findings，全部转为 T0 启动作业。必须在 T1.1 / T2.1 开工前处理：

| ID | 状态 | 问题 | 处理 |
|---|---|---|---|
| **T0.1** | ⏸ 推迟 | PD-1: 缺 e2e 测试 fixture（合并 PT-3: T1.1 缺端到端 fixture） | 与 T1.2/T3.x 真实路径一起做（需 PG） |
| ~~**T0.2**~~ | ✅ done | PB-2: migration 0072-0075 未在 PG 跑过验证 | 2026-05-10 应用：pg_dump 备份 `/tmp/super_agent_v3.before_0072_20260510_145812.dump` + psql 逐个单事务 + `INSERT schema_migrations` 同步。0075 实际转换 2 行（v3-arch-violations-fix-2026-05-06 + contract-audit-fixes）trigger=manual→auto、metadata 删 auto_handoff_phase1 |
| **T0.3** | ⏸ 推迟 | PB-1: 缺 service↔store 跨层集成测试 | 与 T1.2/T3.x 一起做 |
| ~~**T0.4**~~ | ✅ done | PA-1: dag_retry_policy.go 导航注释 | commit `8d32ea1f` |
| ~~**T0.5**~~ | ✅ done | PC-1: archtest 守护 RunStore 待 T1.2 | commit `8f61c839` |
| ~~**T0.6**~~ | ✅ done | PC-4: ADR 0001 §2.5 加三方映射注释 | commit `8d32ea1f` |
| **T0.7** | ⏸ 推迟 | PD-2: thread-DAG 关联 (spawning_thread_id) | T8.1 AI 设计师按钮一并 |
| ~~**T0.8**~~ | ✅ done | PD-3: doc-sync check | commit `f972627d`（4 项检查全过） |
| **T0.9** | ⏸ 推迟 F | PE-1（吃狗粮）: dispatcher 对无 assignee 节点自动 spawn | → **F6.4** dispatcher 重做一并 |

详见审查报告 `handoff/skeleton-audit-{pass1-adr,pass2-tests,pass3-cross-cutting,pass4-prev-closed,synthesis,final-verdict}.md`。

## 1.2 骨架阶段验收总结

**已达成**：
- 17/24 task done + 1 推迟 F (S2.4 → **F6.3**) + 2 推迟 T5 (S6.1+S6.2)
- 14 commit / 28 files / 3113 insertions
- `go build ./...` / `go test ./...` / `go vet ./...` / `scripts/test_with_guard.sh` 全过
- `cmd/agent-terminal/frontend && npm test` 通过（vitest）
- 旧 DAG 创建/查询/更新调用 100% 兼容
- `grep -r auto_handoff_phase1 cmd/ internal/` 0 命中
- ADR 已 commit

- 67 单测全 PASS / 架构守卫全过 / `auto_handoff_phase1` 全代码 0 命中
- ADR 0001 固化全部契约

**实际 14 commit 列表**：
```
4c355d5e  refactor(tools): 删 auto_handoff_phase1 (S15.1)
9130f601  feat(orch): DAG v2 migration + Run 接口位 (S3.1-S3.5)
83d83ea0  docs(adr): 0001 DAG v2 骨架阶段契约 (S16.1)
da79df11  feat(orch): service.ApplyOps stub (S2.2)
61bff08b  feat(nodeexec): OnFailureConfig (S5.3)
0883254b  feat(nodeexec): typed node.config (S5.1+S5.2)
84c5b0da  docs(dag): S2.4 推迟 F + dispatcher fast-lane 说明
c504441e  feat(orch): StartDAG/TerminateDAG/Scheduler stub (S2.1+S2.3)
c972b3f1  feat(orch): UpdateNodeStatus 接通 ValidateTransition (S7.2)
af542629  feat(nodeexec): ValidateTransition + IsTerminal (S7.1)
89073074  feat(nodeexec): typed ops payload (S4.1+S4.2)
66c42c82  docs(p23): deprecation 提示 (S0.1)
9dda3a41  refactor(orch): NodeExecutor → nodeexec
5de5dd44  feat(orch): 三 NodeExecutor stub (S1.3-1.5)
5e1c731e  feat(orch): NodeExecutor 接口契约 (S1.1+S1.2)
```

**骨架阶段封板。**进 T 阶段必读：`docs/adr/0001-dag-v2-contracts.md` + 本节 T0 启动作业。

---

## 2. 阶段 T 工具（18 任务，快车道 5 项 done）

状态图例：✅ done / ⛔ 入 PG 阔 / ⛔ 入前端阔 / ⏸ 推迟

| ID | 状态 | 标题 | 文件 / Commit |
|---|---|---|---|
| ~~**T1.1**~~ | ✅ done | MCP `task_start_dag` schema + handler（stub） | `tools/task_tools.go` + `contract/orchestration.go` (StartDAGRequest/Response) / commit `2ef76d2e` |
| ~~**T1.2**~~ | ✅ done (mid) | `service.StartDAG` 真实实现：创建 run + status 转 running | `cmd/mcp-orch/orchestration/dag.go` + `store/taskdag/{contract.go,store_run.go}` + `sql/queries/task_dag_run.sql` / commit `57075943` (store 层) + `bbf8a988` (service)。T1.2-mid 范围完成：RunStore 5+1 方法 (CRUD/Count/Promote/WithRunTx) + StartDAG 真业务 + 3 sentinel error + 10 unit test。Integration test 合并 T0.1 + T0.3。T1.2-full 升级 → **F6.5**。**ledger**：commit `3f6c6a80` — StartDAG 幂等语义路线 N（failed/cancelled 命中返 ErrIdempotencyKeyExhausted，running/succeeded 仍复用 RunKey） |
| ~~**T2.1**~~ | ✅ done | MCP `task_dag_apply_ops` schema + handler（stub） | `tools/task_tools.go` / commit `2af9539c`（PT-4: raw ops 透传测试由 F4.1-F4.5 各自单测自然覆盖，不单独立项） |
| ~~**T2.2**~~ | ✅ done | `service.ApplyOps` 接通 contract.ApplyOpsRequest（stub）；真实实现归 F4.x | `orchestration/dag.go` / commit `2af9539c`（PT-2: ops 形状校验 / unmarshal fail-fast → **F4.0** 顶层前置） |
| ~~**T3.1**~~ | ✅ done | MCP `task_get_run` schema + handler | `cmd/mcp-orch/tools/task_tools.go` + `orchestration/dag_query.go` + `contract.GetRun{Request,Response}` / commit `360f9bfd`（RunStore.GetRun 接通 + 中英双语 ErrRunNotFound 转译 + A2 不 inline 节点） |
| ~~**T3.2**~~ | ✅ done | MCP `task_list_runs` schema + handler | `cmd/mcp-orch/tools/task_tools.go` + `orchestration/dag_query.go` + `contract.ListRuns{Request,Response}` / commit `cf335dbf`（RunStore.ListRuns 接通 + status 枚举对齐 0080 CHECK + mapRuns 复用 dagRunDTO + {runs} 包对象） |
| ~~**T4.1**~~ | ✅ done | MCP `list_models` 工具 | `tools/registry_tools.go` 新建 / commit `c311259e`（PT-1: F 阶段改读 provider registry） |
| ~~**T4.2**~~ | ✅ 复用 | MCP `prompt_list` 已存在 | `tools/prompt_tools.go` |
| ~~**T4.3**~~ | ✅ 复用 | MCP `command_list` 已存在 | `tools/command_tools.go` |
| ~~**T4.4**~~ | ✅ done | MCP `shared_file_list` + 暴露 allowed_prefixes | `tools/registry_tools.go` / commit `c311259e` |
| **T5.1** | ⛔ 前端方案待审 | UI `useDagDetail` composable | `composables/useDagDetail.js` |
| **T5.2** | ⛔ 前端方案待审 | UI `DagDetailModal` 节点列表 + polling | `components/DagDetailModal.js` |
| **T5.3** | ⛔ 前端方案待审 | UI Start 按钮 | 同上 |
| **T6.1** | ⛔ 前端方案待审 | UI 节点行 → 子 agent thread 链接 | 同上 |
| **T7.1** | ⛔ 前端方案待审 | UI DAG 列表字段显示 + polling | `pages/DagsPage.js` |
| **T8.1** | ⛔ 前端方案待审 | UI 「AI 帮你设计流程」按钮 | `pages/DagsPage.js` |
| **T8.2** | ⛔ 依赖 T8.1 | base 设计师 prompt 占位 | prompt_template 表 |
| **T9.1** | ⛔ 依赖 T1-T4 | codemap 索引刷新 | `docs/doc/codemap/02-mcp-orch.md` |

### 2.1 T 阶段快车道总结 (2026-05-10 二次审查后)

**完成（8 条 commit，含 T0）**：
```
c311259e  T4.1+T4.4 list_models + shared_file_list
2af9539c  T2.1+T2.2 task_dag_apply_ops 接通 contract
2ef76d2e  T1.1 task_start_dag stub
8f61c839  T0.5 archtest 守护
f972627d  T0.8 doc-sync script
8d32ea1f  T0.4 + T0.6 导航注释 + ADR 三方映射
```

**MCP 工具表面 9/9 全就位**：task_create_dag / get_dag / update_node / start_dag / dag_apply_ops / list_models / shared_file_list / prompt_list / command_list。AI 设计师在 thread 里能查全部资源，stub 路径允许“骨架走一趟”。

**T 阶段二次 DAG 审查（`dag_t_phase_audit_20260510`）产出 4 轻度 findings**，全部推迟立项：
- **PT-1**：list_models 硬编码 → F 阶段改读 provider registry
- **PT-2**：ApplyOps stub 不验证 ops 形状 → F4.1 真实落地时 unmarshal fail-fast
- **PT-3**：T1.1 缺端到端 fixture → 合并到 T0.1
- **PT-4**：T2.1 raw ops 透传缺测试 → F4.1 一起做

**剩余 task 阔住点**：
- T0.1 / T0.2 / T0.3 → 需本地 PG 环境（T1.2 / T3.1 / T3.2 已完成）
- T5.x / T6.1 / T7.1 / T8.x → 需前端方案用户审认过
- T9.1 codemap → 依赖 T1-T4 全部完成

详见审查报告 `handoff/t-phase-audit-{pass1-adr,pass2-layer,pass3-tests,pass4-t0-closed,synthesis,final-verdict}.md`。

---

## 2.legacy 原 T 阶段表格（历史保留，可跳过）

| ID | 标题 | 主要触动文件 | 验收 | 依赖 | Size | 并行 |
|---|---|---|---|---|---|---|
| **T1.1** | MCP `task_start_dag` schema + handler | `cmd/mcp-orch/tools/task_tools.go` | 集成测试：调 task_create_dag → task_start_dag → status running | S2.1 | M | Y |
| **T1.2** | `service.StartDAG` 真实实现：创建 run + status 转 running | `cmd/mcp-orch/orchestration/dag_lifecycle.go` | 单测覆盖；run 表插入正确 | T1.1, S3.5 | M | N |
| **T2.1** | MCP `task_dag_apply_ops` schema + handler（draft/ready 状态可改） | `cmd/mcp-orch/tools/task_tools.go` | 集成测试：apply_ops 后 version+1，base_version 不匹配返 conflict | S4.1, S4.2 | M | Y |
| **T2.2** | `service.ApplyOps` stub 接通（draft/ready 状态允许调用，返回 NotImplemented）；真实实现完全归 F4.x | `cmd/mcp-orch/orchestration/dag_ops.go` | 单测：工具 schema 正确 + service stub 调用通 + base_version 不匹配返 conflict | T2.1, S5.2 | M | N |
| **T3.1** | MCP `task_get_run` schema + handler | `cmd/mcp-orch/tools/task_tools.go` | 集成测试：返回完整 run + 节点状态 | S3.5 | S | Y |
| **T3.2** | MCP `task_list_runs(dag_key, limit)` schema + handler | 同上 | 集成测试：分页正确 | S3.5 | S | Y |
| **T4.1** | MCP `list_models` 工具（hardcoded provider→models） | `cmd/mcp-orch/tools/registry_tools.go` 新建 | 集成测试：claude/codex 各自 model 列表 | — | S | Y |
| **T4.2** | MCP `list_prompt_templates` 工具（已有 prompt_list 复用） | 同上 | 集成测试 | — | S | Y |
| **T4.3** | MCP `list_command_cards` 工具（已有 command_list 复用） | 同上 | 集成测试 | — | S | Y |
| **T4.4** | MCP `list_sharedfiles` 工具 | 同上 | 集成测试 | — | S | Y |
| **T5.1** | UI `useDagDetail` composable（fetch task_get_dag） | `cmd/agent-terminal/frontend/vue-app/composables/useDagDetail.js` 重写 | 单测：response → state 映射正确 | T1.1 | M | Y（前端独立） |
| **T5.2** | UI `DagDetailModal` 渲染节点列表（含状态色 + provider/model/agent_key 显示；**状态刷新先用 polling 3-5s**） | `components/DagDetailModal.js` 修改 | e2e：截图前后对比，"暂无 DAG" → 真实节点列表；polling 可控制开关 | T5.1, S6.1, S6.2 | M | N（**方案先发用户**） |
| **T5.3** | UI Start 按钮（draft/ready 时可点 → 调 task_start_dag） | 同上 | e2e：点 Start → 状态变 running | T5.2, T1.1 | M | N |
| **T6.1** | UI 节点行展示 `assigned_to` → 跳到子 agent thread 链接 | 同上 | e2e：点击跳转正确 | T5.2 | S | Y |
| **T7.1** | UI 列表显示 `trigger / status / version / latest_run_status`（同 polling 3-5s 刷新） | `pages/DagsPage.js` / `DataPage.js` | e2e：列表字段渲染；polling 不造成可见闪烁 | T1.1, T3.1 | M | Y |
| **T8.1** | UI「AI 帮你设计流程」按钮（spawn 新 thread + 调 orchestration_launch_agent） | `pages/DagsPage.js` | e2e：点按钮 → 新 thread 创建 → 跳转到 thread | T4.1-T4.4 | M | N（**方案先发用户**） |
| **T8.2** | base 设计师 prompt 占位（实际 prompt 在 F7） | `cmd/mcp-orch/...` 相关 prompt_template 占位 | 占位 prompt 注入正确 | T8.1 | S | N |
| **T9.1** | codemap 索引刷新 | `docs/doc/codemap/02-mcp-orch.md` 等 | 新工具入索引 | T1-T4 完成 | S | N |

**T 阶段验收**：
- M2 里程碑：UI 上能看到 DAG → 点 Start → 节点状态变化（**目前 cron / AI 设计师都还是占位**）
- 端到端 `task_create_dag → task_dag_apply_ops 改一个节点 → task_start_dag → 看到节点状态变化` 通过
- AI（任意 thread 中的 LLM）能调 `task_dag_apply_ops` 改 DAG（手动让它做也能成）

**T 阶段提交粒度**：
- T1.1+T1.2 一次
- T2.1+T2.2 一次（ops 是大改，单独提）
- ~~T3.1+T3.2 一次~~ ✅ commit `360f9bfd` + `cf335dbf`
- T4.1-T4.4 一次（registry 工具集）
- T5.1 / T5.2 / T5.3 各一次（前端**每次方案先发用户**）
- T6.1 单独
- T7.1 单独
- T8.1 / T8.2 各一次
- T9.1 单独（codemap）

约 12-14 个 commit。

---

## 3. 阶段 F 功能（31 行 / 30 待做 + 1 完成占位）

> 26 个原计划 + 5 个从推迟项补位（F4.0 / F6.3 / F6.4 / F6.5 / F14.1） = 31 表位；其中 F6.1 由 T1.2-mid 接手 snapshot 后留为已完成契约占位、不再计为待做，所以待做任务 = 30。
>
> 推迟项拼装表：S2.4 → F6.3；T0.9/PE-1 → F6.4；PT-1 → F14.1；PT-2 → F4.0；T1.2-full → F6.5。

| ID | 标题 | 主要触动文件 | 验收 | 依赖 | Size | 并行 |
|---|---|---|---|---|---|---|
| **F1.1** | `AgentExecutor` 解码 `node.config.exec` → `orchestration_launch_agent` 参数映射 | `cmd/mcp-orch/orchestration/executor_agent.go` | 单测：provider/model/agent_key/effort/language/tools 映射正确 | S1.3, S5.2 | M | Y |
| **F1.2** | `AgentExecutor` 处理 `inputs`：注入 prev nodes results / sharedfiles | 同上 | 集成测试：节点 B 看到节点 A.result | F1.1 | M | N |
| **F1.3** | `AgentExecutor` 处理 `outputs`：写 sharedfile / node.result | 同上 | 集成测试：sharedfile 内容正确写入 | F1.2 | S | N |
| **F1.4** | `AgentExecutor` 处理 transient/quota/validation 三类失败基础重试 | 同上 + `retry_strategy.go` 新建 | 单测：模拟三类失败重试次数正确 | F1.1, S7.1 | M | N |
| **F2.1** | `AutomationExecutor` 解码 `command_ref` → command_get + 执行 | `cmd/mcp-orch/orchestration/executor_automation.go` | 单测：command 执行 + 错误处理 | S1.4, S5.2 | M | Y（与 F1 并行） |
| **F2.2** | `AutomationExecutor` 处理 inputs/outputs | 同上 | 集成测试 | F2.1 | S | N |
| **F3.1** | `HybridExecutor` 串联 automation → agent verifier | `cmd/mcp-orch/orchestration/executor_hybrid.go` | 集成测试：automation 失败时 verifier 不跑 | F1, F2 完成 | M | N |
| **F4.0**（顶层前置） | `ApplyOps` 顶层 unmarshal + 形状校验 + 错误分类（PT-2） | `cmd/mcp-orch/orchestration/dag_ops.go` 顶层 + `nodeexec.Ops UnmarshalJSON` | 单测：非法 op_kind / 缺字段 / 非法 base_version 全拒；错误分类清晰 | T2.2 | S | Y |
| **F4.1** | `ApplyOps` add_node 真实实现 + 环检测 | `cmd/mcp-orch/orchestration/dag_ops.go` | 单测：环检测；version+1；PT-4 raw ops 透传覆盖 | S2.2, T2.2, F4.0 | M | Y |
| **F4.2** | `ApplyOps` update_node 真实实现 | 同上 | 单测 | F4.1 | S | N |
| **F4.3** | `ApplyOps` remove_node 真实实现（含级联清理依赖） | 同上 | 单测：被依赖节点不能删除 | F4.2 | M | N |
| **F4.4** | `ApplyOps` update_dag 真实实现 | 同上 | 单测 | F4.1 | S | Y |
| **F4.5** | `ApplyOps` `status=running` 时只允许 add_node + depends_on 指向 done 节点 | 同上 | 单测：违规 ops 被拒 | F4.1 | M | N |
| **F5.1** | cron daemon 进程入口 + 接 robfig/cron 库 | `cmd/mcp-orch/orchestration/scheduler_cron.go` 新建 | 单测：cron 表达式解析正确 | S2.3 | M | Y |
| **F5.2** | `Scheduler.Tick` 真实实现：扫 `next_run_at <= now` → StartDAG | 同上 | 集成测试：到点自动起 run | F5.1, T1.2 | M | N |
| **F5.3** | cron 多实例锁（避免重复触发） | 同上 | 集成测试：两个进程只一个 tick 成功 | F5.2 | M | N |
| ~~**F6.1**~~ | (snapshot dag.version 部分由 T1.2-mid 完成；events 字段位的业务化写入归 H 阶段。本行保留作为契约位、不再单独 commit) | — | — | T1.2 | — | — |
| **F6.2** | run 终态判定：所有节点 done/failed/cancelled/skipped → run.status finished | `dag_lifecycle.go` 修改 | 集成测试 | T1.2 | M | N |
| **F6.3** | 节点完成时自动 promote 下游 pending→ready（S2.4 / B-14） | `task_dag_node` SQL + sqlc + dispatcher 三层 | 集成测试：节点 A done 后下游 B 自动 ready | T1.2, S2.4 推迟 | M | N |
| **F6.4** | dispatcher 对无 assignee 节点自动 spawn（T0.9 / PE-1） | `wakeup_dispatcher` 重做（与 F6.3 配套） | 集成测试：promote 后无 assignee 节点自动起子 agent | F6.3 | M | N |
| **F6.5** | T1.2-full：复制节点带 run_id + allow 多 run 并发 | `RunStore` 节点复制 + `dag_lifecycle.go` + `StartDAG` 去 reject | 集成测试：同 DAG 两次 StartDAG 都成功，节点行各自 run_id 独立 | T1.2 (mid)、F6.3、F6.4 | L | N |
| **F7.1** | AI 设计师 prompt（中文版） | `internal/...` prompt_template 表 seed 或 migration | 集成测试：prompt 注入完整可用资源列表 | T4.1-T4.4 | M | Y |
| **F7.2** | AI 设计师 prompt（英文版） | 同上 | 同上 | F7.1 | S | N |
| **F8.1** | UI 节点编辑表单（typed schema → form field 映射规则） | `components/NodeEditForm.js` 新建 | 单测：schema 渲染对应控件 | S5.1 | L | Y（前端独立，**方案先发用户**） |
| **F8.2** | UI 表单下拉框接 `list_models` / `list_prompt_templates` | 同上 | e2e：下拉框数据正确 | F8.1, T4.1-T4.4 | M | N |
| **F9.1** | UI mermaid 拓扑图（DAG → mermaid 字符串） | `components/DagTopology.js` 新建 | e2e：截图对比 | T5.2 | M | Y |
| **F10.1** | UI run 历史时间轴 | `components/RunHistoryPanel.js` 新建 | e2e：点 run 看那次状态 | T3.2 | M | Y |
| **F11.1** | UI sharedfile 锁可视化（节点 reads/writes 联动） | `pages/SharedFilesPage.js` 修改 | e2e：sharedfile 显示"被节点 X 占用" | F1.3 | M | Y |
| **F12.1** | 智能重试 strategy dispatcher：`by_class` 分发（capability→escalate_model / validation→append_error / 关键节点→replan spawn planner） | `retry_strategy.go` 修改 | 集成测试：模拟 capability 失败 → 升级到 Opus 重跑；validation 失败 → schema 错误注入重跑；replan 策略 spawn planner agent | F1.4 | L | N |
| **F13.1** | lifecycle hooks 真实触发（before/after/on_state_change/on_failure） | `node_executor_dispatch.go` 新建 | 集成测试：hooks 在正确时机被调 | S1.1, S10 | M | Y |
| **F14.1**（工具升级） | `list_models` 改读 provider registry（PT-1） | `cmd/mcp-orch/tools/registry_tools.go` + 新 registry 模块 | 单测：增改 registry 配置即时反映；F8.2 UI 下拉接这里 | T4.1 | S | Y |

**F 阶段验收**：M3 里程碑端到端用例通过：

> 「点『AI 帮你设计流程』按钮 → 新 thread → 在 thread 里说『帮我设计每天 8 点的报告生成 DAG』 → AI 输出 ops，DAG 创建 → 用户在 UI 改一处 prompt → 点 Start → 第一个 run 跑通 → 第二天 8 点自动起第二个 run → run 历史里看到两次执行 → 一次故意触发 capability 类失败 → 智能重试升级到 Opus → 通过」

**F 阶段提交粒度**：每个 F1-F13 子项独立 commit；同一 task 拆 .1/.2/.3 时，每子项独立 commit（按 prefer-small-commits）。约 25 个 commit。

---

## 4. 阶段 H 加固（按需触发，不预排）

| ID | 主题 | 触发条件 | 优先级 |
|---|---|---|---|
| H1 | 错误信息人话翻译（ErrCallerIdentityRequired / verdict_lost 等） | 用户看不懂报错 | 中 |
| H2 | 节点级 retry / fallback 策略调优 | 节点频繁失败、重试策略效果差 | 中 |
| H3 | 大 DAG 性能（N>50 拆批） | 真有 50+ 节点 DAG | 低 |
| H4 | `task_dag_revisions` 表（编辑历史/回滚 UI） | 用户想 undo | 低 |
| H5 | multi-tenant / 权限模型 | 多人协作场景 | 低 |
| H6 | 监控/告警（cron miss / run timeout） | 跑漏一天后 | 中 |
| H7 | inputs.summarization 真实实现 | 长 DAG 上下文爆炸 | 中 |
| H8 | token budget enforcement | 出现 token 失控成本 | 中 |
| H9 | task_post_message 原语真实落地 | 节点对话 sharedfile 不够用 | 低 |
| H10 | waiting_human HITL 完整流程 | 出现需要人审决策的场景 | 中 |

加固阶段任务**不预排**：每条 H 触发后单独立任务清单。

---

## 5. 关键里程碑映射

### 里程碑 M1 完成 = 阶段 S 全部 22 任务
**用户可见**：什么都没变，但代码内部干净了。

### 里程碑 M2 完成 = 阶段 S + T1.1, T1.2, T3.1, T3.2, T5.1, T5.2, T5.3
**用户可见**：UI 上能看到 DAG → 点 Start → 节点跑起来。
**还不能**：cron 自动触发；AI 帮你设计流程的智能化。
**进度**：T1.1 / T1.2 / T3.1 / T3.2 ✅；T5.x 前端方案待审。后端 MCP 表面全套已在位。

### 里程碑 M3 完成 = 阶段 S + T + F 全部
**用户可见**：两大需求 + 智能重试全部端到端通。

### 你两个核心需求的精确映射

**Need 1（每日定时任务）落地必备 task**：
- S2.3, S3.1, S3.3, S4, S5（cron 字段位 + run 表）
- T1.1 ✅ / T1.2 ✅ / T3.1 ✅ / T3.2 ✅ / T7.1（StartDAG + Run 工具 + UI 显示；后端部分均已完成）
- F5.1, F5.2, F5.3, F6.1, F6.2（cron daemon + Run snapshot）
- F10.1（run 历史 UI）

**Need 2（AI 帮你设计流程）落地必备 task**：
- S1.1, S2.2, S4, S5（NodeExecutor + ops + typed schema）
- T2.1, T2.2, T4.1-T4.4, T5.2, T8.1, T8.2（ops 工具 + registry + UI 按钮）
- F1.1-F1.3, F4.1-F4.5, F7.1, F7.2, F8.1, F8.2（Executor + ApplyOps + 设计师 prompt + 表单）

---

## 6. 提交规范（commit message 模板）

```
<type>(<scope>): <subject>

<body 中文：动机 + 改动要点>

<footer：关联 task ID + 蓝图节号>
```

- `type`: feat / fix / refactor / docs / test / chore
- `scope`: dag / orch / mcp / ui / migrations / executor / scheduler
- `subject`: 中文，一句话
- 关联 task ID 写在 footer，例如 `Task: S1.1 / Blueprint: §10`

示例：
```
feat(orch): 定义 NodeExecutor 接口 + NodeOutcome 类型 (S1.1)

骨架阶段补丁 1：定义 DAG 节点执行器统一接口。
- NodeExecutor.Execute 返回 NodeOutcome 而不是裸 error
- NodeOutcome 含 Status / Result / FailureClass / RetryHint
- Hooks() map 接口位预留

Task: S1.1
Blueprint: docs/plans/dag改造蓝图v2.md §10 补丁 1, §9
```

---

## 7. 每次动手前的 4 步规矩

按 `feedback/任何-清死代码-重构-大批量改动-类工作开始前固定-4-步`：

1. `git fetch origin main`
2. `git log --left-right --oneline HEAD...origin/main` 看双向差距
3. 远程领先就先 pull
4. 写"改动清单 + 验证计划"短纸条给用户

前端 task（S6.x / T5.x / T6.1 / T7.1 / T8.x / F8.x / F9.x / F10.x / F11.x）按 `feedback/threadstore-whitelist-and-hmr.md` 必须先发方案给用户确认才动手。

---

## 8. 验证清单（每个 commit 前）

| 验证项 | 命令 | 何时跑 |
|---|---|---|
| Go build | `go build ./...` | 任何 Go 改动 |
| Go test | `go test ./...` | 任何 Go 改动 |
| Go vet | `go vet ./...` | 任何 Go 改动 |
| Architecture guard | `scripts/test_with_guard.sh` | 任何 Go 改动 |
| Frontend test | `cd cmd/agent-terminal/frontend && npm test` | 任何前端改动 |
| Frontend e2e（snapshot） | `npm run test:e2e` | UI 涉及视觉时 |
| Migration dry run | `make migrate-dry-run`（如有）| 任何 migration 改动 |
| golangci-lint | `golangci-lint run` | push 前必跑 |

push 必须等用户明确指令。

---

## 9. 并行计划（哪些 task 可同时开 worker）

**S 阶段并行图**（→ 表示依赖）：

```
S1.1 → S1.2 → S1.3 / S1.4 / S1.5 (三个 stub 并行)
S2.1 → S2.2
S2.3 (独立)
S3.1 → S3.2 / S3.3 / S3.4 (后两个并行)
       → S3.5
S4.1 → S4.2
S5.1 → S5.2
S6.1 → S6.2 (前端独立链)
S7.1 → S7.2
S15.1 (依赖 S3.4)
S16.1 (最后写)
```

可同时开 4-5 个 worker：
- worker A: S1 → S2 链
- worker B: S3 migration 链
- worker C: S4 / S5 typed schema 链
- worker D: S6 前端链（独立）
- worker E: S7 状态机链

**T 阶段**：T2.2（ApplyOps）是单点瓶颈，其他可并行。
**F 阶段**：F1 / F2 / F5 / F8 各自独立链，可 4 worker 并行。

---

## 10. 当前进度（用 grep 实时查）

```bash
# Need 1 cron 进度
grep -r "next_run_at" cmd/ migrations/ 2>/dev/null | wc -l

# Need 2 ops 进度
grep -r "task_dag_apply_ops" cmd/ 2>/dev/null | wc -l

# 死代码清理进度
grep -r "auto_handoff_phase1" cmd/ internal/ 2>/dev/null | wc -l   # 目标 0

# 状态机进度
grep -r "NodeStatusReady\|NodeStatusRetrying" cmd/ 2>/dev/null | wc -l  # 目标 ≥ 2

# typed schema 进度
grep -r "FailureClass\|OnFailureStrategy" cmd/ 2>/dev/null | wc -l  # 目标 ≥ 14
```

---

## 11. 下一步

进入 **S1.1（NodeExecutor 接口 + NodeOutcome 类型）**，按以下顺序执行：

1. `git fetch origin main` + 看双向差距
2. 写"改动清单 + 验证计划"短纸条给用户
3. 用户确认后落代码
4. `go build` / `go test` / `go vet` / `scripts/test_with_guard.sh` 全过
5. commit（模板见 §6）
6. 不主动 push

按 §9 并行计划，**S1.1 + S2.1 + S3.1 + S6.1（前端独立） + S7.1** 可同时起 5 个 worker，但建议先把 **S1.1 + S1.2** 跑通再扩散，让接口契约稳定后再让其他 task 引用它。

---

## DAG 改造 follow-up issues（非阻塞）

源自路线 N 三视角审查（commit `3f6c6a80` + 注释补强 commit `1877f401`）：

- **抽 RunStatus 常量包**：当前 `running/succeeded/failed/cancelled` 4 状态字面量在 13 处分散（`contract.go` 注释、`0080` CHECK、`dag.go` switch、测试 stub）。等加新 status（如 `timeout` / `paused`）时再统一抽 `taskdag.RunStatus` 常量包，与 `0080` CHECK 单一来源对齐。当前 `0080` CHECK 已锁定全集，分散字面量风险可控。
- **MCP 错误双语化拉齐**：本次仅 StartDAG handler 内 `ErrIdempotencyKeyExhausted` / `ErrDAGAlreadyRunning` / `ErrDAGNotFound` 三个错误双语，其他 `task_*` / `commands_*` / `orchestration_*` handler 仍英文单语。下次迭代统一定义"双语错误规范"（面向 AI agent 的业务错误必须双语，内部错误英文）后批量拉齐。
- **task_get_run.Events 全量返回**：当前 GetRun 一次性返回完整 Events jsonb；run 长跑后可能很大，需要长期分页 / 截断方案。M2 阶段可接受，未来 F 阶段再做。

源自 T3.1/T3.2 落地 + 审查补修（commit `360f9bfd` + `cf335dbf` + `caa9f13b` + `d1f5b0e4` + `498be56d`）：

- commit `360f9bfd` — T3.1 task_get_run（A2 不 inline 节点 / RunStore.GetRun 接通 / ErrRunNotFound 双语转译）
- commit `cf335dbf` — T3.2 task_list_runs（status 枚举对齐 0080 CHECK / mapRuns 复用 dagRunDTO / {runs} 包对象）
- commit `caa9f13b` — T3.1/T3.2 审查应修 1+2：GetRun s==nil 统一返 ErrRunStoreUnset + ListRuns 返回值从指针改值类型（与 GetRun / ApplyOps 同款）
- commit `d1f5b0e4` — T3.1/T3.2 测试加固：stubRunStore 字段化（并发友好，退出包级 var） + limit 边界 3 例 + s==nil receiver 测试 + BudgetLimit cloneInt64 独立性断言
- commit `498be56d` — T3.2 service.ListRuns max=200 cap（防呆） + taskToolDefinitions 按 writes → lifecycle → reads 重排

源自 T3 尾声 codemap 全检与合并仓运作复盘（1877f401 / 5fed929c / 9f302bf9 / 8399ea1b）：

- **§10.58 候选 — cherry-pick hook 兜底纪律**：cherry-pick / rebase 自动提交路径默认 **不** 触发 pre-commit hook（git sequencer 行为，非配置问题）。本会话曾因合并 agent 跳 hook 导致 gofmt 违规漏检。建议 push 前手跑 `bash .githooks/pre-commit` 自检（不是 bypass，是补跑）。详见会话 §10.58 候选。
- **§10.59 候选 — docs/plans 状态同步纪律**：每次 commit 改 task 状态（新增 / done / 推迟）时，必须同步 grep codemap / README / ADR 是否需要更新。本会话 T3.1/T3.2 落地后 04/10 codemap 漏改 1 周才被扫出，实际接口面与文档描述脱节。建议在 `会话习惯.md` 或 task done 检查表里添加“codemap 同步”一轮。
- **listRunsLastFilter 字段冗余**：`dag_query_test.go:211-220` 注释承认与 stubRunStore 字段命名重叠（`lastListFilter` vs stubRunStore.lastFilter）。可去除二选一，保留 stubRunStore 一侧即可。低优先级。
- **t.Parallel() 启用**：`dag_start_test.go` / `dag_query_test.go` 多用例未启用 `t.Parallel()`；T0.5/T1.2/T3.x stub 已并发安全（commit `d1f5b0e4` 字段化 stubRunStore + race test 验证），可启用以压缩本包测试总时。
- **FinishedAt 防御拷贝断言**：`dag_query.go:158` 用 `shared.CloneTime(row.FinishedAt)` 做防御拷贝，但当前测试断言只覆盖 Events / Metadata，未掰 FinishedAt 拷贝表现。低优先级；如后续发现 FinishedAt 在调用者侧被误改再补补丁。
- **service.ListRuns limit cap=200 抽常量**：commit `498be56d` 已在 service 层 cap，但 `200` 仍是字面量（出现于 service.go + dag_query_test.go）。建议提 `defaultListRunsLimitCap = 200` 或者走 contract 层常量，避免文档与代码双多头。

