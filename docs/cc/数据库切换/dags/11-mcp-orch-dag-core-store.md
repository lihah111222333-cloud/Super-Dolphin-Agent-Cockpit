# Task 11: mcp-orch DAG 核心 Store

## Agent Prompt

你负责迁移 `cmd/mcp-orch` DAG 核心 store：DAG template、node CRUD、OCC、apply ops、workspace runs。重点是把 `FOR UPDATE` 语义改成 SQLite 事务 + 条件更新，不改变 tools/RPC 输出 shape。

## Scope

依赖：Task 10。

不可与 Task 12 并行。Task 12 依赖本任务合并后的 DAG run/node schema、store 类型和 generated sqlc；并行会同时触碰 `task_dag_dag.sql`、`task_dag_run.sql` 与 `cmd/mcp-orch/store/taskdag/**`，容易产生不可评审的冲突。

## 修改点

- Modify SQL:
  - `cmd/mcp-orch/sql/queries/task_dag_dag.sql`
  - `cmd/mcp-orch/sql/queries/task_dag_node_read.sql`
  - `cmd/mcp-orch/sql/queries/task_dag_node_write.sql`
  - `cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql`
  - `cmd/mcp-orch/sql/queries/task_dag_node_spawning_thread.sql`
  - `cmd/mcp-orch/sql/queries/workspace_run.sql`
  - `cmd/mcp-orch/sql/queries/prompt_template.sql`
  - `cmd/mcp-orch/sql/queries/command_card.sql`
  - `cmd/mcp-orch/sql/queries/shared_file.sql`
  - `cmd/mcp-orch/sql/queries/task_ack.sql`
- Modify stores:
  - `cmd/mcp-orch/store/taskdag/store.go`
  - `cmd/mcp-orch/store/taskdag/store_dag_ops.go`
  - `cmd/mcp-orch/store/taskdag/store_complete_downstream.go`
  - `cmd/mcp-orch/store/taskdag/store_fail_downstream.go`
  - `cmd/mcp-orch/store/taskdag/store_node_spawn.go`
  - `cmd/mcp-orch/store/workspace/store.go`
- Modify tests:
  - `cmd/mcp-orch/store/taskdag/*_test.go`
  - `cmd/mcp-orch/orchestration/dag*_test.go`
  - `cmd/mcp-orch/workspace/*_test.go`
  - `cmd/mcp-orch/tools/parity_v2_test.go`

## 语义要求

- Replace row-lock reads with `BEGIN IMMEDIATE` or explicit version CAS:
  - read current version.
  - apply mutation with `WHERE dag_key = ? AND version = ?`.
  - if rows affected = 0, return existing OCC conflict error.
- All `BEGIN IMMEDIATE`, CAS writes, DAG apply ops, and workspace status transitions must use the shared bounded retry helper from Task 03, or have a test proving retry is unnecessary.
- `ApplyOps` add/update/delete semantics remain unchanged:
  - Kahn cycle detection remains in `nodeexec/cycle.go`.
  - done nodes cannot have config changed.
  - empty ops still short-circuit without transaction if current behavior does.
- Workspace CAS status transitions remain exact.
- JSON result/config/metadata stay valid JSON text.
- Dashboard/list and tool list queries must use metadata-only projection. Large JSON columns such as `events`, `metadata`, `result`, `config`, and resource payloads are read only by detail-by-id queries unless the existing protocol explicitly requires them.
- Synchronized mcp-orch resource SQL (`prompt_template`, `command_card`, `shared_file`) must stay behavior-compatible with root query equivalents where `cmd/mcp-orch/sql/queries/README.md` requires synchronization.

## 不允许改

- 不要 remove OCC to avoid lock complexity.
- 不要 change MCP tool response fields.
- 不要 collapse workspace disk merge behavior into DB-only behavior.

## 验收方案

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/workspace ./cmd/mcp-orch/workspace ./cmd/mcp-orch/tools -count=1
make sqlc-verify
```

并发测试必须覆盖：

- two transactions apply ops to same DAG version -> one success, one OCC conflict.
- single running run constraint still blocks duplicate active run.
- workspace status CAS rejects stale transition.
