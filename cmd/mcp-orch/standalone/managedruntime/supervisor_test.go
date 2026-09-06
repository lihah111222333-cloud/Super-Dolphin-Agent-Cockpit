package managedruntime

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestCandidateSupervisorRequiresExplicitPrerequisites(t *testing.T) {
	if owner, err := NewCandidateSupervisor(ProductionPrerequisites{}); owner != nil || !errors.Is(err, ErrProductionNoGo) {
		t.Fatalf("missing prerequisites = (%v, %v), want nil ErrProductionNoGo", owner, err)
	}
	prerequisites := validProductionPrerequisites()
	owner, err := NewCandidateSupervisor(prerequisites)
	if err != nil {
		t.Fatalf("NewCandidateSupervisor() error = %v", err)
	}
	if _, ok := any(owner).(Supervisor); !ok {
		t.Fatal("CandidateSupervisor does not implement Supervisor")
	}
	if memory, err := NewMemorySupervisor(prerequisites); err != nil || memory == nil {
		t.Fatalf("NewMemorySupervisor() = (%v, %v), want initialized owner", memory, err)
	}
}

func TestCandidateSupervisorPrepareConsumesOnlySeededReceipt(t *testing.T) {
	owner := newCandidateSupervisor(t)
	request := validPrepareRequest(t, "prepare-seed")
	receipt := validPreparedReceipt(t, request, "prepare-seed")
	if _, err := owner.Prepare(context.Background(), request); !errors.Is(err, ErrPreparedReceiptUnavailable) {
		t.Fatalf("Prepare() without seed = %v, want ErrPreparedReceiptUnavailable", err)
	}
	assertCandidateState(t, owner, request, StateAbsent)
	if err := owner.SeedPrepared(context.Background(), request, receipt); err != nil {
		t.Fatalf("SeedPrepared() error = %v", err)
	}
	if err := owner.SeedPrepared(context.Background(), request, receipt); err != nil {
		t.Fatalf("same SeedPrepared() replay error = %v", err)
	}
	first, err := owner.Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("first Prepare() error = %v", err)
	}
	second, err := owner.Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("second Prepare() error = %v", err)
	}
	if !reflect.DeepEqual(first, receipt) || !reflect.DeepEqual(second, receipt) || first != second {
		t.Fatalf("Prepare() replay = (%+v, %+v), want exact receipt %+v", first, second, receipt)
	}
	assertCandidateState(t, owner, request, StatePrepared)
}

func TestCandidateSupervisorCommitAndAbortAreSingleWinnerAndIdempotent(t *testing.T) {
	t.Run("commit", testCandidateCommitReplay)
	t.Run("abort", testCandidateAbortReplay)
}

// testCandidateCommitReplay 锁定 commit 的 exact replay、digest 冲突和终态拒绝。
func testCandidateCommitReplay(t *testing.T) {
	owner, request, prepared := seededCandidate(t, "commit")
	commit := validCommitRequest(t, prepared, "commit")
	first, err := owner.Commit(context.Background(), commit)
	if err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}
	replay, err := owner.Commit(context.Background(), commit)
	if err != nil || replay != first {
		t.Fatalf("Commit() replay = (%+v, %v), want exact first receipt", replay, err)
	}
	changed := commit
	changed.ActiveBindingDigest = testDigest("commit-foreign-binding")
	changed = mustFinalizeCommit(t, changed)
	if _, err := owner.Commit(context.Background(), changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("same commit operation with changed digest = %v, want ErrOperationConflict", err)
	}
	assertCandidateState(t, owner, request, StateCommitted)
	abort := validAbortRequest(t, prepared, "commit-after")
	if _, err := owner.Abort(context.Background(), abort); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Abort() after commit = %v, want ErrInvalidTransition", err)
	}
}

