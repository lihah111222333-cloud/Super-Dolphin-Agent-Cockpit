package cron

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/google/uuid"
)

// These tests exercise the real SQLite ClaimDueJobsForUpdate statement (no
// stubs) to prove the atomic-claim semantics that replaced PostgreSQL's
// FOR UPDATE SKIP LOCKED. The contract: across concurrent claimers, every
// due job is claimed exactly once: duplicate claims = 0, missing = 0.

const (
	subprocEnvDB    = "CRON_CLAIM_SUBPROC_DB"
	subprocEnvBy    = "CRON_CLAIM_SUBPROC_CLAIMED_BY"
	subprocEnvNow   = "CRON_CLAIM_SUBPROC_NOW_MS"
	subprocEnvLease = "CRON_CLAIM_SUBPROC_LEASE_MS"
)

func TestClaimDueJobsSameProcessNoDuplicates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := openMigratedCronStore(t)

	now := time.Unix(1_700_000_000, 0).UTC()
	const total = 100
	ids := seedDueJobs(ctx, t, store, total, now)

	const workers = 4
	var (
		mu      sync.Mutex
		claimed = make(map[string]int)
		wg      sync.WaitGroup
	)
	workersDone := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-workersDone:
		case <-time.After(time.Second):
			t.Fatal("same-process claim goroutines did not stop")
		}
	})
	for w := range workers {
		worker := w
		wg.Go(func() {
			claimedBy := fmt.Sprintf("scheduler-%d", worker)
			for {
				jobs, err := store.ClaimDueJobsForUpdate(ctx, ClaimDueJobsForUpdateParams{
					Now:            now,
					ClaimedBy:      claimedBy,
					LeaseExpiresAt: now.Add(30 * time.Minute),
					ClaimToken:     uuid.NewString(),
					MaxClaim:       8,
				})
				if err != nil {
					t.Errorf("worker %d claim error: %v", worker, err)
					return
				}
				if len(jobs) == 0 {
					return
				}
				mu.Lock()
				for _, j := range jobs {
					claimed[j.ID]++
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	close(workersDone)

	assertExactlyOnce(t, ids, claimed)
}

func TestClaimDueJobsCrossProcessNoDuplicates(t *testing.T) {
	if os.Getenv(subprocEnvDB) != "" {
		// Running as a claim subprocess: claim everything we can and print
		// each claimed job ID on its own stdout line, then exit.
		runCrossProcessClaimWorker(t)
		return
	}
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "secure", "cron-crossproc.db")
	db := openMigratedCronDB(ctx, t, dbPath)
	store := NewStoreWithDB(db, sqlc.New(db))

	now := time.Unix(1_700_000_000, 0).UTC()
	const total = 100
	ids := seedDueJobs(ctx, t, store, total, now)
	// Release the parent's handle so the two subprocesses are the only
	// writers contending for the SQLite file lock.
	if err := db.Close(); err != nil {
		t.Fatalf("close parent DB before subprocess claim: %v", err)
	}

	leaseMS := now.Add(30 * time.Minute).UnixMilli()
	type result struct {
		ids []string
		err error
		out string
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	workersDone := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-workersDone:
		case <-time.After(2 * time.Second):
			t.Fatal("cross-process claim goroutines did not stop")
		}
	})
	for i := range 2 {
		claimedBy := fmt.Sprintf("proc-%d", i)
		wg.Go(func() {
			out, err := runClaimSubprocess(dbPath, claimedBy, now.UnixMilli(), leaseMS)
			results <- result{ids: parseClaimedIDs(out), err: err, out: out}
		})
	}

	claimed := make(map[string]int)
	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatalf("claim subprocess error: %v\noutput:\n%s", r.err, r.out)
		}
		for _, id := range r.ids {
			claimed[id]++
		}
	}
	wg.Wait()
	close(workersDone)

	assertExactlyOnce(t, ids, claimed)
}

