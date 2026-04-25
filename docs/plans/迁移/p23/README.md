# P23 DAG 自驱执行引擎与多触发源能力补齐总览

> 创建时间：2026-04-25 | 状态：**未开动**
> 当前 authoritative 文档：`README.md`
> 输入基线：2026-04-25 `dag-runtime-audit` / `dag-entry-audit` 事实审计；契约以 `docs/契约/modularity-convention.md §4.4 / §7`、`docs/契约/fx-convention.md §2 / §3`、`docs/契约/rungroup-convention.md §2 / §4` 为准

## 目标

把 DAG 模块从「只存不跑」的数据库表升级成真正的「自驱执行引擎」，并补齐三条触发源能力（主 agent / 定时 / 外部 RPC）。
本文统一采用契约角色术语：

- `fx.Module`：DAG runtime 相关的 constructor + 资源 lifecycle（store / scheduler service 对象 provide）
- `BusModule`：subscribers wiring；callback 只做轻量状态更新或 non-blocking enqueue（terminal turn → node 回写、launch 回执 → run 推进）
- `RunnerModule`：DAG watcher / dispatcher / lease 全部进入 `runner.actors`（active Fx tag: `group:"runners"`）；不允许把长循环放进 `fx` lifecycle 或 callback

实施分两条线：

- `P0-P3`：DAG 自驱运行时收口（无此前置，三个触发源都没意义）
- `P4-P6`：三触发源能力补齐（主 agent / 定时 / 外部 RPC）

`P22` 的 runtime ownership / archtest 守卫是 P23 的硬前置：DAG watcher / dispatcher / lease 必须从一开始就按 `group:"runners"` 落地，不再走 `fx.Invoke` 直拉、bus callback 偷跑 goroutine 的老路。

## 现状校准（事实层）

DAG 模块当前是「只存不跑」：

- **没有 watcher 进程**。`auto_handoff_phase1` metadata 只在 `cmd/mcp-orch/tools/task_tools.go:23,231-235` 写入并被 `cmd/mcp-orch/orchestration/dag.go:109-131` 原样持久化，**没有任何 goroutine / service / cron 在消费它**。
- **DAG 创建即终点**。`task_create_dag` 调用链 `tools/task_tools.go:64-70,96 → orchestration/dag.go:109-131` 只 upsert DAG 与 nodes 后返回；没有 `StartDAG/TriggerDAG`，也没有 `task_start_node`（迁移计划单 `docs/plans/迁移/v3-migration-plan.md:1312-1322` 列了但未实现）。
- **ready 判定逻辑不存在**。`depends_on` 全 done 计算、`max_concurrency` / `queue_policy` / `fail_fast` 在 `tools/task_tools.go:144-146,257-264` 只写进 schedule metadata；`orchestration/dag.go:230,323-331` 只存/读 JSON，无依赖满足执行器。
- **节点状态机由外部推进**。当前唯一推进路径是外部工具调 `task_update_node` → `UpdateNodeStatus`（`tools/task_tools.go:100-105 → dag.go:167-184`），SQL `sql/queries/task_dag_node_write.sql:14-20` 不校验前态。
- **半成品基础件齐全**：
  - `pending → running` 写 `active_wakeup_id` 原语：`sql/queries/task_dag_node_runtime.sql:24-29`、`store/taskdag/store.go:145-155`
  - `running → done/failed`（`CompleteNode`）：`sql/queries/task_dag_node_runtime.sql:37-42`、`store/taskdag/store.go:168-177`
  - wakeup 表 + fence：`migrations/0023_dag_watcher_phase1.sql:9-30`、`store/taskdag/store_wakeup.go:9-104`
  - worker lease 表：`migrations/0023_dag_watcher_phase1.sql:38-43`、`store/taskdag/store_lease.go:9-45`
  - 这些 SQL/Store 全部存在但**无自动调用者**。
- **`command_ref` 占位**。`orchestration/dag.go:231` 只存不解析、不执行，不是当前 launch 路径。
- **真正的 launcher**：`cmd/mcp-orch/tools/orchestration_tools.go:38-57 → orchestration/service.go:299-301 → service_launcher_bridge.go:54-64`，prompt 自动投递在 `service_launcher_bridge.go:89-119`（已确认 first-turn 路径存在）。DAG watcher 必须复用此 launcher，不另起一路。

三触发源现状：

| 触发源 | 现状 | 关键缺口 |
|---|---|---|
| **主 agent（MCP）** | ✅ `task_create_dag/get/update_node` 已暴露（`tools/task_tools.go:64-70,84-90,96,100-105`）；`agent_id` 仅记录为 `created_by`（`migrations/0004_ack_dag.sql:39`），**仅记账不鉴权** | 无显式 start；DAG 创建后不会自跑；状态靠 poll；无 owner/tenant 字段 |
| **定时 / cron** | ⚠️ `internal/module/cron/scheduler.go:26-45,65-87` 已实现完整 cron 模块（robfig/cron），但触发的是 **turn/thread，不是 DAG**；DAG `schedule.trigger` 是自由 string 无 enum（`tools/task_tools.go:118-124,246-250`） | cron 调度器与 DAG 世界完全没打通 |
| **外部 RPC** | ⚠️ TCP JSON-RPC `127.0.0.1:8090` 已暴露 `task/dag/create|get|list`、`task/node/update`（`cmd/mcp-orch/orchestration/rpc.go:127-137`）+ Wails WS `127.0.0.1:4511`（`internal/ui/wails/http_server.go:15,39-46`）+ MCP HTTP `/mcp`（`internal/mcpserver/common/http_transport.go:38-53,73-79`）——**全部无 auth**（`internal/mcpserver/common/server.go:221-264`） | AuthN/AuthZ/tenant/rate-limit/quota/audit-log 全部为零 |

## 当前基线约束

- DAG runtime 默认归 `cmd/mcp-orch`（agent-visible / orchestration 执行面），不是 core；新增 watcher / dispatcher / lease 的 `RunnerModule` 落在 `cmd/mcp-orch/orchestration/<sub>/runner.go`。
- 关系型持久化统一走 `migrations/` + `sql/queries/` + sqlc；新增表/查询只维护 `cmd/mcp-orch/sqlc.yaml`（DAG 是 mcp-orch 消费侧），**不要**回写根 `sqlc.yaml`。
- 所有 DAG 长跑必须以独立 actor 进入 `runner.actors`（active Fx tag: `group:"runners"`），并实现 `Runner.Run(ctx)`：`fx.Module` 只 provide / open / close 资源；`fx.Invoke` 只把 subscriber 注入 `bus.subscribers`；不把任何长循环、drain、轮询塞进 `fx` lifecycle 或 bus callback——这是 `P22` 的契约硬底，本期开工即执行。
- Watcher 必须有 owner、有 stop/drain 语义、有 lease/heartbeat、有幂等 fence；不允许出现"启动后无人 stop"或"`OnStart -> go ...`"形态。
- `command_ref` 在本期不解锁；DAG 节点 launch 仅通过 `nodes[].launch` declarative spec（见 P0）走共享 launcher。`command_ref` 字段保留，但仍是占位。
- `provider` 字段语义冻结为 `codex|claude`；DAG node `launch.provider` 复用同一枚举，不引入第三值。
- 触发源相关参数（`schedule.trigger`、`auto_handoff_phase1`、`schedule.cron_expr`、外部 RPC AuthN）所有缺失字段必须 fail-fast，不做 silent fallback；具体见下节。
- core ↔ orch 事件链路仍走 hook consumer（`cmd/mcp-orch/orchestration/hook_consumer.go:96-220`）；DAG terminal 回写要订阅 hook，不要去 core bus 侦听重发。

