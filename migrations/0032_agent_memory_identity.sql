ALTER TABLE public.agent_threads
    ADD COLUMN IF NOT EXISTS parent_agent_id text DEFAULT ''::text NOT NULL,
    ADD COLUMN IF NOT EXISTS agent_type text DEFAULT ''::text NOT NULL,
    ADD COLUMN IF NOT EXISTS agent_memory_scope text DEFAULT ''::text NOT NULL;

ALTER TABLE public.agent_provider_binding
    ADD COLUMN IF NOT EXISTS parent_agent_id text DEFAULT ''::text NOT NULL,
    ADD COLUMN IF NOT EXISTS agent_type text DEFAULT ''::text NOT NULL,
    ADD COLUMN IF NOT EXISTS agent_memory_scope text DEFAULT ''::text NOT NULL;
