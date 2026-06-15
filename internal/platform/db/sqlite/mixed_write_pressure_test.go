package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	sqliteMixedWriteChildEnv               = "SQLITE_MIXED_WRITE_CHILD"
	sqliteMixedWriteLocalDefaultDuration   = 1200 * time.Millisecond
	sqliteMixedWriteReleaseDefaultDuration = 5 * time.Minute
)

type sqliteMixedWriteChildConfig struct {
	role           string
	dbPath         string
	metricsPath    string
	durationMillis int
}

type sqliteMixedWriteMetrics struct {
	Role          string   `json:"role"`
	Operations    int      `json:"operations"`
	RetryCount    int      `json:"retry_count"`
	MaxWaitMillis int64    `json:"max_wait_millis"`
	FailureCount  int      `json:"failure_count"`
	Failures      []string `json:"failures"`
}

func TestSQLiteMixedWritePressureChild(t *testing.T) {
	role := os.Getenv(sqliteMixedWriteChildEnv)
	if role == "" {
		return
	}
	config := readSQLiteMixedWriteChildConfig(t, role)
	db, err := OpenTest(context.Background(), config.dbPath)
	if err != nil {
		t.Fatalf("mixed write child open DB: %v", err)
	}
	defer db.Close()
	metrics := runSQLiteMixedWriteChild(t, db, config)
	writeSQLiteMixedMetrics(t, config.metricsPath, metrics)
	if metrics.FailureCount > 0 {
		t.Fatalf("mixed write child failures: %v", metrics.Failures)
	}
}

func readSQLiteMixedWriteChildConfig(t *testing.T, role string) sqliteMixedWriteChildConfig {
	t.Helper()
	dbPath := os.Getenv("SQLITE_MIXED_WRITE_DB")
	metricsPath := os.Getenv("SQLITE_MIXED_WRITE_METRICS")
	durationMillis, err := strconv.Atoi(os.Getenv("SQLITE_MIXED_WRITE_DURATION_MS"))
	if dbPath == "" || metricsPath == "" || durationMillis <= 0 || err != nil {
		t.Fatalf("mixed write child missing db/metrics/duration env")
	}
	return sqliteMixedWriteChildConfig{
		role:           role,
		dbPath:         dbPath,
		metricsPath:    metricsPath,
		durationMillis: durationMillis,
	}
}

func runSQLiteMixedWriteChild(t *testing.T, db *sql.DB, config sqliteMixedWriteChildConfig) sqliteMixedWriteMetrics {
	t.Helper()
	metrics := sqliteMixedWriteMetrics{Role: config.role}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.durationMillis+5000)*time.Millisecond)
	defer cancel()
	write, err := sqliteMixedWriteOperation(ctx, db, config.role)
	if err != nil {
		recordSQLiteMixedWriteFailure(&metrics, err)
		return metrics
	}
	deadline := time.Now().Add(time.Duration(config.durationMillis) * time.Millisecond)
	for index := 0; time.Now().Before(deadline); index++ {
		if err := retrySQLitePressureWrite(ctx, &metrics, fmt.Sprintf("%s write %d", config.role, index), func() error {
			return write(index)
		}); err != nil {
			recordSQLiteMixedWriteFailure(&metrics, err)
			break
		}
		metrics.Operations++
	}
	return metrics
}

func sqliteMixedWriteOperation(ctx context.Context, db *sql.DB, role string) (func(index int) error, error) {
	switch role {
	case "main":
		return func(index int) error {
			return runSQLiteMainAppPressureWrite(ctx, db, index)
		}, nil
	case "orch":
		return func(index int) error {
			return runSQLiteOrchPressureWrite(ctx, db, index)
		}, nil
	default:
		return nil, fmt.Errorf("unknown mixed write child role %q", role)
	}
}

func recordSQLiteMixedWriteFailure(metrics *sqliteMixedWriteMetrics, err error) {
	metrics.FailureCount++
	metrics.Failures = append(metrics.Failures, err.Error())
}

