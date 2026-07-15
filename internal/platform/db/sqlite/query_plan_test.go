package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSQLiteQueryPlanSmoke(t *testing.T) {
	db, _ := openMigratedSQLiteDB(t, "query-plan")
	defer db.Close()
	seedSQLiteReleaseFixture(t, db, sqliteMediumFixtureConfig())
	dashboardDAGQueries := readDashboardDAGSnapshotListQueries(t)

	assertQueryPlanUsesIndex(t, db, "idx_agent_threads_agent_key", `
SELECT thread_id, name, status, created_at
FROM agent_threads
WHERE agent_key <> '' AND agent_key = ?
ORDER BY thread_id ASC
LIMIT 25`, fixtureAgentID(1))
	assertQueryPlanUsesIndex(t, db, "idx_system_logs_ts_id", `
SELECT id, ts, level, message
FROM system_logs
ORDER BY ts DESC, id DESC
LIMIT 25`)
	assertQueryPlanUsesIndex(t, db, "idx_system_logs_thread_ts_id", `
SELECT id, ts, level, message
FROM system_logs
WHERE thread_id <> '' AND thread_id = ?
ORDER BY ts DESC, id DESC
LIMIT 25`, fixtureThreadID(1))
	assertQueryPlanUsesIndex(t, db, "idx_prompt_templates_enabled", `
SELECT id, prompt_key, title, agent_key, tool_name
FROM prompt_templates
WHERE enabled = 1
ORDER BY updated_at DESC
LIMIT 25`)
	assertQueryPlanUsesIndex(t, db, "idx_session_insights_approval_observed", `
SELECT id, thread_id, created_at
FROM session_insights
WHERE approval_requests_observed = 1 AND thread_id = ?
ORDER BY created_at DESC, id DESC
LIMIT 25`, fixtureThreadID(1))
	assertQueryPlanUsesIndex(t, db, "idx_cron_jobs_due", `
SELECT id, next_run_at
FROM cron_jobs
WHERE enabled = 1
ORDER BY COALESCE(next_retry_at, next_run_at)
LIMIT 25`)
	assertQueryPlanUsesIndex(t, db, "idx_cron_jobs_created_id", `
SELECT id, created_at
FROM cron_jobs
WHERE (created_at, id) < (CAST(? AS INTEGER), CAST(? AS TEXT))
ORDER BY created_at DESC, id DESC
LIMIT ?`, int64(1<<62), "\U0010FFFF", 26)
	assertQueryPlanUsesIndex(t, db, "idx_cron_job_runs_turn_status", `
SELECT id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
       agent_id, turn_id, submitted_at, status, error, created_at,
       updated_at
FROM cron_job_runs
WHERE turn_id = ?
  AND turn_id <> ''
  AND status IN ('submitted', 'running')
LIMIT 1`, "turn-fixture-0001")
	assertQueryPlanUsesIndex(t, db, "idx_task_dags_updated_id", dashboardDAGQueries.all, 25)
	assertQueryPlanUsesIndex(t, db, "idx_task_dags_status", dashboardDAGQueries.status, "active", 25)
	assertQueryPlanUsesIndex(t, db, "idx_task_dags_status", dashboardDAGQueries.statusKeyword, "active", "fixture", "fixture", "fixture", 25)
	assertQueryPlanUsesIndex(t, db, "idx_task_dag_wakeups_poll", `
SELECT id, dag_key, run_id
FROM task_dag_wakeups
WHERE status = 'pending' AND next_retry_at <= ?
ORDER BY next_retry_at ASC, id ASC
LIMIT 25`, int64(1<<62))
	assertQueryPlanUsesIndex(t, db, "idx_task_dag_wakeups_dispatching_lease", `
SELECT id
FROM task_dag_wakeups
WHERE status = 'dispatching' AND lease_expires_at < ?
ORDER BY lease_expires_at ASC, id ASC
LIMIT 25`, int64(1<<62))
	assertQueryPlanUsesIndex(t, db, "idx_task_dag_runs_dag_status_started", `
SELECT id, run_key, dag_key, status, started_at
FROM task_dag_runs
WHERE dag_key = ? AND status = ?
ORDER BY started_at DESC, id DESC
LIMIT 25`, "fixture-dag-0001", "succeeded")
}

type dashboardDAGSnapshotListQueries struct {
	all           string
	status        string
	statusKeyword string
}

func readDashboardDAGSnapshotListQueries(t *testing.T) dashboardDAGSnapshotListQueries {
	t.Helper()

	body := readDashboardDAGSnapshotSource(t)
	return dashboardDAGSnapshotListQueries{
		all:           extractRawStringConst(t, body, "dashboardListDAGsSnapshotAllQuery"),
		status:        extractRawStringConst(t, body, "dashboardListDAGsSnapshotStatusQuery"),
		statusKeyword: extractRawStringConst(t, body, "dashboardListDAGsSnapshotStatusKeywordQuery"),
	}
}

func TestSQLiteDashboardSnapshotListsOmitLargeRunEventColumns(t *testing.T) {
	body := readDashboardDAGSnapshotSource(t)
	for _, name := range []string{
		"dashboardListRunsSnapshotAllQuery",
		"dashboardListRunsSnapshotStatusQuery",
		"dashboardListLatestRunsByDAGSnapshotQueryTemplate",
	} {
		query := extractRawStringConst(t, string(body), name)
		if regexp.MustCompile(`(?i)\bevents\b`).FindString(query) != "" {
			t.Fatalf("%s selects task_dag_runs.events in a list projection:\n%s", name, query)
		}
	}
	getRunQuery := extractRawStringConst(t, string(body), "dashboardGetRunSnapshotQuery")
	if regexp.MustCompile(`(?i)\bevents\b`).FindString(getRunQuery) == "" {
		t.Fatalf("dashboardGetRunSnapshotQuery must keep events for detail reads:\n%s", getRunQuery)
	}
}

func readDashboardDAGSnapshotSource(t *testing.T) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "module", "dashboard", "dag_snapshot.go"))
	if err != nil {
		t.Fatalf("read dashboard DAG snapshot source: %v", err)
	}
	return string(body)
}

func assertQueryPlanUsesIndex(t *testing.T, db *sql.DB, indexName, query string, args ...any) {
	t.Helper()
	plan := explainSQLiteQueryPlan(t, db, query, args...)
	if !strings.Contains(plan, indexName) {
		t.Fatalf("query plan does not use %s:\n%s\nquery:\n%s", indexName, plan, query)
	}
}

func explainSQLiteQueryPlan(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain query plan: %v\n%s", err, query)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, fmt.Sprintf("%d/%d/%d %s", id, parent, notUsed, detail))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query plan: %v", err)
	}
	return strings.Join(details, "\n")
}

func extractRawStringConst(t *testing.T, body, name string) string {
	t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta(name) + `\s*=\s*` + "`" + `([^` + "`" + `]+)` + "`")
	match := re.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("raw string const %s not found", name)
	}
	return match[1]
}
