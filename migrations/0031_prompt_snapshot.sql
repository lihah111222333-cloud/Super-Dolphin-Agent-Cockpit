ALTER TABLE public.agent_threads
    ADD COLUMN IF NOT EXISTS prompt_snapshot jsonb DEFAULT NULL;
