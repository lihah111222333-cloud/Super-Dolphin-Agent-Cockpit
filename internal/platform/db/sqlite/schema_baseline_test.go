package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

const baselineMigration = "internal/platform/db/sqlite/migrations/001_baseline.sql"

var allPersistentTables = []string{
	"agent_codex_binding",
	"agent_feedback_events",
	"agent_interactions",
	"agent_provider_binding",
	"agent_status",
	"agent_threads",
	"audit_events",
	"bus_exception_logs",
	"command_card_runs",
	"command_card_versions",
	"command_cards",
	"cron_job_runs",
	"cron_jobs",
	"cwd_instance_locks",
	"datasource_v2_documents",
	"datasource_v2_text_chunks",
	"hook_pending_reviews",
	"prompt_intent_drafts",
	"prompt_recall_topics",
	"prompt_routing_tests",
	"prompt_template_sections",
	"prompt_template_versions",
	"prompt_templates",
	"prompt_versions",
	"prompts",
	"runtime_locks",
	"schema_migrations",
	"session_insights",
	"shared_files",
	"system_logs",
	"task_acks",
	"task_dag_nodes",
	"task_dag_runs",
	"task_dag_wakeups",
	"task_dag_worker_leases",
	"task_dags",
	"task_traces",
	"topology_approval_archives",
	"topology_approvals",
	"turn_dedupe_registry",
	"ui_preferences",
	"workspace_run_files",
	"workspace_runs",
}

func TestSQLiteBaselineCreatesRuntimeTables(t *testing.T) {
	db := openBaselineDB(t)

	got := sqliteTables(t, db)
	for _, table := range allPersistentTables {
		if !got[table] {
			t.Fatalf("baseline missing table %q", table)
		}
	}

	for _, table := range requiredBaselineTablesFromModule(t) {
		if !got[table] {
			t.Fatalf("baseline missing requiredBaselineTables floor table %q", table)
		}
	}

	var maxVersion int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&maxVersion); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if maxVersion != 103 {
		t.Fatalf("schema_migrations max version = %d, want 103", maxVersion)
	}
}

func TestSQLiteBaselineCoversSQLCQueryTables(t *testing.T) {
	db := openBaselineDB(t)
	baselineTables := sqliteTables(t, db)

	queryTables := referencedSQLCTables(t)
	for table := range queryTables {
		if !baselineTables[table] {
			t.Fatalf("query references table %q but SQLite baseline does not create it", table)
		}
	}
}

func TestSQLiteBaselineCriticalIndexesAndConstraints(t *testing.T) {
	db := openBaselineDB(t)

	assertPrimaryKey(t, db, "agent_provider_binding", []string{"agent_id"})
	assertIndex(t, db, "agent_provider_binding", "uq_agent_provider_binding_provider_thread", true, "provider_thread_id <> ''")
	assertIndex(t, db, "turn_dedupe_registry", "idx_turn_dedupe_registry_live", false, "terminal_at IS NULL")
	assertIndex(t, db, "cron_jobs", "idx_cron_jobs_due", false, "enabled = 1")
	assertIndex(t, db, "cron_jobs", "idx_cron_jobs_claim", false, "claim_token <> ''")
	assertIndex(t, db, "task_dag_runs", "idx_task_dag_runs_running", false, "status = 'running'")
	assertIndexMissing(t, db, "task_dag_runs", "uniq_task_dag_runs_one_running_per_dag")
	assertIndex(t, db, "prompt_template_sections", "idx_prompt_sections_recall_topic_lookup", false, "trigger_type = 'recall' AND recall_topic <> ''")
	assertIndexMissing(t, db, "prompt_template_sections", "uq_prompt_template_sections_recall_topic")

	assertIndex(t, db, "system_logs", "idx_system_logs_level_ts_id", false, "")
	assertIndex(t, db, "system_logs", "idx_system_logs_source_ts_id", false, "source <> ''")
	assertIndex(t, db, "system_logs", "idx_system_logs_agent_ts_id", false, "agent_id <> ''")
	assertIndex(t, db, "system_logs", "idx_system_logs_thread_ts_id", false, "thread_id <> ''")
	assertIndex(t, db, "system_logs", "idx_system_logs_ts_id", false, "")

	assertIndex(t, db, "session_insights", "idx_session_insights_thread_created", false, "")
	assertIndex(t, db, "session_insights", "idx_session_insights_created", false, "")
	assertIndex(t, db, "session_insights", "idx_session_insights_approval_observed", false, "approval_requests_observed = 1")
	assertIndex(t, db, "session_insights", "idx_session_insights_token_observed", false, "token_snapshot_observed = 1")

	assertIndex(t, db, "cron_job_runs", "idx_cron_job_runs_job_created", false, "")
	assertIndex(t, db, "cron_job_runs", "uq_cron_job_runs_dedupe_key", true, "dedupe_key <> ''")
	assertIndex(t, db, "cron_job_runs", "idx_cron_job_runs_status_active", false, "status IN ('pending', 'submitting', 'submitted', 'running')")
	assertIndex(t, db, "cron_job_runs", "idx_cron_job_runs_turn_running", false, "status = 'running'")

	assertIndex(t, db, "task_dag_wakeups", "idx_task_dag_wakeups_poll", false, "status = 'pending'")
	assertIndex(t, db, "task_dag_wakeups", "idx_task_dag_wakeups_sent_target", false, "status = 'sent'")
	assertIndex(t, db, "task_dag_wakeups", "idx_task_dag_wakeups_run_node", false, "")
	assertIndex(t, db, "task_dag_runs", "idx_task_dag_runs_run_key", false, "")
	assertIndex(t, db, "task_dag_runs", "idx_task_dag_runs_dag_status_started", false, "")
}

