-- 0106_prompt_template_runtime_metadata.sql
--
-- Runtime metadata refresh for DB prompt templates. Task 5 owns only the DAG
-- designer subset; later 0106 owner tasks may append enterprise preset,
-- planning/review/debug, or roster repair metadata to this same migration.
--
-- Keep this migration to metadata, runtime scope tags, and small REPLACE
-- patches. Do not inline long prompt bodies after the 0104 builtin cutover.

BEGIN;

UPDATE public.prompt_templates t
SET prompt_text = REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(
        t.prompt_text,
        '"verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review", "cwd": "/absolute/path/to/project" }',
        '"verifier":   { "agent_key": "code-review", "cwd": "/absolute/path/to/project" }'
    ),
        E'    "provider": "claude",\n    "model": "opus",\n    "agent_key": "code-debug",',
        E'    "agent_key": "code-debug",'
    ),
        E'    "provider": "claude",\n    "model": "opus",',
        E'    "model": "<selected model from list_models()>",'
    ),
        '"escalation_chain": ["sonnet","opus"]',
        '"escalation_chain": []'
    ),
        '"verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review" }',
        '"verifier":   { "agent_key": "code-review" }'
    ),
        'list_models(provider="claude")',
        'list_models()'
    ),
        'model=sonnet',
        'model=<selected model from list_models()>'
    ),
        '`timeout` / `cancelled` / `unknown` / `not_implemented`',
        '`hard` / `needs_human` / `transient` / `quota`'
    ),
        'capability / validation / infrastructure / timeout / cancelled / unknown / not_implemented',
        'transient / quota / validation / capability / hard / needs_human / infrastructure'
    ),
    description = 'DAG 流程设计师：发现模型、提示词、命令卡和 sharedfile 资源，设计 cron、节点依赖、' ||
        'on_failure、to_node_result/to_sharedfile 输出边界，' ||
        '并写入 DAG。',
    when_to_use = '当用户要设计 DAG、定时任务、流程编排、节点依赖、' ||
        'cron 自动化或 sharedfile 输出边界时使用。',
    tags = (
        SELECT COALESCE(jsonb_agg(DISTINCT value), '[]'::jsonb)
        FROM (
            SELECT jsonb_array_elements_text(COALESCE(t.tags, '[]'::jsonb)) AS value
            UNION ALL
            SELECT 'scope.global'
            UNION ALL
            SELECT 'intent:dag_designer'
            UNION ALL
            SELECT 'workflow:dag'
            UNION ALL
            SELECT 'workflow:enterprise'
            UNION ALL
            SELECT 'io:sharedfile'
        ) merged
    ),
    updated_by = 'migration:0106',
    updated_at = NOW()
WHERE t.prompt_key = 'main/dag_designer_zh'
  AND t.created_by IN ('system.seed', 'seed')
  AND (
      t.updated_by IN ('system.seed', 'seed', 'migration')
      OR t.updated_by LIKE 'system.%'
      OR t.updated_by LIKE 'migration:%'
  )
  AND t.manually_edited = FALSE;

UPDATE public.prompt_templates t
SET prompt_text = REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(
        t.prompt_text,
        '"verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review", "cwd": "/absolute/path/to/project" }',
        '"verifier":   { "agent_key": "code-review", "cwd": "/absolute/path/to/project" }'
    ),
        E'    "provider": "claude",\n    "model": "opus",\n    "agent_key": "code-debug",',
        E'    "agent_key": "code-debug",'
    ),
        E'    "provider": "claude",\n    "model": "opus",',
        E'    "model": "<selected model from list_models()>",'
    ),
        '"escalation_chain": ["sonnet","opus"]',
        '"escalation_chain": []'
    ),
        '"verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review" }',
        '"verifier":   { "agent_key": "code-review" }'
    ),
        'list_models(provider="claude")',
        'list_models()'
    ),
        'model=sonnet',
        'model=<selected model from list_models()>'
    ),
        '`timeout` / `cancelled` / `unknown` / `not_implemented`',
        '`hard` / `needs_human` / `transient` / `quota`'
    ),
        'capability / validation / infrastructure / timeout / cancelled / unknown / not_implemented',
        'transient / quota / validation / capability / hard / needs_human / infrastructure'
    ),
    description = 'English DAG designer mirror. Disabled from default runtime discovery until language or mode filtering exists; ' ||
        'keeps schema parity for explicit future use.',
    tags = (
        SELECT COALESCE(jsonb_agg(DISTINCT value), '[]'::jsonb)
        FROM (
            SELECT value
            FROM jsonb_array_elements_text(COALESCE(t.tags, '[]'::jsonb)) tag(value)
            WHERE value <> 'scope.global'
              AND value NOT LIKE 'scope.cwd:%'
            UNION ALL
            SELECT 'intent:dag_designer'
            UNION ALL
            SELECT 'workflow:dag'
            UNION ALL
            SELECT 'workflow:enterprise'
            UNION ALL
            SELECT 'io:sharedfile'
        ) merged
    ),
    enabled = FALSE,
    updated_by = 'migration:0106',
    updated_at = NOW()
