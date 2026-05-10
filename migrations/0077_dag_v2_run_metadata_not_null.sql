-- DAG v2 T1.2-mid 根治 (X1 #2): 把 task_dag_runs.metadata 列从 NULLABLE
-- 升级为 NOT NULL DEFAULT '{}'::jsonb，与 events 列对齐 (events 已经
-- NOT NULL DEFAULT '[]'::jsonb，见 0074_dag_v2_runs.sql:39 vs 36)。
--
-- 背景：原 0074 把 metadata 列声明为 NULLABLE 无 DEFAULT。Go 侧
-- store_run.go::CreateRun 写路径有 nil → json.RawMessage("null") 兜底
-- (避免 sqlc Column5 []byte 传 nil)，但读路径 fromTaskDagRun 直接
-- json.RawMessage(row.Metadata)，row.Metadata 为 nil 时不是合法 JSON,
-- 调用方做 json.Marshal 会出问题。读/写两条路径手工同步 nil 处理逻辑，
-- 没有"持久层永远存合法 JSON"的强不变量 → 容易再现 bug。
--
-- 修法：DB 层强制 metadata NOT NULL DEFAULT '{}'::jsonb。fromTaskDagRun
-- 读到的 row.Metadata 永远非 nil 合法 JSON; CreateRun 写入若不指定也走默认。
-- 对称性问题从根上消失。
--
-- 跑前预检（强制）:
--   SELECT COUNT(*) FROM task_dag_runs WHERE metadata IS NULL;
--   若 > 0：先 UPDATE task_dag_runs SET metadata='{}'::jsonb WHERE metadata IS NULL;
-- 实际跑前预检结果：0 行（无 NULL metadata 行需先 backfill）。
--
-- ROLLBACK (manual):
--   ALTER TABLE task_dag_runs
--     ALTER COLUMN metadata DROP NOT NULL,
--     ALTER COLUMN metadata DROP DEFAULT;

-- 防御性 backfill：即使预检显示 0 行，仍把任何潜在 NULL 收敛到 '{}'。
UPDATE task_dag_runs SET metadata = '{}'::jsonb WHERE metadata IS NULL;

ALTER TABLE task_dag_runs
  ALTER COLUMN metadata SET DEFAULT '{}'::jsonb,
  ALTER COLUMN metadata SET NOT NULL;
