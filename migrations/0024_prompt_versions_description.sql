ALTER TABLE public.prompt_versions
    ADD COLUMN IF NOT EXISTS description text DEFAULT ''::text NOT NULL;
