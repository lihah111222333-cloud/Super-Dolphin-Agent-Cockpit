-- 0086_prompt_template_manually_edited.sql — protect seed-managed prompt templates.
--
-- F7.3 introduces prompt_template skill-card seeds that should be refreshable by
-- later migrations without overwriting user-edited rows. The flag defaults to
-- FALSE for existing seed-managed templates; UI/admin write paths must set it
-- TRUE when a human edits a template.

ALTER TABLE public.prompt_templates
    ADD COLUMN IF NOT EXISTS manually_edited BOOLEAN NOT NULL DEFAULT FALSE;
