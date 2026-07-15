-- name: InsertTaskDagNode :execrows
INSERT INTO task_dag_nodes (dag_key, node_key, title, node_type, assigned_to, depends_on, reads, writes, command_ref, config, created_at, updated_at)
VALUES (:dag_key, :node_key, :title, :node_type, :assigned_to, :depends_on, :reads, :writes, :command_ref, :config, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000));

-- name: UpdateTaskDagNode :execrows
UPDATE task_dag_nodes
SET title = :title,
    node_type = :node_type,
    assigned_to = :assigned_to,
    depends_on = :depends_on,
    reads = :reads,
    writes = :writes,
    command_ref = :command_ref,
    config = :config,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE
  dag_key = :dag_key
  AND node_key = :node_key
  AND run_id IS NULL;

-- name: PatchTaskDagNodeConfigIfUnchanged :execrows
UPDATE task_dag_nodes
SET config = :config, updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key
  AND node_key = :node_key
  AND run_id = :run_id
  AND :run_id > 0
  AND config = :previous_config
  AND status NOT IN ('done', 'failed', 'cancelled', 'skipped');

-- name: DeleteTaskDagNode :execrows
DELETE FROM task_dag_nodes
WHERE dag_key = :dag_key
  AND node_key = :node_key
  AND run_id IS NULL
  AND status IN ('pending', 'ready');

-- name: AssignTaskDagNode :execrows
UPDATE task_dag_nodes
SET assigned_to = :assigned_to,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key
  AND node_key = :node_key
  AND run_id = :run_id
  AND status IN ('pending', 'ready');

-- name: UpdateTaskDagNodeStatusIfCurrent :execrows
UPDATE task_dag_nodes
SET status = :status, result = :result, updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key AND node_key = :node_key
  AND run_id = :run_id
  AND :run_id > 0
  AND status = :expected_status;

-- name: ClaimTaskDagNodeOutputMaterialization :execrows
UPDATE task_dag_nodes
SET result = :result, updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key
  AND node_key = :node_key
  AND run_id = :run_id
  AND :run_id > 0
  AND status IN ('ready', 'running');

-- name: FailTaskDagNodeIfNonTerminal :execrows
UPDATE task_dag_nodes
SET status = :status, result = :result, updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE task_dag_nodes.dag_key = :dag_key
  AND task_dag_nodes.node_key = :node_key
  AND task_dag_nodes.run_id = :run_id
  AND :run_id > 0
  AND status NOT IN ('done', 'failed', 'cancelled', 'skipped')
  AND (:wakeup_id = 0 OR :wakeup_fence_matched = 1);

-- name: CascadeFailPendingTaskDagNode :execrows
UPDATE task_dag_nodes
SET status = 'failed', result = :result, updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key
  AND node_key = :node_key
  AND run_id = :run_id
  AND :run_id > 0
  AND status = 'pending';

-- name: PromoteSingleNodePendingToReady :execrows
UPDATE task_dag_nodes
SET status = 'ready',
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key
  AND node_key = :node_key
  AND run_id = :run_id
  AND :run_id > 0
  AND status = 'pending';
