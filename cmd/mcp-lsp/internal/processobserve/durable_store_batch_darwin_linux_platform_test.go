//go:build darwin || linux

package processobserve_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe"
	"golang.org/x/sync/errgroup"
)

func TestDurableStoreBatchMatchesSequentialPersistence(t *testing.T) {
	snapshot := probeMustFail(t, 3_100_001)
	snapshots := []processprobe.Snapshot{snapshot, snapshot, probeMustFail(t, 3_100_002)}
	sequential := openDurableTestStore(t, canonicalTempRoot(t))
	defer sequential.Close()
	sequentialReturned := make([]processobserve.Decision, 0, len(snapshots))
	for _, item := range snapshots {
		decision, err := sequential.RecordGhost(context.Background(), item)
		if err != nil {
			t.Fatalf("sequential RecordGhost() error = %v", err)
		}
		sequentialReturned = append(sequentialReturned, decision)
	}
	sequentialDecisions, err := sequential.ListDecisions(context.Background())
	if err != nil {
		t.Fatalf("sequential ListDecisions() error = %v", err)
	}

	batched := openDurableTestStore(t, canonicalTempRoot(t))
	defer batched.Close()
	returned, err := batched.RecordGhostBatch(context.Background(), snapshots)
	if err != nil {
		t.Fatalf("batch RecordGhostBatch() error = %v", err)
	}
	if len(returned) != len(snapshots) {
		t.Fatalf("batch decisions = %d, want %d", len(returned), len(snapshots))
	}
	batchedDecisions, err := batched.ListDecisions(context.Background())
	if err != nil {
		t.Fatalf("batch ListDecisions() error = %v", err)
	}
	if len(batchedDecisions) != len(sequentialDecisions) {
		t.Fatalf("batch persisted decisions = %d, sequential = %d", len(batchedDecisions), len(sequentialDecisions))
	}
	for index := range returned {
		assertBatchDecisionEquivalent(t, sequentialReturned[index], returned[index])
	}
	assertBatchDecisionSetsEquivalent(t, sequentialDecisions, batchedDecisions)
}

func TestDurableStoreBatchCancellationReturnsCompletedPrefix(t *testing.T) {
	store := openDurableTestStore(t, canonicalTempRoot(t))
	defer store.Close()
	snapshots := []processprobe.Snapshot{probeMustFail(t, 3_100_003), probeMustFail(t, 3_100_004)}
	ctx := &cancelAfterChecks{Context: context.Background(), cancelAt: 4}
	decisions, err := store.RecordGhostBatch(ctx, snapshots)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RecordGhostBatch() error = %v, want context cancellation", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("completed decisions = %d, want one prefix decision", len(decisions))
	}
	persisted, err := store.ListDecisions(context.Background())
	if err != nil {
		t.Fatalf("ListDecisions() error = %v", err)
	}
	if len(persisted) != 1 || persisted[0].Status() != processobserve.DecisionPersisted {
		t.Fatalf("persisted decisions = %#v, want one persisted prefix", persisted)
	}
}

func TestDurableStoreBatchProjectionFailureLeavesPendingForRetry(t *testing.T) {
	store := openDurableTestStore(t, canonicalTempRoot(t))
	defer store.Close()
	store.InjectProjectionFailureOnceForTest()
	snapshots := []processprobe.Snapshot{probeMustFail(t, 3_100_005), probeMustFail(t, 3_100_006)}
	decisions, err := store.RecordGhostBatch(context.Background(), snapshots)
	if err == nil || len(decisions) != 0 {
		t.Fatalf("RecordGhostBatch() decisions=%d err=%v, want no returned prefix on first failure", len(decisions), err)
	}
	pending, err := store.ListDecisions(context.Background())
	if err != nil {
		t.Fatalf("ListDecisions() error = %v", err)
	}
	if len(pending) != 1 || pending[0].Status() != processobserve.DecisionPairPending {
		t.Fatalf("pending decisions = %#v, want one pair_pending record", pending)
	}
	if _, err := store.RetryPending(context.Background()); err != nil {
		t.Fatalf("RetryPending() error = %v", err)
	}
	retried, err := store.ListDecisions(context.Background())
	if err != nil {
		t.Fatalf("ListDecisions() after retry error = %v", err)
	}
	if len(retried) != 1 || retried[0].Status() != processobserve.DecisionPersisted {
		t.Fatalf("retried decisions = %#v, want one persisted record", retried)
	}
}

