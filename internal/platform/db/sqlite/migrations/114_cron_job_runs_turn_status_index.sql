CREATE INDEX IF NOT EXISTS idx_cron_job_runs_turn_status ON cron_job_runs(turn_id, status) WHERE turn_id <> '' AND status IN ('submitted', 'running');
