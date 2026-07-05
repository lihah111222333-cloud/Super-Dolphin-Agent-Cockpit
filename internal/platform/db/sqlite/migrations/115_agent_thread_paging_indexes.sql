CREATE INDEX IF NOT EXISTS idx_agent_threads_created_thread_id_desc
    ON agent_threads(created_at DESC, thread_id DESC);

CREATE INDEX IF NOT EXISTS idx_agent_threads_status_created_thread_id_desc
    ON agent_threads(status, created_at DESC, thread_id DESC);