// testCandidateAbortReplay 锁定 abort 的 exact replay、digest 冲突和终态拒绝。
func testCandidateAbortReplay(t *testing.T) {
	owner, request, prepared := seededCandidate(t, "abort")
	abort := validAbortRequest(t, prepared, "abort")
	first, err := owner.Abort(context.Background(), abort)
	if err != nil {
		t.Fatalf("first Abort() error = %v", err)
	}
	replay, err := owner.Abort(context.Background(), abort)
	if err != nil || replay != first {
		t.Fatalf("Abort() replay = (%+v, %v), want exact first receipt", replay, err)
	}
	changed := abort
	changed.CleanupEvidenceDigest = testDigest("abort-foreign-evidence")
	changed = mustFinalizeAbort(t, changed)
	if _, err := owner.Abort(context.Background(), changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("same abort operation with changed digest = %v, want ErrOperationConflict", err)
	}
	assertCandidateState(t, owner, request, StateAborted)
	commit := validCommitRequest(t, prepared, "abort-after")
	if _, err := owner.Commit(context.Background(), commit); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Commit() after abort = %v, want ErrInvalidTransition", err)
	}
}

func TestCandidateSupervisorUnknownInstallsStickyQueryOnlyFence(t *testing.T) {
	owner, request, prepared := seededCandidate(t, "unknown")
	query := QueryRequest{SchemaVersion: SchemaVersion, OperationID: request.OperationID, OperationDigest: request.RequestDigest}
	markCandidateUnknown(t, owner, query)
	assertCandidateUnknownQuery(t, owner, query)
	assertCandidateMutationFence(t, owner, request, prepared)
	foreign := query
	foreign.OperationDigest = testDigest("foreign-query")
	if _, err := owner.Query(context.Background(), foreign); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("foreign unknown query = %v, want ErrOperationConflict", err)
	}
}

// markCandidateUnknown 安装 fault-injection unknown marker 并检查 sticky fence。
func markCandidateUnknown(t *testing.T, owner *CandidateSupervisor, query QueryRequest) {
	t.Helper()
	if err := owner.MarkOutcomeUnknown(context.Background(), query, "candidate://managed-runtime/unknown/unknown", testDigest("unknown-evidence")); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("MarkOutcomeUnknown() error = %v, want ErrOutcomeUnknown", err)
	}
	if !owner.IsFenced() {
		t.Fatal("unknown outcome did not install sticky fence")
	}
}

// assertCandidateUnknownQuery 检查 unknown projection 及其 exact query replay。
func assertCandidateUnknownQuery(t *testing.T, owner *CandidateSupervisor, query QueryRequest) {
	t.Helper()
	result, err := owner.Query(context.Background(), query)
	if err != nil {
		t.Fatalf("Query(unknown) error = %v", err)
	}
	if result.State != StateOutcomeUnknown || result.UncertaintyReceiptRef == "" || result.UncertaintyDigest == "" {
		t.Fatalf("unknown QueryResult = %+v", result)
	}
	replay, err := owner.Query(context.Background(), query)
	if err != nil || replay != result {
		t.Fatalf("Query(unknown) replay = (%+v, %v), want exact result", replay, err)
	}
}

// assertCandidateMutationFence 确认 unknown 后全部 mutation 都 fail-closed。
func assertCandidateMutationFence(t *testing.T, owner *CandidateSupervisor, request PrepareRequest, prepared PreparedReceipt) {
	t.Helper()
	commit := validCommitRequest(t, prepared, "unknown-commit")
	abort := validAbortRequest(t, prepared, "unknown-abort")
	if _, err := owner.Commit(context.Background(), commit); !errors.Is(err, ErrSupervisorFenced) {
		t.Fatalf("Commit() after unknown = %v, want ErrSupervisorFenced", err)
	}
	if _, err := owner.Abort(context.Background(), abort); !errors.Is(err, ErrSupervisorFenced) {
		t.Fatalf("Abort() after unknown = %v, want ErrSupervisorFenced", err)
	}
	if _, err := owner.Prepare(context.Background(), request); !errors.Is(err, ErrSupervisorFenced) {
		t.Fatalf("Prepare() after unknown = %v, want ErrSupervisorFenced", err)
	}
	if err := owner.SeedPrepared(context.Background(), request, prepared); !errors.Is(err, ErrSupervisorFenced) {
		t.Fatalf("SeedPrepared() after unknown = %v, want ErrSupervisorFenced", err)
	}
}

