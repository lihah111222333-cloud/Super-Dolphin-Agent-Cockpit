package sqliteauthority

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

type unavailableEvidenceFinalizer struct {
	finalizeCalls int
	queryCalls    int
}

func (f *unavailableEvidenceFinalizer) Finalize(context.Context, FinalizationRequest) (FinalizedEvidence, error) {
	f.finalizeCalls++
	return FinalizedEvidence{}, ErrInvalidEvidence
}

func (f *unavailableEvidenceFinalizer) Query(context.Context, FinalizationRequest) (FinalizationObservation, error) {
	f.queryCalls++
	return FinalizationObservation{}, ErrInvalidEvidence
}

func TestCommitOutcomeUnknownBeforeCallbackFusesAuthority(t *testing.T) {
	config := authorityTestConfig(t, "unknown-before-callback")
	authority := bootstrapAndOpenOutcomeAuthority(t, config, inertFinalizer{})
	defer closeOutcomeAuthority(t, authority)

	request := schemaTestRequest(t)
	handle, err := recoverycustody.NewSubmissionHandle(request)
	if err != nil {
		t.Fatalf("NewSubmissionHandle() error = %v", err)
	}
	runnerCalls := 0
	authority.transactionRunner = func(context.Context, *sql.DB, WriterFence, func(*sql.Conn) error) error {
		runnerCalls++
		return ErrCommitOutcomeUnknown
	}

	unknown, err := authority.Submit(t.Context(), request)
	assertUnknownMutationOutcome(t, unknown, err, handle, ErrCommitOutcomeUnknown)
	if !authority.queryOnly.Load() {
		t.Fatal("authority did not enter sticky query-only mode")
	}
	if runnerCalls != 1 || countOutcomeRows(t, authority.db, "submissions") != 0 {
		t.Fatalf("runner calls/submissions = %d/%d, want 1/0", runnerCalls,
			countOutcomeRows(t, authority.db, "submissions"))
	}

	refused, err := authority.Submit(t.Context(), request)
	assertUnknownMutationOutcome(t, refused, err, handle, ErrAuthorityReadOnly)
	if runnerCalls != 1 {
		t.Fatalf("runner calls after sticky refusal = %d, want 1", runnerCalls)
	}
}

func TestCommitAppliedUnknownReturnsStableIndeterminateAndFusesAuthority(t *testing.T) {
	config := authorityTestConfig(t, "unknown-after-submit-commit")
	authority := bootstrapAndOpenOutcomeAuthority(t, config, inertFinalizer{})
	defer closeOutcomeAuthority(t, authority)

	runnerCalls := 0
	authority.transactionRunner = func(ctx context.Context, db *sql.DB, fence WriterFence, callback func(*sql.Conn) error) error {
		runnerCalls++
		if err := withWriterFenceImmediate(ctx, db, fence, callback); err != nil {
			return err
		}
		return ErrCommitOutcomeUnknown
	}
	request := schemaTestRequest(t)
	handle, err := recoverycustody.NewSubmissionHandle(request)
	if err != nil {
		t.Fatalf("NewSubmissionHandle() error = %v", err)
	}

	unknown, err := authority.Submit(t.Context(), request)
	assertUnknownMutationOutcome(t, unknown, err, handle, ErrCommitOutcomeUnknown)
	if !authority.queryOnly.Load() || runnerCalls != 1 || countOutcomeRows(t, authority.db, "submissions") != 1 {
		t.Fatalf("query-only/runner calls/submissions = %v/%d/%d, want true/1/1", authority.queryOnly.Load(),
			runnerCalls, countOutcomeRows(t, authority.db, "submissions"))
	}

	refused, err := authority.Submit(t.Context(), request)
	assertUnknownMutationOutcome(t, refused, err, handle, ErrAuthorityReadOnly)
	if runnerCalls != 1 {
		t.Fatalf("runner calls after sticky refusal = %d, want 1", runnerCalls)
	}
}

