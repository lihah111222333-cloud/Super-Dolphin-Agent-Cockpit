package sqlite

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
)

type tableContract struct {
	PrimaryKey []string
	NotNull    []string
	Checks     []string
	Indexes    []string
	ForeignKey []foreignKeyContract
}

type foreignKeyContract struct {
	Column string
	Table  string
}

func TestSQLiteBaselineContracts(t *testing.T) {
	db := openBaselineDB(t)
	tables := sqliteTables(t, db)
	contracts := baselineContracts()

	for _, table := range allPersistentTables {
		if _, ok := contracts[table]; !ok {
			t.Fatalf("missing expected contract declaration for persistent table %q", table)
		}
	}
	for table := range tables {
		if _, ok := contracts[table]; !ok {
			t.Fatalf("SQLite baseline created table %q without an expected contract", table)
		}
	}

	for table, contract := range contracts {
		t.Run(table, func(t *testing.T) {
			assertPrimaryKey(t, db, table, contract.PrimaryKey)
			assertNotNullColumns(t, db, table, contract.NotNull)
			assertTableSQLContains(t, db, table, contract.Checks)
			for _, index := range contract.Indexes {
				assertIndex(t, db, table, index, indexLooksUnique(index), "")
			}
			assertForeignKeys(t, db, table, contract.ForeignKey)
		})
	}
}

func TestSQLiteBaselineFixtureForeignKeys(t *testing.T) {
	db := openBaselineDB(t)
	execFile(t, db, "internal/platform/db/sqlite/testdata/minimal_fixture.sql")

	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()

	var violations []string
	for rows.Next() {
		var table string
		var rowID int64
		var parent string
		var fkID int
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			t.Fatalf("scan foreign_key_check: %v", err)
		}
		violations = append(violations, fmt.Sprintf("%s rowid=%d parent=%s fk=%d", table, rowID, parent, fkID))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign_key_check: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("fixture violates foreign keys: %s", strings.Join(violations, "; "))
	}
}

func TestSharedFilesContentLocationMigrationPreservesOldRowsAsInline(t *testing.T) {
	db := openBaselineDB(t)
	mustExec(t, db, "DROP INDEX IF EXISTS idx_shared_files_updated_at")
	mustExec(t, db, "DROP TABLE shared_files")
	mustExec(t, db, `
		CREATE TABLE shared_files (
			path TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			updated_by TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`)
	mustExec(t, db, "CREATE INDEX idx_shared_files_updated_at ON shared_files(updated_at DESC)")
	mustExec(t, db, `
		INSERT INTO shared_files (path, content, updated_by, created_at, updated_at)
		VALUES ('reports/empty.md', '', 'test', 1710000000000, 1710000000001)
	`)

	execFile(t, db, "internal/platform/db/sqlite/migrations/108_shared_files_content_location.sql")

	assertNotNullColumns(t, db, "shared_files", []string{"content", "content_location", "updated_by", "created_at", "updated_at"})
	assertTableSQLContains(t, db, "shared_files", []string{"content_location IN ('inline', 'disk')"})
	assertIndex(t, db, "shared_files", "idx_shared_files_updated_at", false, "")

	var content, location string
	if err := db.QueryRow("SELECT content, content_location FROM shared_files WHERE path = ?", "reports/empty.md").Scan(&content, &location); err != nil {
		t.Fatalf("read migrated shared file: %v", err)
	}
	if content != "" || location != "inline" {
		t.Fatalf("migrated shared file content=%q location=%q, want empty inline", content, location)
	}
}