WHERE t.prompt_key = 'main/dag_designer_en'
  AND t.created_by IN ('system.seed', 'seed')
  AND (
      t.updated_by IN ('system.seed', 'seed', 'migration')
      OR t.updated_by LIKE 'system.%'
      OR t.updated_by LIKE 'migration:%'
  )
  AND t.manually_edited = FALSE;

-- Enterprise workflow preset discovery metadata. Keep this to runtime-visible
-- metadata and schema summaries; prompt bodies remain in the historical 0087
-- seed until a canonical prompt-template asset mechanism exists.
WITH enterprise_metadata(prompt_key, description, when_to_use, schema_tags) AS (
    VALUES
    (
        'main/morning_briefer',
        '企业晨报：基于笔记、链接、任务和 sharedfile 输入来源，' ||
            '按时间窗口输出今日重点、变化、风险、证据来源、' ||
            '置信度、不确定性和 next step。',
        '当需要把每日输入整理成面向团队的晨报、今日重点、风险和后续行动时使用。',
        jsonb_build_array('scope.global','intent:enterprise_workflow','workflow:enterprise',
            'schema:input_sources','schema:output_structure','schema:evidence',
            'schema:time_window','schema:confidence','schema:uncertainty',
            'schema:owner','schema:next_step','brief:today_focus','brief:risk')
    ),
    (
        'main/pr_summarizer',
        'PR 摘要：基于 diff、提交说明和 review 上下文，' ||
            '输出 PR 范围、行为影响、风险区域、review 重点、' ||
            '未确定项、证据和 next step。',
        '当需要把 PR 或变更集整理成审查摘要、风险提示和 reviewer 行动建议时使用。',
        jsonb_build_array('scope.global','intent:enterprise_workflow','workflow:enterprise',
            'schema:input_sources','schema:output_structure','schema:evidence',
            'schema:time_window','schema:confidence','schema:uncertainty',
            'schema:owner','schema:next_step','pr:scope','pr:behavior_impact',
            'pr:risk_area','pr:review_focus')
    ),
    (
        'main/weekly_reviewer',
        '周复盘：基于本周笔记、任务和决策记录，' ||
            '输出本周完成、关键决策、阻塞、下周优先级、' ||
            '负责人、证据和不确定性。',
        '当需要把一周工作整理成复盘、状态同步、阻塞和下周优先级时使用。',
        jsonb_build_array('scope.global','intent:enterprise_workflow','workflow:enterprise',
            'schema:input_sources','schema:output_structure','schema:evidence',
            'schema:time_window','schema:confidence','schema:uncertainty',
            'schema:owner','schema:next_step','weekly:outcomes','weekly:decisions',
            'weekly:blockers')
    ),
    (
        'main/data_inspector',
        '数据检查：基于表格、指标、日志或样本输入，' ||
            '输出数据来源、字段含义、异常值、质量问题、' ||
            '置信度、不确定性和下一步查询。',
        '当需要检查数据样本、指标或日志并归纳质量问题、异常和后续查询时使用。',
        jsonb_build_array('scope.global','intent:enterprise_workflow','workflow:enterprise',
            'schema:input_sources','schema:output_structure','schema:evidence',
            'schema:time_window','schema:confidence','schema:uncertainty',
            'schema:owner','schema:next_step','data:field_meaning','data:outlier',
            'data:quality_issue')
    ),
    (
        'main/email_drafter',
        '邮件起草：基于收件人、语气、目的和上下文输入，' ||
            '输出主题、正文、行动请求、后续跟进、' ||
            '待确认事实和不确定性。',
        '当需要把业务上下文整理成邮件草稿、回复、行动请求或后续跟进时使用。',
        jsonb_build_array('scope.global','intent:enterprise_workflow','workflow:enterprise',
            'schema:input_sources','schema:output_structure','schema:evidence',
            'schema:time_window','schema:confidence','schema:uncertainty',
            'schema:owner','schema:next_step','email:recipient','email:tone',
            'email:purpose','email:action_request','email:follow_up')
    ),
    (
        'main/health_reporter',
        '健康报告：基于监控来源、日志、指标和事件记录，' ||
            '输出健康状态、异常信号、影响范围、owner、' ||
            '证据、置信度和 next step。',
        '当需要把系统状态、监控信号或 incident 材料整理成健康报告和行动建议时使用。',
        jsonb_build_array('scope.global','intent:enterprise_workflow','workflow:enterprise',
            'schema:input_sources','schema:output_structure','schema:evidence',
            'schema:time_window','schema:confidence','schema:uncertainty',
            'schema:owner','schema:next_step','health:status','health:anomaly',
            'health:impact')
    ),
    (
        'main/source_monitor',
        '来源监控：基于 feed、链接、摘录或历史对照，' ||
            '输出来源、变化摘要、触发条件、风险等级、' ||
            '跟进动作、证据和不确定性。',
        '当需要监控信息源变化、筛选重要更新、标记风险并安排跟进动作时使用。',
        jsonb_build_array('scope.global','intent:enterprise_workflow','workflow:enterprise',
            'schema:input_sources','schema:output_structure','schema:evidence',
            'schema:time_window','schema:confidence','schema:uncertainty',
            'schema:owner','schema:next_step','source:change_summary','source:trigger',
            'source:risk_level')
    ),
    (
        'main/note_organizer',
        '笔记整理：基于散乱笔记、会议记录或摘录，' ||
            '输出主题归类、事实、决策、行动项、' ||
            '待确认问题、证据和不确定性。',
        '当需要把杂乱记录整理成结构化事实、决策、行动项和待确认问题时使用。',
        jsonb_build_array('scope.global','intent:enterprise_workflow','workflow:enterprise',
            'schema:input_sources','schema:output_structure','schema:evidence',
            'schema:time_window','schema:confidence','schema:uncertainty',
            'schema:owner','schema:next_step','note:topic','note:fact','note:decision',
            'note:action_item')
    ),
    (
        'main/todo_prioritizer',
        '待办排序：基于待办来源、上下文和约束，' ||
            '输出优先级、依赖、阻塞、下一步、owner、' ||
            '证据、置信度和不确定性。',
        '当需要把待办、backlog 或混合请求整理成优先级、依赖和可执行下一步时使用。',
        jsonb_build_array('scope.global','intent:enterprise_workflow','workflow:enterprise',
            'schema:input_sources','schema:output_structure','schema:evidence',
            'schema:time_window','schema:confidence','schema:uncertainty',
            'schema:owner','schema:next_step','todo:priority','todo:dependency',
            'todo:blocker')
    )
)
UPDATE public.prompt_templates t
SET description = enterprise_metadata.description,
    when_to_use = enterprise_metadata.when_to_use,
    tags = (
        SELECT COALESCE(jsonb_agg(DISTINCT value), '[]'::jsonb)
        FROM (
            SELECT jsonb_array_elements_text(COALESCE(t.tags, '[]'::jsonb)) AS value
            UNION ALL
            SELECT jsonb_array_elements_text(enterprise_metadata.schema_tags) AS value
        ) merged
    ),
    updated_by = 'migration:0106',
    updated_at = NOW()
