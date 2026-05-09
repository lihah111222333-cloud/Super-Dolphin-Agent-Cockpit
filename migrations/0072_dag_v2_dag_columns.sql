-- DAG v2 骨架阶段 S3.1: task_dags 新增一等字段
--
-- 把原本塞进 metadata.schedule JSON 的字段提为表列，让 SQL 能按 trigger /
-- next_run_at / version 直接索引和过滤。
--
--   - trigger: 触发源 (manual | auto | scheduled | external)，旧 row 默认 manual
--   - owner_id: ownership 字段位 (S3 阶段不做权限校验，T/F 阶段接通)
--   - cron_expr: trigger=scheduled 时的 cron 表达式
--   - next_run_at: cron daemon (F5) 扫描入口；NULL 表示未排程
--   - version: typed ops apply 的 OCC 版本号 (蓝图 v2 §5)
--
-- 旧 row 的 metadata.auto_handoff_phase1=true 在 0075 兼容映射里转成 trigger='auto'。
-- task_tools.go 写入 auto_handoff_phase1 的代码在 S15.1 删除。
--
-- ROLLBACK (manual):
--   ALTER TABLE task_dags DROP COLUMN IF EXISTS version;
--   ALTER TABLE task_dags DROP COLUMN IF EXISTS next_run_at;
--   ALTER TABLE task_dags DROP COLUMN IF EXISTS cron_expr;
--   ALTER TABLE task_dags DROP COLUMN IF EXISTS owner_id;
--   ALTER TABLE task_dags DROP COLUMN IF EXISTS trigger;

ALTER TABLE task_dags
  ADD COLUMN IF NOT EXISTS trigger     TEXT        NOT NULL DEFAULT 'manual',
  ADD COLUMN IF NOT EXISTS owner_id    TEXT        NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS cron_expr   TEXT        NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS next_run_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS version     BIGINT      NOT NULL DEFAULT 0;
