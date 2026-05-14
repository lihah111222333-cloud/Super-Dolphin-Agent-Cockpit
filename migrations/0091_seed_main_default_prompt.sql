-- 0091_seed_main_default_prompt.sql — 恢复 main/default 兜底模板（任务 P5）。
--
-- 背景：
--   internal/module/thread/router_resolve.go 行 145 定义
--     const defaultPromptKey = "main/default"
--   作为 pickRoutedTemplate 的终极兜底：当 PromptKey 没 pin、AgentKey 没 pin、
--   classifier 没匹配、match_when auto-route 也没匹配时，findByPromptKey
--   会去找 main/default 这行。
--
--   历史 migration 0040 曾经 seed 过 main/default，但当前生产 DB 里这一行已被
--   删除（可能 UI 误删 / 后续手工运维移除）。其缺失被 main/claude-style 的
--   match_when='{}' auto-route 命中所遮蔽，未在线上暴露，但终极 fallback 设计已断裂：
--   一旦 main/claude-style 也被禁用 / 删除，router 会 return nil，CLI 回退到
--   自带 system prompt —— 与"硬编码兜底 persona"的契约不符。
--
--   本 migration 重新插入 main/default，把契约修回去。
--
-- 设计要点：
--   - match_when = NULL（不是 '{}'）：让 main/default 退出 match_when auto-route
--     候选池（autoRouteCandidates 在 router_resolve.go 行 466 用 len(MatchWhen)==0
--     过滤 NULL 行）。fallback 池让 main/claude-style 单独负责；main/default
--     专做 pickRoutedTemplate 路径的兜底。这一步**反转**了 0054 引入的
--     match_when='{}' 设置 —— 因为 0054 是把 main/default 加入 auto-route，
--     而现在的设计倾向把这两个路径分开。
--   - agent_key = 'main'：与 0040 历史 seed 保持一致；pickRoutedTemplate 命中
--     时会把这值 stamp 到 req.AgentKey，给下游观测使用。
--   - priority = 0：兜底用不到 priority 排序，但 0 表明这是 baseline。
--   - enabled = TRUE：默认开启。
--   - prompt_text：参考 0040 历史版本的"通用工程助手"语气，轻量几百字。
--     不复用 0057 的 5KB Claude 风格长文 —— 那是 main/claude-style 专用。
--     此条只在 fallback 路径生效，简洁即可；具体 section 内容由 0051 的
--     prompt_template_sections 提供（identity / engineering_principles /
--     risky_actions / tone_style / orchestrator_context 等）。
--   - tags = ["main","default","fallback"]：纯元数据，分类用。
--   - manually_edited = FALSE：与 0086/0087 约定一致，标记为 seed 管理。
--
-- 幂等：
--   ON CONFLICT (prompt_key) DO UPDATE，且 WHERE manually_edited = FALSE
--   保护人工编辑过的行不被覆盖（与 0087 同样的策略）。
--
-- 影响面：
--   - 0051_seed_main_default_sections.sql 假设 main/default 存在；之前可能
--     silently 跑空（其 SELECT id FROM ... WHERE prompt_key='main/default'
--     返回空集，INSERT ... SELECT 自然不插入任何 section）。本 migration
--     落地后，如果运维想要补这些 section，需要手工重跑 0051 或者写新 migration。
--     本条不主动重跑 0051 —— 让 section 补齐独立决策。
--   - 0054_seed_main_default_match_when.sql 之前的 UPDATE 对当前 DB 是 noop
--     （行不存在）。新 DB 从头 apply 时，0054 会把 main/default 设成
--     match_when='{}'，然后本 migration 通过 ON CONFLICT 把它改回 NULL；
--     最终状态与本设计一致。

BEGIN;

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
$prompt$你是通用助手，能处理编程和非编程任务。这是系统兜底提示词，在用户没有 pin 模板、没有指定 agent_key、分类器和 match_when 自动路由也都没有命中时生效。

工作约定：
- 保持直接：答 "不知道" 好过编造。
- 动手前先读相关文件 / 信息，不凭记忆。
- 遇到歧义主动追问，不擅自扩大用户需求。
- 用中文沟通，除非用户用英文；代码 / 注释 / 日志保留原语言。
- 控制篇幅：用户问什么答什么，不堆砌；能一句说完的不用三句。
- 完成前验证：跑测试 / 检查输出再说"完成"；做不到就明说"未验证"。$prompt$,
    '{}'::jsonb,
    '["main","default","fallback"]'::jsonb,
    '系统兜底 fallback — 无 pin / 无 agent_key / 分类器与 match_when 自动路由全部未命中时使用。match_when=NULL 表示不参与自动路由竞争，专做 pickRoutedTemplate 终极兜底。',
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
