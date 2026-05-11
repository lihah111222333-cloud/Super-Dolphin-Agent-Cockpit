-- DAG v2 F1.5: task_dag_nodes.spawning_thread_id 字段位。
--
-- 背景：ADR-009 / T0.7(PD-2) 前置要求 DAG node 与 AgentExecutor
-- spawn 出的 child thread 建立稳定软关联。T6.1 / T8.1 UI 后续直接读
-- task_get_dag / task_get_run 的节点 DTO，不再解析 node.result 字符串。
--
-- 设计取舍：
--   - spawning_thread_id TEXT NULL：仅保存最近一次 spawn 成功的 child thread。
--   - 不加外键：thread 子系统与 DAG 编排层跨语义域，ADR-009 §5 Q1 决定只做
--     软引用 + index，避免 thread 生命周期影响 DAG 历史。
--   - partial index 仅覆盖非 NULL 值，支持按 thread_id 反查节点，且不放大空值索引。
--
-- 幂等保护：executeMigration 的 SQL 执行与 schema_migrations bookkeeping 非同一
-- 原子事务；进程中断可能留下“列/索引已建、bookkeeping 漏写”的半成品状态。
-- 因此用 DO 块检查 pg_attribute 后再 ADD COLUMN，索引用 IF NOT EXISTS。
-- 本 migration 不创建 CHECK/FK 约束，所以无需 pg_constraint bookkeeping 分支。
--
-- ROLLBACK (manual): DROP INDEX IF EXISTS idx_task_dag_nodes_spawning_thread_id;
--   ALTER TABLE task_dag_nodes DROP COLUMN IF EXISTS spawning_thread_id;

BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_attribute
    WHERE attrelid = 'public.task_dag_nodes'::regclass
      AND attname = 'spawning_thread_id'
      AND NOT attisdropped
  ) THEN
    ALTER TABLE task_dag_nodes
      ADD COLUMN spawning_thread_id TEXT NULL;
  END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_task_dag_nodes_spawning_thread_id
  ON task_dag_nodes (spawning_thread_id)
  WHERE spawning_thread_id IS NOT NULL;

COMMIT;
