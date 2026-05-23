-- 0108_refresh_dag_designer_prompt_final_node_key.sql
--
-- task_create_dag now accepts final_node_key so designer-created DAGs can
-- produce run-level metadata.final_output. 0084/0085 are DO NOTHING seeds, so
-- already-applied databases need a guarded prompt-text refresh for both the
-- tool signature and the behavioral guidance that makes designers actually set
-- the final node.

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '`task_create_dag(agent_id, dag_key, title, description?, schedule, nodes?)`：新建 DAG。`schedule.trigger ∈ {manual, auto, scheduled}`；scheduled 需要后续 task_dag_apply_ops 写 cron_expr。`agent_id` 填你自己的 orchestration agent id。',
      '`task_create_dag(agent_id, dag_key, title, description?, schedule, final_node_key?, nodes?)`：新建 DAG。`final_node_key` 必须指向唯一的用户可见最终交付节点；run 完成后该节点结果会被索引到 run-level `metadata.final_output`，大结果仍用 Shared Files 承载。`schedule.trigger ∈ {manual, auto, scheduled}`；scheduled 需要后续 task_dag_apply_ops 写 cron_expr。`agent_id` 填你自己的 orchestration agent id。'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0108'
WHERE prompt_key = 'main/dag_designer_zh'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%description?, schedule, nodes?)%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '3. **画 DAG**：在脑子里 (或回复里) 列出节点清单 — 每个节点的 node_key / title / node_type / depends_on / 关键 config。先写文字版给用户看一眼，征得同意再落库。',
      '3. **画 DAG**：在脑子里 (或回复里) 列出节点清单 — 每个节点的 node_key / title / node_type / depends_on / 关键 config，并标明哪个 node_key 是最终交付节点 final_node_key。先写文字版给用户看一眼，征得同意再落库。'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0108'
WHERE prompt_key = 'main/dag_designer_zh'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%每个节点的 node_key / title / node_type / depends_on / 关键 config。%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '5. **展示**：调 task_get_dag 把最终 DAG 读出来，用「节点列表 + 依赖箭头」格式呈现给用户，标明哪个节点是 cron 触发起点 (若有)、哪些节点写 sharedfile。',
      '5. **展示**：调 task_get_dag 把最终 DAG 读出来，用「节点列表 + 依赖箭头」格式呈现给用户，标明哪个节点是 cron 触发起点 (若有)、哪个节点写入 run-level final_output、哪些节点写 sharedfile。'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0108'
WHERE prompt_key = 'main/dag_designer_zh'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%哪些节点写 sharedfile。%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '5. **size_cap**：单节点 `result` JSONB 超 4KB 必须走 sharedfile；DAG ≥ 10 节点要在 `inputs.summarization` 上认真填策略 (ADR-006/H7)。骨架阶段 summarization 仅字段位，但你设计时要把它纳入考虑。
6. **trigger 三种**：',
      '5. **size_cap**：单节点 `result` JSONB 超 4KB 必须走 sharedfile；DAG ≥ 10 节点要在 `inputs.summarization` 上认真填策略 (ADR-006/H7)。骨架阶段 summarization 仅字段位，但你设计时要把它纳入考虑。
6. **最终产物**：每个新 DAG 只选一个 `final_node_key`，它必须匹配已有 `node_key`，用于把该节点结果提升为 run-level `metadata.final_output`。中间产物可写 sharedfile，但不要让用户去 sharedfile 里找最终答案。
7. **trigger 三种**：'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0108'
WHERE prompt_key = 'main/dag_designer_zh'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%6. **trigger 三种**：%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '7. **错误信息一律给到用户**：调工具失败要把错误类型告诉用户 (而不是吞掉重试)，例如 ErrDAGNotFound / ErrVersionConflict / 资源不存在。',
      '8. **错误信息一律给到用户**：调工具失败要把错误类型告诉用户 (而不是吞掉重试)，例如 ErrDAGNotFound / ErrVersionConflict / 资源不存在。'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0108'
WHERE prompt_key = 'main/dag_designer_zh'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%7. **错误信息一律给到用户**%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '4. 用户确认后，调 `task_create_dag` 一次性建好，schedule.trigger="scheduled"，然后 task_dag_apply_ops 把 cron_expr 设上。',
      '4. 用户确认后，调 `task_create_dag` 一次性建好，传 `final_node_key="review"`，schedule.trigger="scheduled"，然后 task_dag_apply_ops 把 cron_expr 设上。'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0108'
