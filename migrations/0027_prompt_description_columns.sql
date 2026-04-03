-- Add description column to prompt_templates and prompt_versions if missing.
-- Fixes: prompt_list/prompt_get "column description does not exist" error.
ALTER TABLE public.prompt_templates ADD COLUMN IF NOT EXISTS description text DEFAULT ''::text NOT NULL;
ALTER TABLE public.prompt_versions ADD COLUMN IF NOT EXISTS description text DEFAULT ''::text NOT NULL;