func TestSQLiteMixedWritePressure(t *testing.T) {
	duration, err := sqliteMixedWritePressureDuration()
	if err != nil {
		t.Fatalf("resolve mixed write pressure duration: %v", err)
	}
	runSQLiteMixedWritePressure(t, duration)
}

func sqliteMixedWritePressureDuration() (time.Duration, error) {
	durationRaw, hasDuration := os.LookupEnv("SQLITE_MIXED_WRITE_DURATION")
	millisRaw, hasMillis := os.LookupEnv("SQLITE_MIXED_WRITE_DURATION_MS")
	if hasDuration && hasMillis {
		return 0, fmt.Errorf("set only one of SQLITE_MIXED_WRITE_DURATION or SQLITE_MIXED_WRITE_DURATION_MS")
	}
	if hasDuration {
		duration, err := time.ParseDuration(strings.TrimSpace(durationRaw))
		if err != nil {
			return 0, fmt.Errorf("parse SQLITE_MIXED_WRITE_DURATION: %w", err)
		}
		if duration <= 0 {
			return 0, fmt.Errorf("SQLITE_MIXED_WRITE_DURATION must be positive")
		}
		return duration, nil
	}
	if hasMillis {
		millis, err := strconv.ParseInt(strings.TrimSpace(millisRaw), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse SQLITE_MIXED_WRITE_DURATION_MS: %w", err)
		}
		if millis <= 0 {
			return 0, fmt.Errorf("SQLITE_MIXED_WRITE_DURATION_MS must be positive")
		}
		return time.Duration(millis) * time.Millisecond, nil
	}
	if strings.TrimSpace(os.Getenv("SQLITE_RELEASE_GATE_ID")) == "G11" {
		return sqliteMixedWriteReleaseDefaultDuration, nil
	}
	return sqliteMixedWriteLocalDefaultDuration, nil
}

func TestSQLiteMixedWritePressureDuration(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		want    time.Duration
		wantErr bool
	}{
		{name: "ordinary test uses short default", want: sqliteMixedWriteLocalDefaultDuration},
		{name: "G11 release gate defaults to five minutes", env: map[string]string{"SQLITE_RELEASE_GATE_ID": "G11"}, want: sqliteMixedWriteReleaseDefaultDuration},
		{name: "explicit duration string overrides release gate default", env: map[string]string{"SQLITE_RELEASE_GATE_ID": "G11", "SQLITE_MIXED_WRITE_DURATION": "1500ms"}, want: 1500 * time.Millisecond},
		{name: "explicit millisecond duration is supported", env: map[string]string{"SQLITE_MIXED_WRITE_DURATION_MS": "2500"}, want: 2500 * time.Millisecond},
		{name: "invalid explicit duration fails fast", env: map[string]string{"SQLITE_MIXED_WRITE_DURATION": "0s"}, wantErr: true},
		{name: "invalid explicit millisecond duration fails fast", env: map[string]string{"SQLITE_MIXED_WRITE_DURATION_MS": "-1"}, wantErr: true},
		{name: "conflicting explicit duration env fails fast", env: map[string]string{"SQLITE_MIXED_WRITE_DURATION": "1500ms", "SQLITE_MIXED_WRITE_DURATION_MS": "1500"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withSQLiteMixedWriteDurationEnv(t, tc.env)
			assertSQLiteMixedWritePressureDuration(t, tc.want, tc.wantErr)
		})
	}
}

func assertSQLiteMixedWritePressureDuration(t *testing.T, want time.Duration, wantErr bool) {
	t.Helper()
	got, err := sqliteMixedWritePressureDuration()
	if wantErr {
		if err == nil {
			t.Fatalf("duration resolver returned %s without error, want fail-fast", got)
		}
		return
	}
	if err != nil {
		t.Fatalf("duration resolver returned error: %v", err)
	}
	if got != want {
		t.Fatalf("duration = %s, want %s", got, want)
	}
}

