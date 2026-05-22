# Migration 回滚 Runbook

本仓库当前的 migration runner（`internal/platform/db/module.go`）**只识别一种文件**：
按文件名字典序 apply `migrations/*.sql`，并把 filename 写入 `schema_migrations`。
没有 down/rollback 概念，也不支持 `.down.sql` 命名约定（若放入 `*.down.sql` 文件，
runner 会把它当成新 migration 误跑，反而执行掉 down 语句）。

因此，回滚以**手工 SQL**方式执行：直接连 PG 跑下方对应的 DDL，然后从 `schema_migrations`
删掉对应 filename 行（若需要重新 apply 同名 up）。

---

## 0076 — partial unique index 同 dag_key 单 running

**up：** `migrations/0076_dag_v2_one_running_run_per_dag.sql`

**down（手工执行）：**

```sql
BEGIN;
DROP INDEX IF EXISTS uniq_task_dag_runs_one_running_per_dag;
DELETE FROM schema_migrations WHERE filename = '0076_dag_v2_one_running_run_per_dag.sql';
COMMIT;
```

**影响：** 删除后同 `dag_key` 可能再次出现并发 `running` 行（StartDAG TOCTOU race），
应用层 GetRun-first idempotency 仍可挡 `idempotency_key` 重复，但挡不住空
idempotency_key 的并发起跑。仅在确诊 unique index 本身阻塞业务时才回滚。

---

## 0077 — task_dag_runs.metadata NOT NULL DEFAULT '{}'

**up：** `migrations/0077_dag_v2_run_metadata_not_null.sql`

**down（手工执行）：**

```sql
BEGIN;
ALTER TABLE task_dag_runs ALTER COLUMN metadata DROP NOT NULL;
ALTER TABLE task_dag_runs ALTER COLUMN metadata DROP DEFAULT;
DELETE FROM schema_migrations WHERE filename = '0077_dag_v2_run_metadata_not_null.sql';
COMMIT;
```

**影响：** 列回到 nullable，未来 INSERT 不显式给 metadata 时会写 NULL；read 路径
`fromTaskDagRun` 期望 jsonb 对象，遇 NULL 会规约违背。回滚前确认上层 Go 写路径未
依赖 DEFAULT '{}' 兜底（commit a2aa68d3 后写路径已自带 `"{}"` 兜底，本回滚相对安全）。

---

## 0078 — task_dag_nodes.depends_on jsonb 数组 CHECK

**up：** `migrations/0078_dag_v2_node_depends_on_array_check.sql`

**down（手工执行）：**

```sql
BEGIN;
ALTER TABLE task_dag_nodes DROP CONSTRAINT IF EXISTS chk_depends_on_is_array;
DELETE FROM schema_migrations WHERE filename = '0078_dag_v2_node_depends_on_array_check.sql';
COMMIT;
```

**影响：** 删除 CHECK 后 `depends_on` 列允许任意 jsonb 值（含 null/object/string）。
应用层 sqlc 写路径仍会塞数组，但读路径若遇到非数组值会 unmarshal 错。仅在
CHECK 误伤合法数据时才回滚。

---

## 注意事项

1. **当前 PG 已 applied 0076/0077/0078/0079/0080**（`schema_migrations.version` 最大 80；以下文 §0079/0080 段为准），
   上述 down 是事故/演练时的手工兜底，不影响日常开发。
2. **历史 0001-0075 没有补 down**，因为本仓库历史没有 down 概念。仅对 0076 起的
   T1.2-mid 一批 schema-tightening migration 做此 runbook 化。
3. **down 不会修复脏数据**：如 0076 down 后若有遗留并发 running 行，需自行清理。
4. **不要把 down SQL 放进 `migrations/` 目录**：runner 不区分 up/down，会把 `*.down.sql`
   当成新 migration 跑掉。

---

## 0079 — task_dag_nodes run_id FK + reads/writes array CHECK + run_id 索引

**up：** `migrations/0079_dag_v2_node_run_id_fk_and_jsonb_checks.sql`

**down（手工执行）：**

```sql
BEGIN;
ALTER TABLE task_dag_nodes DROP CONSTRAINT IF EXISTS fk_task_dag_nodes_run_id;
DROP INDEX IF EXISTS idx_task_dag_nodes_run_id;
ALTER TABLE task_dag_nodes DROP CONSTRAINT IF EXISTS chk_reads_is_array;
ALTER TABLE task_dag_nodes DROP CONSTRAINT IF EXISTS chk_writes_is_array;
DELETE FROM schema_migrations WHERE filename = '0079_dag_v2_node_run_id_fk_and_jsonb_checks.sql';
COMMIT;
```

