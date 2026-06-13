CREATE INDEX IF NOT EXISTS idx_task_dags_updated_id ON task_dags(updated_at DESC, id DESC);
