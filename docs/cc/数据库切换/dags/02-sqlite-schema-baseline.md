# Task 02: SQLite Baseline Schema

## Agent Prompt

你负责建立 SQLite baseline schema。目标不是逐条迁移 PostgreSQL migration，而是从当前有效 PG schema 重建 SQLite baseline：`internal/platform/db/sqlite/migrations/001_baseline.sql`。必须保留本地持久化所需表、约束、唯一索引、partial index、schema gate，并把 JSONB 改成 JSON text + `json_valid`。不要实现 store 代码。

## Scope

依赖：无。

可并行：可与 Task 01 并行；Task 03 依赖本任务。

## 修改点

- Create: `internal/platform/db/sqlite/migrations/001_baseline.sql`
  - baseline 完整性必须从源码反推，而不是信任本任务里的手写表清单。写 schema 前先从 `internal/platform/db/module.go` 的 `requiredBaselineTables`、根 `sqlc.yaml`、`cmd/mcp-orch/sqlc.yaml`、`sql/queries/**`、`cmd/mcp-orch/sql/queries/**` 反推出全部被 runtime/query 引用的表。
  - `requiredBaselineTables` 是 SQLite baseline 的最低硬下限，至少必须包含：`agent_interactions`, `agent_provider_binding`, `agent_status`, `agent_threads`, `audit_events`, `bus_exception_logs`, `command_card_runs`, `command_card_versions`, `command_cards`, `cwd_instance_locks`, `prompt_template_versions`, `prompt_versions`, `prompt_templates`, `prompts`, `shared_files`, `system_logs`, `task_acks`, `task_dag_nodes`, `task_dags`, `task_traces`, `topology_approvals`, `ui_preferences`, `workspace_run_files`, `workspace_runs`。
  - 源码追溯排除项（`sql/queries/` 中无活跃 query 文件，不进入 SQLite runtime）：`agent_codex_binding`（codex 绑定数据已合并至 `agent_provider_binding.codex_thread_id` 列，历史表无 sqlc query）；`topology_approval_archives`（无 query 文件，只存在于 PG migrations 历史中）。实现 PR 中必须列出排除原因与源码依据。
  - 手写表清单只是执行提示，不是全集。根/mcp-orch query 反推出来的额外 runtime 表也必须进入 baseline，例如 `turn_dedupe_registry`, `prompt_template_sections`, `prompt_routing_tests`, `prompt_intent_drafts`, `agent_feedback_events`, `session_insights`, `hook_pending_reviews`, `cron_jobs`, `cron_job_runs`, `task_dag_runs`, `task_dag_wakeups`, `task_dag_worker_leases`。
  - 当前 PG schema 中存在但决定不进入 SQLite runtime 的表，必须在本任务实现 PR 中列出“排除原因 + 源码依据 + 回归测试”；不得无声遗漏。
  - 覆盖主应用 store 表：`agent_threads`, `agent_provider_binding`, `agent_status`, `turn_dedupe_registry`, `ui_preferences`, `cwd_instance_locks`, `system_logs`, `audit_events`, `bus_exception_logs`, `task_traces`, `prompt_templates`, `prompt_versions`, `prompt_template_sections`, `prompt_routing_tests`, `prompt_intent_drafts`, `command_cards`, `command_card_versions`, `command_card_runs`, `shared_files`, `agent_feedback_events`, `session_insights`, `hook_pending_reviews`, `agent_interactions`, `topology_approvals`, `cron_jobs`, `cron_job_runs`。
  - 覆盖 mcp-orch 表：`task_dags`, `task_dag_nodes`, `task_dag_runs`, `task_dag_wakeups`, `task_dag_worker_leases`, `workspace_runs`, `workspace_run_files`。
  - 新增 SQLite 锁表：`runtime_locks(lock_key TEXT PRIMARY KEY, holder TEXT NOT NULL, lease_expires_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`。
  - 保留 `schema_migrations`，插入版本 `103` 或当前 `internal/platform/db.MinRequiredSchemaVersion` 对应值。
