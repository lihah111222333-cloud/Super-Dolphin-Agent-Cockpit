-- DAG v2 骨架阶段 S3.2: task_dag_nodes 新增 run + 文件锁字段位
--
--   - run_id: 节点所属的 task_dag_runs.id（蓝图 v2 §5 决策 C 混合：DAG 模板 + run 实例）
--             同一 DAG 多次触发产生多个 run，每 run 有自己的 node 行（拷贝模板）
--   - reads / writes: 节点读/写哪些 sharedfile（蓝图 v2 §10 补丁 14 / CC Agent Teams 文件锁）
--                     UI 上展示 sharedfile 锁联动
--
-- ROLLBACK (manual):
--   ALTER TABLE task_dag_nodes DROP COLUMN IF EXISTS writes;
--   ALTER TABLE task_dag_nodes DROP COLUMN IF EXISTS reads;
--   ALTER TABLE task_dag_nodes DROP COLUMN IF EXISTS run_id;

ALTER TABLE task_dag_nodes
  ADD COLUMN IF NOT EXISTS run_id BIGINT,
  ADD COLUMN IF NOT EXISTS reads  JSONB NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS writes JSONB NOT NULL DEFAULT '[]'::jsonb;
