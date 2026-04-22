-- 0044_drop_router_priority.sql — remove the now-dead routing layer.
--
-- The RuleRouter and the prompt-classifier preview have been retired: the
-- harness now dispatches purely by explicit agent_key (see 44d20b8, f99197d).
-- That leaves router_priority with no consumer — nothing in resolveRoutedPrompt
-- or anywhere else reads it. Dropping the column and its index tightens the
-- schema to match go-agent-v2's "no routing layer" shape.
--
-- prompt_versions keeps the column via its historical snapshot contract; we
-- don't rewrite old archive rows. Only the live prompt_templates table and
-- its index are touched here. Idempotent.

BEGIN;

DROP INDEX IF EXISTS idx_prompt_templates_router_priority;

ALTER TABLE public.prompt_templates
    DROP COLUMN IF EXISTS router_priority;

COMMIT;