## 默认值安全原则

- `schedule.trigger` 缺失或非白名单值（白名单：`manual` / `auto` / `cron` / `external`）→ `ErrInvalidTrigger`；不做 silent default。
- `nodes[].launch.cwd` / `launch.provider` / `launch.name` 缺失 → fail-fast；不沿用 DAG 创建者 cwd 推断、不静默选 codex。
- `schedule.cron_expr`：`schedule.trigger=cron` 时必填；解析失败立即 `ErrInvalidCronExpr`，**不**回退成 `manual`。
- 外部 RPC 调用缺 caller identity → `ErrCallerIdentityRequired`；不允许"匿名调用 = `created_by=system`"的隐式归属。
- node retry / timeout / on_failure 字段缺失 → 落到 `schedule.default_retry / default_timeout_sec / fail_fast` 三件套；缺失这三件套也只用代码级硬底（`retry=0`、`timeout=0=不超时`、`on_failure=stop`），**不**做"全部 retry 3 次"之类的 hidden default。
- watcher 处理 ready node 失败 → 写入 `node.last_error` + 触发 `on_failure`；**不**自动 disable DAG。
- AuthN 缺失或失败的外部调用 → `jrpc2.InvalidParams`；不允许带"`anonymous` 视作 trusted"的 fallback。

## core ↔ orch 事件链路的真实入口

- DAG terminal 回写依赖 hook consumer：`cmd/mcp-orch/runtime.go:216-219` 的 `subscribeOrchestrationHooks` → `cmd/mcp-orch/hook_subscription.go:13-40` 订 `agent.turn.after / failed / progress` hook → `cmd/mcp-orch/orchestration/hook_consumer.go:96-220` 已有 `TurnCompleted` 与 `ItemCompleted(final_answer)` 分流。
- DAG node 完成回写器（P2）必须在此 hook consumer 链上装 tap，把 turn terminal 映射到 `CompleteNode` / `MarkFailed`；**不**向 `cmd/mcp-orch` 本地 bus 寻找重发后的 core 事件。
- orch 本地 bus（`cmd/mcp-orch/orchestration/events.go`）只承载 DAG / wakeup / lease 自身事件，不双向桥接 core。

## 阶段 0：前置冻结（阻塞所有 track）

阶段 0 是短平快的共享契约 PR，必须先合入：

1. **migration 编号校准**：仓内当前最大已占用编号是 `migrations/0062_add_lsp_sections_enabled_tools_gate.sql`。本期新建 migration 必须从 `0063` 起算：
   - `0063_dag_state_machine.sql`（P0；为现有 `task_dag_nodes` 加 `assigned_agent_id`、`last_activity_at`、`remaining_deps` 等列、状态机 CHECK 约束、`pending→running→done/failed/observe_lost` 终态枚举）
   - `0065_dag_owner_tenant.sql`（P3 / P6；为 DAG 加 `owner_id / tenant_id / scope` 字段并迁移现有 `created_by`）
   - `0066_dag_trigger_cron.sql`（P5；cron→DAG 关联表，承接 `internal/module/cron` 触发 DAG 的能力；**编号从 0064 改为 0066，因为 P5 依赖 P3 owner/tenant，必须晚于 P3**）
   - 0064 编号**保留 unused**作为缓冲（避免后续合入时被误占）；后段 migration 编号冻结为：P8=0067、P9=0068（仅 no-transaction concurrent index / archive / scale policy；`remaining_deps` 已前移 P0 0063 以避免拆号冲突）、P10=0069、P11=0070、P12=0071、P13=0072（P7 仅加列预留，无独立 migration），详见各子任务 stub
   - 编号相互独立但口径统一；实施时每次开 PR 都再跑 `ls migrations/` 校准一次。

2. **DAG state machine 契约冻结**：本期把 node 状态机从"任意 status 可写"收紧成 `pending → running → done | failed | observe_lost`，由 `cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql` 增加 CAS 形 SQL（`WHERE current_status = $expected`），并写入文档：
   - 第三类终态 `observe_lost` 用于 watcher 确认 turn 已 submit 但 hook 永久丢失的情况，**不**自动重试，不增 `failure_count`。
   - 半提交窗口：`running + assigned_agent_id != ''` 但 turn hook 未到达，crash 后由 watcher 通过 `agent_id` 回查 `agent_status` 恢复；不允许 watcher 直接从 `running` 推回 `pending` 重新 launch。
   - `task_update_node` 由外部推 status 的旧路径保留但加 strict mode 开关；**新 DAG 默认 `schedule.strict_state_machine = true` 且不可由外部 RPC 降级**，仅旧 DAG 兼容期可显式 false 并打 deprecation audit，由 P0 owner 单独出契约 PR。

3. **RunnerModule 角色冻结**：DAG runtime 的所有长跑必须以独立 actor 进入 `runner.actors`（active Fx tag: `group:"runners"`）；至少冻结四个 actor 的 owner 划分：
   - `dagWatcherActor`：扫描 ready node、推进 `pending → running`、claim wakeup
   - `dagDispatcherActor`：消费 wakeup → 调 launcher 或 submit turn → bind active turn
   - `dagLeaseActor`：续租所有已 claim 的 node lease（30 min TTL，5 min heartbeat）
   - `dagReconcileActor`：crash recovery + `observe_lost` 检测 + retry budget 推进
   不允许把这四件事合成一个 actor，也不允许在 actor `Run(ctx)` 内 fire-and-forget 子 goroutine。

   后段 RunnerModule inventory 同步冻结：P7 `dagActivityActor`、P8 `dagArbiterActor`、P11 `dagConvergenceActor`（`convergence_evaluator.go`）、P12 `dagSwarmArbiterActor`、P13 `outputValidationActor` 均必须挂 `group:"runners"` 并由 `dag_runner_actors_present` / `dag_actor_no_fire_and_forget` 覆盖。

4. **trigger 字段枚举冻结**：`schedule.trigger` 由自由 string 改为枚举（`manual` / `auto` / `cron` / `external`）+ 配套字段：
   - `manual`：DAG 创建即静止，必须显式调 `dag/start`（P3 新增 RPC method）才推进
   - `auto`：DAG 创建即推进（兼容当前隐式期望）
   - `cron`：必须配 `schedule.cron_expr` + `schedule.timezone`（P5）
   - `external`：仅外部 RPC 凭 caller identity 推进（P6）
   schema 层在 `cmd/mcp-orch/tools/task_tools.go:118-124` 加 enum；旧 DAG（trigger 为空）默认按 `auto` 兼容一轮，但向用户/UI 显示 deprecation 提示。

