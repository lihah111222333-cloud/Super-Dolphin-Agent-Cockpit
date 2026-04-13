package db

import "testing"

func TestBaselineIncludesConflictTargetConstraints(t *testing.T) {
	t.Parallel()

	assertMigrationContains(t, "001_baseline.sql", []string{
		"CONSTRAINT agent_threads_pkey PRIMARY KEY (thread_id)",
		"CONSTRAINT agent_status_pkey PRIMARY KEY (agent_id)",
		"CONSTRAINT command_cards_card_key_key UNIQUE (card_key)",
		"CONSTRAINT cwd_instance_locks_pkey PRIMARY KEY (cwd)",
		"CONSTRAINT prompt_templates_prompt_key_key UNIQUE (prompt_key)",
		"CONSTRAINT shared_files_pkey PRIMARY KEY (path)",
		"CONSTRAINT ui_preferences_pkey PRIMARY KEY (cwd, key)",
		"CONSTRAINT workspace_runs_run_key_key UNIQUE (run_key)",
		"CONSTRAINT workspace_run_files_run_key_relative_path_key UNIQUE (run_key, relative_path)",
	})
}

func TestBaselineSchemaRepairMigrationRebuildsConflictTargetConstraints(t *testing.T) {
	t.Parallel()

	assertMigrationContains(t, "0030_baseline_schema_repair.sql", []string{
		"conrelid = 'public.agent_threads'::regclass",
		"PARTITION BY thread_id",
		"ADD CONSTRAINT agent_threads_pkey PRIMARY KEY (thread_id)",
		"conrelid = 'public.agent_status'::regclass",
		"PARTITION BY agent_id",
		"ADD CONSTRAINT agent_status_pkey PRIMARY KEY (agent_id)",
		"conrelid = 'public.command_cards'::regclass",
		"PARTITION BY card_key",
		"ADD CONSTRAINT command_cards_card_key_key UNIQUE (card_key)",
		"conrelid = 'public.cwd_instance_locks'::regclass",
		"PARTITION BY cwd",
		"ADD CONSTRAINT cwd_instance_locks_pkey PRIMARY KEY (cwd)",
		"conrelid = 'public.prompt_templates'::regclass",
		"PARTITION BY prompt_key",
		"ADD CONSTRAINT prompt_templates_prompt_key_key UNIQUE (prompt_key)",
		"conrelid = 'public.shared_files'::regclass",
		"PARTITION BY path",
		"ADD CONSTRAINT shared_files_pkey PRIMARY KEY (path)",
		"conrelid = 'public.ui_preferences'::regclass",
		"PARTITION BY cwd, key",
		"ADD CONSTRAINT ui_preferences_pkey PRIMARY KEY (cwd, key)",
		"conrelid = 'public.workspace_runs'::regclass",
		"PARTITION BY run_key",
		"ADD CONSTRAINT workspace_runs_run_key_key UNIQUE (run_key)",
		"conrelid = 'public.workspace_run_files'::regclass",
		"PARTITION BY run_key, relative_path",
		"ADD CONSTRAINT workspace_run_files_run_key_relative_path_key",
	})
}
