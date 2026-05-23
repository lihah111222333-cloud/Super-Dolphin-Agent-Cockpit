-- 0107_prompt_template_expert_consolidation.sql — consolidate duplicate developer experts.

BEGIN;

CREATE TABLE IF NOT EXISTS public.prompt_template_expert_consolidation_0107_restore (
    prompt_key TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    agent_key TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    prompt_text TEXT NOT NULL DEFAULT '',
    variables JSONB NOT NULL DEFAULT '{}'::jsonb,
    tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    description TEXT NOT NULL DEFAULT '',
    when_to_use TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    manually_edited BOOLEAN NOT NULL DEFAULT FALSE,
    match_when JSONB,
    priority INTEGER NOT NULL DEFAULT 0,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE public.prompt_template_expert_consolidation_0107_restore
    ALTER COLUMN match_when DROP NOT NULL;

WITH affected_keys(prompt_key) AS (
    VALUES
        ('main/code-generate'),
        ('main/code-refactor'),
        ('main/code-test'),
        ('main/code-explain'),
        ('main/code-task')
)
INSERT INTO public.prompt_template_expert_consolidation_0107_restore (
    prompt_key,
    title,
    agent_key,
    tool_name,
    prompt_text,
    variables,
    tags,
    description,
    when_to_use,
    enabled,
    manually_edited,
    match_when,
    priority,
    created_by,
    updated_by,
    created_at,
    updated_at
)
SELECT
    p.prompt_key,
    p.title,
    p.agent_key,
    p.tool_name,
    p.prompt_text,
    COALESCE(p.variables, '{}'::jsonb),
    COALESCE(p.tags, '[]'::jsonb),
    p.description,
    p.when_to_use,
    p.enabled,
    p.manually_edited,
    p.match_when,
    p.priority,
    p.created_by,
    p.updated_by,
    p.created_at,
    p.updated_at
FROM public.prompt_templates p
JOIN affected_keys ON affected_keys.prompt_key = p.prompt_key
WHERE p.created_by IN ('system.seed', 'seed')
  AND (
      p.updated_by IN ('system.seed', 'seed', 'migration')
      OR p.updated_by LIKE 'system.%'
      OR p.updated_by LIKE 'migration:%'
  )
  AND p.manually_edited = FALSE
ON CONFLICT (prompt_key) DO NOTHING;

WITH duplicate_keys(prompt_key) AS (
    VALUES
        ('main/code-generate'),
        ('main/code-refactor'),
        ('main/code-test'),
        ('main/code-explain')
)
DELETE FROM public.prompt_templates p
USING duplicate_keys
WHERE p.prompt_key = duplicate_keys.prompt_key
  AND p.created_by IN ('system.seed', 'seed')
  AND (
      p.updated_by IN ('system.seed', 'seed', 'migration')
      OR p.updated_by LIKE 'system.%'
      OR p.updated_by LIKE 'migration:%'
  )
  AND p.manually_edited = FALSE;

UPDATE public.prompt_templates p
SET when_to_use = '通用编程实现、重构、解释代码、补测试，覆盖合并后的日常开发任务。',
    updated_by = 'migration:0107',
    updated_at = NOW()
WHERE p.prompt_key = 'main/code-task'
  AND p.created_by IN ('system.seed', 'seed')
  AND (
      p.updated_by IN ('system.seed', 'seed', 'migration')
      OR p.updated_by LIKE 'system.%'
      OR p.updated_by LIKE 'migration:%'
  )
  AND p.manually_edited = FALSE;

COMMIT;