5. **扩展点契约（为 P7 / P8 / P9 预留）**：基于 2026-04-25 五路 gap 调研裁决（详见 [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)），P23 **不实现**活性探针 / 校验闭环 / 大规模能力，但要求 P0 落地的 DDL / actor / hook 链路**预留扩展位**，避免 P7 / P8 / P9 上线时返工：
   - `task_dag_nodes` 在 `0063_dag_state_machine.sql` 中**预留** `last_activity_at TIMESTAMPTZ` 列。本期 watcher / dispatcher / reconcile 不消费它，但 P2 hook tap 必须把 turn progress / tool call 事件回写此字段；P7 直接消费，不再加列。
   - hook consumer（`cmd/mcp-orch/orchestration/hook_consumer.go:96-220`）的 P2 reconcile tap 必须是 **enqueue-only**：不允许在 callback 内做长跑、重 DB 查询、派生 launch；只允许投一条 enqueue 给 actor。这是 P8 verifier gate 的硬前置（verifier 拉起必须独立 actor，不在 callback 内）。
   - **P13 schema validate 例外（2026-04-25 交叉验证裁决）**：hook tap 只允许 bounded parse / payload size cap / enqueue；完整 JSON schema validate 必须在 `outputValidationActor` worker 中执行，并且在写 terminal status、进入 P8 verify 前完成。archtest `dag_hook_tap_enqueue_only` 只对白名单轻量 parse + enqueue 放行，禁止网络 / LLM / 重 DB 查询 / 全量 schema validate。
   - P23 冻结的 state machine `pending → running → done | failed | observe_lost` 描述为「**执行子状态机**」（execution sub-state machine）：上层 P8 会在 `running` 与 `done` 之间插入 `pending_verify / verifying / repairing` 等业务子状态，但**不**破坏 P23 的 SQL CAS 形状——子状态走独立列 `verify_phase`（P8 加），不和 `status` 共枚举，CAS `WHERE current_status = $expected` 仍然成立。P8 会扩 `status` CHECK 约束加入 `verdict_lost` 第三类终态（类比 `observe_lost`），仍属合规扩展，不算破坏 CAS 形状。
   - launcher 并发上限 `maxConcurrentLaunches=10`（`cmd/mcp-orch/orchestration/service_launcher_bridge.go:22-30`）必须**配置化**（提取成 `cmd/mcp-orch` config 参数），P23 不改默认值；P9 升级为全局 token bucket 时不需要再做一次代码迁移。
   - launcher 全局 quota 的占用方在 P8 引入 verifier 后必须包含 verifier launch；不允许 verifier 走独立 quota 通道（防止 P9 规模下 launcher 双队列雪崩）。

## 实施路线图

| 优先级 | 子计划 | 描述 | 当前状态 |
|---|---|---|---|
| **[P0](P0_DAGRuntimeSkeleton.md)** | DAG 自驱运行时骨架 | watcher actor + ready 计算器 + `pending→running` 调度器 + 状态机 CAS；冻结 4 actor 角色 | 🔲 未开动 |
| **[P1](P1_WakeupDispatcherAndLaunchBinding.md)** | wakeup dispatcher + node→launch 绑定 | 消费 wakeup、调共享 launcher、回写 `assigned_agent_id`、bind `active_turn_id` | 🔲 依赖 P0 |
| **[P2](P2_NodeTerminalReconcile.md)** | 节点终态回写 + timeout/retry/on_failure | 订阅 hook consumer 的 turn terminal → `CompleteNode`/`MarkFailed`；执行 `execution.timeout_sec / retry / on_failure` 与 `schedule.fail_fast` | 🔲 依赖 P1 |
| **[P3](P3_ExplicitDAGStartAndOwnership.md)** | DAG 显式 start/trigger 语义 + owner/tenant | 新增 `dag/start` RPC；为 DAG / node 加 `owner_id / tenant_id / scope`；`trigger` 枚举落地 | 🔲 依赖 P0 |
| **[P4](P4_HostAgentTriggerSurface.md)** | 主 agent 触发面 | MCP 工具 `task_create_dag` 接受 `auto_start: true`；DAG terminal 事件通过既有 hook 回 agent；保持 100% 向后兼容 | 🔲 依赖 P3 |
| **[P5](P5_CronTriggerSurface.md)** | Cron 触发面 | 复用 `internal/module/cron` 已有 scheduler/runner；新增 `cron_job.target_dag_key`；cron tick → DAG `trigger=cron` start（原表述 `external` 已修正，外部 RPC 走 `trigger=external`） | 🔲 依赖 P3 + p21 P1b |
| **[P6](P6_ExternalRPCTriggerSurface.md)** | 外部 RPC 触发面 + AuthN/AuthZ/rate/quota/audit | 把现有 TCP JSON-RPC `task/*` 方法挂上调用身份提取层、tenant 鉴权、rate limit、审计日志；非 REST/gRPC 不在本期升级 | 🔲 依赖 P3 |
| **[P7](P7_LivenessProbe.md)** | 心跳式节点活性监控 | 第 5 actor `dagActivityActor`；同 agent 重投 vs 换 agent 重起；长工具反误杀；后段子任务，前段（P0–P6）合入后开工 | 🔲 后段 |
| **[P8](P8_VerificationGate.md)** | 校验闭环 + verdict 仲裁 | hook terminal → verify 子状态机 → runtime-embedded LLM arbiter（默认方案 A）；judge node 仅显式 opt-in；arbiter 不可得落 `verdict_lost` | 🔲 后段（依赖 arbiter 报告） |
| **[P9](P9_ScaleScheduling.md)** | 大规模 DAG 调度 | 千 node 百 agent；批量 create / partial index / 全局 token bucket / hook worker pool / TTL 归档 / SLO + backpressure | 🔲 后段 |
| **[P10](P10_TemplateAndUI.md)** | DAG 模板 + UI 编辑能力 | `dag_templates` 表；`dag/template/*` + `dag/instantiate` + `dag/edit_node` RPC；UI 模板库 / 任务列表 / 编辑器；UX 原则 + 三用户故事 | 🔲 后段（依赖 P3+P6） |
| **[P11](P11_DynamicNodeGrowth.md)** | 自动节点伸缩 / 无限迭代 | `task_spawn_child` 工具；`growth_budget` 硬约束；`convergence` 终止条件；动态生长的 DAG | 🔲 后段（依赖 P0/P1/P2 + P9 backpressure） |
| **[P12](P12_SwarmArbiter.md)** | 蜂群涌现仲裁 | 多 LLM 并行 + consensus 算法（majority / unanimous / weighted）+ dissent_action；P8 ensemble 升级 | 🔲 后段（依赖 P8 单 arbiter 已合入） |
| **[P13](P13_StrictJSONOutput.md)** | JSON 严格输出模式（金融合规） | `output_schema` + `output_validation`；validator + repair / fail；金融场景预设 | 🔲 后段（依赖 P0/P1/P2 + P8 sanitize） |

## 依赖拓扑

```
阶段 0：编号校准 ─┬─► P0（0063_dag_state_machine）
                 ├─► P3（0065_dag_owner_tenant）
                 ├─► P5（0066_dag_trigger_cron；原占 0064 已修正，0064 保留 unused buffer）
                 ├─► P8（0067_dag_verify_phase）
                 ├─► P9（0068_dag_scale_partial_index）
                 ├─► P10（0069_dag_templates）
                 ├─► P11（0070_dag_growth）
                 ├─► P12（0071_dag_swarm）
                 └─► P13（0072_dag_output_validation）

阶段 0：state machine 契约 ─► P0 / P1 / P2 全部依赖

阶段 0：RunnerModule 角色冻结 ─┬─► P0 (watcher / reconcile)
                              ├─► P1 (dispatcher)
                              └─► P5 (cron→dag bridge actor 复用)

阶段 0：trigger 枚举 ─┬─► P3 (manual/auto)
                     ├─► P5 (cron)
                     └─► P6 (external)

P0 ─► P1 ─► P2          # runtime 主链
P0 ─► P3                 # ownership/start 语义
P3 ─► P4                 # 主 agent
P3 + p21 P1b ─► P5       # cron
P3 ─► P6                 # 外部 RPC
P2 ─► (所有触发源 terminal 反馈链路)

# 后段（P7/P8/P9/P10/P11/P12/P13）：必须等前段（P0–P6）合入后才开工
P0 + P1 + P2 ─► P7         # 活性
P0 + P1 + P2 ─► P8         # 校验闭环 + 方案 C arbiter（前置：建轻量 LLM 调用层）
P0 + P1 + P2 ─► P9         # 规模化
P3 + P6 + P7/P8/P9 schema freeze ─► P10  # 模板 + UI（依赖鉴权 + owner/tenant + 后段字段稳定）
P0 + P1 + P2 + P9 ─► P11   # 动态生长（依赖 backpressure）
P8 + P9 ─► P12             # 蜂群仲裁（P8 ensemble 升级；依赖 P9 token bucket / budget ledger）
P0 + P1 + P2 + P8 sanitize + P10 preset ─► P13  # JSON 严格输出（依赖 sanitize + 金融模板预设）
```