WHERE prompt_key = 'main/dag_designer_zh'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%schedule.trigger="scheduled"，然后%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '`task_create_dag(agent_id, dag_key, title, description?, schedule, nodes?)`: Create a DAG. `schedule.trigger ∈ {manual, auto, scheduled}`; scheduled DAGs need a later task_dag_apply_ops call to write cron_expr. Set `agent_id` to your own orchestration agent id.',
      '`task_create_dag(agent_id, dag_key, title, description?, schedule, final_node_key?, nodes?)`: Create a DAG. `final_node_key` must point to the single user-facing final deliverable node; when the run completes, that node result is indexed as run-level `metadata.final_output`, while large payloads still belong in Shared Files. `schedule.trigger ∈ {manual, auto, scheduled}`; scheduled DAGs need a later task_dag_apply_ops call to write cron_expr. Set `agent_id` to your own orchestration agent id.'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0108'
WHERE prompt_key = 'main/dag_designer_en'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%description?, schedule, nodes?)%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '3. **Sketch the DAG**: Prepare a node list, in your head or in the reply — node_key / title / node_type / depends_on / key config for each node. Show the text sketch to the user and get approval before writing it.',
      '3. **Sketch the DAG**: Prepare a node list, in your head or in the reply — node_key / title / node_type / depends_on / key config for each node, and mark which node_key is the final deliverable node final_node_key. Show the text sketch to the user and get approval before writing it.'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0108'
WHERE prompt_key = 'main/dag_designer_en'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%key config for each node. Show the text sketch%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '5. **Present it**: Call task_get_dag to read the final DAG, then present it as "node list + dependency arrows". Mark the cron trigger entry node (if any) and which nodes write sharedfile outputs.',
      '5. **Present it**: Call task_get_dag to read the final DAG, then present it as "node list + dependency arrows". Mark the cron trigger entry node (if any), which node writes run-level final_output, and which nodes write sharedfile outputs.'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0108'
WHERE prompt_key = 'main/dag_designer_en'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%which nodes write sharedfile outputs.%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '5. **size_cap**: Any single node `result` JSONB over 4KB must use sharedfile. DAGs with 10 or more nodes need a serious `inputs.summarization` strategy (ADR-006/H7). In the skeleton phase, summarization is only a field slot, but account for it in your design.
6. **Three triggers**:',
      '5. **size_cap**: Any single node `result` JSONB over 4KB must use sharedfile. DAGs with 10 or more nodes need a serious `inputs.summarization` strategy (ADR-006/H7). In the skeleton phase, summarization is only a field slot, but account for it in your design.
6. **Final deliverable**: Pick exactly one `final_node_key` for each new DAG. It must match an existing `node_key` and is used to promote that node result into run-level `metadata.final_output`. Intermediate artifacts may use sharedfile, but users should not have to search sharedfile for the final answer.
7. **Three triggers**:'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0108'
WHERE prompt_key = 'main/dag_designer_en'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%6. **Three triggers**:%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '7. **Always surface tool errors to the user**: If a tool call fails, tell the user the error type instead of swallowing it and retrying silently, for example ErrDAGNotFound / ErrVersionConflict / resource not found.',
      '8. **Always surface tool errors to the user**: If a tool call fails, tell the user the error type instead of swallowing it and retrying silently, for example ErrDAGNotFound / ErrVersionConflict / resource not found.'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0108'
WHERE prompt_key = 'main/dag_designer_en'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%7. **Always surface tool errors to the user**%';

UPDATE public.prompt_templates
SET prompt_text = REPLACE(
      prompt_text,
      '4. After the user confirms, call `task_create_dag` once to create it with schedule.trigger="scheduled", then call task_dag_apply_ops to set cron_expr.',
      '4. After the user confirms, call `task_create_dag` once with `final_node_key="review"` and schedule.trigger="scheduled", then call task_dag_apply_ops to set cron_expr.'
    ),
    updated_at = NOW(),
    updated_by = 'migration:0108'
WHERE prompt_key = 'main/dag_designer_en'
  AND manually_edited = FALSE
  AND prompt_text LIKE '%once to create it with schedule.trigger="scheduled"%';
