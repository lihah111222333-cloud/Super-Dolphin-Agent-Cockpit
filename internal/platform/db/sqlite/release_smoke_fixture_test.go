package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sqliteSmokeSchemaFloor = 107

type sqliteFixtureConfig struct {
	Threads         int
	SystemLogs      int
	PromptTemplates int
	CronJobs        int
	DAGRuns         int
	Wakeups         int
	SessionInsights int
}

func sqliteMediumFixtureConfig() sqliteFixtureConfig {
	return sqliteFixtureConfig{
		Threads:         1000,
		SystemLogs:      10000,
		PromptTemplates: 1000,
		CronJobs:        500,
		DAGRuns:         500,
		Wakeups:         10000,
		SessionInsights: 1000,
	}
}

func TestSQLiteRuntimeStartupSmoke(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://must-not-be-used.invalid/super_dolphin")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://must-not-be-used.invalid/super_dolphin")

	db, dbPath := openMigratedSQLiteDB(t, "startup")
	defer db.Close()
	assertSQLiteSchemaFloor(t, db)
	assertSQLitePragma(t, db, "journal_mode", "wal")
	assertSQLitePragma(t, db, "foreign_keys", "1")
	insertSmokeThreadPromptCronAndDAG(t, db, "startup")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("SQLite database path was not created: %v", err)
	}
}

func TestSQLiteRuntimeIgnoresPostgresEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://127.0.0.1:1/should-not-connect")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://127.0.0.1:1/should-not-connect")

	db, _ := openMigratedSQLiteDB(t, "postgres-env-ignored")
	defer db.Close()
	insertSmokeThreadPromptCronAndDAG(t, db, "postgres-env-ignored")
}

func TestSQLiteMediumFixtureDistribution(t *testing.T) {
	db, _ := openMigratedSQLiteDB(t, "medium-fixture")
	defer db.Close()
	seedSQLiteReleaseFixture(t, db, sqliteMediumFixtureConfig())
	assertSQLiteMediumFixtureDistribution(t, db)
}

func openMigratedSQLiteDB(t *testing.T, name string) (*sql.DB, string) {
	t.Helper()
	dbPath := sqliteTestDBPath(t, name)
	db, err := OpenTest(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open SQLite test DB: %v", err)
	}
	if err := RunMigrations(context.Background(), db, sqliteMigrationsDir(t)); err != nil {
		_ = db.Close()
		t.Fatalf("run SQLite migrations: %v", err)
	}
	return db, dbPath
}

func sqliteTestDBPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name, name+".db")
}

func sqliteMigrationsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "internal", "platform", "db", "sqlite", "migrations")
}

func assertSQLiteSchemaFloor(t *testing.T, db *sql.DB) {
	t.Helper()
	var version int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version < sqliteSmokeSchemaFloor {
		t.Fatalf("schema version = %d, want >= %d", version, sqliteSmokeSchemaFloor)
	}
	tables := sqliteTables(t, db)
	for _, table := range requiredBaselineTablesFromModule(t) {
		if !tables[table] {
			t.Fatalf("schema floor table %q is missing", table)
		}
	}
}

func assertSQLitePragma(t *testing.T, db *sql.DB, name, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatalf("read PRAGMA %s: %v", name, err)
	}
	if strings.ToLower(got) != want {
		t.Fatalf("PRAGMA %s = %q, want %q", name, got, want)
	}
}

