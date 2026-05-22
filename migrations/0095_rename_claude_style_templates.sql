BEGIN;

-- 1. Rename Claude-style template keys to provider-neutral general keys.
-- Rename even when the template was manually edited: prompt_key is runtime
-- identity, while user-authored prompt text/sections remain attached to the
-- same row. Guard only on destination absence to avoid unique-key collisions.
UPDATE public.prompt_templates
   SET prompt_key = 'main/general-en',
       updated_by = 'system.seed',
       updated_at = NOW()
 WHERE prompt_key = 'main/claude-style'
   AND NOT EXISTS (
       SELECT 1 FROM public.prompt_templates WHERE prompt_key = 'main/general-en'
   );

UPDATE public.prompt_templates
   SET prompt_key = 'main/general-zh',
       updated_by = 'system.seed',
       updated_at = NOW()
 WHERE prompt_key = 'main/claude-style-zh'
   AND NOT EXISTS (
       SELECT 1 FROM public.prompt_templates WHERE prompt_key = 'main/general-zh'
   );

-- 2. Keep the UI preference in sync. value is jsonb, so wrap text via to_jsonb.
UPDATE public.ui_preferences
   SET value = to_jsonb('main/general-zh'::text)
 WHERE key = 'settings.activePromptKey'
   AND value = to_jsonb('main/claude-style-zh'::text)
   AND EXISTS (
       SELECT 1 FROM public.prompt_templates WHERE prompt_key = 'main/general-zh'
   );

UPDATE public.ui_preferences
   SET value = to_jsonb('main/general-en'::text)
 WHERE key = 'settings.activePromptKey'
   AND value = to_jsonb('main/claude-style'::text)
   AND EXISTS (
       SELECT 1 FROM public.prompt_templates WHERE prompt_key = 'main/general-en'
   );

-- 3. Neutralize identity sections. zh/en source text differs, so replace separately.
UPDATE public.prompt_template_sections s
   SET body = REPLACE(
           body,
           '你不是 Claude、Claude Code 或任何 Anthropic 产品',
           '你不是 Claude / GPT / Codex 或任何底层模型产品；无论底层用什么模型，你都是 Super-Dolphin'
       ),
       updated_at = NOW()
  FROM public.prompt_templates t
 WHERE t.id = s.template_id
   AND s.section_key = 'identity'
   AND t.prompt_key = 'main/general-zh';

UPDATE public.prompt_template_sections s
   SET body = REPLACE(
           body,
           'You are a Claude agent, built on Anthropic''s Claude Agent SDK. You are an interactive agent that helps users with software engineering tasks.',
           'You are Super-Dolphin, an interactive agent that helps users with software engineering tasks; whatever the underlying model, you are Super-Dolphin.'
       ),
       updated_at = NOW()
  FROM public.prompt_templates t
 WHERE t.id = s.template_id
   AND s.section_key = 'identity'
   AND t.prompt_key = 'main/general-en';

-- 4. Re-seed main/default fallback from 0091 in case production had deleted it.
INSERT INTO public.prompt_templates (
    prompt_key,
    title,
    agent_key,
    tool_name,
    prompt_text,
    variables,
    tags,
    description,
    enabled,
    match_when,
    priority,
    manually_edited,
    created_by,
    updated_by,
    created_at,
    updated_at
) VALUES (
    'main/default',
    '通用助手 (兜底)',
    'main',
    '',
$prompt$你是通用助手，能处理编程和非编程任务。这是系统兜底提示词，在用户没有 pin 模板、没有指定 agent_key、match_when 自动路由也没有命中时生效。

工作约定：
- 保持直接：答 "不知道" 好过编造。
- 动手前先读相关文件 / 信息，不凭记忆。
- 遇到歧义主动追问，不擅自扩大用户需求。
- 用中文沟通，除非用户用英文；代码 / 注释 / 日志保留原语言。
- 控制篇幅：用户问什么答什么，不堆砌；能一句说完的不用三句。
- 完成前验证：跑测试 / 检查输出再说"完成"；做不到就明说"未验证"。$prompt$,
    '{}'::jsonb,
    '["main","default","fallback"]'::jsonb,
    '系统兜底 fallback - 无 pin / 无 agent_key / match_when 自动路由未命中时使用。match_when=NULL 表示不参与自动路由竞争，专做 pickRoutedTemplate 终极兜底。',
    TRUE,
    NULL,
    0,
    FALSE,
    'system.seed',
    'system.seed',
    NOW(),
    NOW()
)
ON CONFLICT (prompt_key) DO UPDATE SET
    title           = EXCLUDED.title,
    agent_key       = EXCLUDED.agent_key,
    tool_name       = EXCLUDED.tool_name,
    prompt_text     = EXCLUDED.prompt_text,
    variables       = EXCLUDED.variables,
    tags            = EXCLUDED.tags,
    description     = EXCLUDED.description,
    enabled         = EXCLUDED.enabled,
    match_when      = EXCLUDED.match_when,
    priority        = EXCLUDED.priority,
    manually_edited = EXCLUDED.manually_edited,
    updated_by      = EXCLUDED.updated_by,
    updated_at      = NOW()
WHERE public.prompt_templates.manually_edited = FALSE;

COMMIT;
