package gate

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestRemoteBaselineRefreshLeaseConcurrentAcquireOnlyOneWins(t *testing.T) {
	store := newRefreshLeaseTestStore(t)
	request := refreshLeaseRequest()
	var leases [2]RemoteBaselineRefreshLease
	var results [2]error
	var group sync.WaitGroup
	for index := range leases {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			leases[index], results[index] = store.AcquireRemoteBaselineRefreshLease(request)
		}(index)
	}
	group.Wait()
	wins := 0
	for _, result := range results {
		if result == nil {
			wins++
		} else if !errors.Is(result, ErrRemoteBaselineRefreshBusy) {
			t.Fatalf("AcquireRemoteBaselineRefreshLease() error = %v", result)
		}
	}
	if wins != 1 {
		t.Fatalf("successful concurrent leases = %d, want 1", wins)
	}
}

func TestRemoteBaselineRefreshLeaseThrottlesNewAttemptForTwoHours(t *testing.T) {
	store := newRefreshLeaseTestStore(t)
	now := time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }
	first, err := store.AcquireRemoteBaselineRefreshLease(refreshLeaseRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailRemoteBaselineRefreshLease(first, "candidate", "imc-1", "cloud failed"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(cicontract.RefreshMinimumInterval - time.Millisecond)
	if _, err := store.AcquireRemoteBaselineRefreshLease(refreshLeaseRequest()); !errors.Is(err, ErrRemoteBaselineRefreshThrottled) {
		t.Fatalf("acquire before interval error = %v, want throttled", err)
	}
	now = now.Add(time.Millisecond)
	second, err := store.AcquireRemoteBaselineRefreshLease(refreshLeaseRequest())
	if err != nil {
		t.Fatal(err)
	}
	if second.AttemptGeneration != first.AttemptGeneration+1 {
		t.Fatalf("attempt generation = %d, want %d", second.AttemptGeneration, first.AttemptGeneration+1)
	}
}

func TestRemoteBaselineRefreshLeaseExpiredWorkerIsTakenOverWithoutNewAttempt(t *testing.T) {
	store := newRefreshLeaseTestStore(t)
	now := time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }
	request := refreshLeaseRequest()
	request.LeaseDuration = time.Minute
	first, err := store.AcquireRemoteBaselineRefreshLease(request)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	takenOver, err := store.AcquireRemoteBaselineRefreshLease(request)
	if err != nil {
		t.Fatal(err)
	}
	if takenOver.AttemptGeneration != first.AttemptGeneration || takenOver.Token == first.Token {
		t.Fatalf("takeover lease = %#v, first = %#v", takenOver, first)
	}
}

func TestRemoteBaselineRefreshLeaseResumeByToken(t *testing.T) {
	store := newRefreshLeaseTestStore(t)
	lease, err := store.AcquireRemoteBaselineRefreshLease(refreshLeaseRequest())
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := store.ResumeRemoteBaselineRefreshLease(lease.Token)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.AttemptGeneration != lease.AttemptGeneration || resumed.Token != lease.Token {
		t.Fatalf("resumed lease = %#v", resumed)
	}
}

func TestRemoteBaselineRefreshLeaseBindsBuilderIdentityExactlyOnce(t *testing.T) {
	store := newRefreshLeaseTestStore(t)
	lease, err := store.AcquireRemoteBaselineRefreshLease(refreshLeaseRequest())
	if err != nil {
		t.Fatal(err)
	}
	const targetTree = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bound, err := store.BindRemoteBaselineRefreshLeaseBuilder(lease, "oci-builder-job", targetTree)
	if err != nil {
		t.Fatal(err)
	}
	if bound.BuilderJobID != "oci-builder-job" || bound.TargetTreeSHA != targetTree {
		t.Fatalf("bound lease = %#v", bound)
	}
	if _, err := store.BindRemoteBaselineRefreshLeaseBuilder(bound, "oci-builder-job", targetTree); err != nil {
		t.Fatalf("exact builder binding replay error = %v", err)
	}
	if _, err := store.BindRemoteBaselineRefreshLeaseBuilder(bound, "other-job", targetTree); !errors.Is(err, ErrRemoteBaselineRefreshLeaseLost) {
		t.Fatalf("conflicting builder binding error = %v, want lease lost", err)
	}
}

