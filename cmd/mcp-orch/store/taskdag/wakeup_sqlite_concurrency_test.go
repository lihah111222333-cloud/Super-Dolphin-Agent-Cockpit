package taskdag

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	_ "modernc.org/sqlite"
)

const wakeupClaimChildEnv = "TASK12_WAKEUP_CLAIM_CHILD"

func TestWakeupSQLiteClaimChildProcess(t *testing.T) {
	if os.Getenv(wakeupClaimChildEnv) == "" {
		return
	}
	dbPath := os.Getenv("TASK12_WAKEUP_DB")
	outPath := os.Getenv("TASK12_WAKEUP_OUT")
	workerID := os.Getenv("TASK12_WAKEUP_WORKER")
	if dbPath == "" || outPath == "" || workerID == "" {
		t.Fatalf("child missing db/out/worker env")
	}
	db := openTaskDAGSQLiteDBAt(t, dbPath, 4)
	store := NewStore(db).(*store)
	ids, err := claimAllSQLiteWakeups(context.Background(), store, workerID)
	if err != nil {
		t.Fatalf("child claim wakeups: %v", err)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		t.Fatalf("marshal child ids: %v", err)
	}
	if err := os.WriteFile(outPath, raw, 0o600); err != nil {
		t.Fatalf("write child ids: %v", err)
	}
}

func TestSQLiteWakeupClaimConcurrentGoroutinesAndProcesses(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wakeup-claim.sqlite")
	db := openTaskDAGSQLiteDBAt(t, dbPath, 8)
	store := NewStore(db).(*store)
	runID := seedSQLiteWakeupClaimRows(t, ctx, store, 100)

	results := make(chan wakeupClaimResult, 6)
	var wg sync.WaitGroup
	for i := range 4 {
		workerID := fmt.Sprintf("goroutine-%d", i)
		wg.Go(func() {
			ids, err := claimAllSQLiteWakeups(ctx, store, workerID)
			results <- wakeupClaimResult{ids: ids, err: err}
		})
	}
	children := launchWakeupClaimChildren(t, dbPath, dir, 2)
	wg.Wait()
	for _, child := range children {
		waitWakeupClaimChild(t, child)
		results <- wakeupClaimResult{ids: readWakeupClaimChildIDs(t, child.outPath)}
	}
	close(results)

	seen := map[int64]string{}
	var total int
	for result := range results {
		if result.err != nil {
			t.Fatalf("claim worker error = %v", result.err)
		}
		total += len(result.ids)
		for _, claimed := range result.ids {
			if prev, ok := seen[claimed.ID]; ok {
				t.Fatalf("duplicate wakeup id %d claimed by %s and %s", claimed.ID, prev, claimed.Worker)
			}
			seen[claimed.ID] = claimed.Worker
		}
	}
	if total != 100 || len(seen) != 100 {
		t.Fatalf("claimed total=%d unique=%d, want 100/100", total, len(seen))
	}
	assertSQLiteWakeupClaimedRows(t, ctx, db, runID, 100)
}

func TestSQLiteWorkerLeaseCASAndOwnerFencing(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)

	rows, err := store.AcquireWorkerLease(ctx, AcquireWorkerLeaseInput{TargetAgentID: "agent-a", OwnerID: "owner-a", LeaseInterval: "1m"})
	assertWorkerLeaseRows(t, "owner-a acquire", rows, err, 1)
	rows, err = store.AcquireWorkerLease(ctx, AcquireWorkerLeaseInput{TargetAgentID: "agent-a", OwnerID: "owner-b", LeaseInterval: "1m"})
	assertWorkerLeaseRows(t, "owner-b acquire active lease", rows, err, 0)
	rows, err = store.RenewWorkerLease(ctx, RenewWorkerLeaseInput{TargetAgentID: "agent-a", OwnerID: "owner-b", LeaseInterval: "1m"})
	assertWorkerLeaseRows(t, "owner-b renew", rows, err, 0)
	assertWorkerLeaseReleaseFails(t, "owner-b release", store.ReleaseWorkerLease(ctx, ReleaseWorkerLeaseInput{TargetAgentID: "agent-a", OwnerID: "owner-b"}))
	assertWorkerLeaseReleaseSucceeds(t, "owner-a release", store.ReleaseWorkerLease(ctx, ReleaseWorkerLeaseInput{TargetAgentID: "agent-a", OwnerID: "owner-a"}))
	rows, err = store.AcquireWorkerLease(ctx, AcquireWorkerLeaseInput{TargetAgentID: "agent-a", OwnerID: "owner-b", LeaseInterval: "1m"})
	assertWorkerLeaseRows(t, "owner-b acquire after release", rows, err, 1)
}