func withSQLiteMixedWriteDurationEnv(t *testing.T, values map[string]string) {
	t.Helper()
	keys := []string{
		"SQLITE_RELEASE_GATE_ID",
		"SQLITE_MIXED_WRITE_DURATION",
		"SQLITE_MIXED_WRITE_DURATION_MS",
	}
	previous := make(map[string]*string, len(keys))
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			copy := value
			previous[key] = &copy
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}
	for key, value := range values {
		if err := os.Setenv(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
	t.Cleanup(func() {
		for _, key := range keys {
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("cleanup unset %s: %v", key, err)
			}
			if value := previous[key]; value != nil {
				if err := os.Setenv(key, *value); err != nil {
					t.Fatalf("cleanup set %s: %v", key, err)
				}
			}
		}
	})
}

func runSQLiteMixedWritePressure(t *testing.T, duration time.Duration) {
	t.Helper()
	if duration <= 0 {
		t.Fatalf("mixed write pressure duration must be positive: %s", duration)
	}
	db, dbPath := openMigratedSQLiteDB(t, "mixed-write")
	if err := db.Close(); err != nil {
		t.Fatalf("close parent DB before mixed child writers: %v", err)
	}
	dir := t.TempDir()
	mainChild := startSQLiteMixedWriteChild(t, "main", dbPath, filepath.Join(dir, "main.json"), duration)
	orchChild := startSQLiteMixedWriteChild(t, "orch", dbPath, filepath.Join(dir, "orch.json"), duration)
	waitSQLiteMixedWriteChild(t, mainChild)
	waitSQLiteMixedWriteChild(t, orchChild)

	mainMetrics := readSQLiteMixedMetrics(t, mainChild.metricsPath)
	orchMetrics := readSQLiteMixedMetrics(t, orchChild.metricsPath)
	if mainMetrics.Operations == 0 || orchMetrics.Operations == 0 {
		t.Fatalf("mixed write operations main=%d orch=%d, want both > 0", mainMetrics.Operations, orchMetrics.Operations)
	}
	if mainMetrics.FailureCount != 0 || orchMetrics.FailureCount != 0 {
		t.Fatalf("mixed write failures main=%+v orch=%+v", mainMetrics, orchMetrics)
	}

	db, err := OpenTest(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open mixed write DB after children: %v", err)
	}
	defer db.Close()
	assertSQLiteMixedWriteResults(t, db, mainMetrics, orchMetrics)
	walBefore := sqliteFileSize(dbPath + "-wal")
	checkpointSQLiteTruncate(t, db)
	walAfter := sqliteFileSize(dbPath + "-wal")
	t.Logf("mixed write metrics: main=%+v orch=%+v wal_before=%d wal_after=%d", mainMetrics, orchMetrics, walBefore, walAfter)
}

type sqliteMixedWriteChild struct {
	role        string
	metricsPath string
	output      bytes.Buffer
	cmd         *exec.Cmd
}

func startSQLiteMixedWriteChild(t *testing.T, role, dbPath, metricsPath string, duration time.Duration) *sqliteMixedWriteChild {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	child := &sqliteMixedWriteChild{role: role, metricsPath: metricsPath}
	child.cmd = exec.Command(exe, "-test.run=TestSQLiteMixedWritePressureChild$", "-test.v")
	child.cmd.Env = append(os.Environ(),
		sqliteMixedWriteChildEnv+"="+role,
		"SQLITE_MIXED_WRITE_DB="+dbPath,
		"SQLITE_MIXED_WRITE_METRICS="+metricsPath,
		fmt.Sprintf("SQLITE_MIXED_WRITE_DURATION_MS=%d", duration.Milliseconds()),
	)
	child.cmd.Stdout = &child.output
	child.cmd.Stderr = &child.output
	if err := child.cmd.Start(); err != nil {
		t.Fatalf("start mixed write child %s: %v", role, err)
	}
	return child
}

func waitSQLiteMixedWriteChild(t *testing.T, child *sqliteMixedWriteChild) {
	t.Helper()
	if err := child.cmd.Wait(); err != nil {
		t.Fatalf("mixed write child %s failed: %v\n%s", child.role, err, child.output.String())
	}
}