**影响：** 删除 FK 后 run_id 可能悬挂引用不存在的 task_dag_runs.id；删 reads/
writes CHECK 后允许非数组 jsonb 值，UI / 文件锁 联动读路径可能 unmarshal 失败。
仅在 FK / CHECK 误伤合法数据或阻塞业务时回滚。run_id 索引 down 仅影响查询性能，
不影响正确性。

---

## 0080 — task_dag_runs.status CHECK 枚举

**up：** `migrations/0080_dag_v2_run_status_check.sql`

**down（手工执行）：**

```sql
BEGIN;
ALTER TABLE task_dag_runs DROP CONSTRAINT IF EXISTS chk_task_dag_runs_status_enum;
DELETE FROM schema_migrations WHERE filename = '0080_dag_v2_run_status_check.sql';
COMMIT;
```

**影响：** 删除 CHECK 后 status 列允许任意 TEXT，未知字面量会进 DB；service
状态机读到非枚举值会落 default 分支（行为未定义）。0076 partial unique index
仍依赖 'running' 字面量，单边删 CHECK 不破 0076 但放宽了写路径校验。仅在
CHECK 误伤业务（如新增合法 status 字面量未同步迁移）时回滚。

---

## 0081 — task_dags.trigger CHECK 枚举

**up：** `migrations/0081_dag_v2_dag_trigger_check.sql`

**down（手工执行）：**

```sql
BEGIN;
ALTER TABLE task_dags DROP CONSTRAINT IF EXISTS chk_task_dags_trigger_enum;
DELETE FROM schema_migrations WHERE filename = '0081_dag_v2_dag_trigger_check.sql';
COMMIT;
```

**影响：** 删除 CHECK 后 trigger 列允许任意 TEXT，未知字面量会进 DB；
F5 cron daemon / dispatcher 读到非枚举值会落 default 分支（行为未定义）。
0072 default 'manual' 仍存在，单边删 CHECK 不影响默认值写路径，仅放宽
非默认写入校验。仅在 CHECK 误伤业务（如新增合法 trigger 字面量未同步迁移）
时回滚。

---

## 0082 — task_dag_runs.trigger_source CHECK 枚举

**up：** `migrations/0082_dag_v2_run_trigger_source_check.sql`

**down（手工执行）：**

```sql
BEGIN;
ALTER TABLE task_dag_runs DROP CONSTRAINT IF EXISTS chk_task_dag_runs_trigger_source_enum;
DELETE FROM schema_migrations WHERE filename = '0082_dag_v2_run_trigger_source_check.sql';
COMMIT;
```

**影响：** 删除 CHECK 后 trigger_source 列允许任意 TEXT，未知字面量会进 DB；
F5 cron daemon / dispatcher 读到非枚举值会落 default 分支（行为未定义）。
0074 default '' 仍存在，单边删 CHECK 不影响默认值写路径，仅放宽非默认写入
校验。仅在 CHECK 误伤业务（如新增合法 trigger_source 字面量未同步迁移）时
回滚。

**说明：** 本 CHECK 显式允许空串 '' —— 与 0074 DEFAULT '' 兼容。若后续把
default 收敛为 'manual'，需独立 migration 同步把空串移出白名单。详见
docs/plans/dag改造实施计划.md §10 follow-up「trigger_source default 收敛」。

---

## 0083 — task_dag_nodes.spawning_thread_id 列 + partial index (F1.5)

**up：** `migrations/0083_dag_v2_spawning_thread_id.sql`

**down（手工执行）：**

```sql
BEGIN;
DROP INDEX IF EXISTS idx_task_dag_nodes_spawning_thread_id;
-- CASCADE 防未来加上 view / generated column 后 DROP COLUMN 被依赖抦在。
ALTER TABLE task_dag_nodes DROP COLUMN IF EXISTS spawning_thread_id CASCADE;
DELETE FROM schema_migrations WHERE filename = '0083_dag_v2_spawning_thread_id.sql';
COMMIT;
```

**影响：** 删列后 F1.5 / ADR-009 提供的 thread↔node 软关联丢失。`AgentExecutor.RecordNodeSpawn` 调用会打 SQL 错（列不存在）；DTO `DAGNode.SpawningThreadID` 读不到列 → 需 mcp-orch 退回 F1.5 之前二进制。CASCADE 清理任何 partial index 依赖；spawning_thread_id 不带 FK / CHECK，不会连带别处。仅在严重随机问题需回退 F1.5 时才回滚。

