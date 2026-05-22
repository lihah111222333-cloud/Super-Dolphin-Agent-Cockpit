-- 0093: remove retired prompt_templates.match_when tags_has keyword routing.
BEGIN;

UPDATE public.prompt_templates
   SET match_when = NULLIF(match_when - 'tags_has', '{}'::jsonb),
       updated_by = 'system.seed',
       updated_at = NOW()
 WHERE match_when ? 'tags_has';

COMMIT;
