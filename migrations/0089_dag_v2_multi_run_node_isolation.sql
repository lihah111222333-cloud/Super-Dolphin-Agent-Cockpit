-- DAG v2 F6.5: allow multiple runs of the same DAG and isolate runtime nodes by run_id.
--
-- Template nodes remain `run_id IS NULL` and keep one (dag_key,node_key) row.
-- Each StartDAG clones that template into runtime rows with a concrete run_id,
-- so the same node_key can exist independently across concurrent runs.
--
-- ROLLBACK (manual):
--   DROP INDEX IF EXISTS uq_task_dag_nodes_runtime_dag_run_node;
--   DROP INDEX IF EXISTS uq_task_dag_nodes_template_dag_node;
--   ALTER TABLE task_dag_nodes ADD CONSTRAINT uq_task_dag_nodes_dag_node UNIQUE (dag_key, node_key);
--   ALTER TABLE task_dag_wakeups DROP CONSTRAINT IF EXISTS fk_task_dag_wakeups_run_id;
--   DROP INDEX IF EXISTS idx_task_dag_wakeups_run_id;
--   ALTER TABLE task_dag_wakeups DROP COLUMN IF EXISTS run_id;
--   CREATE UNIQUE INDEX IF NOT EXISTS uniq_task_dag_runs_one_running_per_dag
--     ON task_dag_runs (dag_key) WHERE status = 'running';

BEGIN;

-- F6.5 removes the T1.2-mid "one running run per dag_key" guard.
DROP INDEX IF EXISTS uniq_task_dag_runs_one_running_per_dag;

-- Replace the old global node identity with split template/runtime identities.
ALTER TABLE task_dag_nodes
  DROP CONSTRAINT IF EXISTS uq_task_dag_nodes_dag_node;
ALTER TABLE task_dag_nodes
  DROP CONSTRAINT IF EXISTS task_dag_nodes_dag_key_node_key_key;

DROP INDEX IF EXISTS uq_task_dag_nodes_template_dag_node;
CREATE UNIQUE INDEX uq_task_dag_nodes_template_dag_node
  ON task_dag_nodes (dag_key, node_key)
  WHERE run_id IS NULL;

DROP INDEX IF EXISTS uq_task_dag_nodes_runtime_dag_run_node;
CREATE UNIQUE INDEX uq_task_dag_nodes_runtime_dag_run_node
  ON task_dag_nodes (dag_key, run_id, node_key)
  WHERE run_id IS NOT NULL;

-- Wakeups must carry run_id so dispatcher/router can resolve duplicate node_key
-- rows without falling back to a dag_key-wide lookup.
ALTER TABLE task_dag_wakeups
  ADD COLUMN IF NOT EXISTS run_id BIGINT;

ALTER TABLE task_dag_wakeups
  ADD CONSTRAINT fk_task_dag_wakeups_run_id
  FOREIGN KEY (run_id)
  REFERENCES task_dag_runs (id)
  ON DELETE CASCADE
  NOT VALID;

ALTER TABLE task_dag_wakeups
  VALIDATE CONSTRAINT fk_task_dag_wakeups_run_id;

CREATE INDEX IF NOT EXISTS idx_task_dag_wakeups_run_id
  ON task_dag_wakeups (run_id);

COMMIT;