func TestCandidateSupervisorRejectsNilAndCancelledContextWithoutMutation(t *testing.T) {
	var nilOwner *CandidateSupervisor
	request := validPrepareRequest(t, "context")
	if _, err := nilOwner.Prepare(context.Background(), request); !errors.Is(err, ErrSupervisorUnavailable) {
		t.Fatalf("nil owner Prepare() = %v, want ErrSupervisorUnavailable", err)
	}
	owner := newCandidateSupervisor(t)
	var missingContext context.Context
	if _, err := owner.Prepare(missingContext, request); !errors.Is(err, ErrInvalidSupervisorContext) {
		t.Fatalf("nil context Prepare() = %v, want ErrInvalidSupervisorContext", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	receipt := validPreparedReceipt(t, request, "context")
	if err := owner.SeedPrepared(ctx, request, receipt); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled SeedPrepared() = %v, want context.Canceled", err)
	}
	assertCandidateState(t, owner, request, StateAbsent)
}

func TestCandidateSupervisorConcurrentCommitReplayIsRaceSafe(t *testing.T) {
	owner, _, prepared := seededCandidate(t, "parallel")
	commit := validCommitRequest(t, prepared, "parallel")
	const callers = 24
	results := make([]ClosedReceipt, callers)
	errorsByCall := make([]error, callers)
	var wait sync.WaitGroup
	for index := range callers {
		wait.Go(func() {
			results[index], errorsByCall[index] = owner.Commit(context.Background(), commit)
		})
	}
	wait.Wait()
	for index, err := range errorsByCall {
		if err != nil {
			t.Fatalf("Commit() caller %d error = %v", index, err)
		}
		if results[index] != results[0] {
			t.Fatalf("Commit() caller %d receipt differs from winner", index)
		}
	}
}

func newCandidateSupervisor(t *testing.T) *CandidateSupervisor {
	t.Helper()
	owner, err := NewCandidateSupervisor(validProductionPrerequisites())
	if err != nil {
		t.Fatalf("NewCandidateSupervisor() error = %v", err)
	}
	return owner
}

func seededCandidate(t *testing.T, suffix string) (*CandidateSupervisor, PrepareRequest, PreparedReceipt) {
	t.Helper()
	owner := newCandidateSupervisor(t)
	request := validPrepareRequest(t, suffix)
	receipt := validPreparedReceipt(t, request, suffix)
	if err := owner.SeedPrepared(context.Background(), request, receipt); err != nil {
		t.Fatalf("SeedPrepared() error = %v", err)
	}
	if _, err := owner.Prepare(context.Background(), request); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	return owner, request, receipt
}

func assertCandidateState(t *testing.T, owner *CandidateSupervisor, request PrepareRequest, state State) {
	t.Helper()
	result, err := owner.Query(context.Background(), QueryRequest{
		SchemaVersion: SchemaVersion, OperationID: request.OperationID, OperationDigest: request.RequestDigest,
	})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if result.State != state {
		t.Fatalf("Query() state = %q, want %q", result.State, state)
	}
}

func mustFinalizeCommit(t *testing.T, request CommitRequest) CommitRequest {
	t.Helper()
	finalized, err := FinalizeCommitRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeCommitRequestDigest() error = %v", err)
	}
	return finalized
}

func mustFinalizeAbort(t *testing.T, request AbortRequest) AbortRequest {
	t.Helper()
	finalized, err := FinalizeAbortRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeAbortRequestDigest() error = %v", err)
	}
	return finalized
}
