-- 0041_prompt_routing_tests.sql — ops safety net for tag edits.
--
-- Every time we add/remove/tighten a prompt_template tag, we risk silently
-- stealing traffic from or to other agents. This table stores
-- (input, expected_prompt_key) pairs operators can edit; router/runTests
-- feeds each `input` through the live RuleRouter + prompt_templates and
-- asserts the resulting prompt_key matches `expected_prompt_key`.
--
-- Seed covers all 11 agents from migration 0040 with at least one positive
-- test per category plus a few disambiguation cases (things that SHOULD
-- NOT match a given specialist).

BEGIN;

CREATE TABLE IF NOT EXISTS public.prompt_routing_tests (
    id                  BIGSERIAL PRIMARY KEY,
    input               TEXT NOT NULL,
    expected_prompt_key TEXT NOT NULL,
    note                TEXT NOT NULL DEFAULT '',
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Unique per input so reseeds UPDATE rather than accumulate duplicates.
CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_routing_tests_input
    ON public.prompt_routing_tests (input);

-- Seed baseline tests. Keep phrases realistic (what a user actually types),
-- not synthetic 'test_XX' strings.
INSERT INTO public.prompt_routing_tests (input, expected_prompt_key, note) VALUES

-- code-review
('帮我 review 这段代码',             'main/code-review', 'review 显式'),
('看看这段代码有没有问题',            'main/code-review', '看看这段代码'),
('code review this function please', 'main/code-review', 'EN 正式'),

-- code-debug
('为什么这里报错了',                 'main/code-debug',  '为什么报错'),
('stack trace 是这样的 ...',         'main/code-debug',  'stack trace'),
('debug 一下这个 panic',             'main/code-debug',  'panic + debug'),

-- code-task
('帮我写一个计算斐波那契的函数',      'main/code-task',   '写一个函数'),
('重构这段逻辑让它更简洁',            'main/code-task',   '重构'),
('解释这段代码做了什么',              'main/code-task',   '解释这段'),
('写测试覆盖边界情况',                'main/code-task',   '写测试'),

-- sql
('帮我写 SQL 查询订单表',            'main/sql',         '写 SQL'),
('EXPLAIN 这个查询计划',              'main/sql',         'EXPLAIN'),
('给 users 表加个索引',               'main/sql',         '加个索引'),

-- writing
('帮我写一封辞职邮件',                'main/writing',     '写邮件'),
('润色一下这段文案',                  'main/writing',     '润色'),
('draft a release announcement',     'main/writing',     'EN draft'),

-- translate
('把这段翻译成英文',                  'main/translate',   '翻译'),
('translate to Simplified Chinese',  'main/translate',   'EN 翻译'),
('帮我翻译一下这个术语',              'main/translate',   '帮我翻译'),

-- research
('什么是事件溯源',                    'main/research',    '什么是'),
('总结一下这篇论文的要点',            'main/research',    '总结'),
('对比一下 PostgreSQL 和 MySQL',      'main/research',    '对比'),

-- brainstorm
('给我的猫起个名字',                  'main/brainstorm',  '起名'),
('brainstorm 几个营销方案',           'main/brainstorm',  'brainstorm'),
('想几个有创意的标题',                'main/brainstorm',  '想个标题'),

-- planning
('帮我规划下个季度的 OKR',            'main/planning',    '帮我规划'),
('制定一个三步走的实施计划',          'main/planning',    '制定计划'),

-- orchestrator
('orchestrate a multi-agent task',   'main/orchestrator', 'orchestrate'),
('拆分任务并分配给多个 agent',        'main/orchestrator', '拆分任务'),

-- default (fallback) — these should NOT match any specialist
('今天天气真好',                      'main/default',     '闲聊'),
('你好',                              'main/default',     '简单招呼'),
('我想吃午饭了',                      'main/default',     '日常话题')

ON CONFLICT (input) DO UPDATE SET
    expected_prompt_key = EXCLUDED.expected_prompt_key,
    note = EXCLUDED.note,
    updated_at = now();

COMMIT;