**幂等提示：** up 文件使用 `-- SPLIT --` sentinel（详见 internal/platform/db/module.go 中的
`migrationSplitSentinel`）拆为事务内 ALTER TABLE 与事务外
CREATE INDEX CONCURRENTLY 两段。中途中断可能留下 INVALID 状态的部分索引；这时
rollback 前先手工 `DROP INDEX IF EXISTS idx_task_dag_nodes_spawning_thread_id;`
再走上面脚本。

---

## 0084 — AI 设计师 prompt seed（中文版 main/dag_designer_zh）

**up：** `migrations/0084_seed_dag_designer_prompt_zh.sql`

**down（手工执行）：**

```sql
BEGIN;
DELETE FROM prompt_templates WHERE prompt_key = 'main/dag_designer_zh';
DELETE FROM schema_migrations WHERE filename = '0084_seed_dag_designer_prompt_zh.sql';
COMMIT;
```

**影响：** 删后 router 命中 `agent_key='dag_designer'` 会取不到 prompt，「AI 帮你设计流程」UI 按钮 / thread 入口会报 prompt missing。archtest `dag_designer_prompt_seed_test.go` 依赖本行存在 → 回滚后守护测试会跳红。仅在需回退 F7.1 或 prompt 内容废弃不再采用时才回滚。刷新内容请按 `docs/migrations/prompt-seed-policy.md` 规约写新 migration 走 DO UPDATE，不走 down+up 回滚。

**参考：** `docs/migrations/prompt-seed-policy.md`。

---

## 0104 — disable registry-backed system seed prompts

**up：** `migrations/0104_disable_registry_backed_system_seed_prompts.sql`

**down（手工执行）：**

仅在回滚 builtin registry runtime 时重新启用这些历史 seed：

```sql
BEGIN;
UPDATE public.prompt_templates
SET enabled = TRUE,
    updated_by = 'system.seed',
    updated_at = NOW()
WHERE prompt_key IN ('main/default', 'main/general-zh')
  AND updated_by = 'system.registry-migration';

DELETE FROM schema_migrations WHERE filename = '0104_disable_registry_backed_system_seed_prompts.sql';
COMMIT;
```

**影响：** 回滚后 `main/default`、`main/general-zh` 的历史 DB seed 行会重新参与运行时读取；
仅应在 builtin registry runtime 一并回退时执行，避免 registry 与 DB seed 双源重复。

---

## 0105 — delete unused builtin prompt seeds

**up：** `migrations/0105_delete_unused_builtin_prompt_seeds.sql`

**0105 data restore（手工执行）：**

该迁移物理删除废弃 system-owned prompt seed。回滚时从历史 seed 语义重建对应
system seed 行，并恢复已知历史 demo/test sections；`ON CONFLICT DO NOTHING`
保证同 key 用户资产、`updated_by='rpc.prompts'` 或手工编辑资产不被覆盖。历史
provider-branded key 只恢复为 disabled placeholder，且不带 `scope.global` /
`scope.cwd:*` runtime scope。

