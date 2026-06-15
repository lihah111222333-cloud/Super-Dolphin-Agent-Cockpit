INSERT INTO prompt_templates (
    id, prompt_key, title, agent_key, tool_name, prompt_text,
    variables, tags, description, when_to_use, created_by, updated_by,
    created_at, updated_at
) VALUES (
    1, 'main/default', 'Default', 'main', 'chat', 'Be useful.',
    '{}', '["scope.global"]', 'fixture prompt', 'general use', 'fixture', 'fixture',
    1710000000000, 1710000000000
);

INSERT INTO prompt_template_sections (
    id, template_id, section_key, region, ordinal, body, enable_when,
    created_at, updated_at, trigger_type, recall_topic
) VALUES (
    1, 1, 'recall_baseline', 'dynamic', 10, 'SQLite baseline recall body.', '{}',
    1710000000000, 1710000000000, 'recall', 'sqlite-baseline'
);

INSERT INTO cron_jobs (
    id, name, prompt, schedule_expr, cwd, config, skills, next_run_at,
    created_at, updated_at
) VALUES (
    'cron-fixture', 'Cron fixture', 'Run a smoke task', '*/5 * * * *',
    'C:/fixture', '{}', '[]', 1710000300000, 1710000000000, 1710000000000
);

INSERT INTO cron_job_runs (
    id, job_id, scheduled_at, idempotency_key, dedupe_key, status,
    created_at, updated_at
) VALUES (
    'cron-run-fixture', 'cron-fixture', 1710000300000,
    'cron-fixture-1710000300000', 'dedupe-cron-fixture', 'running',
    1710000000000, 1710000000000
);

INSERT INTO task_dags (
    id, dag_key, title, description, status, created_by, metadata,
    created_at, updated_at, trigger, cron_expr, next_run_at, version
) VALUES (
    1, 'dag-fixture', 'Fixture DAG', 'minimal DAG fixture', 'active', 'fixture',
    '{"final_node_key":"node-1"}', 1710000000000, 1710000000000,
    'scheduled', '*/5 * * * *', 1710000300000, 1
);

INSERT INTO task_dag_runs (
    id, run_key, dag_key, dag_version_snapshot, trigger_source, status,
    started_at, events, budget_used, metadata, created_at, updated_at
) VALUES (
    1, 'dag-fixture#run-1', 'dag-fixture', 1, 'scheduled', 'running',
    1710000000000, '[]', 0, '{}', 1710000000000, 1710000000000
);

INSERT INTO task_dag_nodes (
    id, dag_key, node_key, title, node_type, assigned_to, depends_on,
    status, command_ref, config, result, created_at, updated_at, reads, writes
) VALUES (
    1, 'dag-fixture', 'node-1', 'Template node', 'task', 'agent-1', '[]',
    'ready', '', '{}', '{}', 1710000000000, 1710000000000, '[]', '[]'
);

INSERT INTO task_dag_nodes (
    id, dag_key, node_key, title, node_type, assigned_to, depends_on,
    status, command_ref, config, result, created_at, updated_at, run_id, reads, writes
) VALUES (
    2, 'dag-fixture', 'node-1', 'Runtime node', 'task', 'agent-1', '[]',
    'running', '', '{}', '{}', 1710000000000, 1710000000000, 1, '[]', '[]'
);

INSERT INTO task_dag_wakeups (
    id, dag_key, node_key, run_id, wakeup_kind, target_agent_id,
    prompt_payload, idempotency_key, status, next_retry_at,
    created_at, updated_at
) VALUES (
    1, 'dag-fixture', 'node-1', 1, 'start_turn', 'agent-1',
    '{"prompt":"hello"}', 'wakeup-fixture', 'pending', 1710000000000,
    1710000000000, 1710000000000
);

INSERT INTO workspace_runs (
    id, run_key, dag_key, source_root, workspace_path, status,
    created_by, updated_by, metadata, created_at, updated_at
) VALUES (
    1, 'workspace-fixture', 'dag-fixture', 'C:/source', 'C:/workspace',
    'active', 'fixture', 'fixture', '{}', 1710000000000, 1710000000000
);

INSERT INTO workspace_run_files (
    id, run_key, relative_path, baseline_sha256, workspace_sha256,
    source_sha256_before, source_sha256_after, state, created_at, updated_at
) VALUES (
    1, 'workspace-fixture', 'README.md', 'a', 'b', 'c', 'd',
    'tracked', 1710000000000, 1710000000000
);
