-- 0109_refresh_dag_designer_prompt_agent_cwd.sql
--
-- Agent DAG nodes now fail fast when node.config.exec.cwd is missing before
-- task_dispatch_node enqueues a wakeup. 0084/0085 are DO NOTHING seeds, so
-- already-applied databases need a guarded prompt-text refresh that teaches DAG
-- designers to include an explicit absolute cwd for agent launches.

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '    "agent_key": "code-debug",
    "effort": "medium",',
      '    "agent_key": "code-debug",
    "cwd": "/absolute/path/to/project",
    "effort": "medium",'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0109'
WHERE prompt_key = 'main/dag_designer_zh'
  AND manually_edited = FALSE
  AND created_by IN ('system.seed', 'seed')
  AND prompt_text LIKE '%"agent_key": "code-debug",%'
  AND prompt_text NOT LIKE '%"cwd": "/absolute/path/to/project"%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '- `agent_key` 必须来自 prompt_list 的返回。',
      '- `agent_key` 必须来自 prompt_list 的返回。
- `cwd` 必填，必须是待执行项目的绝对路径；若用户未提供且上下文没有当前项目路径，先询问，不要省略或填相对路径。'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0109'
WHERE prompt_key = 'main/dag_designer_zh'
  AND manually_edited = FALSE
  AND created_by IN ('system.seed', 'seed')
  AND prompt_text LIKE '%- `agent_key` 必须来自 prompt_list 的返回。%'
  AND prompt_text NOT LIKE '%`cwd` 必填，必须是待执行项目的绝对路径%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '    "verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review" }',
      '    "verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review", "cwd": "/absolute/path/to/project" }'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0109'
WHERE prompt_key = 'main/dag_designer_zh'
  AND manually_edited = FALSE
  AND created_by IN ('system.seed', 'seed')
  AND prompt_text LIKE '%"agent_key": "code-review"%'
  AND prompt_text NOT LIKE '%"agent_key": "code-review", "cwd": "/absolute/path/to/project"%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '    "agent_key": "code-debug",
    "effort": "medium",',
      '    "agent_key": "code-debug",
    "cwd": "/absolute/path/to/project",
    "effort": "medium",'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0109'
WHERE prompt_key = 'main/dag_designer_en'
  AND manually_edited = FALSE
  AND created_by IN ('system.seed', 'seed')
  AND prompt_text LIKE '%"agent_key": "code-debug",%'
  AND prompt_text NOT LIKE '%"cwd": "/absolute/path/to/project"%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '- `agent_key` must come from prompt_list results.',
      '- `agent_key` must come from prompt_list results.
- `cwd` is required and must be the absolute path of the project to run in; if the user did not provide it and the current project path is not available from context, ask instead of omitting it or using a relative path.'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0109'
WHERE prompt_key = 'main/dag_designer_en'
  AND manually_edited = FALSE
  AND created_by IN ('system.seed', 'seed')
  AND prompt_text LIKE '%- `agent_key` must come from prompt_list results.%'
  AND prompt_text NOT LIKE '%`cwd` is required and must be the absolute path%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '    "verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review" }',
      '    "verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review", "cwd": "/absolute/path/to/project" }'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0109'
WHERE prompt_key = 'main/dag_designer_en'
  AND manually_edited = FALSE
  AND created_by IN ('system.seed', 'seed')
  AND prompt_text LIKE '%"agent_key": "code-review"%'
  AND prompt_text NOT LIKE '%"agent_key": "code-review", "cwd": "/absolute/path/to/project"%';