## 落地顺序建议

1. 先合阶段 0 三件冻结：编号校准 + state machine 契约 + RunnerModule 角色；不要把 watcher 实现先于契约写入。
2. `P0`（runtime 骨架）必须独立合入，并伴随 archtest 守卫（不允许 DAG 长跑 goroutine 出现在 `OnStart` / bus callback 内）。
3. `P0` 合入后 `P1`（wakeup dispatcher）必须紧接着合，否则 `pending→running` 之后会卡死无人 launch。两者强依赖。
4. `P2`（terminal reconcile）必须在 `P1` 之后合：先有 launch 链，才有 terminal 回写链；P2 在 hook consumer 上装 tap，不要重新发明 turn 归因层。
5. `P3`（显式 start + owner/tenant）可与 `P1` / `P2` 并行设计但**代码合入串行**：DAG schema 改动（新增 owner / trigger enum）必须先于触发源 PR。
6. `P4`（主 agent）合入相对低风险，可在 `P3` 后立即并行；`auto_start: true` 是 syntactic sugar，runtime 仍走 P0/P1/P2 链路。
7. `P5`（cron）必须在 p21 P1b 落地之后开工——p21 P1b 是 cron 模块本身的 runner / lease / non-interactive 契约前置；本期 P5 只补 `cron_job.target_dag_key` 字段 + `cron tick → dag/start(trigger=cron)` 桥接 actor（cron 触发的 trigger 枚举值是 `cron`；`external` 只用于外部 RPC 触发上下文）。
8. `P6`（外部 RPC）**只补 auth/tenant/rate/quota/audit**；不在本期把 TCP JSON-RPC 升级成 REST 或 gRPC，那是更晚的事。
9. 任何 PR 不许同时改 runtime + 触发源；写集隔离按"runtime（P0/P1/P2）" / "ownership 契约（P3）" / "触发源（P4/P5/P6）"三组分批。

## 收口口径

### DAG node state machine（authoritative）

- 状态枚举固定为 `pending → running → done | failed | observe_lost`；`observe_lost` 是第三类终态（half-submit window 不可恢复），不计入 retry budget。
- 状态推进必须走 SQL CAS（`WHERE current_status = $expected`），由 P0 在 `sql/queries/task_dag_node_runtime.sql` 落地；外部 `task_update_node` 在 `strict_state_machine` 模式下拒绝跳态写入。
- `assigned_agent_id` 由 dispatcher 在 `pending→running` 同事务内写入；后续不允许覆盖（除非节点 retry policy 显式 relaunch，见下）。
- crash recovery：watcher 重启时对 `running + assigned_agent_id != ''` 的节点逐条调 `agent_status` lookup；命中 → 继续观察 hook；未命中且 lease 过期 → 推进 `observe_lost`；**禁止**自动回退 `running → pending` 触发重 launch。
- node retry：`execution.retry > 0` 且未到上界时，retry 默认**复用已绑定 agent**（再投一轮 turn）；只有显式 `execution.relaunch_on_retry = true` 才走 launcher 重起新 agent，并在事务内换 `assigned_agent_id`。

### `dag_ref` / `nodes[].launch` 协同（authoritative）

- `nodes[].launch` 是 source of truth：watcher 在 ready node 上读取 `launch` spec → 调共享 launcher。
- `orchestration_launch_agent.dag_ref`（仅在 P22 协同设计中预留）只做反向认领 / 去重，不触发 watcher 重新 launch；如果某 node 已有 `assigned_agent_id`，`dag_ref` 调用直接 noop + 返回该 agent_id。
- 这条规则把"谁发起 launch"钉死在 watcher 一侧；任何把 launch 发起权回传 agent / 客户端的方案都视为 P23 范围外。

### 触发源 → DAG start 路径（authoritative）

- 所有触发源最终汇流到一条共享入口 `cmd/mcp-orch/orchestration/dag_start.go:StartDAG(ctx, dag_key, trigger_meta)`（原写 `internal/orchestration/dag.Start`，因 P22 allowlist 不含此包，已修正落点）；不允许任一触发源绕过此函数直接改 DAG status。
- `trigger_meta` 必带 `caller_identity`（`agent_id` / `cron_job_id` / `external_caller_id`）；watcher 把它写进 `dag.last_trigger_at` + `dag.last_trigger_by`。
- 主 agent / cron / 外部 RPC 三条路径在 `Start()` 内共享同一鉴权 + idempotency 入口；triggers 通过 `idempotency_key` 防止双触发同一 DAG 在同一时窗内重入。

### 默认值与硬错误（authoritative）

- `trigger` 缺失 / 非枚举 → `ErrInvalidTrigger`
- `cron` trigger 缺 `cron_expr` / `timezone` → `ErrCronSpecRequired`
- `nodes[].launch.cwd` / `provider` / `name` 缺 → `ErrLaunchSpecIncomplete`
- 外部 RPC 缺 caller identity → `ErrCallerIdentityRequired`（映射到 `jrpc2.InvalidParams`）
- watcher claim 无可用 wakeup → 退避 backoff，不抛错；archtest 守 `dag_watcher_no_panic`

### 可观测性（最低集）

- 队列：`dag_ready_queue_depth`、`dag_wakeup_pending_total`
- 推进延迟：`dag_node_pending_to_running_seconds`、`dag_node_running_to_terminal_seconds`
- launch：`dag_launch_attempts_total{result=ok|failed}`、`dag_launch_relaunch_total`
- 终态：`dag_node_terminal_total{state=done|failed|observe_lost|verdict_lost}`、`dag_observe_lost_total`、`dag_verdict_lost_total`
- crash recovery：`dag_recovery_runs_total`、`dag_recovery_observe_lost_total`
- 触发源：`dag_start_total{trigger=manual|auto|cron|external}`
- 这些指标必须在各 actor 中明确产出；archtest 守 actor 必须订阅或自带 metric register（不接受"行为正确但无指标"的实现）。

### 守卫与 archtest

