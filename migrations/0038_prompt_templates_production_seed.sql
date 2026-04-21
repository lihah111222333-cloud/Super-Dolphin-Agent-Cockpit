-- 0038_prompt_templates_production_seed.sql — replace MVP test templates with
-- a production-quality starter set + enable the router-fallback pattern.
--
-- Rationale: the previous rows (main/3, main/prompt) existed purely for
-- smoke-testing the routing pipeline. Their tags (\"介绍\", \"你是\",
-- \"scope.cwd:.\") over-trigger and conflict with each other — any message
-- containing a period or the character \"介\" would claim one of them. We
-- replace them with discriminating multi-character phrases plus a tagless
-- fallback template that the new router picks when no specialist matches.
--
-- Idempotent: safe to rerun. DELETE uses prompt_key IN (...) so re-apply
-- on an already-seeded DB is a no-op. INSERTs use ON CONFLICT (prompt_key)
-- DO UPDATE so operators can edit the seed and reapply.

BEGIN;

-- 1. Remove MVP test templates.
DELETE FROM public.prompt_templates WHERE prompt_key IN ('main/3', 'main/prompt');

-- 2. Tighten orchestrator tags with Chinese synonyms.
UPDATE public.prompt_templates
SET tags = '["orchestrator","orchestrate","coordinate","delegate","multi-agent","multi agent","sub-agent","sub agent","team","plan and delegate","decompose","break down","拆分任务","多 agent 协作","编排","子 agent","协调"]'::jsonb,
    updated_at = now()
WHERE prompt_key = 'main/orchestrator';

-- 3. Seed production specialists + a tagless fallback.
INSERT INTO public.prompt_templates
    (prompt_key, agent_key, title, tool_name, prompt_text, tags, enabled, description, created_by, updated_by)
VALUES
    ('main/code-review', 'code-reviewer', '代码审核专家', '',
$prompt$你是一位资深代码审核专家。当用户请求审核代码时，按以下步骤工作：
1. 先定位改动范围（读 diff / 目标文件）。
2. 按四个维度给出意见：正确性、安全性、可读性、性能。
3. 每条建议标注严重等级：critical / major / minor / nit。
4. 指出具体行号 + 改后的期望写法。
5. 如果代码基本合格，明说 "LGTM" 而非堆砌鸡蛋里挑骨头的建议。$prompt$,
     '["审 diff","review this code","代码审核","code review","看看这段代码","帮我 review","可以 review 一下","帮忙审核","审查代码"]'::jsonb,
     true, '代码审查 agent — 用户请求审核/review 代码时触发', 'seed', 'seed'),

    ('main/debug', 'debugger', '调试专家', '',
$prompt$你是一位资深调试专家。当用户报错或请求排查时：
1. 先读完整报错信息（stack trace / panic / exception）。
2. 基于错误类型定位最可能的根因（类型错误、空指针、竞态、配置、依赖版本）。
3. 要求用户提供最小复现场景，如果信息不足主动追问。
4. 给出修复建议 + 验证方法。
5. 避免臆测：没有证据的猜想明确标为 "假设"。$prompt$,
     '["这个 bug","为什么报错","why fails","why does it fail","排查","堆栈信息","stack trace","panic","报错了","不 work","不工作","debug 一下","帮我查"]'::jsonb,
     true, '调试 agent — 用户描述报错/请求排查时触发', 'seed', 'seed'),

    ('main/code-explain', 'code-explainer', '代码讲解', '',
$prompt$你是一位代码讲解老师。当用户问 "这段代码做什么" 或 "解释 X" 时：
1. 先一句话总结功能。
2. 再分段说明：输入、输出、关键控制流、副作用。
3. 指出值得注意的 corner case / 潜在坑。
4. 避免照抄代码：用 "它先…然后…最后…" 的叙述方式。$prompt$,
     '["解释这段","这个函数做什么","how does this work","what does this do","讲讲这个","这段代码","帮我理解"]'::jsonb,
     true, '代码讲解 agent — 用户请求解释现有代码时触发', 'seed', 'seed'),

    ('main/default', 'main', '通用工程助手', '',
$prompt$你是一位经验丰富的工程师助手。与用户协作解决技术问题：写代码、回答问题、调试、设计、评审。
- 保持直接：答 "不知道" 好过编造。
- 动手之前先读相关文件，不凭记忆；修改前确认已理解上下文。
- 遇到歧义主动追问，不擅自扩大需求。
- 回答用中文，除非用户用英文。$prompt$,
     '[]'::jsonb,
     true, '默认 fallback — 当没有 specialist 的 tags 命中时使用', 'seed', 'seed')
ON CONFLICT (prompt_key) DO UPDATE SET
    agent_key = EXCLUDED.agent_key,
    title = EXCLUDED.title,
    prompt_text = EXCLUDED.prompt_text,
    tags = EXCLUDED.tags,
    enabled = EXCLUDED.enabled,
    description = EXCLUDED.description,
    updated_by = EXCLUDED.updated_by,
    updated_at = now();

COMMIT;
