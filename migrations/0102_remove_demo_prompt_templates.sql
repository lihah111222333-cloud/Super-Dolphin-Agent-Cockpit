-- 0102_remove_demo_prompt_templates.sql - remove legacy demo/test prompt templates.

BEGIN;

DELETE FROM public.prompt_templates
WHERE prompt_key LIKE 'test/%'
   OR prompt_key = 'examples/sections-demo';

COMMIT;