func insertSmokeThreadPromptCronAndDAG(t *testing.T, db *sql.DB, suffix string) {
	t.Helper()
	now := time.Now().UTC().UnixMilli()
	threadID := "thread-" + suffix
	agentID := "agent-" + suffix
	dagKey := "dag-" + suffix
	runKey := "run-" + suffix
	promptKey := "prompt-" + suffix
	jobID := "cron-" + suffix
	mustExecArgs(t, db, `
INSERT INTO agent_threads (thread_id, name, prompt, model, cwd, status, created_at, updated_at, config_override, prompt_snapshot, agent_key)
VALUES (?, ?, ?, 'gpt-5', ?, 'running', ?, ?, '{}', '{}', ?)`,
		threadID, "smoke thread", "hello", repoRoot(t), now, now, agentID)
	mustExecArgs(t, db, `
INSERT INTO prompt_templates (prompt_key, title, agent_key, tool_name, prompt_text, created_by, updated_by, created_at, updated_at)
VALUES (?, 'Smoke Prompt', ?, 'codex', ?, 'test', 'test', ?, ?)`,
		promptKey, agentID, sqliteJSONPayload(1024), now, now)
	mustExecArgs(t, db, `
INSERT INTO cron_jobs (id, name, prompt, schedule_expr, cwd, next_run_at, thread_id, agent_id, created_at, updated_at)
VALUES (?, 'Smoke Cron', 'run', '* * * * *', ?, ?, ?, ?, ?, ?)`,
		jobID, repoRoot(t), now, threadID, agentID, now, now)
	mustExecArgs(t, db, `
INSERT INTO task_dags (dag_key, title, status, created_by, metadata, started_at, created_at, updated_at, version)
VALUES (?, 'Smoke DAG', 'active', 'test', '{}', ?, ?, ?, 1)`,
		dagKey, now, now, now)
	mustExecArgs(t, db, `
INSERT INTO task_dag_runs (run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, events, metadata, created_at, updated_at)
VALUES (?, ?, 1, 'manual', 'running', ?, '[]', '{}', ?, ?)`,
		runKey, dagKey, now, now, now)
}

func seedSQLiteReleaseFixture(t *testing.T, db *sql.DB, cfg sqliteFixtureConfig) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin medium fixture tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	base := time.Now().UTC().Add(-30 * 24 * time.Hour)

	seedFixtureAgentMetadata(t, tx, base)
	seedFixtureThreads(t, tx, base, cfg.Threads)
	seedFixturePromptTemplates(t, tx, base, cfg.PromptTemplates)
	seedFixtureSystemLogs(t, tx, base, cfg.SystemLogs)
	seedFixtureSessionInsights(t, tx, base, cfg.SessionInsights)
	seedFixtureCronJobs(t, tx, base, cfg.CronJobs)
	runIDs := seedFixtureDAGRuns(t, tx, base, cfg.DAGRuns)
	seedFixtureWakeups(t, tx, base, cfg.Wakeups, runIDs)

	if err = tx.Commit(); err != nil {
		t.Fatalf("commit medium fixture tx: %v", err)
	}
}

func seedFixtureAgentMetadata(t *testing.T, tx *sql.Tx, base time.Time) {
	t.Helper()
	agentStatuses := []string{"running", "idle", "stuck", "error", "disconnected", "unknown"}
	for i := 0; i < 20; i++ {
		ts := base.Add(time.Duration(i) * time.Hour).UnixMilli()
		execTx(t, tx, `
INSERT INTO agent_status (agent_id, agent_name, session_id, status, output_tail, created_at, updated_at)
VALUES (?, ?, ?, ?, '[]', ?, ?)`,
			fixtureAgentID(i), fmt.Sprintf("Agent %02d", i), fmt.Sprintf("session-%02d", i), agentStatuses[i%len(agentStatuses)], ts, ts)
		execTx(t, tx, `
INSERT INTO agent_provider_binding (agent_id, provider, provider_thread_id, codex_thread_id, cwd, created_at, updated_at)
VALUES (?, 'codex', ?, ?, ?, ?, ?)`,
			fixtureAgentID(i), fmt.Sprintf("provider-thread-%02d", i), fmt.Sprintf("codex-thread-%02d", i), repoRoot(t), ts, ts)
	}
}

func seedFixtureThreads(t *testing.T, tx *sql.Tx, base time.Time, count int) {
	t.Helper()
	threadStatuses := []string{"running", "idle", "finished", "failed"}
	for i := 0; i < count; i++ {
		ts := fixtureTimestamp(base, i).UnixMilli()
		execTx(t, tx, `
INSERT INTO agent_threads (thread_id, name, prompt, model, cwd, status, pid, created_at, updated_at, config_override, prompt_snapshot, agent_key)
VALUES (?, ?, ?, 'gpt-5', ?, ?, ?, ?, ?, ?, '{}', ?)`,
			fixtureThreadID(i), fmt.Sprintf("Thread %04d", i), sqliteJSONPayload(payloadSizeForIndex(i)),
			repoRoot(t), threadStatuses[i%len(threadStatuses)], 1000+i, ts, ts,
			sqliteJSONPayload(payloadSizeForIndex(i+7)), fixtureAgentID(i))
	}
}