func runSQLiteMainAppPressureWrite(ctx context.Context, db *sql.DB, index int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixMilli()
	suffix := fmt.Sprintf("mixed-main-%06d", index)
	agentID := "mixed-agent-main-" + suffix
	threadID := "mixed-thread-" + suffix
	if _, err := tx.ExecContext(ctx, `
INSERT INTO system_logs (ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, extra)
VALUES (?, 'INFO', 'mixed-write', ?, ?, 'main-app', 'release-gate', ?, ?, ?, 'thread_write', 'sqlite', 1, ?)`,
		now, suffix, sqliteJSONPayload(1024), agentID, threadID, "trace-"+suffix, sqliteJSONPayload(1024)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO agent_status (agent_id, agent_name, session_id, status, output_tail, created_at, updated_at)
VALUES (?, 'Mixed Main', 'mixed-session', 'running', '[]', ?, ?)
ON CONFLICT(agent_id) DO UPDATE SET status = excluded.status, updated_at = excluded.updated_at`,
		agentID, now, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO agent_provider_binding (agent_id, provider, provider_thread_id, codex_thread_id, cwd, created_at, updated_at)
VALUES (?, 'codex', ?, ?, ?, ?, ?)
ON CONFLICT(agent_id) DO UPDATE SET provider_thread_id = excluded.provider_thread_id, updated_at = excluded.updated_at`,
		agentID, "provider-"+suffix, "codex-"+suffix, os.TempDir(), now, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO agent_threads (thread_id, name, prompt, model, cwd, status, created_at, updated_at, config_override, prompt_snapshot, agent_key)
VALUES (?, ?, ?, 'gpt-5', ?, 'running', ?, ?, '{}', '{}', ?)`,
		threadID, suffix, sqliteJSONPayload(1024), os.TempDir(), now, now, agentID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO prompt_templates (prompt_key, title, agent_key, tool_name, prompt_text, created_by, updated_by, created_at, updated_at)
VALUES (?, ?, ?, 'codex', ?, 'mixed', 'mixed', ?, ?)`,
		"mixed/prompt-"+suffix, suffix, agentID, sqliteJSONPayload(1024), now, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO session_insights (
    thread_id, agent_id, session_id, provider, local_turn_id, provider_turn_id,
    started_at, completed_at, duration_ms, success, status, approval_requests,
    approval_requests_observed, token_input, token_output, token_total,
    token_snapshot_observed, created_at, updated_at
) VALUES (?, ?, ?, 'codex', ?, ?, ?, ?, 1, 1, 'ok', 1, 1, 10, 5, 15, 1, ?, ?)`,
		threadID, agentID, "session-"+suffix, "local-"+suffix, "provider-"+suffix, now, now+1, now, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO cron_jobs (id, name, prompt, schedule_expr, cwd, next_run_at, thread_id, agent_id, created_at, updated_at)
VALUES (?, ?, 'run', '* * * * *', ?, ?, ?, ?, ?, ?)`,
		"mixed-cron-"+suffix, suffix, os.TempDir(), now, threadID, agentID, now, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO cron_job_runs (id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id, agent_id, turn_id, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
		"mixed-cron-run-"+suffix, "mixed-cron-"+suffix, now, "idem-"+suffix, "dedupe-"+suffix, threadID, agentID, "turn-"+suffix, now, now); err != nil {
		return err
	}
	return tx.Commit()
}

func runSQLiteOrchPressureWrite(ctx context.Context, db *sql.DB, index int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixMilli()
	suffix := fmt.Sprintf("mixed-orch-%06d", index)
	dagKey := "mixed-dag-" + suffix
	runKey := "mixed-run-" + suffix
	targetAgent := "mixed-agent-orch"
	runID, err := insertSQLiteMixedOrchRun(ctx, tx, dagKey, runKey, suffix, now)
	if err != nil {
		return err
	}
	idempotencyKey := "mixed-wakeup-" + suffix
	if err := insertSQLiteMixedOrchNodeAndWakeup(ctx, tx, dagKey, suffix, targetAgent, idempotencyKey, runID, now); err != nil {
		return err
	}
	if err := claimSQLiteMixedOrchWakeup(ctx, tx, idempotencyKey, now); err != nil {
		return err
	}
	if err := upsertSQLiteMixedOrchLease(ctx, tx, targetAgent, now); err != nil {
		return err
	}
	if err := appendSQLiteMixedRunEvent(ctx, tx, runID, suffix, now); err != nil {
		return err
	}
	if err := markSQLiteMixedOrchWakeupSent(ctx, tx, idempotencyKey, now); err != nil {
		return err
	}
	return tx.Commit()
}

func insertSQLiteMixedOrchRun(ctx context.Context, tx *sql.Tx, dagKey, runKey, suffix string, now int64) (int64, error) {
	if _, err := tx.ExecContext(ctx, `
INSERT INTO task_dags (dag_key, title, status, created_by, metadata, started_at, created_at, updated_at, version)
VALUES (?, ?, 'active', 'mixed', '{}', ?, ?, ?, 1)`,
		dagKey, suffix, now, now, now); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO task_dag_runs (run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, events, metadata, created_at, updated_at)
VALUES (?, ?, 1, 'manual', 'running', ?, '[]', '{}', ?, ?)`,
		runKey, dagKey, now, now, now)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func insertSQLiteMixedOrchNodeAndWakeup(ctx context.Context, tx *sql.Tx, dagKey, suffix, targetAgent, idempotencyKey string, runID, now int64) error {
	if _, err := tx.ExecContext(ctx, `
INSERT INTO task_dag_nodes (dag_key, node_key, title, node_type, assigned_to, status, command_ref, config, result, started_at, created_at, updated_at, run_id)
VALUES (?, 'root', ?, 'task', ?, 'running', 'mixed', '{}', '{}', ?, ?, ?, ?)`,
		dagKey, suffix, targetAgent, now, now, now, runID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO task_dag_wakeups (dag_key, node_key, run_id, wakeup_kind, target_agent_id, prompt_payload, idempotency_key, status, next_retry_at, created_at, updated_at)
VALUES (?, 'root', ?, 'node_start', ?, ?, ?, 'pending', ?, ?, ?)`,
		dagKey, runID, targetAgent, sqliteJSONPayload(1024), idempotencyKey, now, now, now)
	return err
}

