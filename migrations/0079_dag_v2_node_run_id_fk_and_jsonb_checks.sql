-- DAG v2 T1.2-mid 根治 (B-0073): task_dag_nodes 多列补约束。
--
-- 三件事，原子化在一个 BEGIN/COMMIT：
--
-- (1) run_id BIGINT 加 FK -> task_dag_runs(id) ON DELETE CASCADE。
--     0073 仅声明列、未连父表。run_id NULL 是合法的（骨架阶段 460 行根节点
--     模板 run_id 全 NULL，DAG 模板节点不属于任何具体 run）；FK 约束允许
--     NULL，仅在非 NULL 值上要求父行存在，因此对历史数据安全。
--     ON DELETE CASCADE: 删 run 时该 run 拷贝出来的节点行（F6.5 multi-run
--     升级后会拷贝节点带 run_id）一并删，避免悬挂引用。
--
-- (2) idx_task_dag_nodes_run_id: B-tree on run_id。
--     scheduler / recover / persistent_runtime_rehydrate 等路径会按 run_id
--     聚合节点（蓝图 v2 §5）。WHERE run_id IS NOT NULL partial index 更省，
--     但 0073 全表 460 行 run_id NULL（骨架模板），完整 index 大小相当且未来
--     multi-run 落地后大量 run 节点查询会受益，故走完整 index。
--
-- (3) reads / writes JSONB 加 array CHECK：与 0078 depends_on 的 NOT VALID +
--     VALIDATE 两步同模板。0073 默认 '[]'::jsonb 但列允许任意 jsonb 值
--     （null/object/string 均可），SetReadsWrites 路径若存数组以外的值，
--     UI / 文件锁 联动会运行时 abort。新 INSERT/UPDATE 写非数组立即被拒；
--     既有 460 行预检全 array，VALIDATE 直接通过。
--
-- 跑前预检（强制）:
--   SELECT count(*) FROM task_dag_nodes WHERE run_id IS NULL;
--     -> 期望: 任意整数（NULL 不被 FK 约束）；当前 460。
--   SELECT count(*) FROM task_dag_nodes
--    WHERE run_id IS NOT NULL
--      AND run_id NOT IN (SELECT id FROM task_dag_runs);
--     -> 期望: 0；非 0 时 FK 加约束 abort。
--   SELECT count(*) FROM task_dag_nodes WHERE jsonb_typeof(reads)  <> 'array';
--   SELECT count(*) FROM task_dag_nodes WHERE jsonb_typeof(writes) <> 'array';
--     -> 期望: 0；非 0 时对应 VALIDATE abort。
-- 实际跑前预检结果（HEAD=99ca4d25，schema_migrations=78）：
--   run_id IS NULL: 460；orphan run_id: N/A（task_dag_runs 0 行 -> 反正
--   无 NOT NULL run_id 行可悬挂）；reads/writes 非 array: 0/0。
--
-- ROLLBACK (manual): 见 migrations/ROLLBACK.md §0079。

-- 包 BEGIN/COMMIT 让多步 ALTER 原子化：任一步失败整体 rollback，避免遗留半成品
-- 约束（与 0076/0078 同款 fail-fast 模式）。当前 runner（internal/platform/db/
-- module.go executeMigration）pool.Exec 单次执行整文件、不自动包 tx，因此显式
-- BEGIN/COMMIT 是必须的。
BEGIN;

-- (1) run_id FK
ALTER TABLE task_dag_nodes
  ADD CONSTRAINT fk_task_dag_nodes_run_id
  FOREIGN KEY (run_id)
  REFERENCES task_dag_runs (id)
  ON DELETE CASCADE
  NOT VALID;

ALTER TABLE task_dag_nodes
  VALIDATE CONSTRAINT fk_task_dag_nodes_run_id;

-- (2) run_id 索引（按 run 聚合节点的查询路径）
CREATE INDEX IF NOT EXISTS idx_task_dag_nodes_run_id
  ON task_dag_nodes (run_id);

-- (3a) reads array CHECK
ALTER TABLE task_dag_nodes
  ADD CONSTRAINT chk_reads_is_array
  CHECK (jsonb_typeof(reads) = 'array')
  NOT VALID;

ALTER TABLE task_dag_nodes
  VALIDATE CONSTRAINT chk_reads_is_array;

-- (3b) writes array CHECK
ALTER TABLE task_dag_nodes
  ADD CONSTRAINT chk_writes_is_array
  CHECK (jsonb_typeof(writes) = 'array')
  NOT VALID;

ALTER TABLE task_dag_nodes
  VALIDATE CONSTRAINT chk_writes_is_array;

COMMIT;
