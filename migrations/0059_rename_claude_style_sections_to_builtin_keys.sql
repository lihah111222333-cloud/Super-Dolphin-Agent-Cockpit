-- 0059_rename_claude_style_sections_to_builtin_keys.sql —
-- main/claude-style 的 section_key 对齐内置 SectionIdentity/SectionSystemConstraints/
-- SectionEngineering/SectionActions/SectionToolPreferences/SectionStyle/SectionOutputEfficiency
-- 常量。mergeTemplateSections 按 key 命中内置 → 替换；否则 "tpl:<key>" 追加。
--
-- - engineering_principles → engineering
-- - actions_with_care      → actions
-- - tone_and_style         → style
-- - security_policy 被删除（内置 identity 段已经包含 cyber risk + URL 禁令）。

BEGIN;

-- 拿到 template_id，后续几条更新都复用。
WITH t AS (
    SELECT id FROM public.prompt_templates WHERE prompt_key = 'main/claude-style'
)
UPDATE public.prompt_template_sections s
   SET section_key = CASE s.section_key
       WHEN 'engineering_principles' THEN 'engineering'
       WHEN 'actions_with_care'      THEN 'actions'
       WHEN 'tone_and_style'         THEN 'style'
   END,
       updated_at = NOW()
  FROM t
 WHERE s.template_id = t.id
   AND s.section_key IN ('engineering_principles','actions_with_care','tone_and_style');

-- security_policy 与内置 identity 内容重复，直接删除。
DELETE FROM public.prompt_template_sections s
 USING (SELECT id FROM public.prompt_templates WHERE prompt_key = 'main/claude-style') t
 WHERE s.template_id = t.id
   AND s.section_key = 'security_policy';

COMMIT;