func TestResolutionPreparationCommitUnknownResumesWithQueryOnly(t *testing.T) {
	config := authorityTestConfig(t, "unknown-after-resolution-commit")
	finalizer := &unavailableEvidenceFinalizer{}
	authority := bootstrapAndOpenOutcomeAuthority(t, config, finalizer)

	pending, err := authority.Submit(t.Context(), schemaTestRequest(t))
	if err != nil {
		closeOutcomeAuthority(t, authority)
		t.Fatalf("Submit() error = %v", err)
	}
	resolution := outcomeResolutionRequest(t, pending.Handle, "tx1-unknown")
	runnerCalls := 0
	authority.transactionRunner = func(ctx context.Context, db *sql.DB, fence WriterFence, callback func(*sql.Conn) error) error {
		runnerCalls++
		return commitThenUnknownRunner(ctx, db, fence, callback)
	}

	unknown, err := authority.Resolve(t.Context(), resolution)
	assertUnknownMutationOutcome(t, unknown, err, pending.Handle, ErrCommitOutcomeUnknown)
	refused, err := authority.Resolve(t.Context(), resolution)
	assertUnknownMutationOutcome(t, refused, err, pending.Handle, ErrAuthorityReadOnly)
	if runnerCalls != 1 {
		t.Fatalf("runner calls after sticky tx1 refusal = %d, want 1", runnerCalls)
	}
	assertOutcomeFinalizerCalls(t, finalizer.finalizeCalls, finalizer.queryCalls, 0, 0)
	assertOutcomeRowCount(t, authority.db, "resolutions", 1)
	assertOutcomeRowCount(t, authority.db, "terminal_receipts", 0)
	closeOutcomeAuthority(t, authority)

	reopened := openOutcomeAuthority(t, config, finalizer)
	defer closeOutcomeAuthority(t, reopened)
	resumed, err := reopened.ResumeResolution(t.Context(), pending.Handle)
	assertUnknownMutationOutcome(t, resumed, err, pending.Handle, ErrInvalidEvidence)
	assertOutcomeFinalizerCalls(t, finalizer.finalizeCalls, finalizer.queryCalls, 0, 1)
	assertOutcomeRowCount(t, reopened.db, "terminal_receipts", 0)
	// tx1 已提交但 Query 返回错误时必须保持 indeterminate；只有 definitive absent 才允许同键原子 Finalize。
}

func TestTerminalCommitAppliedUnknownDoesNotRefinalizeOrReanchor(t *testing.T) {
	config := authorityTestConfig(t, "unknown-after-terminal-commit")
	finalizer := &evidenceFinalizerFixture{config: config}
	authority := bootstrapAndOpenOutcomeAuthority(t, config, finalizer)

	pending, err := authority.Submit(t.Context(), schemaTestRequest(t))
	if err != nil {
		closeOutcomeAuthority(t, authority)
		t.Fatalf("Submit() error = %v", err)
	}
	resolution := outcomeResolutionRequest(t, pending.Handle, "tx2-unknown")
	runnerCalls := 0
	authority.transactionRunner = func(ctx context.Context, db *sql.DB, fence WriterFence, callback func(*sql.Conn) error) error {
		runnerCalls++
		if err := withWriterFenceImmediate(ctx, db, fence, callback); err != nil {
			return err
		}
		if runnerCalls == 3 {
			return ErrCommitOutcomeUnknown
		}
		return nil
	}

	terminal, err := authority.Resolve(t.Context(), resolution)
	assertTerminalOutcome(t, terminal, err, pending.Handle, ErrCommitOutcomeUnknown)
	if !authority.queryOnly.Load() {
		closeOutcomeAuthority(t, authority)
		t.Fatal("authority did not enter sticky query-only mode")
	}
	if runnerCalls != 3 {
		closeOutcomeAuthority(t, authority)
		t.Fatalf("runner calls = %d, want 3", runnerCalls)
	}
	assertOutcomeFinalizerCalls(t, finalizer.finalizeCalls, finalizer.queryCalls, 1, 1)
	assertSingleTerminalAnchor(t, authority.db)
	refused, err := authority.Resolve(t.Context(), resolution)
	assertTerminalOutcome(t, refused, err, pending.Handle, ErrAuthorityReadOnly)
	if runnerCalls != 3 {
		t.Fatalf("runner calls after sticky tx2 refusal = %d, want 3", runnerCalls)
	}
	assertOutcomeFinalizerCalls(t, finalizer.finalizeCalls, finalizer.queryCalls, 1, 1)
	assertSingleTerminalAnchor(t, authority.db)
	closeOutcomeAuthority(t, authority)

	reopened := openOutcomeAuthority(t, config, finalizer)
	defer closeOutcomeAuthority(t, reopened)
	replayed, err := reopened.Resolve(t.Context(), resolution)
	assertTerminalOutcome(t, replayed, err, pending.Handle, nil)
	resumed, err := reopened.ResumeResolution(t.Context(), pending.Handle)
	assertTerminalOutcome(t, resumed, err, pending.Handle, nil)
	assertOutcomeFinalizerCalls(t, finalizer.finalizeCalls, finalizer.queryCalls, 1, 1)
	assertSingleTerminalAnchor(t, reopened.db)
}