func TestClaimReclaimsStaleLeaseButNotFreshLease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := openMigratedCronStore(t)

	now := time.Unix(1_700_000_000, 0).UTC()
	ids := seedDueJobs(ctx, t, store, 1, now)
	jobID := ids[0]

	// First claimer takes the job with a lease that expires at now+10m.
	firstToken := uuid.NewString()
	firstLease := now.Add(10 * time.Minute)
	claimOneCronJob(t, ctx, store, ClaimDueJobsForUpdateParams{
		Now:            now,
		ClaimedBy:      "owner-A",
		LeaseExpiresAt: firstLease,
		ClaimToken:     firstToken,
		MaxClaim:       1,
	}, jobID, "first claim")

	// A second claimer at a time before the lease expires must NOT steal it.
	beforeExpiry := firstLease.Add(-time.Minute)
	claimNoCronJobs(t, ctx, store, ClaimDueJobsForUpdateParams{
		Now:            beforeExpiry,
		ClaimedBy:      "owner-B",
		LeaseExpiresAt: beforeExpiry.Add(10 * time.Minute),
		ClaimToken:     uuid.NewString(),
		MaxClaim:       1,
	}, "pre-expiry claim")

	// After the lease expires, the job becomes reclaimable. Note the claim
	// predicate is due-at <= now, so the job is also due again at this time.
	afterExpiry := firstLease.Add(time.Minute)
	reclaimToken := uuid.NewString()
	reclaimed := claimOneCronJob(t, ctx, store, ClaimDueJobsForUpdateParams{
		Now:            afterExpiry,
		ClaimedBy:      "owner-B",
		LeaseExpiresAt: afterExpiry.Add(10 * time.Minute),
		ClaimToken:     reclaimToken,
		MaxClaim:       1,
	}, jobID, "reclaim")
	assertCronJobClaimFields(t, reclaimed, reclaimToken, "owner-B")

	// The preempted original owner must not be able to mutate via its stale
	// token: claim_token fencing rejects it.
	if err := store.MarkFinished(ctx, MarkFinishedParams{
		ID: jobID, ClaimToken: firstToken, RunID: "stale-run", NextRunAt: afterExpiry.Add(time.Hour), Now: afterExpiry,
	}); err == nil {
		t.Fatalf("stale-token MarkFinished succeeded; claim fencing broken")
	}
}

func TestStaleTerminalCannotReleaseNewClaim(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := openMigratedCronStore(t)

	now := time.Unix(1_700_000_000, 0).UTC()
	jobID := seedDueJobs(ctx, t, store, 1, now)[0]
	oldToken := "old-claim-token"
	oldTurnID := "turn-old"
	oldRun := startRunningCronTurn(t, ctx, store, runningCronTurnFixture{
		jobID: jobID, runID: "run-old", token: oldToken, turnID: oldTurnID, owner: "owner-old",
		agentID: "agent-old", now: now, leaseExpiresAt: now.Add(time.Minute),
	})

	reclaimAt := now.Add(2 * time.Minute)
	newToken := "new-claim-token"
	_ = startRunningCronTurn(t, ctx, store, runningCronTurnFixture{
		jobID: jobID, runID: "run-new", token: newToken, turnID: "turn-new", owner: "owner-new",
		agentID: "agent-new", now: reclaimAt, leaseExpiresAt: reclaimAt.Add(time.Minute),
	})
	if err := store.CASRunStatus(ctx, CASRunStatusParams{
		ID: oldRun.ID, ExpectedStatus: StatusRunning, NextStatus: StatusFinished, UpdatedAt: reclaimAt,
	}); err != nil {
		t.Fatalf("old terminal CAS: %v", err)
	}

	err := store.MarkFinished(ctx, MarkFinishedParams{
		ID:                   jobID,
		ClaimToken:           newToken,
		RunID:                oldRun.ID,
		ExpectedActiveTurnID: oldTurnID,
		LastRunAt:            oldRun.ScheduledAt,
		LastTurnID:           oldTurnID,
		NextRunAt:            reclaimAt.Add(time.Hour),
		Now:                  reclaimAt,
	})
	if !errors.Is(err, ErrClaimTokenMismatch) {
		t.Fatalf("stale terminal MarkFinished error = %v, want ErrClaimTokenMismatch", err)
	}
	assertClaimStillActive(t, ctx, store, jobID, newToken, "turn-new", "owner-new")
}