```sql
BEGIN;

WITH restore_templates (
    prompt_key,
    title,
    agent_key,
    prompt_text,
    variables,
    tags,
    description,
    enabled,
    manually_edited,
    created_by,
    updated_by
) AS (
    VALUES
        ('main/3', 'Legacy Smoke Prompt 3', 'main',
         'Rollback restore placeholder for retired smoke-test prompt main/3.',
         '{}'::jsonb, '["legacy","rollback"]'::jsonb,
         'Rollback restore for retired smoke-test prompt main/3.', FALSE, FALSE, 'system.seed', 'system.seed'),
        ('main/prompt', 'Legacy Smoke Prompt', 'main',
         'Rollback restore placeholder for retired smoke-test prompt main/prompt.',
         '{}'::jsonb, '["legacy","rollback"]'::jsonb,
         'Rollback restore for retired smoke-test prompt main/prompt.', FALSE, FALSE, 'system.seed', 'system.seed'),
        ('main/debug', 'Legacy Debug Expert', 'debugger',
         'Rollback restore placeholder for the retired debug expert. Prefer main/code-debug after reapplying 0105.',
         '{}'::jsonb, '["debug","legacy","rollback","scope.global"]'::jsonb,
         'Rollback restore for retired debug expert.', TRUE, FALSE, 'system.seed', 'system.seed'),
        ('main/claude-style', 'Legacy Claude-style Prompt (disabled)', 'main',
         'Rollback restore disabled placeholder for the legacy provider-branded prompt body.',
         '{}'::jsonb, '["legacy","disabled","rollback"]'::jsonb,
         'Disabled rollback placeholder for legacy provider-branded prompt key.', FALSE, FALSE, 'test-seed', 'system.seed'),
        ('main/claude-style-zh', 'Legacy Claude-style ZH Prompt (disabled)', 'main',
         'Rollback restore disabled placeholder for the legacy provider-branded Chinese prompt body.',
         '{}'::jsonb, '["legacy","disabled","rollback"]'::jsonb,
         'Disabled rollback placeholder for legacy provider-branded Chinese prompt key.', FALSE, FALSE, 'system.seed', 'system.seed'),
        ('main/general-en', 'Legacy General EN Prompt (disabled)', 'main',
         'Rollback restore disabled placeholder for the legacy English system prompt body.',
         '{}'::jsonb, '["legacy","disabled","rollback"]'::jsonb,
         'Disabled rollback placeholder for legacy English prompt key.', FALSE, FALSE, 'test-seed', 'system.seed'),
        ('sql/expert', 'Legacy SQL Expert (disabled)', 'main',
         'Rollback restore disabled placeholder for the retired sql/expert seed. Prefer main/sql after reapplying 0105.',
         '{}'::jsonb, '["legacy","disabled","rollback"]'::jsonb,
         'Disabled rollback placeholder for retired duplicate SQL expert key.', FALSE, FALSE, 'system.seed', 'system.seed'),
        ('main/writing', 'Writing Assistant', 'writer',
         'Rollback restore for the retired writing assistant seed.',
         '{}'::jsonb, '["writing","legacy","rollback","scope.global"]'::jsonb,
         'Rollback restore for writing, email, copy, and polishing tasks.', TRUE, FALSE, 'system.seed', 'system.seed'),
        ('main/translate', 'Translate Assistant', 'translator',
         'Rollback restore for the retired translation assistant seed.',
         '{}'::jsonb, '["translate","legacy","rollback","scope.global"]'::jsonb,
         'Rollback restore for translation and localization tasks.', TRUE, FALSE, 'system.seed', 'system.seed'),
        ('main/research', 'Research Assistant', 'researcher',
         'Rollback restore for the retired research assistant seed.',
         '{}'::jsonb, '["research","legacy","rollback","scope.global"]'::jsonb,
         'Rollback restore for concept explanation, summaries, and comparisons.', TRUE, FALSE, 'system.seed', 'system.seed'),
        ('main/brainstorm', 'Brainstorm Assistant', 'brainstormer',
         'Rollback restore for the retired brainstorming assistant seed.',
         '{}'::jsonb, '["brainstorm","legacy","rollback","scope.global"]'::jsonb,
         'Rollback restore for naming, ideation, and creative options.', TRUE, FALSE, 'system.seed', 'system.seed'),
        ('main/paper_summarizer', 'Paper Summarizer', 'paper_summarizer',
         'Rollback restore for the retired paper summarizer seed.',
         '{}'::jsonb, '["research","paper","summary","legacy","rollback","scope.global"]'::jsonb,
         'Rollback restore for paper summary tasks.', TRUE, FALSE, 'system.seed', 'system.seed'),
        ('main/topic_curator', 'Topic Curator', 'topic_curator',
         'Rollback restore for the retired topic curator seed.',
         '{}'::jsonb, '["curation","topics","legacy","rollback","scope.global"]'::jsonb,
         'Rollback restore for topic curation tasks.', TRUE, FALSE, 'system.seed', 'system.seed'),
        ('main/learning_card', 'Learning Card Builder', 'learning_card',
         'Rollback restore for the retired learning card seed.',
         '{}'::jsonb, '["learning","cards","legacy","rollback","scope.global"]'::jsonb,
         'Rollback restore for learning card tasks.', TRUE, FALSE, 'system.seed', 'system.seed'),
        ('main/trip_briefer', 'Trip Briefer', 'trip_briefer',
         'Rollback restore for the retired trip briefer seed.',
         '{}'::jsonb, '["travel","briefing","legacy","rollback","scope.global"]'::jsonb,
         'Rollback restore for trip briefing tasks.', TRUE, FALSE, 'system.seed', 'system.seed'),
        ('examples/sections-demo', 'Sections 示例 (参考模板)', 'sections-demo',
         'Rollback restore for the sectioned prompt layout demo fallback.',
         '{}'::jsonb, '["sections","demo","example","legacy","rollback"]'::jsonb,
         'Rollback restore for the prompt_template_sections demo. Disabled by default.', FALSE, FALSE, 'system', 'system'),
        ('test/greeting', '测试模板 · 友好问候助手', 'main',
         'Rollback restore for the retired greeting test prompt.',
         '{}'::jsonb, '["test","greeting","demo","rollback"]'::jsonb,
         'Rollback restore for the historical greeting test prompt.', TRUE, FALSE, 'test-seed', 'test-seed'),
        ('test/strict-review', '测试模板 · 严格代码审查助手', 'sub',
         'Rollback restore for the retired strict-review test prompt.',
         '{}'::jsonb, '["test","review","strict","demo","rollback"]'::jsonb,
         'Rollback restore for the historical strict-review test prompt.', TRUE, FALSE, 'test-seed', 'test-seed')
)
INSERT INTO public.prompt_templates (
    prompt_key,
    title,
    agent_key,
    prompt_text,
    variables,
    tags,
    description,
    enabled,
    manually_edited,
    created_by,
    updated_by,
    created_at,
    updated_at
)
SELECT
    prompt_key,
    title,
    agent_key,
    prompt_text,
    variables,
    tags,
    description,
    enabled,
    manually_edited,
    created_by,
    updated_by,
    NOW(),
    NOW()
FROM restore_templates
ON CONFLICT (prompt_key) DO NOTHING;

WITH restore_sections(prompt_key, section_key, region, ordinal, body, enable_when, enabled) AS (
    VALUES
        ('examples/sections-demo', 'identity', 'static', 0,
         'Rollback restore for the sectioned prompt layout demo identity section.',
         NULL::jsonb, TRUE),
        ('examples/sections-demo', 'tool_preferences', 'static', 10,
         'Rollback restore for the sectioned prompt layout demo tool preference section.',
         '{}'::jsonb, TRUE),
        ('test/greeting', 'identity', 'static', 0,
         'Rollback restore: 你是友好问候助手，负责温暖欢迎用户。',
         NULL::jsonb, TRUE),
        ('test/strict-review', 'identity', 'static', 0,
         'Rollback restore: 你是严格代码审查助手，只产出审查结论。',
         NULL::jsonb, TRUE)
)
INSERT INTO public.prompt_template_sections (
    template_id,
    section_key,
    region,
    ordinal,
    body,
    enable_when,
    enabled,
    created_at,
    updated_at
)
SELECT
    t.id,
    s.section_key,
    s.region,
    s.ordinal,
    s.body,
    s.enable_when,
    s.enabled,
    NOW(),
    NOW()
FROM restore_sections s
JOIN public.prompt_templates t ON t.prompt_key = s.prompt_key
WHERE t.created_by IN ('system.seed', 'seed', 'system', 'test-seed')
  AND (
      t.updated_by IN ('system.seed', 'seed', 'system', 'test-seed', 'migration')
      OR t.updated_by LIKE 'system.%'
      OR t.updated_by LIKE 'migration:%'
  )
  AND t.manually_edited = FALSE
ON CONFLICT (template_id, section_key) DO NOTHING;

COMMIT;
```