- Create: `internal/platform/db/sqlite/schema_baseline_test.go`
  - 用 SQLite 打开 baseline，验证所有表存在。
  - 读取或等价镜像 `internal/platform/db/module.go` 的 `requiredBaselineTables`，任一下限表缺失时测试失败。
  - 解析 sqlc query 引用的表；任一 query 引用的表不在 SQLite baseline 中时测试失败。
  - 验证关键索引/约束存在：
    - `agent_provider_binding` 的 `agent_id` 主键和 `(provider, provider_thread_id)` 唯一。
    - `turn_dedupe_registry` 的活跃 entry 幂等约束。
    - `cron_jobs` 的 due/claim 索引。
    - `task_dag_runs` 保持 DAG v2 F6.5 multi-run 语义：不得恢复 `uniq_task_dag_runs_one_running_per_dag`，只保留 run lookup/status/running 查询索引。
    - `prompt_template_sections` recall 字段和 lookup index。专用 `prompt_recall_topics` 锁表或等价唯一策略由 Task 09 通过 SQLite-only incremental migration 添加，不属于 Task 02 baseline 的完成条件。
  - 验证大表索引覆盖：
    - `system_logs`: level/source/agent_id/thread_id filters + `ORDER BY ts DESC, id DESC`。
    - `session_insights`: `(thread_id, created_at DESC, id DESC)`、全局 recent、observed approval/token filters。
    - `cron_job_runs`: job_id recent、dedupe_key、active status、turn_id running。
    - `task_dag_wakeups`: pending claim status/next_retry_at/id、target_agent_id sent binding、run_id/dag_key/node_key。
    - `task_dag_runs`: run_key lookup、dag_key/status/started_at/id、running partial index；验收必须证明 `uniq_task_dag_runs_one_running_per_dag` 不存在。
- Create: `internal/platform/db/sqlite/schema_contract_test.go`
  - 为每个持久化表声明 expected contract：primary keys、foreign keys、CHECK constraints、UNIQUE constraints、partial indexes、required non-null columns。
  - 通过 `sqlite_master`、`PRAGMA foreign_key_list`、`PRAGMA index_list` 校验；任何 expected constraint/index 缺失必须失败。
  - 对 baseline fixture 运行 `PRAGMA foreign_key_check`。
- Create: `internal/platform/db/sqlite/testdata/`
  - 放最小 JSON/timestamp/cron/DAG fixture，供后续 store 任务复用。

## 转译规则

- `JSONB` -> `TEXT NOT NULL CHECK(json_valid(column))`，默认 `'{}'` 或 `'[]'`。
- `timestamptz` / `timestamp` -> `INTEGER`，存 UTC epoch milliseconds。
- `BOOLEAN` -> `INTEGER NOT NULL CHECK(column IN (0, 1))`。
- `SERIAL` / identity -> `INTEGER PRIMARY KEY`。
- `NOW()` 不进入 schema 默认值；由 Go 层传入。
- 不保留 `public.`, `pg_catalog`, PL/pgSQL, `::jsonb`, `FOR UPDATE`, advisory lock 函数。

## 不允许改

- 不要编辑根 `migrations/*.sql` 的 PG 历史文件。
- 不要改 `sql/queries/**` 或 generated sqlc。
- 不要新增 PG -> SQLite 数据迁移脚本。

## 验收方案

```bash
./scripts/test_with_guard.sh ./internal/platform/db -count=1
make guard
```

静态扫描：

```bash
rg -n "jsonb|pg_catalog|public\\.|FOR UPDATE|pg_advisory|::" internal/platform/db/sqlite/migrations/001_baseline.sql
```

预期：没有 PostgreSQL-only 语法残留；如 `::` 只允许出现在注释中且应删除注释歧义。
