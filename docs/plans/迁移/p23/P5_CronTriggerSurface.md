# P23.5: Cron 触发面

> 创建时间：2026-04-25 | 状态：**未开动 - 依赖 P3 + p21 P1b**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

复用 `internal/module/cron` 已有 scheduler / lease 模块；在 cron job schema 上加 `target_dag_key` 字段；cron tick → `StartDAG(ctx, dagKey, triggerMeta{trigger_source=cron})` 桥接 actor（原表述写 `trigger=external`，`schedule.trigger=cron` 对应运行时 `trigger_source=cron`；`external` 仅用于 P6 外部 RPC 触发）。p21 P1b（cron 模块本身的 runner / lease / non-interactive 契约）是硬前置。

## 现状校准（事实层）

- cron 模块当前实现：`internal/module/cron/scheduler.go:26-45,65-87`、`internal/module/cron/tick_actor.go:12-18,41-60`、`internal/module/cron/schedule.go:12-24`
- cron 当前触发对象：turn / thread，**不**触发 DAG
- cron lease：`internal/module/cron/lease_actor.go:12-16`（活 turn 续租先例）
- DAG `schedule.trigger` 当前是自由 string：`internal/sidecar/orch/tools/task_tools.go:118-124,246-250`

## 推荐架构

> 待 owner 在开工前补全；约束以 [`README.md`](README.md) §"当前基线约束" / §"默认值安全原则" / §"收口口径" 为准。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| cron schema | `0067_dag_trigger_cron.sql` | `cron_jobs` 增 `target_dag_key` 以及 authorized `owner_id/tenant_id/scope`（或 FK 到带授权的 owner row）；`target_dag_trigger_meta` 不可信 |
| cron contract | `internal/module/cron/contract.go`（扩展） | `TriggerSink.Trigger(ctx, target, meta)`；meta 只携带 cron tick facts，不携带授权 owner 来源 |
| cron scheduler | `internal/module/cron/scheduler.go`（扩展） | `target_dag_key!=''` 时生成 deterministic cron tick key，填充 authenticated cron caller context 后经 sink 触发 |
| bridge | platform bus/RPC sink 或 `cmd/mcp-orch` 本地 cron runtime | 避免 `internal/module/cron` 与 `cmd/mcp-orch` concrete import；最终仍调用 P3 `StartDAG` |
| idempotency | `internal/sidecar/orch/store/dagstart/*.go`（复用 P3） + `cron_job_runs` index | cron tick 写 `dag_start_requests(trigger_source='cron', trigger_instance_key=hash(job_id, scheduled_at_utc, target_dag_key))` |

**已知关键改动方向**：

> ⚠️ **跨 root 边界硬约束**：`internal/module/cron` **不能** import `internal/sidecar/orch/orchestration`（archtest `internal/archtest/dependency_direction_mcp_orch_test.go:49-53` 拦截）。当前 cron scheduler 直接调 `StartTurn`（`internal/module/cron/scheduler.go:288-305`）；扩展为 cron→DAG 必须经接口注入，不能直接函数调用。

- `0067_dag_trigger_cron.sql`（**编号从已占用旧口径改为 0067**，必须晚于 P3 的 `0066_dag_owner_tenant.sql`）：cron job 表加 `target_dag_key` + `target_dag_trigger_meta JSONB` + `owner_id/tenant_id/scope`（或从受权 cron job owner row/FK 派生）；`target_dag_trigger_meta` 只能做 trigger 参数，不能作为 owner/tenant/AuthZ 来源
- **接口设计**：`internal/module/cron/contract.go` 定义 `TriggerSink` interface（`Trigger(ctx, target, meta) error`），cron scheduler 在 `target_dag_key != ''` 时调 `TriggerSink.Trigger(...)`，**不**直接调 DAG Start
- **bridge 装配**：不得形成 `cmd/mcp-orch → internal/module/cron` concrete import；二选一：host/core 侧经 platform bus/RPC sink 通知 mcp-orch，或在 `cmd/mcp-orch` 本地化 cron trigger runtime 并只复用 platform-level interface。`TriggerSink` 只能作为抽象边界，不允许双向 concrete import
- **保持双轨**：`target_dag_key=''` 走旧 cron→turn 路径不变（`internal/module/cron/scheduler.go:288-305`）；`target_dag_key != ''` 走 DAG bridge
- **deterministic idempotency key**：当前 cron run idempotency 是 UUID（`internal/module/cron/scheduler.go:318-326`），DAG 路径必须改为 `hash(cron_job_id, scheduled_at_utc, target_dag_key)`；同时写入 P3 统一 `dag_start_requests(trigger_source='cron', trigger_instance_key=hash(...), params_hash=...)`。该 deterministic key 只覆盖同一 cron tick；external/host/manual 触发使用各自 `trigger_source` scope，不互相去重。v1 不做跨源 active-run 互斥；若业务要求“任意来源同一时刻只允许一个 active run”，必须另加 optional DAG active-run lease/CAS（`tenant_id, dag_key` scoped），不能复用 cron tick key 假装互斥。`cron_job_runs` 表加 partial unique index `UNIQUE (job_id, scheduled_at, target_dag_key) WHERE target_dag_key <> ''`；热表已有数据时用 `CREATE UNIQUE INDEX CONCURRENTLY` 单独 no-transaction migration
- **不需要新 actor**：复用 cron module 已有的 `tick_actor` + `lease_actor`（`internal/module/cron/module.go:31-32`），只在 tick 时分流