func TestRemoteBaselineRefreshLeaseRejectsStaleTokenAndAcceptedCASDrift(t *testing.T) {
	store := newRefreshLeaseTestStore(t)
	now := time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }
	request := refreshLeaseRequest()
	request.LeaseDuration = time.Minute
	first, err := store.AcquireRemoteBaselineRefreshLease(request)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := store.AcquireRemoteBaselineRefreshLease(request); err != nil {
		t.Fatal(err)
	}
	if _, err := store.HeartbeatRemoteBaselineRefreshLease(first, time.Minute, "candidate", "imc-1"); !errors.Is(err, ErrRemoteBaselineRefreshLeaseLost) {
		t.Fatalf("stale heartbeat error = %v, want lease lost", err)
	}

	store = newRefreshLeaseTestStore(t)
	store.nowFunc = func() time.Time { return now }
	lease, err := store.AcquireRemoteBaselineRefreshLease(refreshLeaseRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []cicontract.RefreshPhase{cicontract.RefreshBuilding, cicontract.RefreshCachePreparing, cicontract.RefreshReadyValidated} {
		lease, err = store.AdvanceRemoteBaselineRefreshLease(lease, state)
		if err != nil {
			t.Fatal(err)
		}
	}
	seedRemoteBaselineState(t, store, RemoteBaselineStateRecord{Generation: 2, StateJSON: []byte(`{"state":"changed"}`), StateSHA256: "sha256:changed"})
	err = store.PromoteRemoteBaselineStateWithRefreshLease(lease, RemoteBaselineStateRecord{Generation: 2, StateJSON: []byte(`{"state":"successor"}`), StateSHA256: "sha256:successor"}, "candidate", "imc-2")
	if !errors.Is(err, ErrRemoteBaselineRefreshAcceptedStateChanged) {
		t.Fatalf("stale accepted CAS promotion error = %v", err)
	}
}

func TestRemoteBaselineRefreshLeaseSQLiteSchemaGuard(t *testing.T) {
	store := newRefreshLeaseTestStore(t)
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	columns := sqliteTableColumns(t, database, "ci_remote_baseline_refresh_lease")
	for _, column := range []string{"attempt_generation", "accepted_generation", "accepted_state_sha256", "target_generation", "token", "builder_job_id", "target_tree_sha", "phase", "lease_expires_at_unix_ms", "image_cache_name", "image_cache_id", "successor_generation", "successor_state_sha256", "retiring_image_cache_id"} {
		if !containsSQLiteColumn(columns, column) {
			t.Fatalf("refresh lease schema misses %q: %v", column, columns)
		}
	}
	var ddl string
	if err := database.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='ci_remote_baseline_refresh_lease'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "'unchanged'") {
		t.Fatalf("refresh lease phase CHECK omits unchanged: %s", ddl)
	}
}

