-- 0050_seed_prompt_template_sections_example.sql — reference seed for the
-- Step 1/2/3b sectioned prompt layout.
--
-- Goal: after running this migration, operators see an end-to-end working
-- example of how prompt_template_sections should be populated, including:
--   - region='static'  blocks that flow into --system-prompt (cached prefix)
--   - region='dynamic' blocks that flow into --append-system-prompt (uncached tail)
--   - enable_when gates (language / isWorktree / sessionFlags.<name>)
--
-- Safety:
--   - Creates a dedicated demo template ('examples/sections-demo') so that
--     production personas (main/default, main/orchestrator, specialists) are
--     NOT touched. Remove the demo row if you don't want it live-routable.
--   - Idempotent: ON CONFLICT DO NOTHING keeps operator edits intact on
--     re-apply; section UNIQUE(template_id, section_key) ensures sections
--     dedup too.
--   - The demo template ships with enabled=FALSE to keep it out of router
--     candidate pools until an operator flips it on deliberately.
--
-- Depends on: 0049_prompt_template_sections.sql

BEGIN;

-- 1) Demo template row (disabled by default).
INSERT INTO public.prompt_templates
    (prompt_key, agent_key, title, tool_name, prompt_text, tags, enabled,
     description, created_by, updated_by)
VALUES (
    'examples/sections-demo',
    'sections-demo',
    'Sections 示例 (参考模板)',
    '',
    -- Legacy prompt_text kept as a fallback. When sections exist, the router
    -- prefers them and this string is ignored; when someone drops all sections
    -- the template still produces something routable instead of empty prompt.
    'This is the legacy prompt_text fallback. When prompt_template_sections has rows for this template, the assembler uses those instead.',
    '["sections","demo","example"]'::jsonb,
    FALSE,
    'Step 1/2/3b 参考模板：展示 section 拆分、region 语义、enable_when 三种常见写法。默认禁用，operator 对照抄袭即可。',
    'system',
    'system'
)
ON CONFLICT (prompt_key) DO NOTHING;

-- 2) Four reference sections. template_id is resolved by sub-select so the
--    migration works regardless of the auto-generated primary key.

-- 2a. Static identity — always injected, flows into --system-prompt
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'identity', 'static', 0,
    'You are a demonstration agent for the sectioned prompt layout. Your only job is to confirm that the injection pipeline works.',
    NULL,
    TRUE
FROM public.prompt_templates WHERE prompt_key = 'examples/sections-demo'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- 2b. Static tool preferences — always injected, later ordinal keeps it after
--     identity within the cached prefix.
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'tool_preferences', 'static', 10,
    'Prefer the FileReadTool over shell `cat` when available. Parallelize independent tool calls.',
    '{}'::jsonb,  -- explicit empty object = always inject (same as NULL)
    TRUE
FROM public.prompt_templates WHERE prompt_key = 'examples/sections-demo'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- 2c. Dynamic worktree reminder — only when BuildCtx.IsWorktree is true.
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'worktree_reminder', 'dynamic', 0,
    'Heads up: you are running inside a git worktree. Do not `cd` back to the primary checkout or you will break the isolation contract.',
    '{"isWorktree": true}'::jsonb,
    TRUE
FROM public.prompt_templates WHERE prompt_key = 'examples/sections-demo'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- 2d. Dynamic Chinese-only greeting — AND across two fields (language + flag).
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'zh_debug_banner', 'dynamic', 10,
    '调试模式已开启。所有工具调用会被记录到 scratchpad 目录。',
    '{"language": "zh", "sessionFlags.debug": true}'::jsonb,
    TRUE
FROM public.prompt_templates WHERE prompt_key = 'examples/sections-demo'
ON CONFLICT (template_id, section_key) DO NOTHING;

COMMIT;
