-- 0100_seed_recall_packs_and_when_to_use.sql — seed recall packs and expert guidance.
--
-- Depends on:
--   0094: prompt_templates.when_to_use
--   0095: main/claude-style-zh -> main/general-zh
--   0096: prompt_template_sections.trigger_type / recall_topic
--
-- No DB migration is applied by Codex in this task; this file is a forward
-- migration for the normal migration runner.

BEGIN;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM public.prompt_templates WHERE prompt_key = 'main/general-zh'
    ) THEN
        RAISE EXCEPTION '0100 requires prompt_template main/general-zh; apply 0095 first';
    END IF;

    IF NOT EXISTS (
        SELECT 1
          FROM public.prompt_template_sections s
          JOIN public.prompt_templates t ON t.id = s.template_id
         WHERE (t.prompt_key = 'main/general-zh' AND s.section_key = 'lsp_basics')
            OR (t.prompt_key = 'main/claude-style' AND s.section_key = 'lsp_basics_zh')
    ) THEN
        RAISE EXCEPTION '0100 requires LSP basics source section before seeding recall_lsp_basics';
    END IF;

    IF NOT EXISTS (
        SELECT 1
          FROM public.prompt_template_sections s
          JOIN public.prompt_templates t ON t.id = s.template_id
         WHERE (t.prompt_key = 'main/general-zh' AND s.section_key = 'lsp_advanced')
            OR (t.prompt_key = 'main/claude-style' AND s.section_key = 'lsp_advanced_zh')
    ) THEN
        RAISE EXCEPTION '0100 requires LSP advanced source section before seeding recall_lsp_advanced';
    END IF;
END $$;

-- Copy existing Chinese LSP guidance into recall packs. Missing prerequisites
-- fail above so schema_migrations cannot record 0100 without recall rows.
WITH target AS (
    SELECT id FROM public.prompt_templates WHERE prompt_key = 'main/general-zh'
),
source AS (
    SELECT s.body
      FROM public.prompt_template_sections s
      JOIN public.prompt_templates t ON t.id = s.template_id
     WHERE (t.prompt_key = 'main/general-zh' AND s.section_key = 'lsp_basics')
        OR (t.prompt_key = 'main/claude-style' AND s.section_key = 'lsp_basics_zh')
     ORDER BY CASE WHEN t.prompt_key = 'main/general-zh' THEN 0 ELSE 1 END
     LIMIT 1
)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled, trigger_type, recall_topic)
SELECT target.id, 'recall_lsp_basics', 'dynamic', 0, source.body, NULL::jsonb, TRUE, 'recall', 'lsp-basics'
  FROM target CROSS JOIN source
ON CONFLICT (recall_topic) WHERE trigger_type = 'recall' AND recall_topic <> '' DO NOTHING;

WITH target AS (
    SELECT id FROM public.prompt_templates WHERE prompt_key = 'main/general-zh'
),
source AS (
    SELECT s.body
      FROM public.prompt_template_sections s
      JOIN public.prompt_templates t ON t.id = s.template_id
     WHERE (t.prompt_key = 'main/general-zh' AND s.section_key = 'lsp_advanced')
        OR (t.prompt_key = 'main/claude-style' AND s.section_key = 'lsp_advanced_zh')
     ORDER BY CASE WHEN t.prompt_key = 'main/general-zh' THEN 0 ELSE 1 END
     LIMIT 1
)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled, trigger_type, recall_topic)
SELECT target.id, 'recall_lsp_advanced', 'dynamic', 0, source.body, NULL::jsonb, TRUE, 'recall', 'lsp-advanced'
  FROM target CROSS JOIN source
ON CONFLICT (recall_topic) WHERE trigger_type = 'recall' AND recall_topic <> '' DO NOTHING;