func TestRemoteBaselineRefreshLeasePromotesOnlyReadyValidatedSuccessor(t *testing.T) {
	store := newRefreshLeaseTestStore(t)
	lease, err := store.AcquireRemoteBaselineRefreshLease(refreshLeaseRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []cicontract.RefreshPhase{cicontract.RefreshBuilding, cicontract.RefreshCachePreparing, cicontract.RefreshReadyValidated} {
		lease, err = store.AdvanceRemoteBaselineRefreshLease(lease, state)
		if err != nil {
			t.Fatal(err)
		}
	}
	successor := RemoteBaselineStateRecord{Generation: lease.TargetGeneration, StateJSON: []byte(`{"image_cache_id":"imc-successor"}`), StateSHA256: "sha256:successor"}
	if err := store.PromoteRemoteBaselineStateWithRefreshLease(lease, successor, "candidate", "imc-successor"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRemoteBaselineState()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Generation != successor.Generation || loaded.StateSHA256 != successor.StateSHA256 {
		t.Fatalf("accepted successor = %#v", loaded)
	}
	lease.Phase = cicontract.RefreshPromoted
	for _, phase := range []cicontract.RefreshPhase{cicontract.RefreshRetiring, cicontract.RefreshCleanupPending, cicontract.RefreshIdle} {
		lease, err = store.AdvanceRemoteBaselineRefreshLease(lease, phase)
		if err != nil {
			t.Fatal(err)
		}
		if lease.Phase != phase {
			t.Fatalf("refresh phase = %q, want %q", lease.Phase, phase)
		}
	}
}

func TestRemoteBaselineRefreshCleanupRetryAndAcceptedCacheGuard(t *testing.T) {
	store := newRefreshLeaseTestStore(t)
	lease := promoteRefreshLeaseForCleanup(t, store)
	claimed, err := store.ClaimRemoteBaselineRefreshCleanup(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.RetiringImageCacheID != "imc-accepted" || claimed.SuccessorGeneration != lease.TargetGeneration {
		t.Fatalf("cleanup claim = %#v", claimed)
	}
	if err := store.CompleteRemoteBaselineRefreshCleanup(claimed, errors.New("delete unavailable")); err != nil {
		t.Fatal(err)
	}
	retry, err := store.ClaimRemoteBaselineRefreshCleanup(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Token == claimed.Token {
		t.Fatal("cleanup retry reused stale token")
	}
	if err := store.CompleteRemoteBaselineRefreshCleanup(retry, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRemoteBaselineState()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Generation != lease.TargetGeneration {
		t.Fatalf("cleanup changed accepted state: %#v", loaded)
	}
}

func TestRemoteBaselineRefreshCleanupConcurrentClaimOnlyOneWins(t *testing.T) {
	store := newRefreshLeaseTestStore(t)
	promoteRefreshLeaseForCleanup(t, store)
	var claims [2]RemoteBaselineRefreshLease
	var results [2]error
	var group sync.WaitGroup
	for index := range claims {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			claims[index], results[index] = store.ClaimRemoteBaselineRefreshCleanup(time.Minute)
		}(index)
	}
	group.Wait()
	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrRemoteBaselineRefreshBusy) {
			t.Fatalf("cleanup claim error = %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("successful concurrent cleanup claims = %d, want 1", wins)
	}
}

func TestRemoteBaselineRefreshCleanupRejectsStaleTokenAndAcceptedDrift(t *testing.T) {
	store := newRefreshLeaseTestStore(t)
	lease := promoteRefreshLeaseForCleanup(t, store)
	claimed, err := store.ClaimRemoteBaselineRefreshCleanup(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	stale := claimed
	stale.Token = "stale"
	if err := store.CompleteRemoteBaselineRefreshCleanup(stale, nil); !errors.Is(err, ErrRemoteBaselineRefreshLeaseLost) {
		t.Fatalf("stale cleanup token error = %v", err)
	}
	seedRemoteBaselineState(t, store, RemoteBaselineStateRecord{Generation: lease.TargetGeneration + 1, StateJSON: []byte(`{"image_cache_id":"imc-new"}`), StateSHA256: "sha256:new"})
	if err := store.CompleteRemoteBaselineRefreshCleanup(claimed, nil); !errors.Is(err, ErrRemoteBaselineRefreshAcceptedStateChanged) {
		t.Fatalf("cleanup accepted drift error = %v", err)
	}
}

func TestRemoteBaselineRefreshCleanupRejectsExpiredToken(t *testing.T) {
	store := newRefreshLeaseTestStore(t)
	now := time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }
	promoteRefreshLeaseForCleanup(t, store)
	claimed, err := store.ClaimRemoteBaselineRefreshCleanup(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := store.CompleteRemoteBaselineRefreshCleanup(claimed, nil); !errors.Is(err, ErrRemoteBaselineRefreshLeaseLost) {
		t.Fatalf("expired cleanup token error = %v", err)
	}
}

func promoteRefreshLeaseForCleanup(t *testing.T, store *DurationLedgerStore) RemoteBaselineRefreshLease {
	t.Helper()
	lease, err := store.AcquireRemoteBaselineRefreshLease(refreshLeaseRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []cicontract.RefreshPhase{cicontract.RefreshBuilding, cicontract.RefreshCachePreparing, cicontract.RefreshReadyValidated} {
		lease, err = store.AdvanceRemoteBaselineRefreshLease(lease, phase)
		if err != nil {
			t.Fatal(err)
		}
	}
	successor := RemoteBaselineStateRecord{Generation: lease.TargetGeneration, StateJSON: []byte(`{"image_cache_id":"imc-successor"}`), StateSHA256: "sha256:successor"}
	if err := store.PromoteRemoteBaselineStateWithRefreshLease(lease, successor, "candidate", "imc-successor"); err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestRemoteBaselineRefreshLeaseFailureMatrixUsesContractPhases(t *testing.T) {
	for _, phase := range []cicontract.RefreshPhase{cicontract.RefreshClaimed, cicontract.RefreshBuilding, cicontract.RefreshCachePreparing, cicontract.RefreshReadyValidated} {
		t.Run(string(phase), func(t *testing.T) {
			store := newRefreshLeaseTestStore(t)
			lease, err := store.AcquireRemoteBaselineRefreshLease(refreshLeaseRequest())
			if err != nil {
				t.Fatal(err)
			}
			for _, next := range []cicontract.RefreshPhase{cicontract.RefreshBuilding, cicontract.RefreshCachePreparing, cicontract.RefreshReadyValidated} {
				if lease.Phase == phase {
					break
				}
				lease, err = store.AdvanceRemoteBaselineRefreshLease(lease, next)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := store.FailRemoteBaselineRefreshLease(lease, "candidate", "imc-failed", "candidate failure"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func newRefreshLeaseTestStore(t *testing.T) *DurationLedgerStore {
	t.Helper()
	store, err := NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	seedRemoteBaselineState(t, store, RemoteBaselineStateRecord{Generation: 1, StateJSON: []byte(`{"image_cache_id":"imc-accepted"}`), StateSHA256: "sha256:accepted"})
	return store
}

func refreshLeaseRequest() RemoteBaselineRefreshLeaseRequest {
	return RemoteBaselineRefreshLeaseRequest{AcceptedGeneration: 1, AcceptedStateSHA256: "sha256:accepted", LeaseDuration: 10 * time.Minute}
}
func containsSQLiteColumn(columns []string, wanted string) bool {
	return slices.Contains(columns, wanted)
}