func TestPromptTemplatesNullableMatchWhenMigrationNormalizesLegacyScalars(t *testing.T) {
	db := openBaselineDB(t)
	mustExec(t, db, "DROP INDEX IF EXISTS idx_prompt_templates_agent_tool")
	mustExec(t, db, "DROP INDEX IF EXISTS idx_prompt_templates_enabled")
	mustExec(t, db, "DROP INDEX IF EXISTS idx_prompt_templates_auto_route")
	mustExec(t, db, "DROP TABLE prompt_templates")
	mustExec(t, db, `
		CREATE TABLE prompt_templates (
			id INTEGER PRIMARY KEY,
			prompt_key TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL DEFAULT '',
			agent_key TEXT NOT NULL DEFAULT '',
			tool_name TEXT NOT NULL DEFAULT '',
			prompt_text TEXT NOT NULL,
			variables TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(variables)),
			tags TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(tags)),
			description TEXT NOT NULL DEFAULT '',
			when_to_use TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
			manually_edited INTEGER NOT NULL DEFAULT 0 CHECK(manually_edited IN (0, 1)),
			match_when TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(match_when)),
			priority INTEGER NOT NULL DEFAULT 0,
			created_by TEXT NOT NULL DEFAULT '',
			updated_by TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`)
	mustExec(t, db, "CREATE INDEX idx_prompt_templates_agent_tool ON prompt_templates(agent_key, tool_name)")
	mustExec(t, db, "CREATE INDEX idx_prompt_templates_enabled ON prompt_templates(enabled, updated_at DESC)")
	mustExec(t, db, "CREATE INDEX idx_prompt_templates_auto_route ON prompt_templates(enabled, priority DESC) WHERE match_when <> '{}'")
	mustExec(t, db, `
		INSERT INTO prompt_templates (id, prompt_key, prompt_text, match_when, created_at, updated_at)
		VALUES
			(1, 'legacy-array', 'body', '[]', 1710000000000, 1710000000000),
			(2, 'legacy-string', 'body', '"x"', 1710000000000, 1710000000000),
			(3, 'legacy-object', 'body', '{"provider":"codex"}', 1710000000000, 1710000000000)
	`)

	execFile(t, db, "internal/platform/db/sqlite/migrations/111_prompt_templates_nullable_match_when.sql")

	var arrayMatch, stringMatch sql.NullString
	if err := db.QueryRow("SELECT match_when FROM prompt_templates WHERE prompt_key = 'legacy-array'").Scan(&arrayMatch); err != nil {
		t.Fatalf("read legacy-array match_when: %v", err)
	}
	if err := db.QueryRow("SELECT match_when FROM prompt_templates WHERE prompt_key = 'legacy-string'").Scan(&stringMatch); err != nil {
		t.Fatalf("read legacy-string match_when: %v", err)
	}
	if arrayMatch.Valid || stringMatch.Valid {
		t.Fatalf("legacy scalar match_when = array:%q string:%q, want SQL NULL", arrayMatch.String, stringMatch.String)
	}
	var objectMatch string
	if err := db.QueryRow("SELECT match_when FROM prompt_templates WHERE prompt_key = 'legacy-object'").Scan(&objectMatch); err != nil {
		t.Fatalf("read legacy-object match_when: %v", err)
	}
	if objectMatch != `{"provider":"codex"}` {
		t.Fatalf("legacy object match_when = %q, want object preserved", objectMatch)
	}
}

