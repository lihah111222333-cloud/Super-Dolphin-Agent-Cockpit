package processobserve_test

import (
	"context"
	"os"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe"
)

func TestGhostObservationIsBoundedPairAndOperationSafe(t *testing.T) {
	store, observer := newObserver(t)
	snapshot := probeMustFail(t, os.Getpid()+1000000)
	decision := observeOne(t, observer, snapshot)
	assertGhostPair(t, decision)
	if snapshot.IdentityComplete() {
		t.Fatal("incomplete process probe unexpectedly claimed lifecycle identity")
	}
	second := observeOne(t, observer, snapshot)
	if second.EventID() != decision.EventID() {
		t.Fatalf("second observation changed bucket event id: first=%q second=%q", decision.EventID(), second.EventID())
	}
	if second.OperationID() == decision.OperationID() {
		t.Fatal("repeated observation reused operation ID")
	}
	stats := statsMust(t, store)
	if stats.BucketCount != 1 || stats.SignalCount != 0 {
		t.Fatalf("stats = %#v, want one no-signal bucket", stats)
	}
}

func TestOwnedProcessObservationStillBlocksWithoutOwner(t *testing.T) {
	store, observer := newObserver(t)
	snapshot, err := processprobe.Probe(context.Background(), os.Getpid())
	if err != nil {
		t.Fatalf("Probe(current pid) error = %v", err)
	}
	decision := observeOne(t, observer, snapshot)
	if decision.Reason() != processprobe.ReasonNoAuthoritativeOwner {
		t.Fatalf("reason = %q, want no_authoritative_owner", decision.Reason())
	}
	if decision.BlockedProjection().Reason() != processprobe.ReasonNoAuthoritativeOwner {
		t.Fatalf("blocked reason = %q, want no_authoritative_owner", decision.BlockedProjection().Reason())
	}
	if decision.BlockedProjection().SignalSent() || decision.CandidateProjection().SignalSent() {
		t.Fatal("observation projections authorized a signal")
	}
	if decision.LifecycleKey() != "" || decision.DedupKey() != "" || decision.BucketKey() == "" {
		t.Fatalf("identity keys = lifecycle=%q dedup=%q bucket=%q", decision.LifecycleKey(), decision.DedupKey(), decision.BucketKey())
	}
	_ = store
}

func TestGhostObservationFloodStaysBounded(t *testing.T) {
	store, observer := newObserver(t)
	snapshots := probeFlood(t)
	if _, err := observer.Observe(context.Background(), snapshots); err != nil {
		t.Fatalf("Observe(flood) error = %v", err)
	}
	assertBoundedStats(t, statsMust(t, store))
	decisions := listMust(t, store)
	if len(decisions) == 0 {
		t.Fatal("ListDecisions() returned no decisions")
	}
	overflow := findOverflow(decisions)
	if overflow == nil || overflow.DroppedCount() == 0 || overflow.SeenCount() == 0 || overflow.FirstSeen().IsZero() || overflow.LastSeen().IsZero() {
		t.Fatalf("overflow decision lacks bounded evidence: %#v", overflow)
	}
}

func TestGhostObservationFloodCompactsInMemory(t *testing.T) {
	store, observer := newObserver(t)
	snapshot := probeMustFail(t, 9_999_999)
	snapshots := make([]processprobe.Snapshot, 10_001)
	for i := range snapshots {
		snapshots[i] = snapshot
	}
	if _, err := observer.Observe(context.Background(), snapshots); err != nil {
		t.Fatalf("Observe(10001) error = %v", err)
	}
	decisions := listMust(t, store)
	if len(decisions) != 1 || decisions[0].SeenCount() != 10_001 {
		t.Fatalf("compacted decisions = %#v, want one decision with 10001 observations", decisions)
	}
	if decisions[0].SignalSent() || decisions[0].BlockedProjection().SignalSent() {
		t.Fatal("flood observation authorized a signal")
	}
}