FROM enterprise_metadata
WHERE t.prompt_key = enterprise_metadata.prompt_key
  AND t.created_by IN ('system.seed', 'seed')
  AND (
      t.updated_by IN ('system.seed', 'seed', 'migration')
      OR t.updated_by LIKE 'system.%'
      OR t.updated_by LIKE 'migration:%'
  )
  AND t.manually_edited = FALSE;

-- Planning/review/debug methodology discovery metadata. This only improves
-- routing and available_experts guidance; full prompt body rewrites are
-- deferred until canonical prompt-template assets exist.
WITH methodology_metadata(prompt_key, description, when_to_use, method_tags) AS (
    VALUES
    (
        'main/planning',
        '规划方法论：澄清目标和未知，比较方案、依赖和风险，' ||
            '拆成可验证任务，并在重大分叉前等待确认。',
        '用于阶段化规格、实施计划、依赖、风险和用户确认；' ||
            '输出回链需求编号或验收点的任务清单，规划阶段不写实现代码。',
        jsonb_build_array('scope.global','intent:expert','method:planning',
            'phase:requirements','phase:design','phase:task_breakdown',
            'needs:user_confirmation','output:handoff_plan','evidence:acceptance_link')
    ),
    (
        'main/code-review',
        '代码审查方法论：findings-first，按严重级排序；' ||
            '每条有 file:line、影响、触发条件、证据类型和测试缺口。',
        '用于 diff 风险评估、回归、安全和缺测试审查；' ||
            'findings-first，按严重等级给 file:line，区分事实、推断风险、未知或未验证。',
        jsonb_build_array('scope.global','intent:expert','method:code_review',
            'review:findings_first','review:severity','review:file_line',
            'review:evidence_type','review:test_gap')
    ),
    (
        'main/code-debug',
        '调试方法论：先收集错误文本、日志、输入、环境和最近变更；' ||
            '用最小复现、二分定位和验证命令闭环。',
        '用于报错排查、panic、exception、traceback、配置、依赖和数据边界定位；' ||
            '先看错误证据，做最小复现、根因定位和验证闭环。',
        jsonb_build_array('scope.global','intent:expert','method:debug',
            'debug:error_evidence','debug:minimal_repro','debug:root_cause',
            'debug:verification','debug:unverified_boundary')
    )
)
UPDATE public.prompt_templates t
SET description = methodology_metadata.description,
    when_to_use = methodology_metadata.when_to_use,
    tags = (
        SELECT COALESCE(jsonb_agg(DISTINCT value), '[]'::jsonb)
        FROM (
            SELECT jsonb_array_elements_text(COALESCE(t.tags, '[]'::jsonb)) AS value
            UNION ALL
            SELECT jsonb_array_elements_text(methodology_metadata.method_tags) AS value
        ) merged
    ),
    updated_by = 'migration:0106',
    updated_at = NOW()
