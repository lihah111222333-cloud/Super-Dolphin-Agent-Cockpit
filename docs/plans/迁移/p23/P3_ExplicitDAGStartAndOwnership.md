# P23.3: DAG 显式 start + owner/tenant 落地

> 创建时间：2026-04-25 | 状态：**未开动 - 依赖 P0**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

新增 `dag/start` RPC method（前置：trigger 枚举 `manual` 必须显式调）；为 DAG / node 加 `owner_id / tenant_id / scope` 字段并迁移现有 `created_by`。所有触发源（P4 / P5 / P6）汇流到 **`cmd/mcp-orch/orchestration/dag_start.go` 的 `StartDAG(ctx, dagKey, triggerMeta)`**（DAG runtime 归 mcp-orch，**不**在 `internal/orchestration/` 也**不**在 `internal/`），禁止绕过。

## 现状校准（事实层）

- 当前 DAG 创建即终点：`cmd/mcp-orch/orchestration/dag.go:13,109-131,196`（无 `StartDAG` / `TriggerDAG`）
- `task_start_node` 计划单中有但未实现：`docs/plans/迁移/v3-migration-plan.md:1312-1322`
- 当前归属字段只有 `created_by`：`migrations/0004_ack_dag.sql:33-67`、`cmd/mcp-orch/store/taskdag/contract.go:160-181`
- MCP server 无调用者身份检查：`internal/mcpserver/common/server.go:221-264`
- `agent_id` 仅映射为 `CreatedBy`：`cmd/mcp-orch/tools/task_tools.go:147-172`

## 推荐架构

> 待 owner 在开工前补全；约束以 [`README.md`](README.md) §"当前基线约束" / §"默认值安全原则" / §"收口口径" 为准。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| _待补_ | _待补_ | _待补_ |

**已知关键改动方向**：
- 新增 `dag/start` RPC method（在 `cmd/mcp-orch/orchestration/rpc.go` 注册）→ 调 `StartDAG`
- 共享入口 `cmd/mcp-orch/orchestration/dag_start.go` 的 `StartDAG(ctx, dagKey, triggerMeta)`：所有触发源汇流，包含鉴权 + idempotency；**新建文件不膨胀 `dag.go`**
- `0065_dag_owner_tenant.sql`：加 `owner_id / tenant_id / scope`，迁移 `created_by`
- `trigger_meta.caller_identity` 写进 `dag.last_trigger_at` + `dag.last_trigger_by`
- trigger 枚举落地：`schedule.trigger ∈ {manual, auto, cron, external}` schema 强校验

## DDL / SQL

**0065_dag_owner_tenant.sql** 草案：
- `task_dag` 加 `owner_id TEXT NOT NULL DEFAULT ''`、`tenant_id TEXT NOT NULL DEFAULT ''`、`scope TEXT NOT NULL DEFAULT ''`
- 历史 `created_by` 同步迁移到 `owner_id`（兼容期保留 `created_by` 列）
- `task_dag` 加 `last_trigger_at TIMESTAMPTZ`、`last_trigger_by TEXT NOT NULL DEFAULT ''`

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