func TestDurableStoreBatchCloseConcurrentIsRaceSafe(t *testing.T) {
	store := openDurableTestStore(t, canonicalTempRoot(t))
	defer store.Close()
	snapshots := make([]processprobe.Snapshot, 4)
	for index := range snapshots {
		snapshots[index] = probeMustFail(t, 3_100_100+index)
	}
	ctx := newBlockingBatchContext()
	var group errgroup.Group
	group.Go(func() error {
		_, err := store.RecordGhostBatch(ctx, snapshots)
		return err
	})
	<-ctx.entered
	closeStarted := make(chan struct{})
	group.Go(func() error {
		close(closeStarted)
		return store.Close()
	})
	<-closeStarted
	close(ctx.release)
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent batch/close error = %v", err)
	}
}

func TestRecordGhostBatchEmptyClosedSemantics(t *testing.T) {
	durable := openDurableTestStore(t, canonicalTempRoot(t))
	if err := durable.Close(); err != nil {
		t.Fatalf("durable Close() error = %v", err)
	}
	decisions, err := durable.RecordGhostBatch(context.Background(), nil)
	if err != nil || decisions == nil || len(decisions) != 0 {
		t.Fatalf("closed durable empty batch decisions=%#v err=%v, want non-nil empty success", decisions, err)
	}

	memory := processobserve.NewMemoryStoreForTest()
	if err := memory.Close(); err != nil {
		t.Fatalf("memory Close() error = %v", err)
	}
	decisions, err = memory.RecordGhostBatch(context.Background(), nil)
	if !errors.Is(err, processobserve.ErrDurableStoreClosed) || decisions != nil {
		t.Fatalf("closed memory empty batch decisions=%#v err=%v, want nil and closed error", decisions, err)
	}
}

func assertBatchDecisionEquivalent(t *testing.T, left processobserve.Decision, right processobserve.Decision) {
	t.Helper()
	if left.BucketKey() != right.BucketKey() || left.Reason() != right.Reason() || left.Status() != right.Status() || left.SeenCount() != right.SeenCount() || left.DroppedCount() != right.DroppedCount() {
		t.Fatalf("decisions differ: left=%#v right=%#v", left, right)
	}
	if left.CandidateProjection().Event() != right.CandidateProjection().Event() || left.BlockedProjection().Event() != right.BlockedProjection().Event() || left.CandidateProjection().Acked() != right.CandidateProjection().Acked() || left.BlockedProjection().Acked() != right.BlockedProjection().Acked() {
		t.Fatalf("projection decisions differ: left=%#v right=%#v", left, right)
	}
}

func assertBatchDecisionSetsEquivalent(t *testing.T, left []processobserve.Decision, right []processobserve.Decision) {
	t.Helper()
	byBucket := make(map[string]processobserve.Decision, len(right))
	for _, decision := range right {
		byBucket[decision.BucketKey()] = decision
	}
	for _, decision := range left {
		match, ok := byBucket[decision.BucketKey()]
		if !ok {
			t.Fatalf("missing batch decision for bucket %q", decision.BucketKey())
		}
		assertBatchDecisionEquivalent(t, decision, match)
	}
}

type cancelAfterChecks struct {
	context.Context
	cancelAt int32
	checks   atomic.Int32
}

type blockingBatchContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
	checks  atomic.Int32
}

func newBlockingBatchContext() *blockingBatchContext {
	return &blockingBatchContext{Context: context.Background(), entered: make(chan struct{}), release: make(chan struct{})}
}

func (c *blockingBatchContext) Err() error {
	if c.checks.Add(1) == 2 {
		close(c.entered)
		<-c.release
	}
	return nil
}

func (c *cancelAfterChecks) Err() error {
	if c.checks.Add(1) >= c.cancelAt {
		return context.Canceled
	}
	return nil
}