func bootstrapAndOpenOutcomeAuthority(t *testing.T, config Config, finalizer EvidenceFinalizer) *Authority {
	t.Helper()
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	return openOutcomeAuthority(t, config, finalizer)
}

func openOutcomeAuthority(t *testing.T, config Config, finalizer EvidenceFinalizer) *Authority {
	t.Helper()
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, finalizer, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	return authority
}

func closeOutcomeAuthority(t *testing.T, authority *Authority) {
	t.Helper()
	if err := authority.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func outcomeResolutionRequest(t *testing.T, handle recoverycustody.SubmissionHandle, suffix string) ResolutionRequest {
	t.Helper()
	request := ResolutionRequest{SchemaVersion: 1, ResolutionID: "resolution-" + suffix, Handle: handle,
		Kind: recoverycustody.ReceiptAvailability, FinalizationOperationID: "finalization-" + suffix}
	request, err := FinalizeResolutionRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeResolutionRequestDigest() error = %v", err)
	}
	return request
}

func commitThenUnknownRunner(ctx context.Context, db *sql.DB, fence WriterFence, callback func(*sql.Conn) error) error {
	if err := withWriterFenceImmediate(ctx, db, fence, callback); err != nil {
		return err
	}
	return ErrCommitOutcomeUnknown
}

func assertUnknownMutationOutcome(
	t *testing.T,
	outcome recoverycustody.Outcome,
	err error,
	handle recoverycustody.SubmissionHandle,
	wantErr error,
) {
	t.Helper()
	if !errors.Is(err, wantErr) || outcome.Handle != handle || outcome.Status != recoverycustody.StatusIndeterminate ||
		outcome.Receipt != nil {
		t.Fatalf("mutation outcome = (%+v, %v), want same-handle indeterminate %v", outcome, err, wantErr)
	}
}

func assertTerminalOutcome(
	t *testing.T,
	outcome recoverycustody.Outcome,
	err error,
	handle recoverycustody.SubmissionHandle,
	wantErr error,
) {
	t.Helper()
	if wantErr == nil && err != nil {
		t.Fatalf("terminal outcome error = %v, want nil", err)
	}
	if wantErr != nil && !errors.Is(err, wantErr) {
		t.Fatalf("terminal outcome error = %v, want %v", err, wantErr)
	}
	if outcome.Handle != handle || outcome.Status != recoverycustody.StatusAvailable || outcome.Receipt == nil {
		t.Fatalf("terminal outcome = %+v, want verified same-handle available", outcome)
	}
}

func assertOutcomeFinalizerCalls(t *testing.T, finalizeCalls, queryCalls, wantFinalize, wantQuery int) {
	t.Helper()
	if finalizeCalls != wantFinalize || queryCalls != wantQuery {
		t.Fatalf("finalizer calls = finalize:%d query:%d, want %d/%d",
			finalizeCalls, queryCalls, wantFinalize, wantQuery)
	}
}

func countOutcomeRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func assertOutcomeRowCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	if got := countOutcomeRows(t, db, table); got != want {
		t.Fatalf("%s rows = %d, want %d", table, got, want)
	}
}

func assertSingleTerminalAnchor(t *testing.T, db *sql.DB) {
	t.Helper()
	if checkpoints := countOutcomeRows(t, db, "antirollback_checkpoints"); checkpoints != 1 {
		t.Fatalf("antirollback checkpoints = %d, want 1", checkpoints)
	}
	if receipts := countOutcomeRows(t, db, "terminal_receipts"); receipts != 1 {
		t.Fatalf("terminal receipts = %d, want 1", receipts)
	}
	var state string
	if err := db.QueryRowContext(t.Context(), "SELECT state FROM resolutions").Scan(&state); err != nil {
		t.Fatalf("read resolution state: %v", err)
	}
	if state != resolutionStateTerminal {
		t.Fatalf("resolution state = %q, want %q", state, resolutionStateTerminal)
	}
}
