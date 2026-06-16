# P23.3: DAG 显式 start + owner/tenant 落地

> 创建时间：2026-04-25 | 状态：**未开动 - 依赖 P0**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

新增 `dag/start` RPC method（前置：trigger 枚举 `manual` 必须显式调）；为 DAG / node 加 `owner_id / tenant_id / scope` 字段并迁移现有 `created_by`。所有触发源（P4 / P5 / P6）汇流到 **`internal/sidecar/orch/orchestration/dag_start.go` 的 `StartDAG(ctx, dagKey, triggerMeta)`**（DAG runtime 归 mcp-orch，**不**在 `internal/orchestration/` 也**不**在 `internal/`），禁止绕过。

## 现状校准（事实层）

- 当前 DAG 创建即终点：`internal/sidecar/orch/orchestration/dag.go:13,109-131,196`（无 `StartDAG` / `TriggerDAG`）
- `task_start_node` 计划单中有但未实现：`docs/plans/迁移/v3-migration-plan.md:1312-1322`
- 当前归属字段只有 `created_by`：`migrations/0004_ack_dag.sql:33-67`、`internal/sidecar/orch/store/taskdag/contract.go:160-181`
- MCP server 无调用者身份检查：`internal/mcpserver/common/server.go:221-264`
- `agent_id` 仅映射为 `CreatedBy`：`internal/sidecar/orch/tools/task_tools.go:147-172`

## 推荐架构

所有触发源只允许进入 `internal/sidecar/orch/orchestration/dag_start.go:StartDAG(ctx, dagKey, triggerMeta)`。`triggerMeta.caller_identity` 必须由 authenticated context 注入（P6 transport adapter / P4 MCP context / P5 cron job owner row），不得从 client-supplied `agent_id`、display name、`created_by` 或 `target_dag_trigger_meta` 推导。`agent_id` 参数只作为 legacy/display/assigned-agent hint，可用于审计展示或兼容 `CreatedBy` 文案，**不可**作为 owner/tenant/AuthZ 来源。

`StartDAG` 首步读取 `CallerIdentityFromContext(ctx)`；缺失、未认证、tenant 未授权或 scope 不匹配时 hard fail `ErrCallerIdentityRequired` / `ErrTenantUnauthorized`，并写失败 audit（若 audit 主表不可写则按 P6 fail-closed/spool）。鉴权通过后再执行 idempotency、状态 CAS、wakeup enqueue。

Start idempotency 使用统一表 `dag_start_requests`（或名称等价但字段/约束等价）：唯一 scope 为 `(tenant_id, dag_key, trigger_source, trigger_instance_key)`；同 key 同 `params_hash` 返回已有结果，不同 hash 返回 conflict。`schedule.trigger ∈ {manual, auto, cron, external}`。

运行时 `trigger_source ∈ {manual, auto, cron, external, host}`；`trigger_instance_key` 由触发源确定（manual/UI request id、host tool call id、cron tick deterministic key、external idempotency key）。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| StartDAG service | `internal/sidecar/orch/orchestration/dag_start.go` [NEW] | `StartDAG(ctx, dagKey, triggerMeta)`；从 ctx 取 authenticated caller；执行 AuthZ、idempotency、CAS、wakeup enqueue、audit |
| RPC registration | `internal/sidecar/orch/orchestration/rpc.go`（扩展） | 注册 `dag/start`；handler 只解析参数并调用 StartDAG，不接受 params 内 owner/tenant 覆盖 |
| caller context | `internal/sidecar/orch/orchestration/auth_context.go` [NEW] | `CallerIdentity` / `CallerIdentityFromContext` / hard-fail helper；`agent_id` 仅 legacy/display |
| owner/tenant store | `internal/sidecar/orch/store/taskdag/*.go` + SQL queries | 读取 DAG owner/tenant/scope 并做 Start AuthZ；双写/迁移 `created_by → owner_id` |
| idempotency store | `internal/sidecar/orch/store/dagstart/*.go` [NEW] + `0066_dag_owner_tenant.sql` | `dag_start_requests` CRUD：reserve/complete/fail/conflict |
| audit | `internal/sidecar/orch/store/dagaudit/*.go` 或 P6 audit store | 记录 StartDAG 成功/失败、caller、params_hash、result_code、latency |

