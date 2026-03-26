ALTER TABLE public.agent_threads
    ADD COLUMN IF NOT EXISTS config_override jsonb DEFAULT '{}'::jsonb NOT NULL;
