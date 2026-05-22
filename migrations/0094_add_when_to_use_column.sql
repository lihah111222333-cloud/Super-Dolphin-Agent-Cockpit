-- 0094: add per-template guidance for available_experts rendering.
BEGIN;

ALTER TABLE public.prompt_templates
  ADD COLUMN IF NOT EXISTS when_to_use TEXT NOT NULL DEFAULT '';

COMMIT;