func claimSQLiteMixedOrchWakeup(ctx context.Context, tx *sql.Tx, idempotencyKey string, now int64) error {
	claim, err := tx.ExecContext(ctx, `
UPDATE task_dag_wakeups
SET status = 'dispatching', claimed_at = ?, claimed_by = ?, lease_expires_at = ?, updated_at = ?
WHERE idempotency_key = ? AND status = 'pending'`,
		now, "mixed-worker", now+int64(time.Minute.Milliseconds()), now, idempotencyKey)
	if err != nil {
		return err
	}
	return requireSQLiteMixedOneRow(claim, "claim", idempotencyKey)
}

func upsertSQLiteMixedOrchLease(ctx context.Context, tx *sql.Tx, targetAgent string, now int64) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO task_dag_worker_leases (target_agent_id, owner_id, lease_expires_at, updated_at)
VALUES (?, 'mixed-worker', ?, ?)
ON CONFLICT(target_agent_id) DO UPDATE SET owner_id = excluded.owner_id, lease_expires_at = excluded.lease_expires_at, updated_at = excluded.updated_at`,
		targetAgent, now+int64(time.Minute.Milliseconds()), now)
	return err
}

func markSQLiteMixedOrchWakeupSent(ctx context.Context, tx *sql.Tx, idempotencyKey string, now int64) error {
	sent, err := tx.ExecContext(ctx, `