func TestSQLiteRetryWakeupWithNodeConfigPatchRollsBackRetryOnStaleConfig(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteTaskDAGTemplate(t, ctx, store)
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-smart-retry", "dag-multi")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-multi", run.ID)

	if _, err := store.EnqueueWakeup(ctx, EnqueueWakeupInput{
		DagKey:         "dag-multi",
		NodeKey:        "root",
		RunID:          run.ID,
		WakeupKind:     "node_start",
		TargetAgentID:  "agent-smart",
		PromptPayload:  json.RawMessage(`{}`),
		IdempotencyKey: "smart-retry-wakeup",
	}); err != nil {
		t.Fatalf("EnqueueWakeup() error = %v", err)
	}
	claimed, err := store.ClaimDueWakeups(ctx, ClaimDueWakeupsInput{
		ClaimedBy:     "worker-smart",
		LeaseInterval: "1m",
		Limit:         1,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimDueWakeups() rows=%d error=%v, want one claimed wakeup", len(claimed), err)
	}

	rows, err := store.RetryWakeupWithNodeConfigPatch(ctx, RetryWakeupWithNodeConfigPatchInput{
		RetryWakeup: RetryWakeupInput{
			ID:             claimed[0].ID,
			RetryInterval:  "1m",
			LastError:      "smart retry prepare failed",
			ClaimedAt:      *claimed[0].ClaimedAt,
			ClaimedBy:      claimed[0].ClaimedBy,
			LeaseExpiresAt: *claimed[0].LeaseExpiresAt,
		},
		NodeConfig: NodeConfigPatchInput{
			DagKey:         "dag-multi",
			NodeKey:        "root",
			RunID:          run.ID,
			PreviousConfig: json.RawMessage(`{"template":false}`),
			Config:         json.RawMessage(`{"template":true,"retry":1}`),
		},
	})
	if rows != 0 {
		t.Fatalf("RetryWakeupWithNodeConfigPatch rows=%d, want 0 on stale config", rows)
	}
	if !platformdb.IsNotFound(err) {
		t.Fatalf("RetryWakeupWithNodeConfigPatch err=%v, want not found", err)
	}
	after, err := store.GetWakeup(ctx, claimed[0].ID)
	if err != nil {
		t.Fatalf("GetWakeup() after stale patch error = %v", err)
	}
	assertSQLiteWakeupStillClaimed(t, after, claimed[0])
	assertSQLiteRunNodeConfig(t, ctx, store, "dag-multi", run.ID, "root", `{"template":true}`)
}

func TestSQLiteWakeupAndRunListsOmitLargeColumns(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	run := createSQLiteRunWithMetadata(t, ctx, store, "run-large-list", "dag-large-list", `{"heavy":"metadata"}`)
	if _, err := store.EnqueueWakeup(ctx, EnqueueWakeupInput{
		DagKey:         "dag-large-list",
		NodeKey:        "node-large",
		RunID:          run.ID,
		WakeupKind:     "node_start",
		TargetAgentID:  "agent-large",
		PromptPayload:  json.RawMessage(`{"heavy":"payload"}`),
		IdempotencyKey: "large-list-wakeup",
	}); err != nil {
		t.Fatalf("EnqueueWakeup() error = %v", err)
	}
	if _, err := store.appendTaskDagRunEvent(ctx, "dag-large-list", run.ID, json.RawMessage(`{"heavy":"event"}`)); err != nil {
		t.Fatalf("appendTaskDagRunEvent() error = %v", err)
	}

	assertWakeupListOmitsPayloadAndDetailLoadsIt(t, ctx, store)

	runs, err := store.ListRuns(ctx, ListRunsFilter{DagKey: "dag-large-list", Limit: 5})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	assertRunListOmitsEventsAndMetadata(t, runs)
	gotRun, err := store.GetRun(ctx, "run-large-list")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	assertRunDetailLoadsEventsAndMetadata(t, gotRun)
}

type wakeupClaimResult struct {
	ids []claimedWakeupID
	err error
}

type claimedWakeupID struct {
	ID     int64  `json:"id"`
	Worker string `json:"worker"`
}

func assertWorkerLeaseRows(t *testing.T, label string, rows int64, err error, want int64) {
	t.Helper()
	if err != nil || rows != want {
		t.Fatalf("%s rows=%d err=%v, want %d/nil", label, rows, err, want)
	}
}

