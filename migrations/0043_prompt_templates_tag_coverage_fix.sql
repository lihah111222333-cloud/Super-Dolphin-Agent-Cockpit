-- 0043_prompt_templates_tag_coverage_fix.sql — close routing_tests coverage gaps.
--
-- Running the 0041 routing_tests corpus through the live RuleRouter with the
-- 0040 seed tags and 0042 priorities surfaced 4 mismatches:
--   * "帮我写一个计算斐波那契的函数" -> writing instead of code-task
--     (writing's `帮我写` beats code-task's `写一个函数` substring).
--   * "对比一下 PostgreSQL 和 MySQL" -> sql instead of research
--     (sql priority == research priority, sql scanned first).
--   * "想几个有创意的标题" -> default (no brainstorm tag matched).
--   * "制定一个三步走的实施计划" -> default (no planning tag matched).
--
-- This migration:
--   * adds `帮我写一个` + `一个函数` tags to code-task so programming intent
--     wins over writing for code requests phrased as "帮我写一个 X 的函数"
--     (`帮我写一个` is strictly narrower than writing's `帮我写`).
--   * bumps main/research priority to 120 so comparative prompts route to
--     research before matching sql-specific tags like `PostgreSQL`.
--   * adds `有创意` to brainstorm tags (covers "想几个有创意的…" family).
--   * adds `实施计划` to planning tags (covers "...的实施计划" phrasing).
--
-- Idempotent: each UPDATE is guarded with jsonb `@>` so re-running on a DB
-- already at 0043 state is a no-op.

BEGIN;

UPDATE public.prompt_templates
SET tags = tags || '["帮我写一个","一个函数"]'::jsonb
WHERE prompt_key = 'main/code-task'
  AND NOT tags @> '["帮我写一个"]'::jsonb;

UPDATE public.prompt_templates
SET tags = tags || '["有创意"]'::jsonb
WHERE prompt_key = 'main/brainstorm'
  AND NOT tags @> '["有创意"]'::jsonb;

UPDATE public.prompt_templates
SET tags = tags || '["实施计划"]'::jsonb
WHERE prompt_key = 'main/planning'
  AND NOT tags @> '["实施计划"]'::jsonb;

UPDATE public.prompt_templates
SET router_priority = 120
WHERE prompt_key = 'main/research'
  AND router_priority <> 120;

COMMIT;
