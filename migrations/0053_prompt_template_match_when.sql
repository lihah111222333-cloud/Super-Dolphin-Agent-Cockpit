-- 0053_prompt_template_match_when.sql — 模板级自动路由规则
--
-- 新增列:
--   match_when JSONB  ← 模板级"路由匹配规则"，与 section 级 enable_when 区分
--   priority   INT    ← 多条匹配时 tie-break 排序键（降序）
--
-- 语义（决策 3 = a，显式 opt-in）:
--   match_when IS NULL  → 不参与自动路由（只能 pin / 分类器命中）
--   match_when = '{}'   → 永远匹配（参与竞争，但无筛选条件）
--   match_when = JSON   → 当 BuildCtx 满足所有键时匹配
--
-- 路由新一档（在分类器与 main/default 兜底之间）:
--   router 遍历 enabled && match_when 非空的模板
--   按 priority DESC 排序
--   对每条评估 match_when（复用/扩展 EvaluateEnableWhen）
--   首个命中的 → 用这条；都不命中 → 回退 main/default。

ALTER TABLE prompt_templates
    ADD COLUMN IF NOT EXISTS match_when JSONB,
    ADD COLUMN IF NOT EXISTS priority   INT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_prompt_templates_auto_route
    ON prompt_templates (enabled, priority DESC)
    WHERE match_when IS NOT NULL;