func TestSQLiteBaselineAllowsMultipleRunningRunsPerDAG(t *testing.T) {
	db := openBaselineDB(t)

	mustExec(t, db, `
		INSERT INTO task_dag_runs (
			run_key, dag_key, dag_version_snapshot, trigger_source, status,
			started_at, events, budget_used, metadata, created_at, updated_at
		)
		VALUES
			('dag-a/run-1', 'dag-a', 1, 'manual', 'running', 1710000000000, '[]', 0, '{}', 1710000000000, 1710000000000),
			('dag-a/run-2', 'dag-a', 1, 'manual', 'running', 1710000000001, '[]', 0, '{}', 1710000000001, 1710000000001)
	`)
}

func TestSQLiteBaselineAllowsDuplicateRecallTopicAcrossTemplates(t *testing.T) {
	db := openBaselineDB(t)

	mustExec(t, db, `
		INSERT INTO prompt_templates (
			id, prompt_key, title, agent_key, tool_name, prompt_text,
			variables, tags, description, when_to_use, enabled,
			manually_edited, match_when, priority, created_by, updated_by,
			created_at, updated_at
		)
		VALUES
			(101, 'project-a/recall', 'Project A Recall', 'main', 'codex', 'body', '{}', '["scope.cwd:/project-a"]', '', '', 1, 0, '{}', 0, 'test', 'test', 1710000000000, 1710000000000),
			(102, 'project-b/recall', 'Project B Recall', 'main', 'codex', 'body', '{}', '["scope.cwd:/project-b"]', '', '', 1, 0, '{}', 0, 'test', 'test', 1710000000000, 1710000000000)
	`)
	mustExec(t, db, `
		INSERT INTO prompt_template_sections (
			template_id, section_key, region, ordinal, body, enable_when,
			enabled, created_at, updated_at, trigger_type, recall_topic
		)
		VALUES
			(101, 'recall_topic', 'dynamic', 10, 'project a body', '{}', 1, 1710000000000, 1710000000000, 'recall', 'shared-topic'),
			(102, 'recall_topic', 'dynamic', 10, 'project b body', '{}', 1, 1710000000000, 1710000000000, 'recall', 'shared-topic')
	`)
}

func TestSQLiteBaselineExcludesPostgresOnlyAuxiliaryTables(t *testing.T) {
	db := openBaselineDB(t)
	baselineTables := sqliteTables(t, db)
	queryTables := referencedSQLCTables(t)

	excluded := map[string]string{
		"skill_candidates": "0064/0066 candidate staging schema has no sql/queries or cmd/mcp-orch/sql/queries runtime reference",
		"prompt_template_expert_consolidation_0107_restore": "0107 restore table is rollback scaffolding, not runtime storage",
	}
	for table, reason := range excluded {
		if baselineTables[table] {
			t.Fatalf("excluded table %q exists in SQLite baseline; exclusion reason: %s", table, reason)
		}
		if queryTables[table] {
			t.Fatalf("excluded table %q is referenced by runtime query; exclusion reason is stale: %s", table, reason)
		}
	}
}

func openBaselineDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	execFile(t, db, baselineMigration)
	return db
}

func execFile(t *testing.T, db *sql.DB, repoRelative string) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(repoRelative)))
	if err != nil {
		t.Fatalf("read %s: %v", repoRelative, err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("execute %s: %v", repoRelative, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}

func sqliteTables(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("list sqlite tables: %v", err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table names: %v", err)
	}
	return out
}

func requiredBaselineTablesFromModule(t *testing.T) []string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "platform", "db", "module.go"))
	if err != nil {
		t.Fatalf("read db module: %v", err)
	}
	re := regexp.MustCompile(`(?s)var requiredBaselineTables = \[]string\{(.*?)\}`)
	match := re.FindStringSubmatch(string(body))
	if len(match) != 2 {
		t.Fatal("requiredBaselineTables declaration not found")
	}
	itemRE := regexp.MustCompile(`"([^"]+)"`)
	var tables []string
	for _, item := range itemRE.FindAllStringSubmatch(match[1], -1) {
		tables = append(tables, item[1])
	}
	if len(tables) == 0 {
		t.Fatal("requiredBaselineTables parsed as empty")
	}
	return tables
}

