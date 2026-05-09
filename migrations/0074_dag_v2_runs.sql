-- DAG v2 骨架阶段 S3.3: task_dag_runs 表 + 三条索引
--
-- 蓝图 v2 §5 决策 C 混合：DAG 主表是模板，task_dag_runs 存每次执行实例。
-- 节点通过 task_dag_nodes.run_id 关联到具体 run。
--
-- 字段：
--   - run_key:              对外唯一键（如 dag_xxx#run_2026-05-10T08:00）
--   - dag_version_snapshot: run 创建时的 task_dags.version 快照
--                           run 跑起来后 DAG 模板被改不影响这次 run（Temporal 模型）
--   - trigger_source:       manual / auto / scheduled / external
--   - status:               running / succeeded / failed / cancelled
--   - events:               字段位（Temporal-style event sourcing replay）
--   - budget_used / budget_limit: 字段位（H8 enforce）
--
-- 索引（审查 M-5 要求）：
--   - dag_key + started_at DESC: UI 展示某 DAG 的最近 run 历史
--   - status:                    监控扫所有 running run / 失败 run
--   - 部分索引 idx_task_dags_next_run_scheduled:
--       仅 trigger=scheduled 的 DAG 进 cron 扫描视图，避免全表扫
--
-- ROLLBACK (manual):
--   DROP INDEX IF EXISTS idx_task_dags_next_run_scheduled;
--   DROP INDEX IF EXISTS idx_task_dag_runs_status;
--   DROP INDEX IF EXISTS idx_task_dag_runs_dag_key;
--   DROP TABLE IF EXISTS task_dag_runs;

CREATE TABLE IF NOT EXISTS task_dag_runs (
  id                    BIGSERIAL    PRIMARY KEY,
  run_key               TEXT         NOT NULL UNIQUE,
  dag_key               TEXT         NOT NULL,
  dag_version_snapshot  BIGINT       NOT NULL DEFAULT 0,
  trigger_source        TEXT         NOT NULL DEFAULT '',
  status                TEXT         NOT NULL DEFAULT 'running',
  started_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  finished_at           TIMESTAMPTZ,
  events                JSONB        NOT NULL DEFAULT '[]'::jsonb,
  budget_used           BIGINT       NOT NULL DEFAULT 0,
  budget_limit          BIGINT,
  metadata              JSONB,
  created_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  updated_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_task_dag_runs_dag_key
  ON task_dag_runs (dag_key, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_task_dag_runs_status
  ON task_dag_runs (status);

-- 部分索引：cron daemon 扫描入口，仅命中 trigger=scheduled 的 DAG。
CREATE INDEX IF NOT EXISTS idx_task_dags_next_run_scheduled
  ON task_dags (next_run_at)
  WHERE trigger = 'scheduled';