func TestGhostPairPendingRetriesWithStableProjectionIDs(t *testing.T) {
	store := processobserve.NewMemoryStoreForTest()
	snapshot := probeMustFail(t, 9_999_998)
	store.InjectProjectionFailureOnceForTest()
	decision, err := store.RecordGhost(context.Background(), snapshot)
	if err == nil {
		t.Fatal("RecordGhost() error = nil after injected projection failure")
	}
	if decision.Status() != processobserve.DecisionPairPending {
		t.Fatalf("decision status = %q, want pair_pending", decision.Status())
	}
	candidateID := decision.CandidateProjection().ID()
	blockedID := decision.BlockedProjection().ID()
	if _, err := store.RetryPending(context.Background()); err != nil {
		t.Fatalf("RetryPending() error = %v", err)
	}
	retried := listMust(t, store)
	if len(retried) != 1 || retried[0].Status() != processobserve.DecisionPersisted {
		t.Fatalf("retried decisions = %#v, want persisted one", retried)
	}
	if retried[0].CandidateProjection().ID() != candidateID || retried[0].BlockedProjection().ID() != blockedID {
		t.Fatal("retry changed projection IDs")
	}
	if !retried[0].CandidateProjection().Acked() || !retried[0].BlockedProjection().Acked() {
		t.Fatal("retry did not acknowledge both projections")
	}
}

func newObserver(t *testing.T) (*processobserve.Store, *processobserve.Observer) {
	t.Helper()
	store := processobserve.NewMemoryStoreForTest()
	observer, err := processobserve.NewObserver(store)
	if err != nil {
		t.Fatalf("NewObserver() error = %v", err)
	}
	return store, observer
}

func probeMustFail(t *testing.T, pid int) processprobe.Snapshot {
	t.Helper()
	snapshot, err := processprobe.Probe(context.Background(), pid)
	if err == nil {
		t.Fatalf("Probe(%d) error = nil", pid)
	}
	return snapshot
}

func observeOne(t *testing.T, observer *processobserve.Observer, snapshot processprobe.Snapshot) processobserve.Decision {
	t.Helper()
	decisions, err := observer.Observe(context.Background(), []processprobe.Snapshot{snapshot})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("Observe() decisions = %d, want 1", len(decisions))
	}
	return decisions[0]
}

func assertGhostPair(t *testing.T, decision processobserve.Decision) {
	t.Helper()
	if decision.SignalSent() || decision.Status() != processobserve.DecisionPersisted {
		t.Fatalf("decision = %#v, want persisted no-signal pair", decision)
	}
	if decision.CandidateProjection().Event() != "lsp_ghost_candidate_observed" || decision.BlockedProjection().Event() != "lsp_reclaim_blocked" {
		t.Fatalf("projection events = %q/%q", decision.CandidateProjection().Event(), decision.BlockedProjection().Event())
	}
	if decision.BlockedProjection().Reason() != processprobe.ReasonProbeFailed && decision.BlockedProjection().Reason() != processprobe.ReasonUnknown {
		t.Fatalf("blocked reason = %q", decision.BlockedProjection().Reason())
	}
	if decision.CandidateProjection().ID() == decision.BlockedProjection().ID() {
		t.Fatal("candidate and blocked projection IDs are not distinct")
	}
}

func probeFlood(t *testing.T) []processprobe.Snapshot {
	t.Helper()
	snapshots := make([]processprobe.Snapshot, 0, processobserve.MaxObservationBuckets+128)
	for pid := 1_000_000; pid < 1_000_000+processobserve.MaxObservationBuckets+128; pid++ {
		snapshots = append(snapshots, probeMustFail(t, pid))
	}
	return snapshots
}

func statsMust(t *testing.T, store *processobserve.Store) processobserve.Stats {
	t.Helper()
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	return stats
}

func listMust(t *testing.T, store *processobserve.Store) []processobserve.Decision {
	t.Helper()
	decisions, err := store.ListDecisions(context.Background())
	if err != nil {
		t.Fatalf("ListDecisions() error = %v", err)
	}
	return decisions
}

func assertBoundedStats(t *testing.T, stats processobserve.Stats) {
	t.Helper()
	if stats.BucketCount != processobserve.MaxObservationBuckets {
		t.Fatalf("bucket count = %d, want %d", stats.BucketCount, processobserve.MaxObservationBuckets)
	}
	if stats.TotalBytes > processobserve.MaxObservationBytes {
		t.Fatalf("total bytes = %d, exceeds %d", stats.TotalBytes, processobserve.MaxObservationBytes)
	}
	if stats.SignalCount != 0 {
		t.Fatalf("signal count = %d, want 0", stats.SignalCount)
	}
}

func findOverflow(decisions []processobserve.Decision) *processobserve.Decision {
	for index := range decisions {
		if decisions[index].DroppedCount() > 0 {
			return &decisions[index]
		}
	}
	return nil
}