func seedFixturePromptTemplates(t *testing.T, tx *sql.Tx, base time.Time, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		ts := fixtureTimestamp(base, i).UnixMilli()
		execTx(t, tx, `
INSERT INTO prompt_templates (prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, created_by, updated_by, created_at, updated_at)
VALUES (?, ?, ?, 'codex', ?, '{}', '[]', 'fixture', 'fixture', ?, ?)`,
			fmt.Sprintf("fixture/prompt-%04d", i), fmt.Sprintf("Prompt %04d", i), fixtureAgentID(i),
			sqliteJSONPayload(payloadSizeForIndex(i+13)), ts, ts)
	}
}

func seedFixtureSystemLogs(t *testing.T, tx *sql.Tx, base time.Time, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		ts := fixtureTimestamp(base, i).UnixMilli()
		execTx(t, tx, `
INSERT INTO system_logs (ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, extra)
VALUES (?, ?, 'sqlite-smoke', ?, ?, 'task14', 'release-gate', ?, ?, ?, ?, 'sqlite', ?, ?)`,
			ts, []string{"INFO", "WARN", "ERROR"}[i%3], fmt.Sprintf("log %05d", i),
			sqliteJSONPayload(payloadSizeForIndex(i+29)), fixtureAgentID(i), fixtureThreadID(i),
			fmt.Sprintf("trace-%05d", i), fmt.Sprintf("event-%02d", i%7), i%250,
			sqliteJSONPayload(payloadSizeForIndex(i+31)))
	}
}

func seedFixtureSessionInsights(t *testing.T, tx *sql.Tx, base time.Time, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		ts := fixtureTimestamp(base, i).UnixMilli()
		execTx(t, tx, `
INSERT INTO session_insights (
    thread_id, agent_id, session_id, provider, local_turn_id, provider_turn_id,
    started_at, completed_at, duration_ms, success, status, approval_requests,
    approval_requests_observed, token_input, token_output, token_total,
    token_snapshot_observed, created_at, updated_at
) VALUES (?, ?, ?, 'codex', ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, 1, ?, ?)`,
			fixtureThreadID(i), fixtureAgentID(i), fmt.Sprintf("session-%04d", i),
			fmt.Sprintf("local-turn-%04d", i), fmt.Sprintf("provider-turn-%04d", i),
			ts, ts+1000, 1000+i, i%2, []string{"ok", "error", "unknown"}[i%3],
			1+i%4, 100+i, 50+i, 150+2*i, ts, ts)
	}
}

func seedFixtureCronJobs(t *testing.T, tx *sql.Tx, base time.Time, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		ts := fixtureTimestamp(base, i).UnixMilli()
		jobID := fmt.Sprintf("fixture-cron-%04d", i)
		execTx(t, tx, `
INSERT INTO cron_jobs (id, name, prompt, schedule_expr, cwd, next_run_at, thread_id, agent_id, created_at, updated_at)
VALUES (?, ?, ?, '* * * * *', ?, ?, ?, ?, ?, ?)`,
			jobID, fmt.Sprintf("Cron %04d", i), sqliteJSONPayload(1024), repoRoot(t), ts,
			fixtureThreadID(i), fixtureAgentID(i), ts, ts)
		execTx(t, tx, `
INSERT INTO cron_job_runs (id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id, agent_id, turn_id, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("fixture-cron-run-%04d", i), jobID, ts, fmt.Sprintf("idem-%04d", i),
			fmt.Sprintf("cron-dedupe-%04d", i), fixtureThreadID(i), fixtureAgentID(i),
			fmt.Sprintf("turn-%04d", i), []string{"pending", "submitting", "submitted", "running", "finished", "failed"}[i%6], ts, ts)
	}
}

func seedFixtureDAGRuns(t *testing.T, tx *sql.Tx, base time.Time, count int) []int64 {
	t.Helper()
	runStatuses := []string{"running", "succeeded", "failed", "cancelled"}
	runIDs := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		ts := fixtureTimestamp(base, i).UnixMilli()
		dagKey := fmt.Sprintf("fixture-dag-%04d", i)
		execTx(t, tx, `
INSERT INTO task_dags (dag_key, title, description, status, created_by, metadata, started_at, created_at, updated_at, version)
VALUES (?, ?, 'medium fixture dag', ?, 'fixture', ?, ?, ?, ?, 1)`,
			dagKey, fmt.Sprintf("DAG %04d", i), []string{"active", "draft", "paused"}[i%3],
			sqliteJSONPayload(payloadSizeForIndex(i+41)), ts, ts, ts)
		result := execTx(t, tx, `
INSERT INTO task_dag_runs (run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, events, budget_used, budget_limit, metadata, created_at, updated_at)
VALUES (?, ?, 1, 'manual', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("fixture-run-%04d", i), dagKey, runStatuses[i%len(runStatuses)], ts,
			nullableFinishedAt(ts, runStatuses[i%len(runStatuses)]), sqliteEventsJSON(20),
			i*10, 100000, sqliteJSONPayload(payloadSizeForIndex(i+43)), ts, ts)
		runID, scanErr := result.LastInsertId()
		if scanErr != nil {
			t.Fatalf("read inserted DAG run id: %v", scanErr)
		}
		runIDs = append(runIDs, runID)
		execTx(t, tx, `
INSERT INTO task_dag_nodes (dag_key, node_key, title, node_type, assigned_to, status, command_ref, config, result, started_at, created_at, updated_at, run_id)
VALUES (?, 'root', ?, 'task', ?, ?, 'fixture', ?, '{}', ?, ?, ?, ?)`,
			dagKey, fmt.Sprintf("Node %04d", i), fixtureAgentID(i), []string{"pending", "running", "succeeded", "failed"}[i%4],
			sqliteJSONPayload(payloadSizeForIndex(i+47)), ts, ts, ts, runID)
	}
	return runIDs
}

