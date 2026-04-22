-- Prompt template sections: split monolithic prompt_text into ordered, region-aware blocks.
-- region='static' → feeds CachedPrefix (--system-prompt, enters CC CLI's cached prefix block)
-- region='dynamic' → feeds UncachedTail (--append-system-prompt, stays in the volatile block)
-- enable_when is reserved for Step 2 feature-gate DSL; unused in Step 1.

-- Repair (prerequisite): 001_baseline.sql declared prompt_templates.id / prompt_versions.id
-- as `bigint NOT NULL` without a PRIMARY KEY, so the FK below (REFERENCES prompt_templates(id))
-- fails with SQLSTATE 42830 on any database bootstrapped from baseline. Add the missing PKs
-- idempotently here before creating the dependent table.
DO $$
BEGIN
    IF to_regclass('public.prompt_templates') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1 FROM pg_constraint
           WHERE conrelid = 'public.prompt_templates'::regclass
             AND contype  = 'p'
       ) THEN
        ALTER TABLE public.prompt_templates
            ADD CONSTRAINT prompt_templates_pkey PRIMARY KEY (id);
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regclass('public.prompt_versions') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1 FROM pg_constraint
           WHERE conrelid = 'public.prompt_versions'::regclass
             AND contype  = 'p'
       ) THEN
        ALTER TABLE public.prompt_versions
            ADD CONSTRAINT prompt_versions_pkey PRIMARY KEY (id);
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS prompt_template_sections (
    id           BIGSERIAL PRIMARY KEY,
    template_id  BIGINT NOT NULL REFERENCES prompt_templates(id) ON DELETE CASCADE,
    section_key  TEXT   NOT NULL,
    region       TEXT   NOT NULL CHECK (region IN ('static','dynamic')),
    ordinal      INT    NOT NULL DEFAULT 0,
    body         TEXT   NOT NULL,
    enable_when  JSONB,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(template_id, section_key)
);

CREATE INDEX IF NOT EXISTS idx_prompt_template_sections_lookup
    ON prompt_template_sections (template_id, enabled, region, ordinal);