func TestSystemLogsTraceSpanMigrationPreservesAgentV3Rows(t *testing.T) {
	db := openBaselineDB(t)
	for _, index := range []string{
		"idx_system_logs_ts_id",
		"idx_system_logs_level_ts_id",
		"idx_system_logs_source_ts_id",
		"idx_system_logs_agent_ts_id",
		"idx_system_logs_thread_ts_id",
		"idx_system_logs_trace_ts_id",
		"idx_system_logs_span_ts_id",
		"idx_system_logs_logger",
		"idx_system_logs_event",
		"idx_system_logs_tool",
	} {
		mustExec(t, db, "DROP INDEX IF EXISTS "+index)
	}
	mustExec(t, db, "DROP TABLE system_logs")
	mustExec(t, db, `
		CREATE TABLE system_logs (
			id INTEGER PRIMARY KEY,
			ts INTEGER NOT NULL,
			level TEXT NOT NULL,
			logger TEXT NOT NULL,
			message TEXT NOT NULL,
			raw TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			component TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			thread_id TEXT NOT NULL DEFAULT '',
			trace_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			tool_name TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER,
			extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra))
		)
	`)
	mustExec(t, db, `
		INSERT INTO system_logs (
			id, ts, level, logger, message, raw, source, component,
			agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, extra
		)
		VALUES (
			7, 1710000000000, 'warn', 'mcp-control', 'agent-v3',
			'raw', 'mcp-control', 'mcp-lsp', 'agent-1', 'thread-1',
			'trace-1', 'ctl/log', 'definition', 42, '{"ok":true}'
		)
	`)

	execFile(t, db, "internal/platform/db/sqlite/migrations/112_system_logs_trace_span.sql")

	assertNotNullColumns(t, db, "system_logs", []string{"span_id", "parent_span_id"})
	assertIndex(t, db, "system_logs", "idx_system_logs_trace_ts_id", false, "trace_id <> ''")
	assertIndex(t, db, "system_logs", "idx_system_logs_span_ts_id", false, "span_id <> ''")

	var traceID, spanID, parentSpanID, extra string
	if err := db.QueryRow("SELECT trace_id, span_id, parent_span_id, extra FROM system_logs WHERE id = 7").Scan(&traceID, &spanID, &parentSpanID, &extra); err != nil {
		t.Fatalf("read migrated system log: %v", err)
	}
	if traceID != "trace-1" || spanID != "" || parentSpanID != "" || extra != `{"ok":true}` {
		t.Fatalf("migrated system log trace=%q span=%q parent=%q extra=%q", traceID, spanID, parentSpanID, extra)
	}
}