func assertWorkerLeaseReleaseFails(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s err=nil, want owner fence error", label)
	}
}

func assertWorkerLeaseReleaseSucceeds(t *testing.T, label string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s err=%v, want nil", label, err)
	}
}

func assertSQLiteWakeupStillClaimed(t *testing.T, got *Wakeup, want Wakeup) {
	t.Helper()
	if got.Status != "dispatching" {
		t.Fatalf("wakeup status after stale patch = %q, want dispatching", got.Status)
	}
	if got.ClaimedBy != want.ClaimedBy {
		t.Fatalf("claimed_by after stale patch = %q, want %q", got.ClaimedBy, want.ClaimedBy)
	}
	if got.AttemptCount != want.AttemptCount {
		t.Fatalf("attempt_count after stale patch = %d, want %d", got.AttemptCount, want.AttemptCount)
	}
	assertSQLiteTimePtrEqual(t, "claimed_at", got.ClaimedAt, want.ClaimedAt)
	assertSQLiteTimePtrEqual(t, "lease_expires_at", got.LeaseExpiresAt, want.LeaseExpiresAt)
	if got.LastError != want.LastError {
		t.Fatalf("last_error after stale patch = %q, want %q", got.LastError, want.LastError)
	}
}

func assertSQLiteTimePtrEqual(t *testing.T, label string, got, want *time.Time) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s after stale patch = nil, want %v", label, want)
	}
	if want == nil {
		t.Fatalf("%s before stale patch = nil", label)
	}
	if !got.Equal(*want) {
		t.Fatalf("%s after stale patch = %v, want %v", label, got, want)
	}
}

func assertSQLiteRunNodeConfig(t *testing.T, ctx context.Context, store *store, dagKey string, runID int64, nodeKey string, want string) {
	t.Helper()
	nodes, err := store.ListRunNodes(ctx, dagKey, runID)
	if err != nil {
		t.Fatalf("ListRunNodes(%s/%d) error = %v", dagKey, runID, err)
	}
	for _, node := range nodes {
		if node.NodeKey != nodeKey {
			continue
		}
		if string(node.Config) != want {
			t.Fatalf("node %s config = %s, want %s", nodeKey, node.Config, want)
		}
		return
	}
	t.Fatalf("node %s not found in run %s/%d", nodeKey, dagKey, runID)
}

func assertWakeupListOmitsPayloadAndDetailLoadsIt(t *testing.T, ctx context.Context, store *store) {
	t.Helper()
	wakeups, err := store.ListPendingOrDispatchingWakeups(ctx)
	if err != nil {
		t.Fatalf("ListPendingOrDispatchingWakeups() error = %v", err)
	}
	if len(wakeups) != 1 || len(wakeups[0].PromptPayload) != 0 {
		t.Fatalf("pending wakeup list payload len=%d rows=%d, want omitted payload in one row", len(wakeups[0].PromptPayload), len(wakeups))
	}
	detail, err := store.GetWakeup(ctx, wakeups[0].ID)
	if err != nil {
		t.Fatalf("GetWakeup() error = %v", err)
	}
	if string(detail.PromptPayload) != `{"heavy":"payload"}` {
		t.Fatalf("GetWakeup payload = %s, want full payload", detail.PromptPayload)
	}
}

func assertRunListOmitsEventsAndMetadata(t *testing.T, runs []Run) {
	t.Helper()
	if len(runs) != 1 || len(runs[0].Events) != 0 || len(runs[0].Metadata) != 0 {
		t.Fatalf("run list rows=%d events=%q metadata=%q, want large columns omitted", len(runs), runs[0].Events, runs[0].Metadata)
	}
}

func assertRunDetailLoadsEventsAndMetadata(t *testing.T, run *Run) {
	t.Helper()
	if string(run.Metadata) != `{"heavy":"metadata"}` || len(run.Events) == 0 {
		t.Fatalf("GetRun metadata/events = %s / %s, want detail columns", run.Metadata, run.Events)
	}
}