基于 2026-04-25 6 路 impl/compliance 调研裁决（详见 [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §裁决 9 + [`COMPLIANCE_GATES.md`](COMPLIANCE_GATES.md)），P23 必落 21 项 archtest（原 15 项 + 6 项交叉验证补充）：

| archtest | 守的内容 | 落点 |
|---|---|---|
| `dag_watcher_no_lifecycle_loop` | DAG 长循环不在 lifecycle / bus callback | `internal/archtest/dag_runtime_ownership_test.go` |
| `dag_runner_actors_present` | P0 4 actor + P7/P8/P11/P12/P13 后续 actor 都进 `group:"runners"` | 同上 |
| `dag_actor_no_fire_and_forget` | actor `Run(ctx)` 禁 `go` / `SafeGo` / 裸 ticker | 扩 `runner_actor_guard_test.go` |
| `dag_status_cas_only` | status 写入必须带 `WHERE current_status = $expected` | `internal/archtest/dag_status_sql_test.go` |
| `dag_trigger_enum_only` | `schedule.trigger` 必须命中枚举 | `internal/archtest/dag_schema_test.go` |
| `dag_external_rpc_authn` | 外部 RPC 必经 `WithCallerIdentity` middleware | `internal/archtest/dag_rpc_authn_test.go` |
| `dag_launcher_shared_path_only` | dispatcher 复用共享 launcher，禁绕路 | `internal/archtest/dag_launcher_test.go` |
| `dag_hook_tap_enqueue_only` | P2 hook tap 禁重 DB / 派生 launch / LLM；P13 只允许 bounded parse + enqueue，完整 validate 进 worker | `internal/archtest/dag_hook_test.go` |
| `dag_llm_light_boundary` | P8/P12 只能经 `cmd/mcp-orch/orchestration/llm/light/*`，不裸调 provider | `internal/archtest/dag_llm_boundary_test.go` |
| `dag_growth_spawn_only` | P11 禁绕过 `SpawnChildNodes` 直接 INSERT node | `internal/archtest/dag_growth_test.go` |
| `dag_swarm_quota_only` | P12 swarm 共用 P9 token bucket（**不**起独立 quota） | `internal/archtest/dag_swarm_test.go` |
| `dag_output_validate_before_verify` | P13 schema validate 必须早于 P8 verify_phase | `internal/archtest/dag_output_validation_test.go` |
| `dag_template_no_cmd_import` | UI/template 不反向 import cmd concrete | `internal/archtest/dag_template_boundary_test.go` |
| `dag_migration_sequence_guard` | 必须存在 `0063,0065,0066,0067,0068,0069,0070,0071,0072`；任何 `0064_*.sql` hard fail；除 0064 显式保留外不得缺号/倒序/依赖冲突 | `internal/archtest/dag_migration_test.go` |
| `cron_dag_bridge_no_concrete_orch_import` | `internal/module/cron/*.go` 禁 import `cmd/mcp-orch/`，且 `cmd/mcp-orch` 禁直接 import `internal/module/cron` concrete，桥接只经登记 interface/platform sink | `internal/archtest/dag_cron_bridge_test.go` |
| `dag_audit_append_only` | `dag_audit_log` / `dag_arbiter_calls` / `dag_swarm_consensus` / `dag_output_validations` 禁 UPDATE/DELETE，必须 append-only + hash chain | `internal/archtest/dag_audit_append_only_test.go` |
| `dag_terminal_turn_fence` | `CompleteTaskDagNode` 必须校验 `active_turn_id` 与当前 turn 一致，late completed 不能覆盖 aborted（a5） | `internal/archtest/dag_turn_fence_test.go` |
| `dag_no_status_naked_write` | 禁裸 status 写；所有写必带 `WHERE current_status = $expected` CAS（a5 补 `task_dag_node_write.sql:14-20`） | `internal/archtest/dag_status_sql_test.go`（扩） |
| `dag_tenant_filter_required` | DAG/template/audit 查询 RPC 必带 `tenant_id` filter（a4 critical） | `internal/archtest/dag_tenant_filter_test.go` |
| `dag_pii_redaction_present` | `repair_prompt` / `error_detail` / arbiter input/output 在落库前走统一 redactor（a4 + P13 金融需求） | `internal/archtest/dag_pii_redaction_test.go` |
| `dag_mcp_orch_dependency_allowlist_tight` | 收紧 `internal/archtest/dependency_direction_mcp_orch_test.go` 中 cmd/mcp-orch allowlist；禁止继续放行未登记的 `internal/store` / `internal/module` 直连 | `internal/archtest/dependency_direction_mcp_orch_test.go:TestMCPOrchDependencyDirection` |

## 风险

- **半成品基础件复用风险**：`migrations/0023_dag_watcher_phase1.sql` 设计意图与本期 watcher 是否完全一致需在 P0 owner 启动前一次性核对（重点是 wakeup 字段语义、lease TTL 默认、worker_id 来源）；不一致就要在 0063 里同步修，不允许"先用着再说"。
- **crash window**：dispatcher 在 `pending→running` CAS 后、launcher 调用前 / 调用后 hook 到达前两个窗口都可能崩；恢复必须按 `assigned_agent_id` 是否已绑定 + agent 是否真起来分支处理（参考 p21 P1b crash-window state machine 章节，本文 state machine 直接对齐其形式）。
- **launch 失败映射**：launcher 异步成功 ≠ agent 真起来；P1 必须区分 `launcher 接受请求成功`（CAS 推进 `pending→running`）vs `agent 启动失败`（reconcile actor 兜底转 `failed`）；不能把异步 ack 当作 launch 成功。
- **lease 抖动**：DAG node lease 与 cron job lease（p21 P1b）共享 `internal/store` lease 抽象但**不复用同一表**；不要为了"统一"把两者合表，会把 retry / TTL / 终态语义搅在一起。
- **外部 RPC 暴露面**：本期只补 auth + tenant + rate；`127.0.0.1:8090` 默认绑定不变，未明确 opt-in 0.0.0.0 之前不算"对外服务"。任何在 P6 之外把端口公网化的 PR 视为越界。
- **cron 双触发**：cron tick 触发 DAG start 必须按 `idempotency_key=hash(cron_job_id, scheduled_at)` 去重；防止 cron 续租失败再 claim 时重触发同一时点 DAG。
- **观测断层**：watcher / dispatcher / lease / reconcile 四 actor 必须各自产出独立 actor lifecycle 日志（start / stop / iteration tick / error），并从 hook 起点贯穿 `trace_id` 到 wakeup / launcher / arbiter / audit；只有"全局 tracer"无 per-actor heartbeat 视为未收口。
- **向后兼容**：旧 `auto_handoff_phase1: true` 的 DAG 视作 `trigger: "auto"`；旧 `auto_handoff_phase1: false` 的 DAG 视作 `trigger: "manual"`；本期 README 完成后所有新建 DAG 必须显式带 `trigger` 字段，UI / agent prompt 中要更新引导。退役窗口：P0 后新 DAG strict=true；P3 双写 `created_by→owner_id`；P6 后新读路径禁只按 `created_by` 授权；P10 前旧 trigger 空值只读兼容，1 release 后 hard fail。
- **安全 critical 3 项（a4 调研，2026-04-25）**：跨租户访问（RPC 只过滤 `created_by`，缺 `tenant_id` filter） / 鉴权绕过（`127.0.0.1:8090` JSON-RPC + TCP + MCP HTTP 全无 auth） / 特权升级（MCP tool 无 caller，spawn 入口未实现统一授权，模板 fork 权限弱）。高级风险：`dag_audit_log` 无 append-only/hash chain；arbiter input/output 明文落库（PII）；sanitize 层未实现（prompt injection）。
- **数据一致性 3 项（a5 调研）**：`task_dag_node_write.sql:14-20` 还允许裸 status 写（无 `WHERE current_status = $expected`） / `CompleteTaskDagNode` 不校验 `active_turn_id`，late completed 可覆盖 aborted / DAG/node/wakeup/arbiter/validation 多表仅靠 `dag_key` 逻辑关联，无 FK/GC 闭环。
- **N=10000 击穿（a3 调研）**：三大瓶颈——DB ready/lock 全量读 + 整 DAG `FOR UPDATE`；hook/launcher 背压缺失；LLM verdict 开环（P8/P12 无 batch / token bucket）。P9 必须 SQL `FOR UPDATE SKIP LOCKED LIMIT K` + result 拆摘要 + actor buffer 容量化。
- **跨 agent 共识（a1–a10）**：sanitize 层 / 租户 filter / append-only audit / promhttp 陈列被多个调研重复点名，作为“阶段 0 之前必补”优先项。
- **archtest allowlist 真实代码未收紧（a1 ❌）**：`internal/archtest/dependency_direction_mcp_orch_test.go:23-29` 仍放行 `internal/store` / `internal/module`，P0 前必须收紧到 P22 modularity 名录；未收紧前不得把 README/COMPLIANCE 的新 archtest 视为已闭环。

### 整体合规风险评级：**高**（基于 6 路 impl/compliance 调研，2026-04-25）

主要漂移源（必须在阶段 0 之前修正，否则后段全员被迫返工）：

- **P3 落点漂移**：stub 写 `internal/orchestration/dag.Start` 与 README §"当前基线约束"（DAG runtime 默认归 `cmd/mcp-orch`）冲突。✅ 已修正：改为 `cmd/mcp-orch/orchestration/dag_start.go` + 共享入口 `StartDAG(ctx, dagKey, triggerMeta)`。
- **P5 跨 root 边界违规**：`internal/module/cron` 直接调 `cmd/mcp-orch/orchestration` 会被 `internal/archtest/dependency_direction_mcp_orch_test.go:49-53` 拦截。✅ 已修正：改为 cron 模块定义 `TriggerSink` 接口，mcp-orch 装配实现 bridge。
- **P5 idempotency 缺失**：当前 cron run 用 UUID（`internal/module/cron/scheduler.go:318-326`），不是 deterministic `hash(cron_job_id, scheduled_at)`。✅ 已修正：在 P5 stub 写明必须 deterministic key + 唯一约束 `(job_id, scheduled_at, target_dag_key)`。
- **migration 编号顺序冲突**：P5 (0064) 早于 P3 (0065) 但依赖 P3。✅ 已修正：P5 改 0066，0064 保留 unused。
- **`internal/llm/light/*` 落点违反 allowlist**：P22 archtest `dependency_direction_mcp_orch_test.go:23-29` 不允许 `internal/llm`。✅ 已修正：改为 `cmd/mcp-orch/orchestration/llm/light/*`（只服务 DAG arbiter，未来其它模块需要再升级到 `internal/platform/llm/light`）。

未修正的关键缺口（合规保障依赖项，需阶段 0 之前补）：

- **CI workflow 不存在**：仓库无 `.github/workflows/`。P23 的 archtest gate 当前只能跑本地 `make guard` + `go test ./internal/archtest/...`。要么先建 CI workflow，要么 fallback 到 pre-commit + 本地强制 + PR 模板贴本地输出。详见 [`COMPLIANCE_GATES.md`](COMPLIANCE_GATES.md)。
- **runtime metrics / alert 链路缺失**：仓库只有 `internal/platform/metrics/metrics.go` 的 counter 声明，没有 promhttp exporter / alert 链路。P9 SLO + P11 budget alert + P6 audit fail alert 都依赖此能力——必须先补 promhttp + 统一日志告警 sink，或这些 SLO/alert 暂时降级为 scheduled audit + log only。
- **P9 hook worker pool 是 P21 Observation Contract 级重构**：terminal precedence / 归因不能变（README §"core ↔ orch 事件链路"+ P21 Canonical Turn Observation Contract）。owner 启动前必须读 P21 Observation Contract。

### 与后段子任务（P7 / P8 / P9 / P10 / P11 / P12 / P13）的耦合风险（必须先冻结）

- **活性 × 校验**：P7 活性 actor 必须识别 P8 引入的 `pending_verify / verifying / repairing` 子状态，禁止把"等待 verifier"误判为 idle；relaunch 与 reject/repair 共用 CAS fence，禁止双推进同一 node。
- **活性 × 规模**：P9 引入百 agent 后，P7 全表扫描 `last_activity_at` 必须分片 + lease jitter；不允许 N=1000 时活性扫描定时器形成 DB 周期性尖峰。
- **校验 × 规模**：P8 verifier 在 P9 规模下会 N 倍放大 launcher / hook 压力；verifier 必须共用 launcher quota、每 DAG verifier budget 上限、可降级到采样校验或同批互验。
- **加和效应**：千 node × 探活每 N 分钟 × verifier gate 二次执行 = 终态链路 2-4 倍放大。最弱环节是 hook consumer + DB CAS 队列回写，不是 launcher 本身。P23 P2 reconcile 必须从一开始按 worker pool 形态设计（即使本期 pool size=1）；禁止写成"hook callback 内同步推 status"。
- **方案 C 仲裁器（P8 实现）的成本**：用户 2026-04-25 决策 verifier verdict 走方案 C（默认 A：runtime-embedded LLM；opt-in B：judge node）。千 node × 每 node 一次 arbiter 调用 = 千次 LLM 调用 / DAG。P8 必须配 batch 聚合（多 verifier 报告攒一次 LLM 调用）+ 失败终态 `verdict_lost`（arbiter 调用失败**不**自动降级 B；用户需要 B 必须显式 opt-in，避免隐藏成本）。

## 非目标

- 不在本期把 `command_ref` 解锁为可执行 launch 路径；P23 的 launch 路径只走 `nodes[].launch` declarative spec。
- 不在本期把 TCP JSON-RPC 升级成 REST / gRPC；外部 RPC 触发源（P6）只补 auth/tenant/rate/quota/audit，传输层维持现状。
- 不在本期重做 `internal/module/cron`；P5 只补"cron job → DAG start"桥接，不改 cron 自身的 lease / runner / non-interactive 契约（那是 p21 P1b 的责任）。
- 不在本期改 `task_create_dag` 的 schema 结构（除冻结 `trigger` 枚举与新增可选 `nodes[].launch` 之外）；现有调用方 100% 向后兼容。
- 不把 DAG / node 升级成全局 bus event 通道；orch 本地 bus 只承载 DAG 内部事件，跨进程 / 跨 root 走 hook consumer。
- P0–P6 前段不接管"DAG 是否可视化 / UI 编辑"；P10 后段正式承接 UI/模板能力，依赖 P3/P6/P7–P9 schema 稳定。
- 不引入第三种 provider；`provider` 仍冻结为 `codex|claude`。
- **不实现活性探针 / 校验闭环 / 大规模能力**——这三类能力分别拆出 P7 / P8 / P9 三个子任务（见下节边界）；P23 只负责给它们冻结扩展位与冲突缓解契约。

## 未来扩展边界 / 不可纳入本期

基于 2026-04-25 五路 gap 调研（`gap-liveness` / `gap-verify` / `gap-scale` / `gap-synth` / `gap-arbiter`，详见 [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)），下列三类能力**明确不在 P23 范围内**，由 P23 后段子任务（P7 / P8 / P9）承接。这是 P23「不变成 mega-plan」的硬边界。

### P7 心跳式节点活性监控（Liveness Probe）

承接「DAG 每 N 分钟探测 node 上 agent 有无新动向，sleeping agent 自动 relaunch」。

- 新增 `dagActivityActor`（第 5 个 actor），扫描 `last_activity_at` 老化的 running node
- 区分两种 relaunch 语义：(a) **同 agent_id 复用 thread 重投 turn**（保留上下文，仅当原 agent 仍可达）；(b) **换 agent_id 重起新 agent**（CAS 推进 status，兼容 P23 `relaunch_on_retry`）
- 长工具调用（如 `code_run` 5 min）反误杀：`ToolCallBegin` 在 hook 层标记"工具运行中"，配合 `tool_idle_timeout_sec` 与 `idle_timeout_sec` 双阈值
- 新字段：`schedule.default_idle_timeout_sec` + `nodes[].execution.idle_timeout_sec` + `nodes[].execution.tool_idle_timeout_sec`
- 依赖：P23 P0 完成 + `last_activity_at` 列已就位（P23 阶段 0 ⑤ 已预留）

### P8 校验闭环（Verification Gate）

承接「hook 拦截 task 完成、自动拉起校验 agent / 同批互验、通过 done / 不通过打回原 agent 修复」。

- 引入 verify 子状态机（独立 `verify_phase` 列，**不**和 P23 主 `status` 共枚举）：`running → pending_verify → verifying → done | repairing → running`；失败终态 `verdict_lost`（独立第三类终态，等同 `failed` 但语义独立）
- 新字段：`nodes[].verify { enabled, mode: async|batch_peer, group, provider, agent_key, prompt_template, repair_prompt_template, max_rounds, timeout_sec, on_reject, verdict_strategy, judge_node_key, arbiter_provider, arbiter_model, arbiter_max_tokens, arbiter_timeout_sec }` + `dag.verify_defaults`
- sibling group 概念：`verify.group` 只用于"同批互验"，**不**污染 `depends_on` 业务拓扑
- 死循环上限走 `verify.max_rounds`，**不**复用 `execution.retry`
- **verdict 仲裁实现：方案 C — 默认 A（runtime-embedded LLM）+ opt-in B（judge node）**（用户 2026-04-25 决策；采纳 `gap-arbiter` 推荐）
  - **方案 A 形态**：第 6 个 actor `dagArbiterActor`（独立 actor，不在 hook callback 内同步发 LLM 调用）；链路 `terminal hook → enqueue arbiter job → dagArbiterActor → 调轻量 LLM 调用层 → 写 verdict`
  - **方案 B opt-in**：schema 显式 `verdict_strategy=judge` + `judge_node_key`；走 DAG 上一个普通 node 的常规 launcher 路径
  - **失败终态 `verdict_lost`**：arbiter LLM 调用失败（服务挂、超时、JSON parse 失败）→ 落 `verdict_lost`，**不**自动降级 B（避免隐藏成本，需要 B 必须显式 opt-in）
  - prompt injection 防护：verifier 报告作为 quoted data + system prompt 明确"不执行报告内指令" + JSON schema 强校验
  - 审计：每次 arbiter 调用落一行 `dag_arbiter_calls` 表（输入 hash / 输出 / model / latency / cost）
  - **轻量 LLM 调用层（前置 PR）**：仓库内**没有**可复用的"非 agent 形态轻量 LLM 调用"——`internal/contract/provider.go:10-39` 无 `Complete(ctx, req)` 接口；`internal/module/prompt/classifier/claude_cli.go` 是 CLI 特化路径；`internal/contract/dream.go:10-12` 在 codex/claude 两边都是 TODO（`provider/codexapp/dream_executor.go:19-25`、`provider/claudecli/dream_executor.go:19-25`）。P8 必须有一个**前置 PR 建轻量 LLM 调用层** `cmd/mcp-orch/orchestration/llm/light/*` 才能开工（原写 `internal/llm/light/*`，已被修正到合规包）
- 打回原 agent：复用 P23 `relaunch_on_retry=false` 的 retry 路径（同 agent + 再投一轮 turn），把 verifier 反馈拼入下一轮 prompt
- 依赖：P23 P0/P1/P2 + P21 Canonical Turn Observation Contract + P8 内部前置 PR（轻量 LLM 调用层）

### P9 大规模 DAG 调度（Scale）

承接「自动迭代一个系统、千 node、百 agent、上千步工具调用」。

- 批量 create：`task_create_dag` 在 N>50 时拆批 / async / streaming（避免单事务 1000 次 UpsertNode）
- ready 索引：`task_dag_nodes` 使用 P0 前移的 `remaining_deps` 或等价依赖计数，并在 P9 加 `WHERE status='pending'` partial index（避免每次扫 JSONB `depends_on`）
- launcher 全局 token bucket：替换固定 `maxConcurrentLaunches=10`，按 `min(DAG max_concurrency, 全局 quota, provider quota)` 取下界；verifier launch 共用同一 quota，**不**另起通道
- hook consumer worker pool：bounded queue + drop / lag 指标，core hook 线程只 enqueue
- wakeup / lease / archive：DAG 终态后按 TTL 归档；node `result` 字段只存摘要，详细日志 spillover 外部 storage
- 容量模型 + SLO 指标：`dag_node_per_second` / `dag_launcher_queue_lag_seconds` / `dag_hook_consumer_lag_seconds` / `dag_wakeup_age_seconds`（p99 由查询计算）
- backpressure：watcher / dispatcher 按 launcher / hook lag 退避，不再开环
- cost/budget：P9 提供 `dag/cost_preview` + tenant/subscription budget ledger；token bucket 只控速率，预算耗尽必须 hard stop
- 依赖：P23 P0/P1/P2 + P22 archtest 守卫

### P10 DAG 模板 + UI 编辑能力

承接「DAG 要有 UI，用户可以保持模版，然后编辑模版或者编辑 dag 任务」（用户 2026-04-25 提出）。

- 新增 `dag_templates` 表 + `task_dags.template_key/template_version/template_snapshot` 列；模板版本历史 `dag_template_revisions`
- 新增 RPC：`dag/template/create|get|list|update|delete` + `dag/instantiate(template_key, params)` + `dag/edit_node` + `dag/edit_dag`
- UI：模板库 tab + 任务列表 tab + 拓扑编辑器（v1 mermaid 渐进式，v2 视需求换 d3）
- 编辑权限：模板任意编辑；任务级未启动可编辑；节点级仅 `status=pending` 可编辑（CAS fence）
- 解耦：实例化时拷贝模板 snapshot 进任务，后续模板编辑不影响已实例化任务
- 依赖：P3（owner/tenant 已就位）+ P6（外部 RPC AuthN，UI 调用方需鉴权）+ P7–P9 schema 稳定

### P11 自动节点伸缩 / 无限迭代（Dynamic Node Growth）

承接「自动节点伸缩（无限迭代）」（用户 2026-04-25 提出）。

- DAG 从「创建即固定结构」升级为「运行中可动态生长」：agent 通过 `task_spawn_child` 在 running node 下追加 child node
- 增长预算硬约束：`dag.growth_budget = { max_total_nodes, max_depth, max_spawn_per_node, max_runtime_sec }`，超额 `ErrGrowthBudgetExceeded`
- 收敛 / 终止条件：`dag.convergence.condition_template` + `convergence.timeout_action`（`graceful_stop` / `hard_stop` / `mark_partial_success`）
- 与 P9 协同：watcher 在 ready 计算时检查 80% budget 阈值，触发 backpressure 通知 agent 收敛
- 与 P12 协同：swarm verifier spawn 也吃 growth budget
- 依赖：P0 / P1 / P2 + P9 backpressure（千 node × 动态增长可能 N=10000）

### P12 蜂群涌现仲裁（Swarm Arbiter）

承接「LLM 裁决（蜂群涌现）」（用户 2026-04-25 提出）。**是 P8 单 arbiter 的 ensemble 升级**。

- N 个 LLM 实例并行出 verdict → consensus 聚合算法（`majority` / `unanimous` / `weighted`）
- dissent_action（分歧处理）：`verdict_lost` / `escalate_judge`（升级到 P8 opt-in B）/ `repair_with_dissent_summary`
- 第 7 个 actor `dagSwarmArbiterActor`，复用 P8 前置的 `cmd/mcp-orch/orchestration/llm/light/*` 调用层
- 与 P8 兼容：`verify.arbiter_swarm.members` 长度=1 退化为 P8 单 arbiter；≥2 走 swarm
- 金融场景默认 `unanimous + dissent_action=verdict_lost`（保守）
- 审计：`dag_swarm_consensus` 表 + `dag_arbiter_calls.swarm_round_id` 列
- 依赖：P8 已合入（含轻量 LLM 调用层）+ P9 全局 token bucket（N 倍 LLM 调用必须在 quota 内）

### P13 JSON 严格输出模式（Strict JSON Output / 金融合规）

承接「JSON 输出模式（金融场景）」（用户 2026-04-25 提出）。

- node 级 `output_schema` 声明 + `output_validation = { on_invalid: repair|fail, max_repair_rounds }`
- hook terminal 只做 bounded parse + durable enqueue；`outputValidationActor` 在写 terminal status / 进入 P8 verify 前完成 JSON schema validate，invalid 走 repair（拼 schema 错误进 repair_prompt 回 agent）或 fail
- provider structured output 适配/探测：codex JSON mode + claude tool use 只能作为优化；不支持或未验证时 fallback prompt 工程 + runtime validate，金融 hard guarantee 只来自 runtime validate + audit
- 与 P8 verifier 关系：output_validation 是**语法层**（schema 符合），verifier 是**语义层**（结果对错）；先语法后语义，两层串联
- 金融场景预设（与 P10 模板配合）：`unanimous swarm + additionalProperties=false + audit_log=true`
- 审计：`dag_output_validations` 表
- 依赖：P0 / P1 / P2 + P8 sanitize layer + P10 模板（预设场景）

### 三子任务叠加冲突缓解契约（authoritative，由 P23 P0 阶段锚定）

P7 / P8 / P9 / P11 / P12 / P13 六者**不能各自独立设计**，必须共享下列由 P23 阶段 0 锚定的冲突缓解契约（P10 是 UI / 模板能力，不参与运行时冲突，但 schema 上必须能表达 P7–P13 全部新字段）：

1. 活性 actor 与 verifier gate 共用 CAS fence，禁止双推进同一 node（P7 + P8）
2. 活性扫描必须分片 + lease jitter，不允许 N=1000 形成 DB 周期性尖峰（P7 + P9）
3. verifier launch 共用 launcher 全局 quota，不另起独立 quota（P8 + P9）
4. P8 方案 C arbiter 调用必须有 batch 聚合 + 失败终态 `verdict_lost`（**不**自动降级 B），避免千次 LLM 调用打爆 LLM 服务且保持"降级 B 必须显式 opt-in"语义（P8 + P9）
5. P7 / P8 / P9 / P10 / P11 / P12 / P13 任何 PR 不允许修改 P23 主 `status` 枚举；新增状态必须走独立列（如 `verify_phase` / `activity_state` / `growth_phase`），保持 P23 CAS 形状不变（注：P8 加 `verdict_lost` 是 status 枚举扩展，由 P8 在 `0067_dag_verify_phase.sql` 内合规扩 CHECK 约束；P11 不加 status 枚举，只加 DAG 级 `growth_capped` 标志；P12 不引入新主枚举；P13 使用独立 `output_validation_phase`，invalid 不进入 P8 `verify_phase`，且不扩主 `status` 枚举）
6. P11 spawn 入口必须经 `SpawnChildNodes` 服务函数 + growth_budget 硬约束；不允许任何代码绕路直接 INSERT `task_dag_nodes`。archtest 守。
7. P13 output_schema 验证发生在 P8 verify gate **之前**（语法层先于语义层）；invalid 直接走 repair / fail，**不**进 verify_phase 流程。
8. P12 swarm 调用必须共用 P9 全局 token bucket（与 P8 单 arbiter 同一通道），不另起独立 LLM quota；archtest 守。

## P23 与其它计划的关系

- **P21 P1b（Cron）**：`P5_CronTriggerSurface.md` 的 owner 必须先确认 p21 P1b 已合入（cron tick / lease / non-interactive / submit window 状态机），再开工；否则 cron 触发 DAG 的 idempotency 与 lease 续租会走错抽象层。
- **P22（runtime ownership）**：本期所有 4 个 DAG actor 必须按 P22 `[4]` runtime owner 门 收口；不允许在 P22 守卫之外另开例外。
- **P21 Observation Contract**：P2（节点终态回写）订阅 hook consumer 必须遵守 P21 §"Canonical Turn Observation Contract"（terminal precedence、token snapshot、call_id→turn_id 映射），不再为 DAG 单独发明一套 turn 归因。

**总计**：P23 在 P22 守卫已落地的前提下约 8-12 工程日（runtime 主链 5-7 日 + 三触发源 3-5 日，假设 cron 触发因 p21 P1b 已就绪而成本可控）。日历时间按 2-3 周准备，因为阶段 0 的 state machine 与 RunnerModule 角色冻结需要跨 P0/P1/P2 owner 共同 review，不能并行开工。

## 子计划清单（stub 已建，待 owner 补实现细节）

| 文件 | 主题 | 状态 |
|---|---|---|
| `P0_DAGRuntimeSkeleton.md` | watcher / ready 计算 / `pending→running` CAS / 4 actor 骨架 | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P1_WakeupDispatcherAndLaunchBinding.md` | wakeup 消费 / launcher 调用 / `assigned_agent_id` 绑定 | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P2_NodeTerminalReconcile.md` | hook consumer tap / `CompleteNode` 回写 / timeout-retry-on_failure 执行 | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P3_ExplicitDAGStartAndOwnership.md` | `dag/start` RPC / owner-tenant 字段 / trigger 枚举落地 | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P4_HostAgentTriggerSurface.md` | `auto_start: true` / agent 收 DAG terminal hook | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P5_CronTriggerSurface.md` | `cron_job.target_dag_key` / cron→dag bridge actor | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P6_ExternalRPCTriggerSurface.md` | AuthN middleware / tenant 鉴权 / rate-limit / audit-log | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P7_LivenessProbe.md` | dagActivityActor / `last_activity_at` 消费 / 同 agent vs 换 agent relaunch / 长工具反误杀 | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P8_VerificationGate.md` | verify 子状态机 / `nodes[].verify` schema / sibling group / runtime-embedded LLM arbiter / judge 显式 opt-in / `verdict_lost` | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P9_ScaleScheduling.md` | 批量 create / partial index / 全局 token bucket / hook worker pool / wakeup TTL / SLO + backpressure | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P10_TemplateAndUI.md` | `dag_templates` 表 / `dag/template/*` + `dag/instantiate` + `dag/edit_node` RPC / UI 模板库 / 任务列表 / 编辑器 / UX 原则 + 三用户故事 | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P11_DynamicNodeGrowth.md` | `task_spawn_child` 工具 / `growth_budget` 硬约束 / `convergence` 终止条件 / 动态生长 DAG | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P12_SwarmArbiter.md` | 第 7 actor `dagSwarmArbiterActor` / 多 LLM 并行 / consensus / dissent / `dag_swarm_consensus` 表 | 🔄 stub 已建 / 待 owner 补实现细节 |
| `P13_StrictJSONOutput.md` | `output_schema` / validator / repair-or-fail / provider 强制结构化（codex JSON mode / claude tool use）/ 金融场景预设 | 🔄 stub 已建 / 待 owner 补实现细节 |

> README 提供入口级派工与阻塞说明；具体每个子计划的 DDL、SQL 形状、actor 状态机表、必测项必须在各自子计划文件里展开。本文不替代子计划。
> P0–P6 是 P23 前段「自驱底座 + 三触发源」；P7–P13 是 P23 后段「能力进阶」；前段合入后才开后段。
