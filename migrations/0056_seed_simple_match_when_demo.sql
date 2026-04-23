-- 0056_seed_simple_match_when_demo.sql — 最简 match_when 演示
--
-- 玩法：
--   1. 不「设为启动」、不打开「智能启动分类器」
--   2. 开一个新 thread（什么都不打也行）
--   3. 因为 test/auto-high 的 priority=20 > test/auto-low 的 priority=10，
--      系统会挑 test/auto-high，AI 的第一句话一定以「[高优先级]」开头
--   4. 去侧边栏「提示词」编辑 test/auto-high，把优先级改成 5（低于 10）保存
--   5. 再开新 thread → AI 第一句变成「[低优先级]」，说明切换生效
--
-- 两个模板都用 match_when={}，代表「任何场景都参与候选」，完全不依赖 cwd /
-- language 之类的环境变量，方便用户肉眼看到 priority 控制的切换。

BEGIN;

INSERT INTO public.prompt_templates
    (prompt_key, agent_key, title, tool_name, prompt_text, tags, enabled,
     description, match_when, priority, created_by, updated_by)
VALUES (
    'test/auto-high',
    'main',
    '测试 · 自动路由 高优先级',
    '',
    '你被 match_when 自动路由选中，因为你的 priority 在所有候选里最高。回复用户的第一句话必须以「[高优先级]」开头，然后正常协助。',
    '["test","match_when","demo"]'::jsonb,
    TRUE,
    '[测试] match_when={}，priority=20。默认比 test/auto-low 先被选中。',
    '{}'::jsonb,
    20,
    'test-seed',
    'test-seed'
)
ON CONFLICT (prompt_key) DO UPDATE SET
    title       = EXCLUDED.title,
    prompt_text = EXCLUDED.prompt_text,
    tags        = EXCLUDED.tags,
    description = EXCLUDED.description,
    match_when  = EXCLUDED.match_when,
    priority    = EXCLUDED.priority,
    enabled     = TRUE,
    updated_at  = NOW();

INSERT INTO public.prompt_templates
    (prompt_key, agent_key, title, tool_name, prompt_text, tags, enabled,
     description, match_when, priority, created_by, updated_by)
VALUES (
    'test/auto-low',
    'main',
    '测试 · 自动路由 低优先级',
    '',
    '你被 match_when 自动路由选中，因为高优先级那条被关掉 / 改低了，轮到你兜底。回复用户的第一句话必须以「[低优先级]」开头，然后正常协助。',
    '["test","match_when","demo"]'::jsonb,
    TRUE,
    '[测试] match_when={}，priority=10。高优先级被改低 / 禁用时这条接管。',
    '{}'::jsonb,
    10,
    'test-seed',
    'test-seed'
)
ON CONFLICT (prompt_key) DO UPDATE SET
    title       = EXCLUDED.title,
    prompt_text = EXCLUDED.prompt_text,
    tags        = EXCLUDED.tags,
    description = EXCLUDED.description,
    match_when  = EXCLUDED.match_when,
    priority    = EXCLUDED.priority,
    enabled     = TRUE,
    updated_at  = NOW();

COMMIT;