**已知关键改动方向**：
- 新增 `dag/start` RPC method（在 `internal/sidecar/orch/orchestration/rpc.go` 注册）→ 调 `StartDAG`
- 共享入口 `internal/sidecar/orch/orchestration/dag_start.go` 的 `StartDAG(ctx, dagKey, triggerMeta)`：所有触发源汇流，包含鉴权 + idempotency；**新建文件不膨胀 `dag.go`**
- `0066_dag_owner_tenant.sql`：加 `owner_id / tenant_id / scope`，迁移 `created_by`
- `trigger_meta.caller_identity` 写进 `dag.last_trigger_at` + `dag.last_trigger_by`
- trigger 枚举落地：`schedule.trigger ∈ {manual, auto, cron, external}` schema 强校验。
- `host` 只作为 runtime `trigger_source`，不得进入 schedule enum。

## DDL / SQL

**0066_dag_owner_tenant.sql** 草案：
- `task_dags` 加 `owner_id TEXT NOT NULL DEFAULT ''`、`tenant_id TEXT NOT NULL DEFAULT ''`、`scope TEXT NOT NULL DEFAULT ''`
- 历史 `created_by` 同步迁移到 `owner_id`（兼容期保留 `created_by` 列；不得把未来请求的 `agent_id` 当授权 owner）
- `task_dags` 加 `last_trigger_at TIMESTAMPTZ`、`last_trigger_by TEXT NOT NULL DEFAULT ''`
- 新增 `dag_start_requests`：`request_id TEXT PRIMARY KEY`、`tenant_id TEXT NOT NULL`、`dag_key TEXT NOT NULL`、`trigger_source TEXT NOT NULL CHECK (trigger_source IN ('manual','auto','cron','external','host'))`、`trigger_instance_key TEXT NOT NULL`、`params_hash TEXT NOT NULL`、`caller_id TEXT NOT NULL`、`status TEXT NOT NULL CHECK (status IN ('reserved','started','conflict','failed'))`、`result JSONB NOT NULL DEFAULT '{}'::jsonb`、`created_at/updated_at TIMESTAMPTZ`、`UNIQUE (tenant_id, dag_key, trigger_source, trigger_instance_key)`

## 依赖

- P0 已合入
- trigger 枚举冻结（README §阶段 0 ④）已写入文档

## 风险

- 旧 DAG（`trigger` 为空）需按 `auto_handoff_phase1` 兼容映射，避免静默不跑
- `created_by` 迁移到 `owner_id` 的兼容期：保留双写一段时间，避免立即破坏 UI / 旧 RPC 调用方
- 鉴权失败时返回 `jrpc2.InvalidParams` 而不是 silent ignore

## 必测项

- `dag/start` 显式调用推进
- `trigger=manual` DAG 创建后不自跑，必须显式 start
- `trigger=auto` DAG 创建即推进
- `created_by → owner_id` 兼容映射
- 缺 `caller_identity` 返 `ErrCallerIdentityRequired`

## 输入材料

- README §"触发源 → DAG start 路径（authoritative）"
- README §"默认值与硬错误（authoritative）"
- `dag-entry-audit` 报告（外部 RPC / 鉴权缺口）



## StartDAG idempotency / active-run 口径

P3 v1 采用同源去重：`dag_start_requests` 唯一键为 `(tenant_id, dag_key, trigger_source, trigger_instance_key)`；同 key 同 `params_hash` 返回已有结果，不同 hash 返回 conflict。不同来源（例如 cron 与 external）默认不跨源互斥，因此不得写“cron + external 一定不双跑”的测试。若产品需要跨源互斥，必须显式启用 optional active-run lease（`tenant_id, dag_key` scoped，带 lease TTL/owner/fence），并让 P5/P6 同时走该 lease。