// runCrossProcessClaimWorker is the body executed when the test binary is
// re-invoked as a claim subprocess. It opens the shared DB, claims in a loop
// until drained, prints each claimed ID, and exits 0.
func runCrossProcessClaimWorker(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	dbPath := os.Getenv(subprocEnvDB)
	claimedBy := os.Getenv(subprocEnvBy)
	nowMS := mustAtoi64(os.Getenv(subprocEnvNow))
	leaseMS := mustAtoi64(os.Getenv(subprocEnvLease))
	now := time.UnixMilli(nowMS).UTC()
	lease := time.UnixMilli(leaseMS).UTC()

	db, err := sqliteruntime.OpenTest(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "subproc open: %v\n", err)
		os.Exit(2)
	}
	defer func() { _ = db.Close() }()
	store := NewStoreWithDB(db, sqlc.New(db))

	for {
		jobs, err := store.ClaimDueJobsForUpdate(ctx, ClaimDueJobsForUpdateParams{
			Now:            now,
			ClaimedBy:      claimedBy,
			LeaseExpiresAt: lease,
			ClaimToken:     uuid.NewString(),
			MaxClaim:       8,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "subproc claim: %v\n", err)
			os.Exit(3)
		}
		if len(jobs) == 0 {
			break
		}
		for _, j := range jobs {
			fmt.Fprintf(os.Stdout, "CLAIMED %s\n", j.ID)
		}
	}
	os.Exit(0)
}

func runClaimSubprocess(dbPath, claimedBy string, nowMS, leaseMS int64) (string, error) {
	cmd := exec.Command(os.Args[0], "-test.run=TestClaimDueJobsCrossProcessNoDuplicates", "-test.v")
	cmd.Env = append(os.Environ(),
		subprocEnvDB+"="+dbPath,
		subprocEnvBy+"="+claimedBy,
		fmt.Sprintf("%s=%d", subprocEnvNow, nowMS),
		fmt.Sprintf("%s=%d", subprocEnvLease, leaseMS),
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func parseClaimedIDs(out string) []string {
	var ids []string
	for line := range strings.SplitSeq(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "CLAIMED "); ok {
			ids = append(ids, strings.TrimSpace(rest))
		}
	}
	return ids
}

func mustAtoi64(s string) int64 {
	var v int64
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		fmt.Fprintf(os.Stderr, "bad int %q: %v\n", s, err)
		os.Exit(2)
	}
	return v
}

// ----- shared helpers -----

type runningCronTurnFixture struct {
	jobID          string
	runID          string
	token          string
	turnID         string
	owner          string
	agentID        string
	now            time.Time
	leaseExpiresAt time.Time
}

func claimOneCronJob(t *testing.T, ctx context.Context, store Store, p ClaimDueJobsForUpdateParams, wantID, label string) Job {
	t.Helper()
	rows, err := store.ClaimDueJobsForUpdate(ctx, p)
	if err != nil {
		t.Fatalf("%s error: %v", label, err)
	}
	if len(rows) != 1 || rows[0].ID != wantID {
		t.Fatalf("%s rows = %+v, want job %s", label, rows, wantID)
	}
	return rows[0]
}

func claimNoCronJobs(t *testing.T, ctx context.Context, store Store, p ClaimDueJobsForUpdateParams, label string) {
	t.Helper()
	rows, err := store.ClaimDueJobsForUpdate(ctx, p)
	if err != nil {
		t.Fatalf("%s error: %v", label, err)
	}
	if len(rows) != 0 {
		t.Fatalf("%s rows = %+v, want none", label, rows)
	}
}

func assertCronJobClaimFields(t *testing.T, job Job, wantToken, wantOwner string) {
	t.Helper()
	if job.ClaimToken != wantToken || job.ClaimedBy != wantOwner {
		t.Fatalf("claim fields token=%q by=%q, want token=%q by=%q", job.ClaimToken, job.ClaimedBy, wantToken, wantOwner)
	}
}

