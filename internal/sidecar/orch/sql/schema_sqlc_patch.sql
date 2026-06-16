-- sqlc-only schema patch.
--
-- Migration 0083 adds task_dag_nodes.spawning_thread_id inside a DO block so
-- production migration can be idempotent after partial application. sqlc v1.30
-- does not apply the ALTER TABLE inside that block, so keep the parseable
-- column shape here for typed query generation. This file is not a runtime
-- migration.

ALTER TABLE task_dag_nodes
  ADD COLUMN spawning_thread_id TEXT NULL;
