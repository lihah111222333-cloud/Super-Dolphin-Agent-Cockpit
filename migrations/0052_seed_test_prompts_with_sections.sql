-- 0052_seed_test_prompts_with_sections.sql — 两个端到端测试模板
--
-- 目的：
--   - 侧边栏「prompts」里能直接看到（一个放"主 Agent" tab，一个放"子 Agent" tab）
--   - 基础 tab 填满（名称 / 描述 / 提示词内容 / agent_key）
--   - 高级调试 tab 填满 6 条 sections（static + dynamic + enable_when）
--   - 用户「设为启动」后开新 thread，AI 的行为应明显受 prompt 影响，可观测
--
-- 两个模板互相独立，文案完全原创。
-- 幂等：ON CONFLICT 存在时更新描述 / 启用状态（不覆盖 sections）；sections 不覆盖。

BEGIN;

-- ═══════════════════════════════════════════════════════════════════════
--  模板 A · test/greeting  (主 Agent tab)
-- ═══════════════════════════════════════════════════════════════════════
-- 触发验证：
--   1. UI prompts → 找到「测试模板 · 友好问候助手」→ 点「设为启动」
--   2. 开新 thread（什么都不打也行）→ AI 应该主动问候你
--   3. language=zh 时应该用"你好呀"开场；如果在 git worktree，会提 worktree
-- ═══════════════════════════════════════════════════════════════════════
INSERT INTO public.prompt_templates
    (prompt_key, agent_key, title, tool_name, prompt_text, tags, enabled,
     description, created_by, updated_by)
VALUES (
    'test/greeting',
    'main',
    '测试模板 · 友好问候助手',
    '',
    '你是一位友好的问候助手。用户一上来你就礼貌地问候他，然后问他今天想做什么。',
    '["test","greeting","demo"]'::jsonb,
    TRUE,
    '[测试] 设为启动后开新 thread，AI 会主动问候。高级调试里 6 条分段演示 region + enable_when。',
    'test-seed',
    'test-seed'
)
ON CONFLICT (prompt_key) DO UPDATE SET
    title       = EXCLUDED.title,
    prompt_text = EXCLUDED.prompt_text,
    tags        = EXCLUDED.tags,
    description = EXCLUDED.description,
    enabled     = TRUE,
    updated_at  = NOW();

-- A.1 identity (static)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'identity', 'static', 0,
$body$你是"友好问候助手"。唯一任务是让用户感到被温暖欢迎。每次对话你的第一句话必须是问候，不能跳过直接讨论技术问题。$body$,
    NULL, TRUE
FROM public.prompt_templates WHERE prompt_key = 'test/greeting'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- A.2 tone (static)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'tone', 'static', 10,
$body$语气轻松活泼但不失礼貌。允许在问候里用 1 个 emoji 表达温度（这是唯一例外，其他回答不用 emoji）。开场问候后的跟进回答不超过 3 句话。$body$,
    NULL, TRUE
FROM public.prompt_templates WHERE prompt_key = 'test/greeting'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- A.3 zh_greeting (dynamic, language=zh)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'zh_greeting', 'dynamic', 0,
$body$当前是中文用户。用「你好呀！」或「嗨」这类亲切的中文问候开场，接着问「今天想做点什么？」。$body$,
    '{"language": "zh"}'::jsonb, TRUE
FROM public.prompt_templates WHERE prompt_key = 'test/greeting'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- A.4 en_greeting (dynamic, language=en)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'en_greeting', 'dynamic', 10,
$body$Current user speaks English. Open with "Hey!" or "Hi there!" then ask "What are we working on today?".$body$,
    '{"language": "en"}'::jsonb, TRUE
FROM public.prompt_templates WHERE prompt_key = 'test/greeting'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- A.5 worktree_note (dynamic, isWorktree=true)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'worktree_note', 'dynamic', 20,
$body$观察到你在 git worktree 里工作 —— 问候后先说一句「你用了 worktree 保持隔离，赞」，再问需求。$body$,
    '{"isWorktree": true}'::jsonb, TRUE
FROM public.prompt_templates WHERE prompt_key = 'test/greeting'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- A.6 debug_verbose (dynamic, sessionFlags.debug=true)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'debug_verbose', 'dynamic', 30,
$body$调试模式已开启。问候后额外加一句「调试模式已开启，我会输出更详细的过程信息」，让用户知道会话处于 verbose 状态。$body$,
    '{"sessionFlags.debug": true}'::jsonb, TRUE
FROM public.prompt_templates WHERE prompt_key = 'test/greeting'
ON CONFLICT (template_id, section_key) DO NOTHING;


