//go:build sqlite_stress

package sqlite

import (
	"database/sql"
	"testing"
	"time"
)

func sqliteLargeFixtureConfig() sqliteFixtureConfig {
	return sqliteFixtureConfig{
		Threads:         1000,
		SystemLogs:      100000,
		PromptTemplates: 1000,
		CronJobs:        20000,
		DAGRuns:         20000,
		Wakeups:         20000,
		SessionInsights: 50000,
	}
}

func TestSQLiteLargeFixtureStressExplicitRun(t *testing.T) {
	start := time.Now()
	db, _ := openMigratedSQLiteDB(t, "large-fixture-stress")
	defer db.Close()

	seedSQLiteReleaseFixture(t, db, sqliteLargeFixtureConfig())
	assertSQLiteLargeFixtureDistribution(t, db)
	assertLargeFixtureLimitedListPlans(t, db)

	t.Logf(
		"sqlite large fixture stress counts: system_logs=%d session_insights=%d cron_job_runs=%d task_dag_runs=%d task_dag_wakeups=%d elapsed=%s",
		countRows(t, db, "system_logs"),
		countRows(t, db, "session_insights"),
		countRows(t, db, "cron_job_runs"),
		countRows(t, db, "task_dag_runs"),
		countRows(t, db, "task_dag_wakeups"),
		time.Since(start).Round(time.Millisecond),
	)
}

func assertSQLiteLargeFixtureDistribution(t *testing.T, db *sql.DB) {
	t.Helper()
	assertCountAtLeast(t, db, "system_logs", 100000)
	assertCountAtLeast(t, db, "session_insights", 50000)
	assertCountAtLeast(t, db, "cron_job_runs", 20000)
	assertCountAtLeast(t, db, "task_dag_runs", 20000)
	assertCountAtLeast(t, db, "task_dag_wakeups", 20000)
	assertWhereCountAtLeast(t, db, "task_dag_runs", "length(events) >= 100", nil, 20000)
	assertDistinctAtLeast(t, db, "system_logs", "thread_id", 100)
	assertDistinctAtLeast(t, db, "system_logs", "agent_id", 20)
	for _, status := range []string{"pending", "submitting", "submitted", "running", "finished", "failed"} {
		assertWhereCountAtLeast(t, db, "cron_job_runs", "status = ?", status, 1)
	}
	assertSQLiteMediumFixtureDistribution(t, db)
}

func assertLargeFixtureLimitedListPlans(t *testing.T, db *sql.DB) {
	t.Helper()
	assertQueryPlanUsesIndex(t, db, "idx_system_logs_ts_id", `
SELECT id, ts, level, message
FROM system_logs
ORDER BY ts DESC, id DESC
LIMIT 100`)
	assertQueryPlanUsesIndex(t, db, "idx_session_insights_approval_observed", `
SELECT id, thread_id, created_at
FROM session_insights
WHERE approval_requests_observed = 1 AND thread_id = ?
ORDER BY created_at DESC, id DESC
LIMIT 100`, fixtureThreadID(1))
	assertQueryPlanUsesIndex(t, db, "idx_task_dag_wakeups_poll", `
SELECT id, dag_key, run_id
FROM task_dag_wakeups
WHERE status = 'pending' AND next_retry_at <= ?
ORDER BY next_retry_at ASC, id ASC
LIMIT 100`, int64(1<<62))
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return got
}