func baselineContracts() map[string]tableContract {
	return map[string]tableContract{
		"schema_migrations": {PrimaryKey: []string{"version"}, NotNull: []string{"name", "filename", "applied_at"}},
		"runtime_locks":     {PrimaryKey: []string{"lock_key"}, NotNull: []string{"holder", "lease_expires_at", "updated_at"}, Indexes: []string{"idx_runtime_locks_expiry"}},

		"agent_codex_binding":    {PrimaryKey: []string{"agent_id"}, NotNull: []string{"codex_thread_id", "created_at", "updated_at", "cwd", "archived"}, Checks: []string{"archived IN (0, 1)"}, Indexes: []string{"idx_acb_codex_thread", "idx_acb_created_at_desc", "idx_acb_cwd"}},
		"agent_provider_binding": {PrimaryKey: []string{"agent_id"}, NotNull: []string{"provider", "provider_thread_id", "codex_thread_id", "cwd", "archived", "created_at", "updated_at", "session_uuid", "codex_home", "codex_instance_key", "codex_model_provider"}, Checks: []string{"provider <> ''", "archived IN (0, 1)"}, Indexes: []string{"uq_agent_provider_binding_provider_thread", "idx_agent_provider_binding_codex_thread"}},
		"agent_status":           {PrimaryKey: []string{"agent_id"}, NotNull: []string{"agent_name", "session_id", "status", "stagnant_sec", "error", "output_tail", "created_at", "updated_at"}, Checks: []string{"json_valid(output_tail)", "stagnant_sec >= 0"}, Indexes: []string{"idx_agent_status_status_updated", "idx_agent_status_updated_at_desc"}},
		"agent_threads":          {PrimaryKey: []string{"thread_id"}, NotNull: []string{"name", "prompt", "model", "cwd", "status", "port", "pid", "created_at", "updated_at", "config_override", "prompt_snapshot", "pending_launch", "manually_renamed"}, Checks: []string{"json_valid(config_override)", "json_valid(prompt_snapshot)", "pending_launch IN (0, 1)", "manually_renamed IN (0, 1)"}, Indexes: []string{"idx_agent_threads_status", "idx_agent_threads_port", "idx_agent_threads_pid", "idx_agent_threads_workspace_run_key", "idx_agent_threads_owner_thread_id", "idx_agent_threads_agent_key", "idx_agent_threads_pending_launch"}},

		"audit_events":       {PrimaryKey: []string{"id"}, NotNull: []string{"ts", "event_type", "action", "result", "actor", "target", "detail", "level", "extra"}, Checks: []string{"json_valid(extra)"}, Indexes: []string{"idx_audit_events_ts", "idx_audit_events_event_type", "idx_audit_events_action", "idx_audit_events_result", "idx_audit_events_actor"}},
		"bus_exception_logs": {PrimaryKey: []string{"id"}, NotNull: []string{"ts", "category", "severity", "source", "tool_name", "message", "traceback", "extra", "has_traceback", "has_extra"}, Checks: []string{"json_valid(extra)", "has_traceback IN (0, 1)", "has_extra IN (0, 1)"}, Indexes: []string{"idx_bus_exception_logs_ts", "idx_bus_exception_logs_category", "idx_bus_exception_logs_severity"}},
		"system_logs":        {PrimaryKey: []string{"id"}, NotNull: []string{"ts", "level", "logger", "message", "raw", "source", "component", "agent_id", "thread_id", "trace_id", "span_id", "parent_span_id", "event_type", "tool_name", "extra"}, Checks: []string{"json_valid(extra)"}, Indexes: []string{"idx_system_logs_ts_id", "idx_system_logs_level_ts_id", "idx_system_logs_source_ts_id", "idx_system_logs_agent_ts_id", "idx_system_logs_thread_ts_id", "idx_system_logs_trace_ts_id", "idx_system_logs_span_ts_id", "idx_system_logs_logger", "idx_system_logs_event", "idx_system_logs_tool"}},
		"task_traces":        {PrimaryKey: []string{"id"}, NotNull: []string{"trace_id", "span_id", "parent_span_id", "span_name", "component", "status", "input_payload", "output_payload", "metadata", "started_at", "duration_ms"}, Checks: []string{"json_valid(input_payload)", "json_valid(output_payload)", "json_valid(metadata)", "duration_ms >= 0"}, Indexes: []string{"idx_task_traces_trace_started", "idx_task_traces_component_started"}},

		"prompts":                  {PrimaryKey: []string{"id"}, NotNull: []string{"agent_key", "tool_name", "prompt_text", "is_pinned", "sort_order", "created_at", "updated_at"}, Checks: []string{"is_pinned IN (0, 1)"}, Indexes: []string{"idx_prompts_agent_key", "idx_prompts_sort_order"}},
		"prompt_templates":         {PrimaryKey: []string{"id"}, NotNull: []string{"prompt_key", "title", "agent_key", "tool_name", "prompt_text", "variables", "tags", "description", "when_to_use", "enabled", "manually_edited", "priority", "created_by", "updated_by", "created_at", "updated_at"}, Checks: []string{"json_valid(variables)", "json_valid(tags)", "match_when IS NULL OR (json_valid(match_when) AND json_type(match_when) = 'object')", "enabled IN (0, 1)", "manually_edited IN (0, 1)"}, Indexes: []string{"idx_prompt_templates_agent_tool", "idx_prompt_templates_enabled", "idx_prompt_templates_auto_route"}},
		"prompt_template_versions": {PrimaryKey: []string{"id"}, NotNull: []string{"prompt_key", "title", "agent_key", "tool_name", "prompt_text", "variables", "tags", "enabled", "created_by", "updated_by", "created_at", "archived_at"}, Checks: []string{"json_valid(variables)", "json_valid(tags)", "enabled IN (0, 1)"}, Indexes: []string{"idx_prompt_template_versions_key_id"}},
		"prompt_versions":          {PrimaryKey: []string{"id"}, NotNull: []string{"prompt_key", "title", "agent_key", "tool_name", "prompt_text", "variables", "tags", "description", "enabled", "created_by", "updated_by", "created_at", "archived_at"}, Checks: []string{"json_valid(variables)", "json_valid(tags)", "enabled IN (0, 1)"}, Indexes: []string{"idx_prompt_versions_key_id"}},
		"prompt_template_sections": {PrimaryKey: []string{"id"}, NotNull: []string{"template_id", "section_key", "region", "ordinal", "body", "enable_when", "enabled", "created_at", "updated_at", "trigger_type", "recall_topic"}, Checks: []string{"region IN ('static', 'dynamic')", "json_valid(enable_when)", "enabled IN (0, 1)", "trigger_type IN ('always', 'keyword', 'recall')"}, Indexes: []string{"idx_prompt_template_sections_lookup", "idx_prompt_sections_recall_topic_lookup"}, ForeignKey: []foreignKeyContract{{Column: "template_id", Table: "prompt_templates"}}},
		"prompt_recall_topics":     {PrimaryKey: []string{"cwd", "topic"}, NotNull: []string{"cwd", "topic", "template_id", "section_key"}, Checks: []string{"trim(cwd) <> ''", "trim(topic) <> ''", "template_id >= 0"}},
		"prompt_routing_tests":     {PrimaryKey: []string{"id"}, NotNull: []string{"input", "expected_prompt_key", "note", "enabled", "created_at", "updated_at"}, Checks: []string{"enabled IN (0, 1)"}},
		"prompt_intent_drafts":     {PrimaryKey: []string{"id"}, NotNull: []string{"draft_key", "cwd", "kind", "raw_input", "source_type", "generated_card", "confidence", "status", "scope", "issues", "created_at", "updated_at"}, Checks: []string{"kind IN ('expert', 'recall', 'default_rule')", "json_valid(generated_card)", "status IN ('draft', 'ready_to_save', 'enabled', 'rejected')", "scope IN ('project', 'global')", "json_valid(issues)"}, Indexes: []string{"idx_prompt_intent_drafts_cwd_status_updated", "idx_prompt_intent_drafts_kind_cwd"}},

		"command_cards":         {PrimaryKey: []string{"id"}, NotNull: []string{"card_key", "title", "description", "command_template", "args_schema", "risk_level", "enabled", "created_by", "updated_by", "created_at", "updated_at"}, Checks: []string{"json_valid(args_schema)", "enabled IN (0, 1)"}, Indexes: []string{"idx_command_cards_risk_enabled"}},
		"command_card_versions": {PrimaryKey: []string{"id"}, NotNull: []string{"card_key", "title", "description", "command_template", "args_schema", "risk_level", "enabled", "created_by", "updated_by", "created_at", "archived_at"}, Checks: []string{"json_valid(args_schema)", "enabled IN (0, 1)"}, Indexes: []string{"idx_command_card_versions_key_id"}},
		"command_card_runs":     {PrimaryKey: []string{"id"}, NotNull: []string{"card_key", "requested_by", "params", "rendered_command", "risk_level", "status", "requires_review", "output", "error", "created_at", "updated_at"}, Checks: []string{"json_valid(params)", "requires_review IN (0, 1)"}, Indexes: []string{"idx_command_card_runs_status_created", "idx_command_card_runs_card_key"}},
		"shared_files":          {PrimaryKey: []string{"path"}, NotNull: []string{"content", "content_location", "updated_by", "created_at", "updated_at"}, Checks: []string{"content_location IN ('inline', 'disk')"}, Indexes: []string{"idx_shared_files_updated_at"}},
		"datasource_v2_documents": {
			PrimaryKey: []string{"id"},
			NotNull:    []string{"source_path", "file_name", "extension", "size_bytes", "chunk_count", "total_chars", "status", "created_at", "updated_at"},
			Checks:     []string{"status IN ('importing', 'ready', 'failed')", "source_path <> ''", "file_name <> ''", "size_bytes >= 0", "chunk_count >= 0", "total_chars >= 0", "status <> 'ready' OR content_hash IS NOT NULL"},
		},
		"datasource_v2_text_chunks": {
			PrimaryKey: []string{"id"},
			NotNull:    []string{"document_id", "chunk_index", "content", "char_count", "byte_count", "created_at"},
			Checks:     []string{"chunk_index >= 0", "content <> ''", "char_count > 0", "byte_count > 0"},
			Indexes:    []string{"idx_datasource_v2_text_chunks_document_order"},
			ForeignKey: []foreignKeyContract{
				{Column: "document_id", Table: "datasource_v2_documents"},
			},
		},

		"agent_feedback_events": {PrimaryKey: []string{"id"}, NotNull: []string{"thread_id", "turn_id", "agent_key", "event_type", "actor", "payload", "created_at"}, Checks: []string{"json_valid(payload)"}, Indexes: []string{"idx_agent_feedback_events_thread", "idx_agent_feedback_events_agent_key", "idx_agent_feedback_events_prompt_version", "idx_agent_feedback_events_event_type"}},
		"session_insights":      {PrimaryKey: []string{"id"}, NotNull: []string{"thread_id", "agent_id", "session_id", "provider", "local_turn_id", "provider_turn_id", "duration_ms", "status", "tool_calls", "tool_calls_observed", "tool_failures", "tool_failures_observed", "approval_requests", "approval_requests_observed", "token_input", "token_output", "token_total", "token_snapshot_observed", "context_window_tokens", "ui_projection", "skills_selected", "created_at", "updated_at"}, Checks: []string{"duration_ms >= 0", "tool_calls >= 0", "tool_failures >= 0", "approval_requests >= 0", "json_valid(skills_selected)"}, Indexes: []string{"idx_session_insights_thread_created", "idx_session_insights_created", "uq_session_insights_local_turn", "uq_session_insights_provider_turn", "idx_session_insights_approval_observed", "idx_session_insights_token_observed"}},
		"hook_pending_reviews":  {PrimaryKey: []string{"hook_call_id"}, NotNull: []string{"topic", "agent_id", "thread_id", "turn_id", "subscriber_lease", "payload", "decision", "reason", "default_action", "status", "created_at", "deadline_at", "idempotency_key", "resolved_by"}, Checks: []string{"json_valid(payload)"}, Indexes: []string{"idx_hook_pending_agent", "idx_hook_pending_deadline"}},
		"agent_interactions":    {PrimaryKey: []string{"id"}, NotNull: []string{"thread_id", "sender", "receiver", "msg_type", "status", "requires_review", "reviewed_by", "review_note", "payload", "created_at", "updated_at"}, Checks: []string{"requires_review IN (0, 1)", "json_valid(payload)"}, Indexes: []string{"idx_agent_interactions_thread_created", "idx_agent_interactions_sender_receiver", "idx_agent_interactions_status_review"}},
		"mcp_tool_lifecycle":    {PrimaryKey: []string{"workspace_root", "server_name", "tool_name"}, NotNull: []string{"workspace_root", "server_name", "manifest_name", "tool_name", "state", "reason", "replacement_tool", "last_seen_at", "created_at", "updated_at"}, Checks: []string{"workspace_root <> ''", "server_name <> ''", "tool_name <> ''", "state IN ('enabled', 'disabled', 'suspended', 'removed')", "last_seen_at >= 0", "created_at >= 0", "updated_at >= 0"}, Indexes: []string{"idx_mcp_tool_lifecycle_server"}},

		"topology_approvals":         {PrimaryKey: []string{"id"}, NotNull: []string{"status", "requested_by", "reason", "created_at", "expire_at", "reviewer", "review_note", "arch_hash", "proposed_architecture"}, Checks: []string{"json_valid(proposed_architecture)"}, Indexes: []string{"idx_topology_approvals_status_created_at", "idx_topology_approvals_arch_hash"}},
		"topology_approval_archives": {PrimaryKey: []string{"id"}, NotNull: []string{"status", "requested_by", "reason", "created_at", "expire_at", "reviewer", "review_note", "arch_hash", "proposed_architecture", "archived_at"}, Checks: []string{"json_valid(proposed_architecture)"}, Indexes: []string{"idx_topology_approval_archives_archived_at"}},
		"ui_preferences":             {PrimaryKey: []string{"cwd", "key"}, NotNull: []string{"cwd", "key", "value", "updated_at"}, Checks: []string{"json_valid(value)"}, Indexes: []string{"idx_ui_preferences_key"}},

		"cwd_instance_locks":   {PrimaryKey: []string{"cwd"}, NotNull: []string{"instance_id", "pid", "acquired_at", "heartbeat_at"}, Indexes: []string{"idx_cwd_instance_locks_heartbeat"}},
		"turn_dedupe_registry": {PrimaryKey: []string{"dedupe_key"}, NotNull: []string{"local_turn_id", "provider_turn_id", "thread_id", "created_at", "updated_at"}, Indexes: []string{"idx_turn_dedupe_registry_updated_at", "idx_turn_dedupe_registry_live"}},
		"cron_jobs":            {PrimaryKey: []string{"id"}, NotNull: []string{"name", "prompt", "schedule_type", "schedule_expr", "timezone", "provider", "model", "cwd", "config", "skills", "enabled", "next_run_at", "failure_count", "max_attempts", "created_at", "updated_at"}, Checks: []string{"schedule_expr <> ''", "provider IN ('codex', 'claude')", "cwd <> ''", "json_valid(config)", "json_valid(skills)", "enabled IN (0, 1)", "failure_count >= 0", "max_attempts >= 0"}, Indexes: []string{"idx_cron_jobs_due", "idx_cron_jobs_claim"}},
		"cron_job_runs":        {PrimaryKey: []string{"id"}, NotNull: []string{"job_id", "scheduled_at", "idempotency_key", "dedupe_key", "thread_id", "agent_id", "turn_id", "status", "error", "created_at", "updated_at"}, Checks: []string{"status IN ('pending', 'submitting', 'submitted', 'running', 'finished', 'failed', 'observe_lost')"}, Indexes: []string{"uq_cron_job_runs_idempotency", "uq_cron_job_runs_dedupe_key", "idx_cron_job_runs_job_created", "idx_cron_job_runs_status_active", "idx_cron_job_runs_turn_running"}, ForeignKey: []foreignKeyContract{{Column: "job_id", Table: "cron_jobs"}}},

		"task_acks":              {PrimaryKey: []string{"id"}, NotNull: []string{"ack_key", "title", "description", "assigned_to", "requested_by", "priority", "status", "progress", "ack_message", "result_summary", "metadata", "created_at", "updated_at"}, Checks: []string{"progress >= 0", "progress <= 100", "json_valid(metadata)"}, Indexes: []string{"idx_task_acks_status", "idx_task_acks_priority", "idx_task_acks_assigned_to", "idx_task_acks_due_at"}},
		"task_dags":              {PrimaryKey: []string{"id"}, NotNull: []string{"dag_key", "title", "description", "status", "created_by", "metadata", "created_at", "updated_at", "trigger", "owner_id", "cron_expr", "version"}, Checks: []string{"json_valid(metadata)", "trigger IN ('manual', 'auto', 'scheduled', 'external')"}, Indexes: []string{"idx_task_dags_status", "idx_task_dags_updated_id", "idx_task_dags_next_run_scheduled"}},
		"task_dag_runs":          {PrimaryKey: []string{"id"}, NotNull: []string{"run_key", "dag_key", "dag_version_snapshot", "trigger_source", "status", "started_at", "events", "budget_used", "metadata", "created_at", "updated_at"}, Checks: []string{"trigger_source IN ('manual', 'auto', 'scheduled', 'external', '')", "status IN ('running', 'succeeded', 'failed', 'cancelled')", "json_valid(events)", "json_valid(metadata)"}, Indexes: []string{"idx_task_dag_runs_run_key", "idx_task_dag_runs_dag_key_started", "idx_task_dag_runs_dag_status_started", "idx_task_dag_runs_status", "idx_task_dag_runs_running"}},
		"task_dag_nodes":         {PrimaryKey: []string{"id"}, NotNull: []string{"dag_key", "node_key", "title", "node_type", "assigned_to", "depends_on", "status", "command_ref", "config", "result", "created_at", "updated_at", "reads", "writes"}, Checks: []string{"json_valid(depends_on)", "json_type(depends_on) = 'array'", "json_valid(config)", "json_valid(result)", "json_valid(reads)", "json_type(reads) = 'array'", "json_valid(writes)", "json_type(writes) = 'array'"}, Indexes: []string{"idx_task_dag_nodes_dag_key", "idx_task_dag_nodes_status", "idx_task_dag_nodes_assigned_status", "idx_task_dag_nodes_run_id", "idx_task_dag_nodes_spawning_thread_id", "uq_task_dag_nodes_template_dag_node", "uq_task_dag_nodes_runtime_dag_run_node"}, ForeignKey: []foreignKeyContract{{Column: "run_id", Table: "task_dag_runs"}}},
		"task_dag_wakeups":       {PrimaryKey: []string{"id"}, NotNull: []string{"dag_key", "node_key", "wakeup_kind", "target_agent_id", "prompt_payload", "idempotency_key", "status", "attempt_count", "next_retry_at", "claimed_by", "last_error", "created_at", "updated_at"}, Checks: []string{"json_valid(prompt_payload)", "status NOT IN ('pending', 'dispatching')"}, Indexes: []string{"idx_task_dag_wakeups_poll", "idx_task_dag_wakeups_sent_target", "idx_task_dag_wakeups_run_node", "idx_task_dag_wakeups_run_id"}, ForeignKey: []foreignKeyContract{{Column: "run_id", Table: "task_dag_runs"}}},
		"task_dag_worker_leases": {PrimaryKey: []string{"target_agent_id"}, NotNull: []string{"owner_id", "lease_expires_at", "updated_at"}},

		"workspace_runs":      {PrimaryKey: []string{"id"}, NotNull: []string{"run_key", "dag_key", "source_root", "workspace_path", "status", "created_by", "updated_by", "metadata", "created_at", "updated_at"}, Checks: []string{"json_valid(metadata)"}, Indexes: []string{"idx_workspace_runs_status_updated", "idx_workspace_runs_dag"}},
		"workspace_run_files": {PrimaryKey: []string{"id"}, NotNull: []string{"run_key", "relative_path", "baseline_sha256", "workspace_sha256", "source_sha256_before", "source_sha256_after", "state", "last_error", "created_at", "updated_at"}, Indexes: []string{"idx_workspace_run_files_run_state", "idx_workspace_run_files_run_path"}, ForeignKey: []foreignKeyContract{{Column: "run_key", Table: "workspace_runs"}}},
	}
}

