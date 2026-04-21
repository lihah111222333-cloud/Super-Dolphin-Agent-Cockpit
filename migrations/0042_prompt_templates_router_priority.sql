-- 0042_prompt_templates_router_priority.sql — explicit conflict ordering.
--
-- Rule-router matching currently returns whichever candidate happens to be
-- scanned first when multiple tags could match. Order came from the List
-- query's "ORDER BY updated_at DESC", which is fine for MVP but makes
-- conflicts non-deterministic (editing a tag reshuffles the priority).
--
-- Add a first-class router_priority column so operators can pin ordering
-- intent without relying on update order.
--
-- Conventions:
--   200  orchestrator-level (meta-agents, always win when invoked)
--   150  high-value specialists (debug — user is usually stuck when asking)
--   100  default specialist priority (most rows)
--    50  niche / experimental specialists
--     0  fallback (main/default) — lowest, intentionally lose to any match
--
-- Router scan order becomes: ORDER BY router_priority DESC, updated_at DESC.
-- First-match-wins semantics inside rule.go are unchanged.
--
-- Idempotent: IF NOT EXISTS + UPDATEs scoped to seeded rows.

BEGIN;

ALTER TABLE public.prompt_templates
    ADD COLUMN IF NOT EXISTS router_priority INTEGER NOT NULL DEFAULT 100;

CREATE INDEX IF NOT EXISTS idx_prompt_templates_router_priority
    ON public.prompt_templates (router_priority DESC, updated_at DESC)
    WHERE enabled = true;

-- Seed priorities for the 0040 multi-scenario roster. Operators can edit
-- any row later; this just sets sensible defaults.
UPDATE public.prompt_templates SET router_priority = 200 WHERE prompt_key = 'main/orchestrator';
UPDATE public.prompt_templates SET router_priority = 150 WHERE prompt_key = 'main/code-debug';
UPDATE public.prompt_templates SET router_priority = 100 WHERE prompt_key IN (
    'main/code-review','main/code-task','main/sql','main/writing',
    'main/translate','main/research','main/brainstorm','main/planning'
);
UPDATE public.prompt_templates SET router_priority = 0 WHERE prompt_key = 'main/default';

COMMIT;
