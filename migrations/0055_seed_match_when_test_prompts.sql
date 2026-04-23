-- 0055_seed_match_when_test_prompts.sql — 两个演示 match_when 自动路由的测试模板
--
-- 目的：
--   - 侧边栏 prompts 里能看到（都挂在"主 Agent" tab）
--   - 不「设为启动」、不打开「智能启动分类器」，直接开新 thread，
--     系统会按 match_when 自动在这两个里挑一个（priority DESC）
--   - 用户可以通过切换 cwd / language 来观察自动路由切换命中的模板
--
-- 两个模板分别用两种常见触发方式：
--   test/match-by-cwd      cwd_prefix 匹配 agnet 工作区
--   test/match-by-language language=zh 时匹配
--
-- 幂等：ON CONFLICT 时只刷新元信息 + 重新打开 match_when / priority。

BEGIN;

-- ═══════════════════════════════════════════════════════════════════════
--  模板 A · test/match-by-cwd  (cwd_prefix 触发)
-- ═══════════════════════════════════════════════════════════════════════
-- 触发验证：
--   1. 不打开"智能启动分类器"、不"设为启动"任何模板
--   2. 开新 thread，CWD 在 /Users/mac/Desktop/agnet 下
--   3. 路由命中 test/match-by-cwd；AI 第一句会说"[CWD 匹配命中]"
--   4. 改到其它路径（比如 /tmp 下）再开新 thread → 命中 language=zh
--      的 test/match-by-language 或 main/default 兜底
-- ═══════════════════════════════════════════════════════════════════════
INSERT INTO public.prompt_templates
    (prompt_key, agent_key, title, tool_name, prompt_text, tags, enabled,
     description, match_when, priority, created_by, updated_by)
VALUES (
    'test/match-by-cwd',
    'main',
    '测试模板 · CWD 自动路由',
    '',
    '你被 match_when 路由因为当前工作目录匹配 /Users/mac/Desktop/agnet 前缀。回复用户的第一句话必须以"[CWD 匹配命中]"开头，以便用户观测路由效果。然后正常协助。',
    '["test","match_when","cwd"]'::jsonb,
    TRUE,
    '[测试 · match_when] 当 CWD 以 /Users/mac/Desktop/agnet 开头时自动命中；priority=100 先于 language 规则。',
    '{"cwd_prefix":"/Users/mac/Desktop/agnet"}'::jsonb,
    100,
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

-- ═══════════════════════════════════════════════════════════════════════
--  模板 B · test/match-by-language  (language=zh 触发)
-- ═══════════════════════════════════════════════════════════════════════
-- 触发验证：
--   1. 不打开"智能启动分类器"、不"设为启动"任何模板
--   2. CWD 不在 agnet 前缀下（比如 /tmp），确保不被模板 A 抢先
--   3. 开新 thread，StartRequest.language=zh（UI 默认值）
--   4. 路由命中 test/match-by-language；AI 第一句会说"[语言匹配命中]"
--   5. 改 language=en 再开新 thread → 所有 match_when 都不命中 → 走 main/default
-- ═══════════════════════════════════════════════════════════════════════
INSERT INTO public.prompt_templates
    (prompt_key, agent_key, title, tool_name, prompt_text, tags, enabled,
     description, match_when, priority, created_by, updated_by)
VALUES (
    'test/match-by-language',
    'main',
    '测试模板 · 语言自动路由',
    '',
    '你被 match_when 路由因为 BuildCtx.language="zh"。回复用户的第一句话必须以"[语言匹配命中]"开头，以便用户观测路由效果。然后正常协助，全程用中文。',
    '["test","match_when","language"]'::jsonb,
    TRUE,
    '[测试 · match_when] 当 language=zh 时自动命中；priority=50 低于 CWD 规则，CWD 命中时这条会让位。',
    '{"language":"zh"}'::jsonb,
    50,
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
