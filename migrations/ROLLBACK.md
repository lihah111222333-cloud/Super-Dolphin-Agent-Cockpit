# Migration 回滚 Runbook

本仓库当前的 migration runner（`internal/platform/db/module.go`）**只识别一种文件**：
按文件名字典序 apply `migrations/*.sql`，并把 filename 写入 `schema_migrations`。
没有 down/rollback 概念，也不支持 `.down.sql` 命名约定（若放入 `*.down.sql` 文件，
runner 会把它当成新 migration 误跑，反而执行掉 down 语句）。

因此，回滚以**手工 SQL**方式执行：直接连 PG 跑下方对应的 DDL，然后从 `schema_migrations`
删掉对应 filename 行（若需要重新 apply 同名 up）。

---

## 0076 — partial unique index 同 dag_key 单 running

**up：** `migrations/0076_dag_v2_one_running_run_per_dag.sql`

**down（手工执行）：**

```sql
BEGIN;
DROP INDEX IF EXISTS uniq_task_dag_runs_one_running_per_dag;
DELETE FROM schema_migrations WHERE filename = '0076_dag_v2_one_running_run_per_dag.sql';
COMMIT;
```

**影响：** 删除后同 `dag_key` 可能再次出现并发 `running` 行（StartDAG TOCTOU race），
应用层 GetRun-first idempotency 仍可挡 `idempotency_key` 重复，但挡不住空
idempotency_key 的并发起跑。仅在确诊 unique index 本身阻塞业务时才回滚。

---

## 0077 — task_dag_runs.metadata NOT NULL DEFAULT '{}'

**up：** `migrations/0077_dag_v2_run_metadata_not_null.sql`

**down（手工执行）：**

```sql
BEGIN;
ALTER TABLE task_dag_runs ALTER COLUMN metadata DROP NOT NULL;
ALTER TABLE task_dag_runs ALTER COLUMN metadata DROP DEFAULT;
DELETE FROM schema_migrations WHERE filename = '0077_dag_v2_run_metadata_not_null.sql';
COMMIT;
```

**影响：** 列回到 nullable，未来 INSERT 不显式给 metadata 时会写 NULL；read 路径
`fromTaskDagRun` 期望 jsonb 对象，遇 NULL 会规约违背。回滚前确认上层 Go 写路径未
依赖 DEFAULT '{}' 兜底（commit a2aa68d3 后写路径已自带 `"{}"` 兜底，本回滚相对安全）。

---

## 0078 — task_dag_nodes.depends_on jsonb 数组 CHECK

**up：** `migrations/0078_dag_v2_node_depends_on_array_check.sql`

**down（手工执行）：**

```sql
BEGIN;
ALTER TABLE task_dag_nodes DROP CONSTRAINT IF EXISTS chk_depends_on_is_array;
DELETE FROM schema_migrations WHERE filename = '0078_dag_v2_node_depends_on_array_check.sql';
COMMIT;
```

**影响：** 删除 CHECK 后 `depends_on` 列允许任意 jsonb 值（含 null/object/string）。
应用层 sqlc 写路径仍会塞数组，但读路径若遇到非数组值会 unmarshal 错。仅在
CHECK 误伤合法数据时才回滚。

---

## 注意事项

1. **当前 PG 已 applied 0076/0077/0078/0079/0080**（`schema_migrations.version` 最大 80；以下文 §0079/0080 段为准），
   上述 down 是事故/演练时的手工兜底，不影响日常开发。
2. **历史 0001-0075 没有补 down**，因为本仓库历史没有 down 概念。仅对 0076 起的
   T1.2-mid 一批 schema-tightening migration 做此 runbook 化。
3. **down 不会修复脏数据**：如 0076 down 后若有遗留并发 running 行，需自行清理。
4. **不要把 down SQL 放进 `migrations/` 目录**：runner 不区分 up/down，会把 `*.down.sql`
   当成新 migration 跑掉。

---

## 0079 — task_dag_nodes run_id FK + reads/writes array CHECK + run_id 索引

**up：** `migrations/0079_dag_v2_node_run_id_fk_and_jsonb_checks.sql`

**down（手工执行）：**

```sql
BEGIN;
ALTER TABLE task_dag_nodes DROP CONSTRAINT IF EXISTS fk_task_dag_nodes_run_id;
DROP INDEX IF EXISTS idx_task_dag_nodes_run_id;
ALTER TABLE task_dag_nodes DROP CONSTRAINT IF EXISTS chk_reads_is_array;
ALTER TABLE task_dag_nodes DROP CONSTRAINT IF EXISTS chk_writes_is_array;
DELETE FROM schema_migrations WHERE filename = '0079_dag_v2_node_run_id_fk_and_jsonb_checks.sql';
COMMIT;
```

**影响：** 删除 FK 后 run_id 可能悬挂引用不存在的 task_dag_runs.id；删 reads/
writes CHECK 后允许非数组 jsonb 值，UI / 文件锁 联动读路径可能 unmarshal 失败。
仅在 FK / CHECK 误伤合法数据或阻塞业务时回滚。run_id 索引 down 仅影响查询性能，
不影响正确性。

---

## 0080 — task_dag_runs.status CHECK 枚举

**up：** `migrations/0080_dag_v2_run_status_check.sql`

**down（手工执行）：**

```sql
BEGIN;
ALTER TABLE task_dag_runs DROP CONSTRAINT IF EXISTS chk_task_dag_runs_status_enum;
DELETE FROM schema_migrations WHERE filename = '0080_dag_v2_run_status_check.sql';
COMMIT;
```

**影响：** 删除 CHECK 后 status 列允许任意 TEXT，未知字面量会进 DB；service
状态机读到非枚举值会落 default 分支（行为未定义）。0076 partial unique index
仍依赖 'running' 字面量，单边删 CHECK 不破 0076 但放宽了写路径校验。仅在
CHECK 误伤业务（如新增合法 status 字面量未同步迁移）时回滚。

---

## 0081 — task_dags.trigger CHECK 枚举

**up：** `migrations/0081_dag_v2_dag_trigger_check.sql`

**down（手工执行）：**

```sql
BEGIN;
ALTER TABLE task_dags DROP CONSTRAINT IF EXISTS chk_task_dags_trigger_enum;
DELETE FROM schema_migrations WHERE filename = '0081_dag_v2_dag_trigger_check.sql';
COMMIT;
```

**影响：** 删除 CHECK 后 trigger 列允许任意 TEXT，未知字面量会进 DB；
F5 cron daemon / dispatcher 读到非枚举值会落 default 分支（行为未定义）。
0072 default 'manual' 仍存在，单边删 CHECK 不影响默认值写路径，仅放宽
非默认写入校验。仅在 CHECK 误伤业务（如新增合法 trigger 字面量未同步迁移）
时回滚。