UPDATE task_dag_wakeups
SET status = 'sent', sent_at = ?, updated_at = ?
WHERE idempotency_key = ? AND status = 'dispatching'`,
		now, now, idempotencyKey)
	if err != nil {
		return err
	}
	return requireSQLiteMixedOneRow(sent, "dispatch", idempotencyKey)
}

func requireSQLiteMixedOneRow(result sql.Result, operation, idempotencyKey string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("duplicate or missing wakeup %s for %s: rows=%d", operation, idempotencyKey, rows)
	}
	return nil
}

func appendSQLiteMixedRunEvent(ctx context.Context, tx *sql.Tx, runID int64, suffix string, now int64) error {
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT events FROM task_dag_runs WHERE id = ?", runID).Scan(&raw); err != nil {
		return err
	}
	var events []map[string]any
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		return err
	}
	events = append(events, map[string]any{"kind": "run_event_append", "suffix": suffix, "ts": now})
	encoded, err := json.Marshal(events)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE task_dag_runs SET events = ?, updated_at = ? WHERE id = ?", string(encoded), now, runID); err != nil {
		return err
	}
	return nil
}

func retrySQLitePressureWrite(ctx context.Context, metrics *sqliteMixedWriteMetrics, label string, fn func() error) error {
	const maxAttempts = 20
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%s cancelled before retry attempt %d: %w", label, attempt+1, err)
		}
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !isSQLiteBusyLockedText(lastErr) {
			return fmt.Errorf("%s failed without retryable SQLite lock: %w", label, lastErr)
		}
		metrics.RetryCount++
		waitStart := time.Now()
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%s retry wait cancelled after SQLITE_BUSY/LOCKED: %w", label, ctx.Err())
		case <-timer.C:
		}
		waitMillis := time.Since(waitStart).Milliseconds()
		if waitMillis > metrics.MaxWaitMillis {
			metrics.MaxWaitMillis = waitMillis
		}
	}
	return fmt.Errorf("%s retry exhausted after %d attempts; last error: %w", label, maxAttempts, lastErr)
}

func isSQLiteBusyLockedText(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database is busy") ||
		strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "SQLITE_LOCKED")
}

func assertSQLiteMixedWriteResults(t *testing.T, db *sql.DB, mainMetrics, orchMetrics sqliteMixedWriteMetrics) {
	t.Helper()
	assertWhereCountAtLeast(t, db, "system_logs", "source = 'main-app'", nil, mainMetrics.Operations)
	assertWhereCountAtLeast(t, db, "task_dag_wakeups", "idempotency_key LIKE 'mixed-wakeup-%' AND status = 'sent'", nil, orchMetrics.Operations)
	var wakeups, distinctKeys int
	if err := db.QueryRow("SELECT COUNT(*), COUNT(DISTINCT idempotency_key) FROM task_dag_wakeups WHERE idempotency_key LIKE 'mixed-wakeup-%'").Scan(&wakeups, &distinctKeys); err != nil {
		t.Fatalf("count mixed wakeup idempotency: %v", err)
	}
	if wakeups != distinctKeys {
		t.Fatalf("mixed wakeups total=%d distinct_keys=%d, duplicate dispatch detected", wakeups, distinctKeys)
	}
	var eventCount int
	if err := db.QueryRow("SELECT COALESCE(SUM(json_array_length(events)), 0) FROM task_dag_runs WHERE run_key LIKE 'mixed-run-%'").Scan(&eventCount); err != nil {
		t.Fatalf("count mixed run events: %v", err)
	}
	if eventCount != orchMetrics.Operations {
		t.Fatalf("mixed run event count = %d, want %d", eventCount, orchMetrics.Operations)
	}
}

func writeSQLiteMixedMetrics(t *testing.T, path string, metrics sqliteMixedWriteMetrics) {
	t.Helper()
	body, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		t.Fatalf("marshal mixed write metrics: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write mixed write metrics: %v", err)
	}
}

func readSQLiteMixedMetrics(t *testing.T, path string) sqliteMixedWriteMetrics {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read mixed write metrics %s: %v", path, err)
	}
	var metrics sqliteMixedWriteMetrics
	if err := json.Unmarshal(body, &metrics); err != nil {
		t.Fatalf("decode mixed write metrics %s: %v", path, err)
	}
	return metrics
}

func sqliteFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