**0105 routing restore（手工执行）：**

该 block 只恢复 0041 历史 routing test 的稳定 input 行；本地已经存在同 input
时不覆盖，避免破坏运维手工维护的 routing test。

```sql
BEGIN;

INSERT INTO public.prompt_routing_tests (input, expected_prompt_key, note, enabled, created_at, updated_at)
VALUES
    ('帮我写一封辞职邮件', 'main/writing', '写邮件', TRUE, NOW(), NOW()),
    ('润色一下这段文案', 'main/writing', '润色', TRUE, NOW(), NOW()),
    ('draft a release announcement', 'main/writing', 'EN draft', TRUE, NOW(), NOW()),
    ('把这段翻译成英文', 'main/translate', '翻译', TRUE, NOW(), NOW()),
    ('translate to Simplified Chinese', 'main/translate', 'EN 翻译', TRUE, NOW(), NOW()),
    ('帮我翻译一下这个术语', 'main/translate', '帮我翻译', TRUE, NOW(), NOW()),
    ('什么是事件溯源', 'main/research', '什么是', TRUE, NOW(), NOW()),
    ('总结一下这篇论文的要点', 'main/research', '总结', TRUE, NOW(), NOW()),
    ('对比一下 PostgreSQL 和 MySQL', 'main/research', '对比', TRUE, NOW(), NOW()),
    ('给我的猫起个名字', 'main/brainstorm', '起名', TRUE, NOW(), NOW()),
    ('brainstorm 几个营销方案', 'main/brainstorm', 'brainstorm', TRUE, NOW(), NOW()),
    ('想几个有创意的标题', 'main/brainstorm', '想个标题', TRUE, NOW(), NOW())
ON CONFLICT (input) DO NOTHING;

COMMIT;
```