func referencedSQLCTables(t *testing.T) map[string]bool {
	t.Helper()

	roots := []string{
		filepath.Join(repoRoot(t), "sql", "queries"),
		filepath.Join(repoRoot(t), "cmd", "mcp-orch", "sql", "queries"),
	}
	out := make(map[string]bool)
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatalf("read query dir %s: %v", root, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			path := filepath.Join(root, entry.Name())
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read query file %s: %v", path, err)
			}
			for table := range extractTableReferences(string(body)) {
				out[table] = true
			}
		}
	}
	return out
}

func extractTableReferences(sqlText string) map[string]bool {
	cleaned := stripSQLComments(sqlText)
	re := regexp.MustCompile(`(?i)\b(?:FROM|JOIN|INTO|UPDATE)\s+([a-z_][a-z0-9_]*(?:\.[a-z_][a-z0-9_]*)?)|\bDELETE\s+FROM\s+([a-z_][a-z0-9_]*(?:\.[a-z_][a-z0-9_]*)?)`)
	ignored := map[string]bool{
		"dag_meta": true, "deleted": true, "derived_logs": true, "derived_statuses": true,
		"due": true, "final": true, "final_node": true, "final_output": true,
		"node_counts": true, "old": true, "picked": true, "scoped": true, "updated": true,
		"set": true, "skip": true,
	}
	out := make(map[string]bool)
	for _, match := range re.FindAllStringSubmatchIndex(cleaned, -1) {
		start, end := match[2], match[3]
		if start < 0 {
			start, end = match[4], match[5]
		}
		name := strings.ToLower(cleaned[start:end])
		if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
			name = name[dot+1:]
		}
		after := strings.TrimLeft(cleaned[end:], " \t\r\n")
		if strings.HasPrefix(after, "(") || ignored[name] {
			continue
		}
		out[name] = true
	}
	return out
}

func stripSQLComments(sqlText string) string {
	block := regexp.MustCompile(`(?s)/\*.*?\*/`)
	line := regexp.MustCompile(`(?m)--.*$`)
	return line.ReplaceAllString(block.ReplaceAllString(sqlText, ""), "")
}

func assertPrimaryKey(t *testing.T, db *sql.DB, table string, want []string) {
	t.Helper()

	got := primaryKeyColumns(t, db, table)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s primary key = %v, want %v", table, got, want)
	}
}

func primaryKeyColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()

	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		t.Fatalf("table_info %s: %v", table, err)
	}
	defer rows.Close()

	type pkCol struct {
		name string
		pos  int
	}
	var cols []pkCol
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info %s: %v", table, err)
		}
		if pk > 0 {
			cols = append(cols, pkCol{name: name, pos: pk})
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info %s: %v", table, err)
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].pos < cols[j].pos })
	out := make([]string, 0, len(cols))
	for _, col := range cols {
		out = append(out, col.name)
	}
	return out
}

func assertIndex(t *testing.T, db *sql.DB, table, index string, unique bool, whereContains string) {
	t.Helper()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name = ?`, table, index).Scan(&count); err != nil {
		t.Fatalf("lookup index %s.%s: %v", table, index, err)
	}
	if count != 1 {
		t.Fatalf("missing index %s.%s", table, index)
	}

	indexes := indexList(t, db, table)
	if indexes[index] != unique {
		t.Fatalf("index %s.%s unique=%v, want %v", table, index, indexes[index], unique)
	}
	if whereContains == "" {
		return
	}
	var sqlText string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&sqlText); err != nil {
		t.Fatalf("read index sql %s: %v", index, err)
	}
	if !strings.Contains(strings.ToLower(sqlText), strings.ToLower(whereContains)) {
		t.Fatalf("index %s SQL %q does not contain predicate %q", index, sqlText, whereContains)
	}
}

func assertIndexMissing(t *testing.T, db *sql.DB, table, index string) {
	t.Helper()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name = ?`, table, index).Scan(&count); err != nil {
		t.Fatalf("lookup index %s.%s: %v", table, index, err)
	}
	if count != 0 {
		t.Fatalf("unexpected index %s.%s exists", table, index)
	}
}

func mustExec(t *testing.T, db *sql.DB, stmt string) {
	t.Helper()

	if _, err := db.Exec(stmt); err != nil {
		t.Fatalf("execute SQL: %v\n%s", err, stmt)
	}
}

func indexList(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()

	rows, err := db.Query(fmt.Sprintf("PRAGMA index_list(%s)", table))
	if err != nil {
		t.Fatalf("index_list %s: %v", table, err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var seq int
		var name, origin string
		var unique, partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index_list %s: %v", table, err)
		}
		out[name] = unique == 1
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index_list %s: %v", table, err)
	}
	return out
}