## DDL / SQL

**0067_dag_trigger_cron.sql** 草案：

```sql
ALTER TABLE public.cron_jobs ADD COLUMN target_dag_key TEXT NOT NULL DEFAULT '';
ALTER TABLE public.cron_jobs ADD COLUMN target_dag_trigger_meta JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE public.cron_jobs ADD COLUMN owner_id TEXT NOT NULL DEFAULT '';
ALTER TABLE public.cron_jobs ADD COLUMN tenant_id TEXT NOT NULL DEFAULT '';
ALTER TABLE public.cron_jobs ADD COLUMN scope TEXT NOT NULL DEFAULT 'private';
-- owner_id/tenant_id/scope 可由既有 authorized owner row 回填；StartDAG caller_identity 必须从这些受权列派生，不读 target_dag_trigger_meta。
ALTER TABLE public.cron_job_runs ADD COLUMN target_dag_key TEXT NOT NULL DEFAULT '';

```

**0067_dag_trigger_cron_index_no_tx.sql**（no-transaction，禁止 `BEGIN/COMMIT`）：

```sql
-- deterministic idempotency：同一 cron_job 在同一时点不能重复触发同一 DAG
CREATE UNIQUE INDEX CONCURRENTLY uq_cron_run_dag_trigger
    ON public.cron_job_runs (job_id, scheduled_at, target_dag_key)
    WHERE target_dag_key <> '';
```

> 注：`cron_job_runs.target_dag_key` 必须在唯一索引前新增；若 P21 P1b 后续已加列，本 migration 需用幂等 DDL 或拆迁移避免重复。

## 依赖

- p21 P1b 已合入（cron 模块本身 runner / lease / submit-window 状态机）
- P3 已合入（`internal/sidecar/orch/orchestration/dag_start.go:StartDAG` 共享入口）

## 风险

- cron tick 失败重 claim 重触发 DAG：必须 idempotency 去重
- cron job target DAG 不存在 / 已 archive：返 `ErrTargetDAGNotFound` 并标 cron job 失败而非创建空 DAG
- cron schema 改动不能破坏现有 cron→turn 路径（双轨：`target_dag_key=''` 走旧 turn 路径）
- **跨 root archtest**：`internal/archtest/dependency_direction_mcp_orch_test.go:49-53` 拦截 `internal/module/cron` import `cmd/mcp-orch`；P5 必须经 `TriggerSink` interface，不能绕路
- **新增 archtest** `cron_dag_bridge_no_concrete_orch_import`：禁止 `internal/module/cron/*.go` 出现 `cmd/mcp-orch/` import，也禁止 `cmd/mcp-orch` 直接 import `internal/module/cron` concrete；桥接只能经登记 interface/platform sink

## 必测项

- cron tick 触发 DAG start
- 同一 cron tick 重 claim 不双跑；cron 与 external 已分别触发时，默认允许跨源各自 start，除非 optional active-run lease 显式启用
- cron job target DAG 不存在 → 失败计数 + 不创空 DAG
- 旧 cron→turn 路径不受影响

## 输入材料

- README §阶段 0 ① 编号校准（`0067_dag_trigger_cron.sql`）
- README §"风险" "cron 双触发" 条
- p21 P1b 完整章节（必读，特别是 lease 与 idempotency 设计）

## 待办

- 含 a9 调研建议：`target_dag_key=''` 双轨窗口不能无限期，P5 owner 需明确废弃窗口（1 个 release 后评估转 hard fail）。
- DST / 时区漂移（a8）：`scheduled_at` 必须 UTC 落库，idempotency hash 以 UTC 为准，避免 DST 同一本地时点重触发。
- 跨 a4/a5 共识：`cron_job_runs` 也走 append-only；bridge 在调 `StartDAG` 前必须带 `tenant_id`（从 cron job 继承）。