**bookkeeping（按需手工执行）：**

```sql
DELETE FROM schema_migrations WHERE filename = '0105_delete_unused_builtin_prompt_seeds.sql';
```

---

## 0106 — prompt template runtime metadata

**up：** `migrations/0106_prompt_template_runtime_metadata.sql`

**0106 data restore（手工执行）：**

该 block 恢复 0106 当前已落地的 DAG designer metadata/scope/FailureClass 小范围
修补、企业流程 preset 发现面 metadata、planning/review/debug 方法论发现面
metadata，以及默认开发专家 roster repair。它带 system-owned、未手工编辑 guard，
不覆盖 `created_by='rpc.prompts'`、`updated_by='rpc.prompts'` 或
`manually_edited=TRUE` 的同 key 用户资产。

```sql
BEGIN;

UPDATE public.prompt_templates t
SET prompt_text = REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(
        t.prompt_text,
        E'    "agent_key": "code-debug",',
        E'    "provider": "claude",\n    "model": "opus",\n    "agent_key": "code-debug",'
    ),
        '"escalation_chain": []',
        '"escalation_chain": ["sonnet","opus"]'
    ),
        '"verifier":   { "agent_key": "code-review" }',
        '"verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review" }'
    ),
        'model=<selected model from list_models()>',
        'model=sonnet'
    ),
        'list_models()',
        'list_models(provider="claude")'
    ),
        '`hard` / `needs_human` / `transient` / `quota`',
        '`timeout` / `cancelled` / `unknown` / `not_implemented`'
    ),
        'transient / quota / validation / capability / hard / needs_human / infrastructure',
        'capability / validation / infrastructure / timeout / cancelled / unknown / not_implemented'
    ),
    description = 'AI 流程设计师 (中文) — 把用户口语化的需求翻译成可执行 DAG，调 list_models / prompt_list / command_list / shared_file_list 摸清资源后用 task_create_dag / task_dag_apply_ops 落库。Seeded by migration 0084 (F7.1)。',
    when_to_use = '',
    tags = '["AI 设计流程","帮我设计流程","设计 DAG","设计 dag","设计流程","流程编排","编排流程","设计任务图","DAG 设计","dag 设计","帮我编排","自动化流程","每天定时","每天 8 点","cron","定时任务","定时跑","报告流程","流水线设计","pipeline 设计","工作流","workflow 设计","设计工作流","scope.global"]'::jsonb,
    updated_by = 'migration:0090',
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
SET prompt_text = REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(
        t.prompt_text,
        E'    "agent_key": "code-debug",',
        E'    "provider": "claude",\n    "model": "opus",\n    "agent_key": "code-debug",'
    ),
        '"escalation_chain": []',
        '"escalation_chain": ["sonnet","opus"]'
    ),
        '"verifier":   { "agent_key": "code-review" }',
        '"verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review" }'
    ),
        'model=<selected model from list_models()>',
        'model=sonnet'
    ),
        'list_models()',
        'list_models(provider="claude")'
    ),
        '`hard` / `needs_human` / `transient` / `quota`',
        '`timeout` / `cancelled` / `unknown` / `not_implemented`'
    ),
        'transient / quota / validation / capability / hard / needs_human / infrastructure',
        'capability / validation / infrastructure / timeout / cancelled / unknown / not_implemented'
    ),
    description = 'AI Flow Designer (English) — turns natural-language workflow requests into executable DAGs, discovers resources with list_models / prompt_list / command_list / shared_file_list, then persists the design with task_create_dag / task_dag_apply_ops. Seeded by migration 0085 (F7.2).',
    when_to_use = '',
    -- Restores pre-0106 default-visible English DAG designer scope and enabled state. Reapply 0106 to disable it from default runtime discovery again.
    tags = '["AI 设计流程","帮我设计流程","设计 DAG","设计 dag","设计流程","流程编排","编排流程","设计任务图","DAG 设计","dag 设计","帮我编排","自动化流程","每天定时","每天 8 点","cron","定时任务","定时跑","报告流程","流水线设计","pipeline 设计","工作流","workflow 设计","设计工作流","AI design flow","design flow","design DAG","flow design","design workflow","workflow design","schedule task","scheduled task","cron expression","daily report","report flow","pipeline design","flow orchestration","automation flow","daily at 8","scope.global"]'::jsonb,
    enabled = TRUE,
    updated_by = 'migration:0090',
    updated_at = NOW()
WHERE t.prompt_key = 'main/dag_designer_en'
  AND t.created_by IN ('system.seed', 'seed')
  AND (
      t.updated_by IN ('system.seed', 'seed', 'migration')
      OR t.updated_by LIKE 'system.%'
      OR t.updated_by LIKE 'migration:%'
  )
  AND t.manually_edited = FALSE;

WITH enterprise_restore(prompt_key, description, when_to_use, tags) AS (
    VALUES
    (
        'main/morning_briefer',
        'Create a concise morning brief from upstream context and sharedfiles.',
        '把笔记、链接、任务和共享文件整理成晨报',
        '["briefing","daily","summary","operations","morning","scope.global"]'::jsonb
    ),
    (
        'main/pr_summarizer',
        'Summarize pull request changes and review focus areas.',
        'PR 变更摘要、行为影响、风险区域和 review 重点',
        '["code","pull-request","review","summary","engineering","scope.global"]'::jsonb
    ),
    (
        'main/weekly_reviewer',
        'Create a weekly review from project notes and task updates.',
        '周报复盘、完成事项、决策、风险和下周优先级',
        '["weekly","review","planning","status","follow-up","scope.global"]'::jsonb
    ),
    (
        'main/data_inspector',
        'Inspect datasets or metrics and highlight trends, gaps, and anomalies.',
        '数据样本检查、字段含义、异常值和质量问题归纳',
        '["data","metrics","inspection","analysis","quality","scope.global"]'::jsonb
    ),
    (
        'main/email_drafter',
        'Draft concise emails from supplied context and requested outcomes.',
        '邮件起草、回复、语气调整和收件人导向改写',
        '["email","writing","communication","draft","business","scope.global"]'::jsonb
    ),
    (
        'main/health_reporter',
        'Write operational health reports from logs, metrics, and status notes.',
        '系统健康、状态摘要、异常信号和行动建议',
        '["health","ops","status","incident","monitoring","scope.global"]'::jsonb
    ),
    (
        'main/source_monitor',
        'Monitor source material and summarize meaningful changes.',
        '监控来源更新、提取变化、标出风险和跟进项',
        '["sources","monitoring","changes","research","report","scope.global"]'::jsonb
    ),
    (
        'main/note_organizer',
        'Organize messy notes into structured facts, decisions, and actions.',
        '整理散乱笔记、归类主题、提取行动项和决策',
        '["notes","organization","cleanup","actions","knowledge","scope.global"]'::jsonb
    ),
    (
        'main/todo_prioritizer',
        'Prioritize tasks with dependencies, blockers, and defer decisions.',
        '整理待办、排序优先级、识别依赖和阻塞',
        '["todo","priority","planning","backlog","execution","scope.global"]'::jsonb
    )
)
UPDATE public.prompt_templates t
SET description = enterprise_restore.description,
    when_to_use = enterprise_restore.when_to_use,
    tags = enterprise_restore.tags,
    updated_by = 'system.seed',
    updated_at = NOW()
FROM enterprise_restore
WHERE t.prompt_key = enterprise_restore.prompt_key
  AND t.created_by IN ('system.seed', 'seed')
  AND (
      t.updated_by IN ('system.seed', 'seed', 'migration')
      OR t.updated_by LIKE 'system.%'
      OR t.updated_by LIKE 'migration:%'
  )
  AND t.manually_edited = FALSE;

WITH methodology_restore(prompt_key, description, when_to_use, tags) AS (
    VALUES
    (
        'main/code-review',
        '代码审核 — 用户请求 review/审核代码',
        '代码审查、diff 风险评估、回归与安全问题检查',
        '["code review","审 diff","审核代码","review 一下","review 这段","review this","帮我 review","可以 review","审查代码","看看这段代码","code-review","scope.global"]'::jsonb
    ),
    (
        'main/code-debug',
        '调试 — 用户报错/请求排查',
        '错误排查、panic/exception/traceback 分析、最小复现定位',
        '["这个 bug","为什么报错","why fails","why does it fail","stack trace","堆栈信息","panic","报错了","不 work","不工作","debug 一下","帮我查 bug","排查一下","traceback","exception","报错信息","scope.global"]'::jsonb
    ),
    (
        'main/planning',
        '规划 — 任务拆分 / 方案设计 / 路线图（编程和非编程通用）',
        '任务拆解、实施计划、里程碑、风险和依赖梳理',
        '["帮我规划","任务规划","step by step","拆分任务","make a plan","planning this","制定计划","分步实施","里程碑","roadmap","技术方案","implementation plan","怎么落地","项目计划","实施计划","scope.global"]'::jsonb
    )
)
UPDATE public.prompt_templates t
SET description = methodology_restore.description,
    when_to_use = methodology_restore.when_to_use,
    tags = methodology_restore.tags,
    updated_by = 'system.seed',
    updated_at = NOW()
FROM methodology_restore
WHERE t.prompt_key = methodology_restore.prompt_key
  AND t.created_by IN ('system.seed', 'seed')
  AND (
      t.updated_by IN ('system.seed', 'seed', 'migration')
      OR t.updated_by LIKE 'system.%'
      OR t.updated_by LIKE 'migration:%'
  )
  AND t.manually_edited = FALSE;

DELETE FROM public.prompt_templates t
WHERE t.prompt_key IN ('main/git-ops', 'main/docs')
  AND t.created_by IN ('system.seed', 'seed')
  AND t.updated_by = 'migration:0106'
  AND t.manually_edited = FALSE;

UPDATE public.prompt_templates t
SET description = '多 agent 编排',
    when_to_use = '',
    tags = '["orchestrator","orchestrate","coordinate","delegate","multi-agent","multi agent","sub-agent","sub agent","plan and delegate","decompose","break down","拆分任务","多 agent 协作","子 agent 协作","编排","协调多个"]'::jsonb,
    enabled = TRUE,
    updated_by = 'system.seed',
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
```

