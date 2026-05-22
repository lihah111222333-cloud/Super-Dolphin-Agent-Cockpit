BEGIN;

WITH registry_backed(prompt_key) AS (
    VALUES
        ('main/default'),
        ('main/general-zh')
)
UPDATE public.prompt_templates t
SET enabled = FALSE,
    updated_by = 'system.registry-migration',
    updated_at = NOW()
FROM registry_backed r
WHERE t.prompt_key = r.prompt_key
  AND t.enabled = TRUE
  AND t.created_by IN ('system.seed', 'seed')
  AND (t.updated_by IN ('system.seed', 'seed', 'migration') OR t.updated_by LIKE 'system.%')
  AND t.manually_edited = FALSE;

COMMIT;
