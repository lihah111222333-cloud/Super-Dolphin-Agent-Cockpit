package orchestrationcommand

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

type submissionProviderStub struct {
	calls atomic.Int32
	ack   ProviderAcknowledgement
	err   error
	panic bool
}

func (p *submissionProviderStub) Submit(context.Context, SubmissionPermit) (ProviderAcknowledgement, error) {
	p.calls.Add(1)
	if p.panic {
		// archguard:ignore panic_count -- this stub must exercise provider panic recovery.
		panic("provider panic must become unknown")
	}
	return p.ack, p.err
}

func submissionRequestFixture(commandID string) SubmissionRequest {
	return SubmissionRequest{CommandID: commandID, IdempotencyNamespace: "send:agent-1", IdempotencyKeyDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RequestDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
}

// assertSubmissionUnknown 校验 provider 不确定结果的 sticky 状态。
func assertSubmissionUnknown(t *testing.T, submission Submission, err error, expected error, label string) {
	t.Helper()
	if !errors.Is(err, expected) {
		t.Fatalf("%s error = %v, want %v", label, err, expected)
	}
	if submission.Status != SubmissionOutcomeUnknown {
		t.Fatalf("%s status = %q, want outcome_unknown", label, submission.Status)
	}
}

// assertSubmissionProviderCalls 校验并发执行只触发一次 provider。
func assertSubmissionProviderCalls(t *testing.T, winners int, provider *submissionProviderStub) {
	t.Helper()
	if winners != 1 {
		t.Fatalf("submission winners = %d, want one", winners)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want one", provider.calls.Load())
	}
}

func TestSubmissionPrepareSameKeyKeepsCommandSubmissionIdentity(t *testing.T) {
	store := NewSubmissionStore()
	first, err := store.Prepare(context.Background(), submissionRequestFixture("command-one"))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.Prepare(context.Background(), submissionRequestFixture("command-one"))
	if err != nil {
		t.Fatal(err)
	}
	assertSubmissionReplay(t, first, replay)
	otherCommand := submissionRequestFixture("command-two")
	assertSubmissionConflict(t, store, otherCommand, "same key with different command")
	conflict := submissionRequestFixture("command-one")
	conflict.RequestDigest = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	assertSubmissionConflict(t, store, conflict, "different digest")
}

func assertSubmissionReplay(t *testing.T, first, replay Submission) {
	t.Helper()
	if replay.Request != first.Request || replay.Permit != first.Permit {
		t.Fatalf("replay = %#v, first = %#v", replay, first)
	}
	if first.Permit.DispatchReceipt == first.Permit.Permit || first.Permit.Permit == first.Permit.Nonce || first.Permit.DispatchReceipt == first.Permit.Nonce {
		t.Fatalf("submission materials must be independently random: %#v", first.Permit)
	}
}

func assertSubmissionConflict(t *testing.T, store *SubmissionStore, req SubmissionRequest, label string) {
	t.Helper()
	if _, err := store.Prepare(context.Background(), req); !errors.Is(err, ErrSubmissionConflict) {
		t.Fatalf("%s error = %v, want conflict", label, err)
	}
}

func TestSubmissionExecuteProviderErrorIsStickyAndQueryOnly(t *testing.T) {
	store := NewSubmissionStore()
	record, err := store.Prepare(context.Background(), submissionRequestFixture("command-error"))
	if err != nil {
		t.Fatal(err)
	}
	permit, err := store.Issue(context.Background(), record.Request)
	if err != nil {
		t.Fatal(err)
	}
	providerErr := errors.New("transport uncertain")
	provider := &submissionProviderStub{err: providerErr}
	got, err := store.Execute(context.Background(), permit, provider)
	assertSubmissionUnknown(t, got, err, ErrSubmissionUnresolved, "Execute")
	if !errors.Is(err, providerErr) {
		t.Fatalf("Execute error = %v, want provider error", err)
	}
	queried, err := store.Query(context.Background(), SubmissionKey{CommandID: record.Request.CommandID, SubmissionID: record.Request.SubmissionID})
	if err != nil || queried.Status != SubmissionOutcomeUnknown {
		t.Fatalf("Query = %#v err=%v", queried, err)
	}
	if _, err := store.Issue(context.Background(), record.Request); !errors.Is(err, ErrSubmissionUnresolved) {
		t.Fatalf("Issue after unknown = %v", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want one", provider.calls.Load())
	}
}

func TestSubmissionIssueIsOneShotBeforeProvider(t *testing.T) {
	store := NewSubmissionStore()
	req := submissionRequestFixture("command-issue-once")
	permit, err := store.Issue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Issue(context.Background(), req); !errors.Is(err, ErrSubmissionUnresolved) {
		t.Fatalf("replayed Issue = %v, want unresolved", err)
	}
	if _, err := store.Consume(context.Background(), permit); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
}

func TestSubmissionPrepareCannotBypassIssue(t *testing.T) {
	store := NewSubmissionStore()
	record, err := store.Prepare(context.Background(), submissionRequestFixture("command-bypass"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Consume(context.Background(), record.Permit); !errors.Is(err, ErrSubmissionNotIssued) {
		t.Fatalf("Prepare permit Consume = %v, want not-issued", err)
	}
}

func TestSubmissionExecuteConcurrentHasOneProviderCall(t *testing.T) {
	store := NewSubmissionStore()
	record, err := store.Prepare(context.Background(), submissionRequestFixture("command-concurrent"))
	if err != nil {
		t.Fatal(err)
	}
	permit, err := store.Issue(context.Background(), record.Request)
	if err != nil {
		t.Fatal(err)
	}
	provider := &submissionProviderStub{ack: ProviderAcknowledgement{Kind: SubmissionProviderAcknowledged, ProviderReceiptID: "receipt-1"}}
	winners := runConcurrentSubmissionExecutes(t, store, permit, provider)
	assertSubmissionProviderCalls(t, winners, provider)
}

// runConcurrentSubmissionExecutes 并发触发执行并等待所有受控 goroutine。
func runConcurrentSubmissionExecutes(t *testing.T, store *SubmissionStore, permit SubmissionPermit, provider *submissionProviderStub) int {
	t.Helper()
	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		runtimesafe.SafeGo(context.Background(), nil, "orchestrationcommand.test.submission-execute-concurrent", func(context.Context) {
			defer group.Done()
			<-start
			_, executeErr := store.Execute(context.Background(), permit, provider)
			results <- executeErr
		})
	}
	close(start)
	group.Wait()
	close(results)
	return countSubmissionWinners(t, results)
}

// countSubmissionWinners 汇总并发执行结果并拒绝未预期错误。
func countSubmissionWinners(t *testing.T, results <-chan error) int {
	t.Helper()
	winners := 0
	for executeErr := range results {
		if executeErr == nil {
			winners++
			continue
		}
		if !errors.Is(executeErr, ErrSubmissionConsumed) && !errors.Is(executeErr, ErrSubmissionTerminal) && !errors.Is(executeErr, ErrSubmissionUnresolved) {
			t.Fatalf("unexpected concurrent Execute() error = %v", executeErr)
		}
	}
	return winners
}

func TestSubmissionReconcileUnknownUsesOriginalPermitOnly(t *testing.T) {
	store := NewSubmissionStore()
	record, err := store.Prepare(context.Background(), submissionRequestFixture("command-reconcile"))
	if err != nil {
		t.Fatal(err)
	}
	permit, err := store.Issue(context.Background(), record.Request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Consume(context.Background(), permit); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ReconcileUnknownCandidate(context.Background(), permit, ProviderAcknowledgement{Kind: SubmissionProviderAcknowledged, ProviderSubmissionID: "provider-submission-1"})
	if err != nil || resolved.Status != SubmissionProviderAcknowledged {
		t.Fatalf("resolved = %#v err=%v", resolved, err)
	}
	if _, err := store.ReconcileUnknownCandidate(context.Background(), permit, ProviderAcknowledgement{Kind: SubmissionProviderAcknowledged, ProviderReceiptID: "other"}); !errors.Is(err, ErrSubmissionUnresolved) {
		t.Fatalf("second reconcile = %v", err)
	}
}

func TestSubmissionExecuteProviderPanicStaysUnknown(t *testing.T) {
	store := NewSubmissionStore()
	record, err := store.Prepare(context.Background(), submissionRequestFixture("command-panic"))
	if err != nil {
		t.Fatal(err)
	}
	permit, err := store.Issue(context.Background(), record.Request)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Execute(context.Background(), permit, &submissionProviderStub{panic: true})
	if !errors.Is(err, ErrSubmissionUnresolved) || got.Status != SubmissionOutcomeUnknown {
		t.Fatalf("panic Execute = %#v err=%v, want sticky unknown", got, err)
	}
}

func TestSubmissionExecuteProviderDefinitiveFailure(t *testing.T) {
	store := NewSubmissionStore()
	record, err := store.Prepare(context.Background(), submissionRequestFixture("command-failure"))
	if err != nil {
		t.Fatal(err)
	}
	permit, err := store.Issue(context.Background(), record.Request)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Execute(context.Background(), permit, &submissionProviderStub{ack: ProviderAcknowledgement{Kind: SubmissionProviderFailed, FailureCode: "rejected"}})
	if err != nil || got.Status != SubmissionProviderFailed || got.Acknowledgement.FailureCode != "rejected" {
		t.Fatalf("failure Execute = %#v err=%v", got, err)
	}
}

func TestSubmissionBoundaryRejectsNilContextAndProviderBeforeConsume(t *testing.T) {
	store := NewSubmissionStore()
	record, err := store.Prepare(context.Background(), submissionRequestFixture("command-nil"))
	if err != nil {
		t.Fatal(err)
	}
	var nilCtx context.Context
	if _, err := store.Query(nilCtx, SubmissionKey{CommandID: record.Request.CommandID, SubmissionID: record.Request.SubmissionID}); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("nil context = %v", err)
	}
	if _, err := store.Execute(context.Background(), record.Permit, nil); !errors.Is(err, ErrSubmissionProvider) {
		t.Fatalf("nil provider = %v", err)
	}
	current, err := store.Query(context.Background(), SubmissionKey{CommandID: record.Request.CommandID, SubmissionID: record.Request.SubmissionID})
	if err != nil || current.Status != SubmissionPending {
		t.Fatalf("nil provider consumed permit: %#v err=%v", current, err)
	}
}

func TestSubmissionZeroValueStoreFailsClosed(t *testing.T) {
	var store SubmissionStore
	req := submissionRequestFixture("command-zero")
	if _, err := store.Prepare(context.Background(), req); !errors.Is(err, ErrSubmissionStoreUnavailable) {
		t.Fatalf("zero Prepare = %v", err)
	}
	if _, err := store.Query(context.Background(), SubmissionKey{CommandID: req.CommandID, SubmissionID: "submission-id"}); !errors.Is(err, ErrSubmissionStoreUnavailable) {
		t.Fatalf("zero Query = %v", err)
	}
	if _, err := store.Issue(context.Background(), req); !errors.Is(err, ErrSubmissionStoreUnavailable) {
		t.Fatalf("zero Issue = %v", err)
	}
	if _, err := store.Consume(context.Background(), SubmissionPermit{}); !errors.Is(err, ErrSubmissionStoreUnavailable) {
		t.Fatalf("zero Consume = %v", err)
	}
	if _, err := store.Execute(context.Background(), SubmissionPermit{}, &submissionProviderStub{}); !errors.Is(err, ErrSubmissionStoreUnavailable) {
		t.Fatalf("zero Execute = %v", err)
	}
}

func TestProviderAcknowledgementValidationSeparatesUnknownAndAccepted(t *testing.T) {
	if err := (ProviderAcknowledgement{Kind: SubmissionOutcomeUnknown, ProviderReceiptID: "receipt"}).Validate(); !errors.Is(err, ErrSubmissionAcknowledgement) {
		t.Fatalf("unknown with receipt = %v", err)
	}
	if err := (ProviderAcknowledgement{Kind: SubmissionProviderAcknowledged}).Validate(); !errors.Is(err, ErrSubmissionAcknowledgement) {
		t.Fatalf("accepted without receipt = %v", err)
	}
	if err := (ProviderAcknowledgement{Kind: SubmissionProviderFailed, FailureCode: "provider-rejected"}).Validate(); err != nil {
		t.Fatalf("valid failure = %v", err)
	}
	if err := (ProviderAcknowledgement{Kind: SubmissionProviderAcknowledged, ProviderReceiptID: "\xff"}).Validate(); !errors.Is(err, ErrSubmissionAcknowledgement) {
		t.Fatalf("invalid UTF-8 receipt = %v", err)
	}
}

// TestSubmissionCandidateCrossInstanceIsNotDurable is an explicit boundary
// test: this candidate must not be cited as cross-process/reopen persistence.
func TestSubmissionCandidateCrossInstanceIsNotDurable(t *testing.T) {
	first := NewSubmissionStore()
	record, err := first.Prepare(context.Background(), submissionRequestFixture("command-reopen"))
	if err != nil {
		t.Fatal(err)
	}
	second := NewSubmissionStore()
	_, err = second.Query(context.Background(), SubmissionKey{CommandID: record.Request.CommandID, SubmissionID: record.Request.SubmissionID})
	if !errors.Is(err, ErrSubmissionNotFound) {
		t.Fatalf("fresh candidate Query() = %v, want explicit non-durable not-found", err)
	}
}
