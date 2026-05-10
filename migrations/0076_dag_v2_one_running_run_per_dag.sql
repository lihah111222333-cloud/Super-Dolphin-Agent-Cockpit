-- DAG v2 T1.2-mid 根治 (X1 #1): 把"同 dag_key 任意时刻最多 1 个 running run"
-- 这个业务约束从应用层下沉到 DB partial unique index。
--
-- 背景：service.StartDAG 原方案在事务外做 CountActiveRunsByDagKey 检查，
-- 然后在事务内 CreateRun。两个并发 StartDAG 在同 dag_key 下：
--   T1: count=0 → 通过；T2: count=0（T1 尚未 INSERT）→ 通过
--   T1: CreateRun(run_key#1, nanos 唯一) 成功
--   T2: CreateRun(run_key#2, nanos 唯一) 成功
--   → 同 dag_key 两个 running run，违反 T1.2-mid 单 run 约束
-- 这是经典 TOCTOU race。task_dag_runs.run_key UNIQUE 仅守 run_key 维度，
-- 不防同 dag_key 多 running。
--
-- 修法：partial unique index ON (dag_key) WHERE status='running'。PG B-tree
-- partial unique 在 INSERT 时取 row-level 锁（_bt_check_unique + buffer
-- lock）：两并发 INSERT status='running' 同 dag_key 时，后到者会阻塞至前者
-- 提交/回滚再判重 → 仅一者成功。其它状态（finished/failed/cancelled）不在
-- predicate 内，不参与判重。这是真不变量。
--
-- T1.2-mid 范围保持：每 DAG 最多 1 个 running run；F6.5 升级 multi-run 时
-- 要 DROP 这个约束并配套节点复制带 run_id。
--
-- 跑前预检（强制）:
--   SELECT dag_key, COUNT(*) FROM task_dag_runs
--    WHERE status='running' GROUP BY dag_key HAVING COUNT(*) > 1;
--   → 必须 0 行；非 0 时 ALTER 直接 abort、不可恢复。
-- 实际跑前预检结果：0 行（task_dag_runs 当前为空表，T1.2 未在生产使用过）。
--
-- ROLLBACK (manual):
--   DROP INDEX IF EXISTS uniq_task_dag_runs_one_running_per_dag;

-- Fail-fast 自检：CREATE UNIQUE INDEX 前先检查是否已存在违反约束的重复 running
-- 行。若有则直接 RAISE EXCEPTION 中止 migration，避免 CREATE UNIQUE INDEX 在脏
-- 数据上失败遗留半成品状态（CREATE UNIQUE INDEX 失败时 PG 会自己 rollback，
-- 但报错信息没有自检 RAISE 友好；此处把人工预检步骤固化进 migration）。
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM task_dag_runs
        WHERE status = 'running'
        GROUP BY dag_key
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION 'migration 0076 abort: duplicate running runs per dag_key detected; manual cleanup required before applying partial unique index';
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS uniq_task_dag_runs_one_running_per_dag
  ON task_dag_runs (dag_key)
  WHERE status = 'running';

