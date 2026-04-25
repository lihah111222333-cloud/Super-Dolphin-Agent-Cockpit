-- 0063_agent_thread_name.sql — persist display name separately from prompt.
--
-- agent_id/thread_id remains the identity source. name is only a durable
-- display label; prompt is kept for older rows and legacy callers.

ALTER TABLE public.agent_threads
    ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '';

UPDATE public.agent_threads
SET name = prompt
WHERE name = ''
  AND prompt <> '';