func assertNotNullColumns(t *testing.T, db *sql.DB, table string, want []string) {
	t.Helper()

	info := tableInfo(t, db, table)
	for _, col := range want {
		if !info[col].notNull {
			t.Fatalf("%s.%s is nullable, want NOT NULL", table, col)
		}
	}
}

func assertTableSQLContains(t *testing.T, db *sql.DB, table string, fragments []string) {
	t.Helper()
	if len(fragments) == 0 {
		return
	}

	var sqlText string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&sqlText); err != nil {
		t.Fatalf("read table sql for %s: %v", table, err)
	}
	normalized := normalizeSQL(sqlText)
	for _, fragment := range fragments {
		if !strings.Contains(normalized, normalizeSQL(fragment)) {
			t.Fatalf("%s SQL does not contain %q; SQL=%s", table, fragment, sqlText)
		}
	}
}

func assertForeignKeys(t *testing.T, db *sql.DB, table string, want []foreignKeyContract) {
	t.Helper()

	got := foreignKeys(t, db, table)
	for _, fk := range want {
		if got[fk.Column] != fk.Table {
			t.Fatalf("%s.%s foreign key = %q, want %q", table, fk.Column, got[fk.Column], fk.Table)
		}
	}
}

type columnInfo struct {
	notNull bool
}

func tableInfo(t *testing.T, db *sql.DB, table string) map[string]columnInfo {
	t.Helper()

	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		t.Fatalf("table_info %s: %v", table, err)
	}
	defer rows.Close()

	out := make(map[string]columnInfo)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info %s: %v", table, err)
		}
		out[name] = columnInfo{notNull: notNull == 1 || pk > 0}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info %s: %v", table, err)
	}
	return out
}

func foreignKeys(t *testing.T, db *sql.DB, table string) map[string]string {
	t.Helper()

	rows, err := db.Query(fmt.Sprintf("PRAGMA foreign_key_list(%s)", table))
	if err != nil {
		t.Fatalf("foreign_key_list %s: %v", table, err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var id, seq int
		var parent, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan foreign_key_list %s: %v", table, err)
		}
		out[from] = parent
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign_key_list %s: %v", table, err)
	}
	return out
}

func normalizeSQL(s string) string {
	fields := strings.Fields(strings.ToLower(s))
	return strings.Join(fields, " ")
}

func indexLooksUnique(name string) bool {
	return strings.HasPrefix(name, "uq_") || strings.HasPrefix(name, "uniq_")
}

func sortedKeys[K ~string, V any](m map[K]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	return keys
}
