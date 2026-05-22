-- 0105_delete_unused_builtin_prompt_seeds.sql — remove retired system-owned prompt seeds.

BEGIN;

WITH delete_keys(prompt_key) AS (
    VALUES
        ('main/3'),
        ('main/prompt'),
        ('main/debug'),
        ('main/claude-style'),
        ('main/claude-style-zh'),
        ('main/general-en'),
        ('sql/expert'),
        ('main/writing'),
        ('main/translate'),
        ('main/research'),
        ('main/brainstorm'),
        ('main/paper_summarizer'),
        ('main/topic_curator'),
        ('main/learning_card'),
        ('main/trip_briefer')
)
DELETE FROM public.prompt_templates p
USING delete_keys
WHERE p.prompt_key = delete_keys.prompt_key
  AND p.created_by IN ('system.seed', 'seed', 'test-seed')
  AND (
      p.updated_by IN ('system.seed', 'seed', 'test-seed', 'migration')
      OR p.updated_by LIKE 'system.%'
      OR p.updated_by LIKE 'migration:%'
  )
  AND p.manually_edited = FALSE;

DELETE FROM public.prompt_templates p
WHERE (p.prompt_key LIKE 'test/%' OR p.prompt_key = 'examples/sections-demo')
  AND p.created_by IN ('system.seed', 'seed', 'system', 'test-seed')
  AND (
      p.updated_by IN ('system.seed', 'seed', 'system', 'test-seed', 'migration')
      OR p.updated_by LIKE 'system.%'
      OR p.updated_by LIKE 'migration:%'
  )
  AND p.manually_edited = FALSE;

WITH obsolete_routing_tests(input, expected_prompt_key) AS (
    VALUES
        ('帮我写一封辞职邮件', 'main/writing'),
        ('润色一下这段文案', 'main/writing'),
        ('draft a release announcement', 'main/writing'),
        ('把这段翻译成英文', 'main/translate'),
        ('translate to Simplified Chinese', 'main/translate'),
        ('帮我翻译一下这个术语', 'main/translate'),
        ('什么是事件溯源', 'main/research'),
        ('总结一下这篇论文的要点', 'main/research'),
        ('对比一下 PostgreSQL 和 MySQL', 'main/research'),
        ('给我的猫起个名字', 'main/brainstorm'),
        ('brainstorm 几个营销方案', 'main/brainstorm'),
        ('想几个有创意的标题', 'main/brainstorm')
)
DELETE FROM public.prompt_routing_tests r
USING obsolete_routing_tests o
WHERE r.input = o.input
  AND r.expected_prompt_key = o.expected_prompt_key;

COMMIT;
