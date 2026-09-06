package orchestration

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	taskdag "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	_ "modernc.org/sqlite"
)

const (
	wakeupCrashWindowChildEnv  = "ORCH_WAKEUP_CRASH_WINDOW_CHILD"
	wakeupCrashWindowDBEnv     = "ORCH_WAKEUP_CRASH_WINDOW_DB"
	wakeupCrashWindowMarkerEnv = "ORCH_WAKEUP_CRASH_WINDOW_MARKER"
)

// TestWakeupDispatcherSQLiteCrashWindow_RED_NO_GO is an explicit planning
// candidate, not a safety proof. It reproduces the real two-connection window
// where a fake provider accepts a legacy launch, the process exits before
// MarkWakeupSent, and the reclaimer makes the same wakeup dispatchable again.
// The second accepted launch is the observed RED result: CAS fencing protects
// stale state writes, but there is no durable command/attempt owner spanning
// provider acceptance and the sent mark. No external provider is started.
func TestWakeupDispatcherSQLiteCrashWindow_RED_NO_GO(t *testing.T) {
	if os.Getenv(wakeupCrashWindowChildEnv) == "1" {
		runWakeupCrashWindowChild(t)
		return
	}
	runWakeupCrashWindowParent(t)
}

// runWakeupCrashWindowParent drives the two-connection crash/reclaim sequence.
func runWakeupCrashWindowParent(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wakeup-crash-window.sqlite")
	markerPath := filepath.Join(dir, "provider-accepted.marker")
	dbA := openWakeupCrashWindowSQLiteDB(t, dbPath)
	wakeupID := seedWakeupCrashWindowRow(t, ctx, dbA)
	runWakeupCrashWindowChildProcess(t, dbPath, markerPath)
	if err := dbA.Close(); err != nil {
		t.Fatalf("close first SQLite connection: %v", err)
	}
	assertWakeupCrashWindowRedelivery(t, ctx, dbPath, wakeupID)
}

// runWakeupCrashWindowChildProcess exits after fake provider acceptance.
func runWakeupCrashWindowChildProcess(t *testing.T, dbPath, markerPath string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestWakeupDispatcherSQLiteCrashWindow_RED_NO_GO$")
	cmd.Env = append(os.Environ(),
		wakeupCrashWindowChildEnv+"=1",
		wakeupCrashWindowDBEnv+"="+dbPath,
		wakeupCrashWindowMarkerEnv+"="+markerPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled provider-accept child error = %v\n%s", err, output)
	}
	if got, err := os.ReadFile(markerPath); err != nil || string(got) != "accepted\n" {
		t.Fatalf("child accepted marker = %q error=%v, want accepted\\n", got, err)
	}
}

// assertWakeupCrashWindowRedelivery verifies reclaim enables a second launch.
func assertWakeupCrashWindowRedelivery(t *testing.T, ctx context.Context, dbPath string, wakeupID int64) {
	t.Helper()
	dbB := openWakeupCrashWindowSQLiteDB(t, dbPath)
	storeB := taskdag.NewStore(dbB)
	if _, err := dbB.ExecContext(ctx, `UPDATE task_dag_wakeups SET lease_expires_at = 0 WHERE id = ?`, wakeupID); err != nil {
		t.Fatalf("expire claimed wakeup lease on second connection: %v", err)
	}
	if rows, err := storeB.ReclaimStaleDispatchingWakeups(ctx); err != nil || rows != 1 {
		t.Fatalf("ReclaimStaleDispatchingWakeups() rows=%d error=%v, want 1/nil", rows, err)
	}

	launcher := &dispatcherStubLauncher{}
	d, err := NewWakeupDispatcher(storeB, launcher, nil, WakeupDispatcherConfig{ClaimedBy: "worker-after-crash", LeaseInterval: "30s"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher() error = %v", err)
	}
	handled, err := d.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("ProcessBatch() after reclaim error = %v", err)
	}
	if handled != 1 || len(launcher.calls) != 1 {
		t.Fatalf("reclaimed dispatch handled=%d launcher calls=%d, want 1/1", handled, len(launcher.calls))
	}
	got, err := storeB.GetWakeup(ctx, wakeupID)
	if err != nil {
		t.Fatalf("GetWakeup() error = %v", err)
	}
	assertCrashWindowWakeupOutcome(t, got)

	t.Logf("RED/NO-GO: provider accepted once before controlled crash and once after lease reclaim; wakeup_id=%d accepted_launches=2", wakeupID)
}

// assertCrashWindowWakeupOutcome checks the durable duplicate-delivery result.
func assertCrashWindowWakeupOutcome(t *testing.T, got *taskdag.Wakeup) {
	t.Helper()
	if got.Status != "sent" {
		t.Fatalf("wakeup status after duplicate accepted launch = %q, want sent", got.Status)
	}
	if got.AttemptCount != 2 {
		t.Fatalf("wakeup attempt_count after duplicate accepted launch = %d, want 2", got.AttemptCount)
	}
}

func runWakeupCrashWindowChild(t *testing.T) {
	t.Helper()
	dbPath := strings.TrimSpace(os.Getenv(wakeupCrashWindowDBEnv))
	markerPath := strings.TrimSpace(os.Getenv(wakeupCrashWindowMarkerEnv))
	if dbPath == "" || markerPath == "" {
		t.Fatalf("crash child requires db and marker paths")
	}
	db := openWakeupCrashWindowSQLiteDB(t, dbPath)
	launcher := crashAfterWakeupAcceptedLauncher{markerPath: markerPath}
	d, err := NewWakeupDispatcher(taskdag.NewStore(db), &launcher, nil, WakeupDispatcherConfig{ClaimedBy: "worker-before-crash", LeaseInterval: "30s"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher() child error = %v", err)
	}
	handled, processErr := d.ProcessBatch(context.Background())
	t.Fatalf("controlled crash launcher returned unexpectedly handled=%d err=%v", handled, processErr)
}

type crashAfterWakeupAcceptedLauncher struct {
	markerPath string
}

func (l *crashAfterWakeupAcceptedLauncher) LaunchAgent(context.Context, LaunchRequest) error {
	if err := os.WriteFile(l.markerPath, []byte("accepted\n"), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
	return nil
}

func openWakeupCrashWindowSQLiteDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", orchestrationSQLiteDSN(path))
	if err != nil {
		t.Fatalf("open crash-window SQLite database: %v", err)
	}
	db.SetMaxOpenConns(4)
	if err := sqliteruntime.RunMigrations(context.Background(), db, orchestrationSQLiteMigrationsDir(t)); err != nil {
		_ = db.Close()
		t.Fatalf("run crash-window SQLite migrations: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedWakeupCrashWindowRow(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	now := time.Now().UTC().UnixMilli()
	payload := []byte(`{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"agent-crash-window","prompt":"accepted"}`)
	result, err := db.ExecContext(ctx, `INSERT INTO task_dag_wakeups (
		dag_key, node_key, run_id, wakeup_kind, target_agent_id, prompt_payload,
		payload_schema_version, idempotency_key, status, attempt_count, next_retry_at,
		claimed_by, created_at, updated_at
	) VALUES ('', '', NULL, 'start', 'agent-crash-window',
		?, 1, 'red-crash-window-1', 'pending', 0, ?, '', ?, ?)`, payload, now-1000, now, now)
	if err != nil {
		t.Fatalf("seed crash-window wakeup: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read crash-window wakeup id: %v", err)
	}
	return id
}
