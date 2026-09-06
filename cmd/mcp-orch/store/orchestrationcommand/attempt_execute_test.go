package orchestrationcommand

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

type executeProviderStub struct {
	calls  atomic.Int32
	result ProviderResult
	err    error
	panic  bool
}

// newAttemptExecuteFixture 创建执行测试所需的 intent、存储和网关。
func newAttemptExecuteFixture(t *testing.T, commandID string) (Intent, *AttemptStore, *CommandAttemptGateway) {
	t.Helper()
	intent, intents, db := attemptIntentFixture(t, commandID)
	store, gateway := mustNewAttemptGateway(t, db, intents, "execute")
	return intent, store, gateway
}

// assertExecuteAccepted 校验 provider 成功后的 attempt 结果。
func assertExecuteAccepted(t *testing.T, result Attempt, receipt string) {
	t.Helper()
	if result.Phase != AttemptProviderAccepted {
		t.Fatalf("Execute phase = %q, want provider_accepted", result.Phase)
	}
	if result.Result.ReceiptID != receipt {
		t.Fatalf("Execute receipt = %q, want %q", result.Result.ReceiptID, receipt)
	}
}

// assertExecuteUnknown 校验 provider 异常后的 unknown attempt。
func assertExecuteUnknown(t *testing.T, result Attempt, err error, expected error, label string) {
	t.Helper()
	if !errors.Is(err, expected) {
		t.Fatalf("%s error = %v, want %v", label, err, expected)
	}
	if result.Phase != AttemptOutcomeUnknown {
		t.Fatalf("%s phase = %q, want outcome_unknown", label, result.Phase)
	}
}

func (p *executeProviderStub) Submit(context.Context, DispatchPermit) (ProviderResult, error) {
	p.calls.Add(1)
	if p.panic {
		// archguard:ignore panic_count -- this stub must exercise provider panic recovery.
		panic("provider secret should not escape")
	}
	return p.result, p.err
}

func TestCommandAttemptExecuteConsumesBeforeProviderAndReconciles(t *testing.T) {
	intent, _, gateway := newAttemptExecuteFixture(t, "attempt-execute-success")
	var err error
	permit, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatal(err)
	}
	provider := &executeProviderStub{result: ProviderResult{Kind: ProviderResultSubmission, ReceiptID: "receipt-execute"}}
	result, err := gateway.Execute(context.Background(), permit, provider)
	assertExecuteAccepted(t, result, "receipt-execute")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want one", provider.calls.Load())
	}
	if _, err := gateway.Execute(context.Background(), permit, provider); !errors.Is(err, ErrAttemptConsumed) && !errors.Is(err, ErrAttemptTerminal) {
		t.Fatalf("replayed Execute() = %v, want consumed or terminal", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("replayed provider calls = %d, want one", provider.calls.Load())
	}
}

func TestCommandAttemptExecuteProviderErrorStaysUnknown(t *testing.T) {
	intent, store, gateway := newAttemptExecuteFixture(t, "attempt-execute-error")
	var err error
	permit, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatal(err)
	}
	providerErr := errors.New("provider transport failed")
	provider := &executeProviderStub{err: providerErr}
	attempt, err := gateway.Execute(context.Background(), permit, provider)
	assertExecuteUnknown(t, attempt, err, ErrAttemptUnresolved, "unknown Execute()")
	if !errors.Is(err, providerErr) {
		t.Fatalf("unknown Execute() error = %v, want provider error", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want one", provider.calls.Load())
	}
	unknown, err := store.QueryUnknown(context.Background(), AttemptKey{Namespace: intent.IdempotencyNamespace, Digest: intent.IdempotencyKeyDigest})
	if err != nil || unknown.Phase != AttemptOutcomeUnknown {
		t.Fatalf("QueryUnknown() = %#v err=%v", unknown, err)
	}
}

func TestCommandAttemptExecuteConcurrentHasOneProviderCall(t *testing.T) {
	intent, _, gateway := newAttemptExecuteFixture(t, "attempt-execute-concurrent")
	var err error
	permit, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatal(err)
	}
	provider := &executeProviderStub{result: ProviderResult{Kind: ProviderResultSubmission, SubmissionID: "submission-concurrent"}}
	const workers = 12
	errs := make(chan error, workers)
	start := make(chan struct{})
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		runtimesafe.SafeGo(context.Background(), nil, "orchestrationcommand.test.attempt-execute-concurrent", func(context.Context) {
			defer group.Done()
			<-start
			_, executeErr := gateway.Execute(context.Background(), permit, provider)
			errs <- executeErr
		})
	}
	close(start)
	group.Wait()
	close(errs)
	winners := 0
	for executeErr := range errs {
		if executeErr == nil {
			winners++
			continue
		}
		if !errors.Is(executeErr, ErrAttemptConsumed) && !errors.Is(executeErr, ErrAttemptTerminal) {
			t.Fatalf("concurrent Execute() error = %v", executeErr)
		}
	}
	if winners != 1 || provider.calls.Load() != 1 {
		t.Fatalf("winners=%d provider_calls=%d, want one/one", winners, provider.calls.Load())
	}
}

func TestCommandAttemptExecuteRejectsNilProviderBeforeConsume(t *testing.T) {
	intent, store, gateway := newAttemptExecuteFixture(t, "attempt-execute-nil")
	var err error
	permit, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.Execute(context.Background(), permit, nil); !errors.Is(err, ErrProviderSubmitterRequired) {
		t.Fatalf("nil provider Execute() = %v, want ErrProviderSubmitterRequired", err)
	}
	current, err := store.GetByCommand(context.Background(), intent.CommandID)
	if err != nil || current.Phase != AttemptEffectPending {
		t.Fatalf("nil provider consumed permit unexpectedly: attempt=%#v err=%v", current, err)
	}
}

func TestCommandAttemptExecuteProviderPanicStaysUnknown(t *testing.T) {
	intent, _, gateway := newAttemptExecuteFixture(t, "attempt-execute-panic")
	var err error
	permit, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := gateway.Execute(context.Background(), permit, &executeProviderStub{panic: true})
	assertExecuteUnknown(t, attempt, err, ErrAttemptUnresolved, "panic Execute()")
}
