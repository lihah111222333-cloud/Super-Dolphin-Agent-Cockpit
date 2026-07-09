-- 0111_refresh_orchestration_short_tool_names.sql
--
-- Current registry-backed DAG designer prompt assets no longer teach the retired
-- orchestration_* tool names. Disable the older system-seed rows so runtime
-- prompt assembly uses the registry assets instead of stale SQL bodies.

BEGIN;

WITH registry_backed(prompt_key) AS (
    VALUES
        ('main/dag_designer_zh'),
        ('main/dag_designer_en')
)
UPDATE public.prompt_templates t
SET enabled = FALSE,
    updated_by = 'migration:0111',
    updated_at = NOW()
FROM registry_backed r
WHERE t.prompt_key = r.prompt_key
  AND t.enabled = TRUE
  AND t.created_by IN ('system.seed', 'seed')
  AND (
      t.updated_by IN ('system.seed', 'seed', 'migration')
      OR t.updated_by LIKE 'system.%'
      OR t.updated_by LIKE 'migration:%'
  )
  AND t.manually_edited = FALSE;

COMMIT;
