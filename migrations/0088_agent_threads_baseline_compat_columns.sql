-- 0088_agent_threads_baseline_compat_columns.sql
--
-- Repair agent_threads columns that existed in 001_baseline.sql but were not
-- introduced by the incremental 0012-era migration path. Older local
-- databases created from 0012_agent_threads.sql can have schema_migrations
-- fully caught up while still missing these columns, and current sqlc thread
-- queries select them unconditionally.

ALTER TABLE public.agent_threads
    ADD COLUMN IF NOT EXISTS workspace_run_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS owner_thread_id   TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_agent_threads_workspace_run_key
    ON public.agent_threads (workspace_run_key);

CREATE INDEX IF NOT EXISTS idx_agent_threads_owner_thread_id
    ON public.agent_threads (owner_thread_id);