func startRunningCronTurn(t *testing.T, ctx context.Context, store Store, f runningCronTurnFixture) Run {
	t.Helper()
	run, err := store.InsertRun(ctx, InsertRunParams{
		ID: f.runID, JobID: f.jobID, ScheduledAt: f.now, IdempotencyKey: f.runID + "-idempotency",
		DedupeKey: f.runID + "-dedupe", Status: StatusPending, CreatedAt: f.now, UpdatedAt: f.now,
	})
	if err != nil {
		t.Fatalf("insert %s: %v", f.runID, err)
	}
	claimOneCronJob(t, ctx, store, ClaimDueJobsForUpdateParams{
		Now: f.now, ClaimedBy: f.owner, LeaseExpiresAt: f.leaseExpiresAt, ClaimToken: f.token, MaxClaim: 1,
	}, f.jobID, f.owner+" claim")
	if err := store.SetRunTurn(ctx, SetRunTurnParams{ID: run.ID, TurnID: f.turnID, ThreadID: "thread-1", AgentID: f.agentID, SubmittedAt: f.now, UpdatedAt: f.now}); err != nil {
		t.Fatalf("set %s turn: %v", f.runID, err)
	}
	if err := store.CASRunStatus(ctx, CASRunStatusParams{ID: run.ID, ExpectedStatus: StatusPending, NextStatus: StatusRunning, UpdatedAt: f.now}); err != nil {
		t.Fatalf("%s running: %v", f.runID, err)
	}
	if err := store.SetActiveTurn(ctx, SetActiveTurnParams{ID: f.jobID, ClaimToken: f.token, ActiveTurnID: f.turnID, ThreadID: "thread-1", AgentID: f.agentID, Now: f.now}); err != nil {
		t.Fatalf("set %s active turn: %v", f.runID, err)
	}
	return run
}

func assertClaimStillActive(t *testing.T, ctx context.Context, store Store, jobID, token, turnID, owner string) {
	t.Helper()
	job, err := store.GetJobByID(ctx, jobID)
	if err != nil {
		t.Fatalf("get job after stale terminal: %v", err)
	}
	if job.ClaimToken != token || job.ActiveTurnID != turnID || job.ClaimedBy != owner {
		t.Fatalf("active claim token=%q turn=%q by=%q, want token=%q turn=%q by=%q",
			job.ClaimToken, job.ActiveTurnID, job.ClaimedBy, token, turnID, owner)
	}
}

func seedDueJobs(ctx context.Context, t *testing.T, store Store, n int, now time.Time) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := range n {
		id := fmt.Sprintf("job-%04d", i)
		_, err := store.CreateJob(ctx, CreateJobParams{
			ID:           id,
			Name:         id,
			Prompt:       "run",
			ScheduleType: "cron",
			ScheduleExpr: "0 * * * *",
			Provider:     ProviderCodex,
			CWD:          "/repo",
			Config:       json.RawMessage(`{}`),
			Skills:       json.RawMessage(`[]`),
			Enabled:      true,
			// due now: next_run_at <= now so the claim predicate matches.
			NextRunAt: now.Add(-time.Minute),
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("seed job %s: %v", id, err)
		}
		ids = append(ids, id)
	}
	return ids
}

func assertExactlyOnce(t *testing.T, want []string, got map[string]int) {
	t.Helper()
	var dups, missing []string
	for id, n := range got {
		if n > 1 {
			dups = append(dups, fmt.Sprintf("%s(x%d)", id, n))
		}
	}
	for _, id := range want {
		if got[id] == 0 {
			missing = append(missing, id)
		}
	}
	sort.Strings(dups)
	sort.Strings(missing)
	if len(dups) != 0 {
		t.Fatalf("duplicate claims (must be 0): %v", dups)
	}
	if len(missing) != 0 {
		t.Fatalf("missing claims (must be 0): %d of %d, e.g. %v", len(missing), len(want), missing)
	}
	if len(got) != len(want) {
		t.Fatalf("claimed %d distinct jobs, want %d", len(got), len(want))
	}
}

func openMigratedCronStore(t *testing.T) (Store, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "secure", "cron-claim.db")
	db := openMigratedCronDB(ctx, t, dbPath)
	return NewStoreWithDB(db, sqlc.New(db)), db
}

func openMigratedCronDB(ctx context.Context, t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.OpenTest(ctx, dbPath)
	if err != nil {
		t.Fatalf("open SQLite test DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunMigrations(ctx, db, sqliteMigrationsDir(t)); err != nil {
		t.Fatalf("run SQLite migrations: %v", err)
	}
	return db
}

// sqliteMigrationsDir walks up to the go.mod root and returns the canonical
// SQLite migrations directory used by the production runner.
func sqliteMigrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for range 8 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "internal", "platform", "db", "sqlite", "migrations")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("go.mod not found walking up from %s", file)
	return ""
}
