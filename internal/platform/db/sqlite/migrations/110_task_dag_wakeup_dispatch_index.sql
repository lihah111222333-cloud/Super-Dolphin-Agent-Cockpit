CREATE INDEX IF NOT EXISTS idx_task_dag_wakeups_dispatching_lease
ON task_dag_wakeups(lease_expires_at, id)
WHERE status = 'dispatching';
