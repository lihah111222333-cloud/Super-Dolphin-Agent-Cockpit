-- 0090_refresh_dag_designer_prompt_run_id_signature.sql
--
-- F6.5 made task_update_node run-scoped. 0084/0085 are DO NOTHING seeds and
-- already-applied databases will not see edits to those files, so refresh the
-- prompt text with a new guarded migration per docs/migrations/prompt-seed-policy.md.

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '`task_update_node(dag_key, node_key, status, result?)`：改单节点运行态。',
      '`task_update_node(dag_key, node_key, run_id, status, result?)`：改单次运行里的节点运行态。'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0090'
WHERE prompt_key = 'main/dag_designer_zh'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%`task_update_node(dag_key, node_key, status, result?)`%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '`task_update_node(dag_key, node_key, status, result?)`: Change a single node''s runtime state.',
      '`task_update_node(dag_key, node_key, run_id, status, result?)`: Change a single node''s runtime state within one run.'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0090'
WHERE prompt_key = 'main/dag_designer_en'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%`task_update_node(dag_key, node_key, status, result?)`%';