FROM methodology_metadata
WHERE t.prompt_key = methodology_metadata.prompt_key
  AND t.created_by IN ('system.seed', 'seed')
  AND (
      t.updated_by IN ('system.seed', 'seed', 'migration')
      OR t.updated_by LIKE 'system.%'
      OR t.updated_by LIKE 'migration:%'
  )
  AND t.manually_edited = FALSE;

-- Default developer expert roster repair. main/git-ops and main/docs were
-- present in 0039 but absent after 0040; keep the inserted prompt_text as a
-- short provider-neutral expert card until canonical prompt-template assets
-- can carry longer bodies.
INSERT INTO public.prompt_templates (
    prompt_key,
    agent_key,
    title,
    tool_name,
    prompt_text,
    tags,
    enabled,
    description,
    when_to_use,
    manually_edited,
    created_by,
    updated_by
)
VALUES
(
    'main/git-ops',
    'git-ops',
    'Git 操作专家',
    '',
    'Git 操作专家：基于 diff、log、冲突或提交上下文，产出可验证的 git 操作建议；危险历史改写必须要求用户确认。',
    jsonb_build_array('scope.global','intent:expert','domain:developer','workflow:git'),
    TRUE,
    'Git 操作：diff、log、blame、commit message、冲突、revert 和 cherry-pick。',
    '当需要解释 git diff/log/blame、写 commit message、处理冲突、revert 或 cherry-pick 时使用。',
    FALSE,
    'system.seed',
    'migration:0106'
),
(
    'main/docs',
    'docs-writer',
    '文档专家',
    '',
    '技术文档专家：基于代码、接口、变更和目标读者，产出结构清楚、可维护的 README、API 文档、注释或 changelog 草稿。',
    jsonb_build_array('scope.global','intent:expert','domain:developer','workflow:documentation'),
    TRUE,
    '技术文档：README、API 文档、注释、changelog 和面向开发者的结构化说明。',
    '当需要撰写或整理 README、API 文档、注释、changelog 或技术说明时使用。',
    FALSE,
    'system.seed',
    'migration:0106'
)
ON CONFLICT (prompt_key) DO NOTHING;

UPDATE public.prompt_templates t
SET description = '编排协调者：拆分复杂任务、分配子 agent、跟踪依赖和汇总结果，' ||
        '适合开发工程和企业工作中的多角色协作；DAG/cron/节点依赖设计交给 DAG designer。',
    when_to_use = '当用户要求多 agent 协作、拆分任务、并行子任务、跨领域协调、' ||
        '或汇总多个子 agent 结果时使用。',
    tags = (
        SELECT COALESCE(jsonb_agg(DISTINCT value), '[]'::jsonb)
        FROM (
            SELECT jsonb_array_elements_text(COALESCE(t.tags, '[]'::jsonb)) AS value
            UNION ALL
            SELECT 'scope.global'
            UNION ALL
            SELECT 'intent:expert'
            UNION ALL
            SELECT 'domain:developer'
            UNION ALL
            SELECT 'workflow:orchestration'
            UNION ALL
            SELECT 'workflow:enterprise'
        ) merged
    ),
    updated_by = 'migration:0106',
    updated_at = NOW()
WHERE t.prompt_key = 'main/orchestrator'
  AND t.created_by IN ('system.seed', 'seed')
  AND (
      t.updated_by IN ('system.seed', 'seed', 'migration')
      OR t.updated_by LIKE 'system.%'
      OR t.updated_by LIKE 'migration:%'
  )
  AND t.manually_edited = FALSE;

COMMIT;
