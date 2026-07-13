CREATE INDEX IF NOT EXISTS idx_cron_jobs_created_id ON cron_jobs(created_at DESC, id DESC);
