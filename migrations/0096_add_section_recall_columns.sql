BEGIN;

ALTER TABLE public.prompt_template_sections
  ADD COLUMN IF NOT EXISTS trigger_type TEXT NOT NULL DEFAULT 'always'
    CHECK (trigger_type IN ('always', 'keyword', 'recall'));

ALTER TABLE public.prompt_template_sections
  ADD COLUMN IF NOT EXISTS recall_topic TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_sections_recall_topic
  ON public.prompt_template_sections (recall_topic)
  WHERE trigger_type = 'recall' AND recall_topic <> '';

COMMIT;