---

## 0107 — prompt template expert consolidation

**up：** `migrations/0107_prompt_template_expert_consolidation.sql`

**0107 data restore（手工执行）：**

该迁移把历史重复开发专家 `main/code-generate`、`main/code-refactor`、
`main/code-test`、`main/code-explain` 合并进 `main/code-task`。forward
migration 会先把它实际触碰到的 system-owned、未手工编辑行快照到
`prompt_template_expert_consolidation_0107_restore`。因此完整链路上如果
这些旧专家早已不存在，rollback 不会凭空复活它们；只有 0107 实际快照过的
system-owned 行会被恢复。同 key 用户资产、`updated_by='rpc.prompts'` 或
`manually_edited=TRUE` 行不会被覆盖。

```sql
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
    when_to_use,
    enabled,
    manually_edited,
    match_when,
    priority,
    created_by,
    updated_by,
    created_at,
    updated_at
)
SELECT
    r.prompt_key,
    r.title,
    r.agent_key,
    r.tool_name,
    r.prompt_text,
    r.variables,
    r.tags,
    r.description,
    r.when_to_use,
    r.enabled,
    r.manually_edited,
    r.match_when,
    r.priority,
    r.created_by,
    r.updated_by,
    NOW(),
    NOW()
FROM public.prompt_template_expert_consolidation_0107_restore r
WHERE r.prompt_key IN (
    'main/code-generate',
    'main/code-refactor',
    'main/code-test',
    'main/code-explain'
)
ON CONFLICT (prompt_key) DO NOTHING;

UPDATE public.prompt_templates p
SET description = r.description,
    when_to_use = r.when_to_use,
    tags = r.tags,
    enabled = r.enabled,
    updated_by = r.updated_by,
    updated_at = NOW()
FROM public.prompt_template_expert_consolidation_0107_restore r
WHERE r.prompt_key = 'main/code-task'
  AND p.prompt_key = r.prompt_key
  AND p.created_by IN ('system.seed', 'seed')
  AND p.updated_by = 'migration:0107'
  AND p.manually_edited = FALSE;

DROP TABLE IF EXISTS public.prompt_template_expert_consolidation_0107_restore;

COMMIT;
```

**影响：** 回滚后，如果 0107 曾实际删除历史重复开发专家，它们会按快照恢复；
如果 0107 在完整迁移链上没有删除这些旧行，rollback 不会把早已由 0040
收敛掉的专家重新插入。`main/code-task` 的 `when_to_use` 会恢复到 0107
更新前的快照值。