func seedFixtureWakeups(t *testing.T, tx *sql.Tx, base time.Time, count int, runIDs []int64) {
	t.Helper()
	if count > 0 && len(runIDs) == 0 {
		t.Fatal("fixture wakeups require DAG runs")
	}
	wakeupStatuses := []string{"pending", "dispatching", "sent", "failed"}
	for i := 0; i < count; i++ {
		runIndex := i % len(runIDs)
		ts := fixtureTimestamp(base, i).UnixMilli()
		status := wakeupStatuses[i%len(wakeupStatuses)]
		execTx(t, tx, `
INSERT INTO task_dag_wakeups (
    dag_key, node_key, run_id, wakeup_kind, target_agent_id, prompt_payload,
    idempotency_key, status, next_retry_at, claimed_at, claimed_by,
    lease_expires_at, sent_at, last_error, created_at, updated_at
) VALUES (?, 'root', ?, 'node_start', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("fixture-dag-%04d", runIndex), runIDs[runIndex], fixtureAgentID(i),
			sqliteJSONPayload(payloadSizeForIndex(i+53)), fmt.Sprintf("fixture-wakeup-%05d", i),
			status, ts, nullableClaimedAt(ts, status), nullableClaimedBy(i, status),
			nullableLeaseExpiresAt(ts, status), nullableSentAt(ts, status), nullableLastError(status), ts, ts)
	}
}

func assertSQLiteMediumFixtureDistribution(t *testing.T, db *sql.DB) {
	t.Helper()
	assertCountAtLeast(t, db, "agent_threads", 1000)
	assertCountAtLeast(t, db, "system_logs", 10000)
	assertCountAtLeast(t, db, "prompt_templates", 1000)
	assertCountAtLeast(t, db, "cron_jobs", 500)
	assertCountAtLeast(t, db, "cron_job_runs", 500)
	assertCountAtLeast(t, db, "task_dag_runs", 500)
	assertCountAtLeast(t, db, "task_dag_wakeups", 10000)
	assertDistinctAtLeast(t, db, "system_logs", "thread_id", 100)
	assertDistinctAtLeast(t, db, "system_logs", "agent_id", 20)
	for _, status := range []string{"pending", "dispatching", "sent", "failed"} {
		assertWhereCountAtLeast(t, db, "task_dag_wakeups", "status = ?", status, 1)
	}
	for _, status := range []string{"running", "succeeded", "failed", "cancelled"} {
		assertWhereCountAtLeast(t, db, "task_dag_runs", "status = ?", status, 1)
	}
	assertWhereCountAtLeast(t, db, "session_insights", "approval_requests_observed = 1 AND approval_requests > 0", nil, 1)
	assertWhereCountAtLeast(t, db, "session_insights", "token_snapshot_observed = 1 AND token_total > 0", nil, 1)
	assertWhereCountAtLeast(t, db, "system_logs", "length(extra) >= 1024", nil, 1)
	assertWhereCountAtLeast(t, db, "system_logs", "length(extra) >= 16384", nil, 1)
	assertWhereCountAtLeast(t, db, "system_logs", "length(extra) >= 65536", nil, 1)
	assertFixtureSpansThirtyDays(t, db)
}

func assertCountAtLeast(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got < want {
		t.Fatalf("%s count = %d, want >= %d", table, got, want)
	}
}

func assertDistinctAtLeast(t *testing.T, db *sql.DB, table, column string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(DISTINCT " + column + ") FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count distinct %s.%s: %v", table, column, err)
	}
	if got < want {
		t.Fatalf("%s.%s distinct count = %d, want >= %d", table, column, got, want)
	}
}

func assertWhereCountAtLeast(t *testing.T, db *sql.DB, table, where string, arg any, want int) {
	t.Helper()
	var row *sql.Row
	query := "SELECT COUNT(*) FROM " + table + " WHERE " + where
	if arg == nil {
		row = db.QueryRow(query)
	} else {
		row = db.QueryRow(query, arg)
	}
	var got int
	if err := row.Scan(&got); err != nil {
		t.Fatalf("count %s where %s: %v", table, where, err)
	}
	if got < want {
		t.Fatalf("%s where %s count = %d, want >= %d", table, where, got, want)
	}
}

func assertFixtureSpansThirtyDays(t *testing.T, db *sql.DB) {
	t.Helper()
	var minTS, maxTS int64
	if err := db.QueryRow("SELECT MIN(ts), MAX(ts) FROM system_logs").Scan(&minTS, &maxTS); err != nil {
		t.Fatalf("system_logs timestamp span: %v", err)
	}
	if maxTS-minTS < int64((29 * 24 * time.Hour).Milliseconds()) {
		t.Fatalf("system_logs timestamp span = %s, want at least 29 days", time.Duration(maxTS-minTS)*time.Millisecond)
	}
}

func execTx(t *testing.T, tx *sql.Tx, query string, args ...any) sql.Result {
	t.Helper()
	result, err := tx.Exec(query, args...)
	if err != nil {
		t.Fatalf("execute fixture SQL: %v\n%s", err, query)
	}
	return result
}

func mustExecArgs(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("execute SQL: %v\n%s", err, query)
	}
}

func fixtureAgentID(index int) string {
	return fmt.Sprintf("agent-%02d", index%20)
}

func fixtureThreadID(index int) string {
	return fmt.Sprintf("thread-%04d", index)
}

func fixtureTimestamp(base time.Time, index int) time.Time {
	return base.Add(time.Duration(index%720) * time.Hour)
}

func payloadSizeForIndex(index int) int {
	switch {
	case index%9973 == 1:
		return 64 * 1024
	case index%4099 == 2:
		return 16 * 1024
	default:
		return 1024
	}
}

func sqliteJSONPayload(size int) string {
	prefix := `{"payload":"`
	suffix := `"}`
	fill := size - len(prefix) - len(suffix)
	if fill < 1 {
		fill = 1
	}
	return prefix + strings.Repeat("x", fill) + suffix
}

func sqliteEventsJSON(count int) string {
	events := make([]string, 0, count)
	for i := 0; i < count; i++ {
		events = append(events, fmt.Sprintf(`{"seq":%d,"kind":"node_transition"}`, i))
	}
	return "[" + strings.Join(events, ",") + "]"
}

func nullableFinishedAt(ts int64, status string) any {
	if status == "running" {
		return nil
	}
	return ts + 1000
}

func nullableClaimedAt(ts int64, status string) any {
	if status == "dispatching" {
		return ts
	}
	return nil
}

func nullableClaimedBy(index int, status string) string {
	if status == "dispatching" {
		return fmt.Sprintf("worker-%02d", index%7)
	}
	return ""
}

func nullableLeaseExpiresAt(ts int64, status string) any {
	if status == "dispatching" {
		return ts + int64(time.Minute.Milliseconds())
	}
	return nil
}

func nullableSentAt(ts int64, status string) any {
	if status == "sent" {
		return ts
	}
	return nil
}

func nullableLastError(status string) string {
	if status == "failed" {
		return "fixture failure"
	}
	return ""
}