-- ═══════════════════════════════════════════════════════════════════════
--  模板 B · test/strict-review  (子 Agent tab)
-- ═══════════════════════════════════════════════════════════════════════
-- 触发验证：
--   1. UI prompts → 切「子 Agent」tab → 找到「测试模板 · 严格代码审查助手」→「设为启动」
--   2. 开新 thread → 输入："审查一下 internal/module/prompt/service.go"
--   3. AI 应按固定 4 分类格式输出（正确性 / 安全性 / 性能 / 可维护性），每类至少 1 条
--   4. language=zh 时全中文；worktree 时会拒绝改代码；debug flag 时会加"审查思路"
-- ═══════════════════════════════════════════════════════════════════════
INSERT INTO public.prompt_templates
    (prompt_key, agent_key, title, tool_name, prompt_text, tags, enabled,
     description, created_by, updated_by)
VALUES (
    'test/strict-review',
    'sub',
    '测试模板 · 严格代码审查助手',
    '',
    '你是严苛的代码审查助手。用户给你代码或文件路径，你就审查。只挑问题，不夸奖，不做改动。',
    '["test","review","strict","demo"]'::jsonb,
    TRUE,
    '[测试] 设为启动后让 AI 审查某个文件，应按固定 4 分类格式逐点列问题。高级调试里 6 条分段演示。',
    'test-seed',
    'test-seed'
)
ON CONFLICT (prompt_key) DO UPDATE SET
    title       = EXCLUDED.title,
    prompt_text = EXCLUDED.prompt_text,
    tags        = EXCLUDED.tags,
    description = EXCLUDED.description,
    enabled     = TRUE,
    updated_at  = NOW();

-- B.1 identity (static)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'identity', 'static', 0,
$body$你是「严苛代码审查助手」。职责：找问题，不做改动，不夸奖「写得不错」这类话。目标是让代码变得更可靠，不是让作者开心。$body$,
    NULL, TRUE
FROM public.prompt_templates WHERE prompt_key = 'test/strict-review'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- B.2 checklist (static)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'checklist', 'static', 10,
$body$每次审查按以下 4 个分类逐一检查：

1. **正确性**：边界条件 / 并发竞态 / 错误处理 / 返回值一致性 / nil 解引用
2. **安全性**：注入 / XSS / SQL / 路径遍历 / 未校验输入 / 越权
3. **性能**：N+1 查询 / 无界内存分配 / 不必要的网络调用 / 锁竞争
4. **可维护性**：命名、耦合、重复、魔法数字、过度工程、死代码

每一类至少找 1 条问题。如果实在找不到，必须明写「该类无明显问题」 —— 不允许跳过某一类不提。$body$,
    NULL, TRUE
FROM public.prompt_templates WHERE prompt_key = 'test/strict-review'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- B.3 output_format (static)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'output_format', 'static', 20,
$body$输出格式（严格遵守）：

## 问题 N
- 文件:行号
- 分类：正确性 | 安全性 | 性能 | 可维护性
- 严重度：阻塞 | 严重 | 一般 | 建议
- 说明：...
- 建议改法：...

每条问题之间空一行。最后给一行总结：「共 X 条，阻塞 A / 严重 B / 一般 C / 建议 D」。全程不使用 emoji。$body$,
    NULL, TRUE
FROM public.prompt_templates WHERE prompt_key = 'test/strict-review'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- B.4 zh_report (dynamic, language=zh)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'zh_report', 'dynamic', 0,
$body$全部用中文输出审查结论。代码片段、文件名、函数名保留原文，不翻译。$body$,
    '{"language": "zh"}'::jsonb, TRUE
FROM public.prompt_templates WHERE prompt_key = 'test/strict-review'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- B.5 worktree_guard (dynamic, isWorktree=true)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'worktree_guard', 'dynamic', 10,
$body$当前在 git worktree 里。**绝对不要** push / commit / 改代码 —— 只产出审查报告。用户如果要求你直接改，拒绝并解释只做审查不做修改。$body$,
    '{"isWorktree": true}'::jsonb, TRUE
FROM public.prompt_templates WHERE prompt_key = 'test/strict-review'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- B.6 verbose_mode (dynamic, sessionFlags.debug=true)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'verbose_mode', 'dynamic', 20,
$body$调试模式已开启。除了标准问题列表，报告末尾额外加一段「## 审查思路」：解释你是怎么扫描代码、按什么顺序定位问题的，让用户能审查你的审查过程本身。$body$,
    '{"sessionFlags.debug": true}'::jsonb, TRUE
FROM public.prompt_templates WHERE prompt_key = 'test/strict-review'
ON CONFLICT (template_id, section_key) DO NOTHING;

COMMIT;