WITH target AS (
    SELECT id FROM public.prompt_templates WHERE prompt_key = 'main/general-zh'
),
seed(section_key, recall_topic, body) AS (
    VALUES
    ('recall_sqlc_workflow', 'sqlc-workflow', $body$SQLC 工作流：先改 schema/migration，再改 sql/queries/*.sql，最后运行 make sqlc-generate 和 make sqlc-verify。

注意事项：
- `sqlc.yaml` 的 schema 是显式清单；新增影响 sqlc 解析的 migration 后必须把文件加入 schema 列表。
- 不要手改 internal/store/sqlc 生成文件来“修”类型错误；回到源 SQL 或 migration 修正后重新生成。
- store 层新增查询时，同步 contract、querier interface、mapper、fake/stub 和同包测试。
- `make sqlc-verify` 会 regenerate 并比较 generated diff；在未提交工作区里如 generated 文件本身是本任务 diff，可用临时 index 验证源 SQL 与生成物一致。$body$),
    ('recall_prompt_template_editing', 'prompt-template-editing', $body$Prompt template 编辑：模板由 prompt_templates 元数据和 prompt_template_sections 分段组成，运行时优先使用 section 组装。

规则：
- `region='static'` 进入 cached prefix，适合稳定身份和工程约束；`region='dynamic'` 进入 uncached tail，适合随上下文变化的说明。
- `trigger_type='recall'` 的 section 只作为 prompt_recall 知识包，不应进入系统提示词正文；注入路径必须过滤 recall section。
- `enable_when` 是 section 级 gate；template 级 `match_when` 只负责自动路由，两者不要混用。
- 修改默认 prompt 或 section 后，重启 super-agent-debug 或触发 prompt assembly invalidation，避免观察到旧缓存。$body$),
    ('recall_frontend_vue3', 'frontend-vue3', $body$Vue 前端约定：优先沿用现有 composable、store、page 组织方式，避免把页面逻辑塞回超大 setup。

检查项：
- 改 UI 行为时同步更新对应 behavior test，尤其是 payload 字段、按钮状态和持久化 preference。
- 运行 `node scripts/size-guard.cjs`；函数超过 250 行、文件超过 800 行、嵌套过深都会被拦。
- `npm run build` 的 chunk size warning 不等于失败，但测试和 size guard 失败都必须修。
- 不要只靠截图判断状态；能用 vitest 锁住的交互优先写测试。$body$),
    ('recall_migration_rules', 'migration-rules', $body$Migration 规则：编号保持单调，不重编已出现的缺号；每个 migration 必须可重复运行或明确依赖前序状态。

写法：
- DDL 用 `IF NOT EXISTS` / `IF EXISTS`，数据 seed 用 `INSERT ... ON CONFLICT` 或 guarded `UPDATE`。
- 不要在 Codex 执行任务时直接 apply 生产/本地 DB migration，除非用户明确要求。
- 回滚说明要具体到 DELETE/UPDATE/ALTER 的反向动作。
- seed 数据不要覆盖用户手工编辑；需要更新时加 `WHERE manually_edited = FALSE` 或仅填空值。$body$),
    ('recall_guard_rules', 'guard-rules', $body$Guard 规则：仓库 guard 是完成条件，不是形式检查；失败即任务未完成，先定位根因再继续。

常见门槛：
- Go 生产文件默认不超过 600 行，函数不超过 80 行，圈复杂度不超过 10，包总行数不超过 10000。
- fix 类改动必须带同提交的回归测试、fixture、golden 或 snapshot。
- 不要用 `go run scripts/code_size_guard.go --freeze` 或降低阈值来通过任务，除非用户明确要求更新 guard。
- 先跑命中包测试，再跑 guard/build；报告中区分本次回归和仓库既有失败。$body$)
)
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled, trigger_type, recall_topic)
SELECT target.id, seed.section_key, 'dynamic', 0, seed.body, NULL::jsonb, TRUE, 'recall', seed.recall_topic
  FROM target CROSS JOIN seed
ON CONFLICT (recall_topic) WHERE trigger_type = 'recall' AND recall_topic <> '' DO NOTHING;

-- Seed concise guidance used by the available_experts dynamic section. Only
-- fill blanks so UI/user edits are preserved.
WITH seed(prompt_key, when_to_use) AS (
    VALUES
    ('coder/prompt', '代码任务、bug 修复、重构、测试编写、跨文件实现'),
    ('frontend', 'Vue 前端、交互状态、CSS 布局、前端测试与构建'),
    ('main/code-review', '代码审查、diff 风险评估、回归与安全问题检查'),
    ('main/code-debug', '错误排查、panic/exception/traceback 分析、最小复现定位'),
    ('main/code-task', '通用编程实现、重构、解释代码、补测试'),
    ('main/code-refactor', '代码重构、复杂度收敛、命名和结构调整'),
    ('main/code-explain', '解释现有代码、控制流、输入输出和边界行为'),
    ('main/code-generate', '新功能实现、接口设计、最小可行代码和测试'),
    ('main/code-test', '单元测试、集成测试、table-driven case、覆盖边界路径'),
    ('main/sql', 'SQL 查询、schema 设计、migration、索引、sqlc 工作流'),
    ('main/git-ops', 'Git diff/log/blame、commit message、冲突解决、revert/cherry-pick'),
    ('main/docs', 'README、API 文档、注释、changelog、技术文档结构化'),
    ('main/writing', '邮件、公告、文案、商务文档、中文润色'),
    ('main/translate', '中英翻译、本地化、技术术语翻译与多版本措辞'),
    ('main/research', '资料查询、概念解释、文章总结、方案对比'),
    ('main/brainstorm', '起名、创意发散、方案生成、假设挑战'),
    ('main/planning', '任务拆解、实施计划、里程碑、风险和依赖梳理'),
    ('main/morning_briefer', '把笔记、链接、任务和共享文件整理成晨报'),
    ('main/paper_summarizer', '研究论文摘要、方法、发现、局限和后续问题'),
    ('main/pr_summarizer', 'PR 变更摘要、行为影响、风险区域和 review 重点'),
    ('main/weekly_reviewer', '周报复盘、完成事项、决策、风险和下周优先级'),
    ('main/data_inspector', '数据样本检查、字段含义、异常值和质量问题归纳'),
    ('main/email_drafter', '邮件起草、回复、语气调整和收件人导向改写'),
    ('main/health_reporter', '系统健康、状态摘要、异常信号和行动建议'),
    ('main/learning_card', '把材料整理成学习卡片、概念解释和记忆提示'),
    ('main/note_organizer', '整理散乱笔记、归类主题、提取行动项和决策'),
    ('main/source_monitor', '监控来源更新、提取变化、标出风险和跟进项'),
    ('main/todo_prioritizer', '整理待办、排序优先级、识别依赖和阻塞'),
    ('main/topic_curator', '围绕主题筛选资料、组织阅读路径和关键问题'),
    ('main/trip_briefer', '行程摘要、旅行准备、风险提示和日程安排')
)
UPDATE public.prompt_templates p
   SET when_to_use = seed.when_to_use,
       updated_by = 'system.seed',
       updated_at = NOW()
  FROM seed
 WHERE p.prompt_key = seed.prompt_key
   AND p.enabled = TRUE
   AND BTRIM(p.when_to_use) = ''
   AND p.manually_edited = FALSE
   AND p.created_by IN ('system.seed', 'seed')
   AND (p.updated_by IN ('system.seed', 'seed', 'migration') OR p.updated_by LIKE 'system.%');

COMMIT;