func seedSQLiteWakeupClaimRows(t *testing.T, ctx context.Context, store *store, count int) int64 {
	t.Helper()
	if _, err := store.UpsertDAG(ctx, DAG{DagKey: "dag-claim", Title: "claim", Status: "draft", CreatedBy: "tester", Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("UpsertDAG() error = %v", err)
	}
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-claim", "dag-claim")
	for i := range count {
		if _, err := store.EnqueueWakeup(ctx, EnqueueWakeupInput{
			DagKey:         "dag-claim",
			NodeKey:        fmt.Sprintf("node-%03d", i),
			RunID:          run.ID,
			WakeupKind:     "node_start",
			TargetAgentID:  "agent-claim",
			PromptPayload:  json.RawMessage(`{}`),
			IdempotencyKey: fmt.Sprintf("claim-%03d", i),
		}); err != nil {
			t.Fatalf("EnqueueWakeup(%d) error = %v", i, err)
		}
	}
	return run.ID
}

func claimAllSQLiteWakeups(ctx context.Context, store *store, workerID string) ([]claimedWakeupID, error) {
	claimedIDs := make([]claimedWakeupID, 0)
	for {
		claimed, err := store.ClaimDueWakeups(ctx, ClaimDueWakeupsInput{
			ClaimedBy:     workerID,
			LeaseInterval: "1m",
			Limit:         3,
		})
		if err != nil {
			return nil, err
		}
		if len(claimed) == 0 {
			return claimedIDs, nil
		}
		for _, wakeup := range claimed {
			claimedIDs = append(claimedIDs, claimedWakeupID{ID: wakeup.ID, Worker: workerID})
		}
	}
}

type wakeupClaimChild struct {
	index   int
	outPath string
	cmd     *exec.Cmd
	output  *bytes.Buffer
}

func launchWakeupClaimChildren(t *testing.T, dbPath, dir string, count int) []wakeupClaimChild {
	t.Helper()
	children := make([]wakeupClaimChild, 0, count)
	for i := range count {
		outPath := filepath.Join(dir, fmt.Sprintf("child-%d.json", i))
		cmd := exec.Command(os.Args[0], "-test.run=TestWakeupSQLiteClaimChildProcess")
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		cmd.Env = append(os.Environ(),
			wakeupClaimChildEnv+"=1",
			"TASK12_WAKEUP_DB="+dbPath,
			"TASK12_WAKEUP_OUT="+outPath,
			fmt.Sprintf("TASK12_WAKEUP_WORKER=process-%d", i),
		)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child %d: %v", i, err)
		}
		children = append(children, wakeupClaimChild{index: i, outPath: outPath, cmd: cmd, output: &output})
	}
	return children
}

func waitWakeupClaimChild(t *testing.T, child wakeupClaimChild) {
	t.Helper()
	if err := child.cmd.Wait(); err != nil {
		t.Fatalf("child %d failed: %v\n%s", child.index, err, child.output.String())
	}
}

func readWakeupClaimChildIDs(t *testing.T, path string) []claimedWakeupID {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read child ids %s: %v", path, err)
	}
	var ids []claimedWakeupID
	if err := json.Unmarshal(raw, &ids); err != nil {
		t.Fatalf("unmarshal child ids %s: %v", path, err)
	}
	return ids
}

func assertSQLiteWakeupClaimedRows(t *testing.T, ctx context.Context, db *sql.DB, runID int64, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dag_wakeups WHERE run_id = ? AND status = 'dispatching'`, runID).Scan(&got); err != nil {
		t.Fatalf("count dispatching wakeups: %v", err)
	}
	if got != want {
		t.Fatalf("dispatching wakeups = %d, want %d", got, want)
	}
}

func createSQLiteRunWithMetadata(t *testing.T, ctx context.Context, store *store, runKey, dagKey, metadata string) *Run {
	t.Helper()
	if _, err := store.UpsertDAG(ctx, DAG{DagKey: dagKey, Title: dagKey, Status: "draft", CreatedBy: "tester", Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("UpsertDAG(%s) error = %v", dagKey, err)
	}
	run, err := store.CreateRun(ctx, CreateRunInput{RunKey: runKey, DagKey: dagKey, DagVersionSnapshot: 1, TriggerSource: "manual", Metadata: json.RawMessage(metadata)})
	if err != nil {
		t.Fatalf("CreateRun(%s) error = %v", runKey, err)
	}
	return run
}

func openTaskDAGSQLiteDBAt(t *testing.T, path string, maxOpenConns int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", taskDAGSQLiteDSN(path))
	if err != nil {
		t.Fatalf("open sqlite %s: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(maxOpenConns)
	if err := sqliteruntime.RunMigrations(context.Background(), db, taskDAGSQLiteMigrationsDir(t)); err != nil {
		t.Fatalf("run sqlite migrations: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping sqlite %s: %v", path, err)
	}
	return db
}
