# P23.9: 大规模 DAG 调度（Scale）

> 创建时间：2026-04-25 | 状态：**未开动（后段子任务，依赖 P0/P1/P2 + P22 archtest）**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

支持「自动迭代一个系统、千 node、百 agent、上千步工具调用」级别的 DAG。批量 create / partial index / 全局 token bucket / hook worker pool / TTL 归档 / SLO + backpressure。**后段子任务**。

## 现状校准（事实层）

**7 大规模化瓶颈**（`gap-scale` 报告，[`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §3）：

| 瓶颈点 | N=1000 影响 | file:line | 建议方向 |
|---|---|---|---|
| DAG 创建 | 单事务内 1000 次 `UpsertNode` + 全量 load，O(N) 串行 | `internal/sidecar/orch/orchestration/dag.go:109-126,202-208,211-220`、`internal/sidecar/orch/sql/queries/task_dag_node_write.sql:1-12` | 拆批 / async / streaming |
| ready 计算 | JSONB `depends_on` 扫描，最坏 O(N²) | `migrations/0004_ack_dag.sql:58-62,70-71` | partial index `(dag_key, id) WHERE status='pending'` + 依赖计数列 |
| wakeup 表 | 5000 行/DAG，多 DAG 后 M 快速膨胀，无 GC | `migrations/0023_dag_watcher_phase1.sql:9-36`、`internal/sidecar/orch/sql/queries/task_dag_wakeup_query.sql:1-16` | TTL / DAG archive / 分区 |
| launcher | 当前无固定并发上限，百 agent 会直接并发打到下游 | `internal/sidecar/orch/orchestration/service_launcher_bridge.go` | 若未来需要容量治理，必须显式设计全局 token bucket / provider quota，而不是恢复硬编码上限 |
| hook 风暴 | `OnTurnCompleted` 同步 dispatch | `internal/sidecar/orch/orchestration/hook_consumer.go:105-116,260-275,285-294` | non-blocking enqueue + worker pool + bounded queue |
| 状态存储 | `result jsonb` 承载 verifier/tool log，行膨胀 | `migrations/0004_ack_dag.sql:62`、`internal/sidecar/orch/sql/queries/task_dag_node_read.sql:1-18` | result 只存摘要，详细日志 spillover |
| 全量锁读 | `GetNodesForUpdate` 锁整 DAG | `internal/sidecar/orch/sql/queries/task_dag_node_read.sql:13-18`、`internal/sidecar/orch/store/taskdag/store.go:100-103` | claim 小批 ready，`SKIP LOCKED` |

## 推荐架构

P9 提供统一容量控制层：ready claim 小批 `SKIP LOCKED`、bounded queues、global/tenant/DAG/workload token bucket、budget ledger、promhttp 指标与 scheduled-audit fallback。P7/P11/P12/P13 依赖的 backpressure 信号必须先可观测：若 promhttp 未启用，至少以定时 audit/health row 输出 queue depth、lag、DB p99、budget usage，供自动 relaunch/spawn/swarm/validation gate fail-safe。

`schedule.queue_policy` 冻结 enum：`fifo`、`weighted_fair`、`tenant_fair`、`dag_fair`、`priority_aging`。映射规则：`fifo` 仅单 tenant/dev；生产默认 `weighted_fair`；`tenant_fair` 先 tenant 再 DAG round-robin；`dag_fair` 限制单 DAG max share；`priority_aging` 在优先级基础上随等待时间提升，防饥饿。未知值 schema fail，不允许 silently fallback。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| ready claim SQL | `internal/sidecar/orch/sql/queries/task_dag_node_read.sql` / runtime queries | `FOR UPDATE SKIP LOCKED LIMIT K`；禁止整 DAG 锁读 |
| scheduler fairness | `internal/sidecar/orch/orchestration/runtime/scheduler_queue.go` [NEW] | `queue_policy` enum 映射 weighted fair / tenant fair / DAG share / priority aging |
| token bucket | `internal/sidecar/orch/orchestration/runtime/token_bucket.go` [NEW] | global/tenant/DAG/workload 四级 min；verifier/swarm/launcher 共用 |
| budget ledger | `internal/sidecar/orch/store/dagbudget/*.go` [NEW] | launch/spawn/swarm/repair/storage spend reservation、refund、hard stop |
| hook queues | `internal/sidecar/orch/orchestration/hook_consumer.go`（扩展） | durable terminal/reconcile/validation；progress coalesce/drop with metric |
| metrics | `internal/sidecar/orch/orchestration/runtime/metrics.go` [NEW] + promhttp wiring | 输出 lag/depth/p99/budget；promhttp 不可用时 scheduled-audit fallback |
| archive/spillover | `internal/sidecar/orch/store/taskdag/*.go` + archive migrations | result summary cap、blob ref、TTL/archive union pagination |
| archtest | `internal/archtest/dag_scale_scheduling_test.go` [NEW] | 禁止无界 channel、整 DAG lock、未知 queue_policy fallback |

**已知关键改动方向**：
- `task_create_dag` N>50 时拆批 / async / streaming
- P0 `0065_dag_state_machine.sql` 已前移 `remaining_deps` 或等价依赖计数列；P9 只补 `WHERE status='pending'` partial index、回填校验与归档/预算调度
- launcher 配置化（P23 阶段 0 ⑤ 已预留 hook）→ 全局 token bucket（**verifier launch 共用同一 quota，不另起通道**，README §三子任务叠加冲突缓解契约 第 3 条）
- hook consumer worker pool：bounded queue + lag 指标；terminal/reconcile/validation 用 durable outbox，progress/delta 才允许 coalesce/drop
- wakeup / lease / archive：DAG 终态后 TTL 归档；node `result` 只存摘要，详细日志 spillover 外部 storage
- SLO 指标：`dag_node_per_second` / `dag_launcher_queue_lag_seconds` / `dag_hook_consumer_lag_seconds` / `dag_wakeup_age_seconds`（p99 由 PromQL 计算）
- backpressure：watcher / dispatcher 按 launcher / hook lag 退避，不再开环

## DDL / SQL

- `remaining_deps INTEGER NOT NULL DEFAULT 0` 已裁决前移到 P0 `0065_dag_state_machine.sql`；P9 不再占用事务 migration 做该列，避免与 `CREATE INDEX CONCURRENTLY` 编号冲突
- 加 partial index（单独 PR）：`CREATE INDEX CONCURRENTLY idx_task_dag_nodes_pending ON task_dag_nodes (dag_key, remaining_deps, id) WHERE status='pending'`；该 migration 文件不得包 `BEGIN/COMMIT`，需与回填列事务 migration 拆开
- 补 tenant/fence 复合索引：`(tenant_id, dag_key, status, remaining_deps)` 与 `(dag_key, node_key, active_turn_id)`，抵消 a4/a5 filter + turn fence 热点
- 新增归档表 `task_dags_archive` / `task_dag_nodes_archive`（或按 DAG 分区），并定义 DAG→node/wakeup/arbiter/output_validation 归档级联校验。所有跨表关联必须采用 FK；archive 分区无法真实 FK 时，必须采用 archive-safe logical FK + postcheck SQL + TTL/retention owner。

## 依赖

- P0 / P1 / P2 全部合入
- P22 archtest 守卫已就位
- 容量模型 + SLO 数字已敲定（在本子任务 owner 启动前必须先有数字）

## 风险

- 批量 create 长事务 vs 拆批 partial failure：必须有 cleanup 路径
- partial index 维护成本：写入路径加列必须更新索引 hint
- 全局 token bucket 失效（如 leader election failure）：必须降级回固定并发，不能 0 并发卡死
- hook consumer worker pool 队列溢出：drop policy 必须显式（不允许默认丢弃 silent）
- archive 表与活表的查询路径分离：UI / RPC 必须 union 两表
- 与 P7 `last_activity_at` 全表扫共振：分片 + jitter 必须协同设计
- **N=10000 击穿三大瓶颈（a3 调研）**：
  - DB ready/lock 路径全量读 + 整 DAG `FOR UPDATE`：`task_dag_node_read.sql:13-18` 必须改为 `FOR UPDATE SKIP LOCKED LIMIT K`（K 默认 64）。
  - hook/launcher 背压缺失：`hook_consumer.go:105-116` 当前 同步 dispatch，必须换 bounded queue + capacity drop 信号。
  - LLM verdict 开环（P8/P12 无 batch / token bucket）：3000 × swarm 并发击穿 provider RPM；P9 必提供统一 token bucket、budget ledger 与 batch 聚合点。
- a10 成本：N=10000 × swarm fanout 会走到 \$1M+/月；P9 owner 必须推 token bucket、spend/budget ledger、subscription level 映射，并提供 `dag/cost_preview` dry-run API 为 P10/P11/P12/P13 提供硬闸。
## 必测项

- N=1000 创建吞吐（拆批前 vs 拆批后对比）
- partial index 命中率（pending 扫描 latency p99）
- 全局 token bucket 限流准确性（verifier + 普通 launch 共享同一 quota）
- hook worker pool drop / backpressure
- TTL 归档 + UI 查询 union
- SLO 指标全部产出

## 输入材料

- README §"P9 大规模 DAG 调度（Scale）"
- `gap-scale` 报告（[`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §3）
- README §三子任务叠加冲突缓解契约 第 2 / 3 条

## 待办

- **SQL `FOR UPDATE SKIP LOCKED LIMIT K` ownership**（a3/a10 仲裁）：首版 ready claim 由 P0/P1 落地；P9 只调 K、索引、fairness 与容量策略。若 P0/P1 未改 `store/taskdag/store.go:100-103` + `task_dag_node_read.sql:13-18`，P9 必须阻塞，不能重复实现一套 claim。
- **result 拆摘要 + spillover**（a3+a10）：`result jsonb` 只留摘要（隐藏 max：N 字节），verifier/tool log 走外部 storage；DAG detail/list 必须 cursor 分页，默认 `include_result=false`，与 N=10000 UI payload 安全联动。
- **actor buffer 容量化**（a3）：hook consumer / arbiter / swarm enqueue 通道必设上限；容量公式、shutdown drain 顺序、drop policy（默认不得 silent drop）都有明示 metric。
- **CONCURRENTLY**（a9）：partial index DDL 已提升为改动清单要求，必须由单独 no-transaction migration 走 `CREATE INDEX CONCURRENTLY`，与回填列 PR 拆两步；archtest 禁该 migration 包 `BEGIN/COMMIT`，避免大表写阻塞。
- **token bucket 与 subscription 映射表**（a8+a10）：P9 输出一份 `subscription_id → RPM/TPM/concurrency` 映射，为 P12 swarm fanout / P11 growth 预留额度。
- **dag_terminal_turn_fence 已前移 P2（a2+a5 仲裁）**：P9 只优化索引/热点，不再承担 terminal fence 首次落地；若发现 P2 未实现 active_turn_id check，P9 必须阻塞。

## 容量与公平调度硬契约（需求补全仲裁）

| 项 | v1 默认 / 要求 |
|---|---|
| ready claim K | 默认 64，可按 DB p99 调整；禁止整 DAG `FOR UPDATE` |
| create batch size | N>50 走 async job；单批默认 100 nodes，先全量拓扑校验再分批写 |
| hook queue | terminal/reconcile/validation durable；progress/delta 可 coalesce，队列满触发 watcher/dispatcher backpressure |
| launcher in-flight | 按 global、tenant、DAG、workload 四级取 min；verifier/swarm 有 max share |
| fairness | per-tenant weighted fair queue；per-DAG max share；aging 防饥饿；budget exhausted hard stop |
| shutdown | 引用 COMPLIANCE drain protocol；deadline 前不丢 terminal，progress drop 必打 metric |

### queue_policy enum / fairness mapping

`queue_policy` 只允许：`fifo`、`weighted_fair`、`tenant_fair`、`dag_fair`、`priority_aging`。schema / RPC / template UI 三处必须同一 enum。默认映射：dev/single-tenant 可用 `fifo`；multi-tenant 默认 `weighted_fair`（tenant weight → DAG max share → workload share）；`tenant_fair` 保证 tenant round-robin；`dag_fair` 防单 DAG 占满 launcher；`priority_aging` 计算 `effective_priority = base_priority + floor(wait_seconds/aging_step_sec)` 并受 budget hard stop 约束。fairness 决策必须暴露 redacted audit：chosen_queue、skipped_reason、budget_state、token_bucket_state_hash。

### metrics / audit fallback 前置要求

全局 verify phase 命名采用 P8 `awaiting_verify / verifying / repairing`；P9 backpressure/metrics 不得引入旧 verify 前态别名。P7/P11/P12/P13 依赖 P9 指标做安全决策。P9 owner 必须先接 `promhttp` 指标；若 deployment 禁用 promhttp，则启用 scheduled-audit fallback，周期写 `dag_scheduler_health`（或等价）包含 hook lag、launcher lag、ready depth、DB p99、budget usage、last_sample_at。两者都缺失时，自动 relaunch、dynamic spawn、swarm fanout、strict JSON repair fanout 必须 feature-gate off。

### batch create API

`task_create_dag` N>50 返回 async create job id；请求带 idempotency key，params hash 冲突返回 conflict。拓扑校验必须先于写入；partial failure 走 cleanup/roll-forward；progress 用 cursor 查询，禁止在一个事务中包外部 launch。

### spillover / archive / pagination

`result` 活表只保 summary + blob ref；blob key 包含 tenant/dag/node/turn/hash，写入前 redaction，TTL/GC 与 archive 级联；owner 必须提供 orphan postcheck SQL 与 retention owner。DAG detail 默认 `include_result=false`、字段投影、cursor pagination；archive union 必须定义跨表排序 cursor，默认查活表，用户显式 include_archive 才 union。

## Backpressure / Shutdown Action Matrix（第三轮仲裁必修）

| 信号 | 阈值 / 默认 | 影响 actor | 动作 | 恢复 | metric |
|---|---|---|---|---|---|
| hook lag | p99 > 5s 或 durable queue > 80% | watcher/dispatcher/outputValidation/dagActivityActor | 暂停新 launch，terminal durable 优先，progress 可 coalesce；P7 只延后 `next_probe_at`，不 relaunch/不写终态 | p99 连续 3 窗口恢复 | `dag_hook_consumer_lag_seconds` |
| launcher lag | p99 > 30s 或 depth > quota×3 | dispatcher/arbiter/swarm/dagActivityActor | 降低 claim K，verifier/swarm 限 share；P7 只延后 `next_probe_at` | lag < 10s | `dag_launcher_queue_lag_seconds` |
| DB p99 | ready claim/update p99 > 200ms | watcher/reconcile | K 减半 + jitter 加倍 | p99 < 100ms | `dag_db_query_duration_seconds` |
| ready queue depth | per-tenant ready > quota×10 | watcher | per-tenant fair queue 限流 | depth < quota×5 | `dag_ready_queue_depth` |
| growth budget | 80% / 100% | watcher/spawn | 80% 返 `ErrApproachingBudget`；100% hard stop | owner 调高预算并 audit | `dag_spawn_budget_usage_ratio` |
| SIGTERM | 收到信号 | 全 actor | stop new claim/start，drain durable terminal/LLM/audit 到 deadline | 重启后 outbox replay | `dag_actor_drain_duration_seconds` |

容量默认值：hook queue terminal durable、progress queue 默认 10k/coalesce；terminal/reconcile/validation durable outbox 默认 retention 7–14 天，replay batch size 默认 100，磁盘/表水位 80% 触发 backpressure，poison message 隔离到 dead-letter，去重键至少包含 `(dag_key,node_key,turn_id,event_type)`；launcher verifier+swarm share 默认 ≤30%；node result summary cap 默认 8KiB；detail pagination 默认 page_size=100/max=500；archive TTL hot 7–14 天；shutdown drain deadline 默认 30s，超时只允许丢 progress，不丢 terminal/audit/validation。LLM token bucket owner PR 必填 RPM/TPM/concurrency 扣减顺序、reservation TTL、失败 refund、budget exhausted terminal/blocked 行为。v1 hard target=N=10000；100k 规模为非目标，需 partition/sharding/archive-only 查询另案。
