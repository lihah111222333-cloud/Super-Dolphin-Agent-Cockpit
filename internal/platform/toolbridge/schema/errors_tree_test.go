package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

type delayedCyclicError struct {
	delay time.Duration
}

func (err *delayedCyclicError) Error() string {
	return "cyclic fixture"
}

func (err *delayedCyclicError) Unwrap() error {
	time.Sleep(err.delay)
	return err
}

type nonComparableUnwrapError struct {
	children []error
}

func (err nonComparableUnwrapError) Error() string {
	return "non-comparable fixture"
}

func (err nonComparableUnwrapError) Unwrap() []error {
	return err.children
}

func TestHelperLimiterReturnsSlotAfterNestedLateReap(t *testing.T) {
	limiter := newHelperLimiter(1)
	var completeLateReap func()
	nested := fmt.Errorf("cleanup failed: %w", errors.Join(
		errors.New("cleanup context"),
		newDiagnostic(CodeReapFailed, "fixture did not reap", nil),
	))
	operationErr := errors.Join(
		newDiagnostic(CodeTimeout, "fixture timed out", context.DeadlineExceeded),
		nested,
	)
	if ErrorCode(operationErr) != CodeTimeout {
		t.Fatalf("joined operation code = %q, want primary %q", ErrorCode(operationErr), CodeTimeout)
	}

	if _, err := limiter.run(context.Background(), func(capacity *helperCapacityTracker) (Result, error) {
		completeLateReap = capacity.registerLateReap()
		return Result{}, operationErr
	}); !errors.Is(err, operationErr) {
		t.Fatalf("limiter.run() error = %v, want joined operation error", err)
	}

	started := false
	_, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		started = true
		return Result{}, nil
	})
	if started {
		t.Fatal("operation started after a nested reap failure consumed capacity")
	}
	if ErrorCode(err) != CodeCapacityExhausted {
		t.Fatalf("capacity code = %q, want %q; error=%v", ErrorCode(err), CodeCapacityExhausted, err)
	}
	if completeLateReap == nil {
		t.Fatal("late reap release callback was not provided")
	}
	completeLateReap()
	if _, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		return Result{}, nil
	}); err != nil {
		t.Fatalf("limiter.run() after nested late reap error = %v", err)
	}
}

func TestHelperLimiterKeepsSlotForMixedManagedAndUnmanagedReapFailures(t *testing.T) {
	limiter := newHelperLimiter(1)
	var completeManagedReap func()
	managed := newDiagnostic(CodeReapFailed, "managed fixture did not reap", nil)
	unmanaged := newDiagnostic(CodeReapFailed, "unmanaged fixture did not reap", nil)

	if _, err := limiter.run(context.Background(), func(capacity *helperCapacityTracker) (Result, error) {
		completeManagedReap = capacity.registerLateReap()
		return Result{}, errors.Join(managed, unmanaged)
	}); !errors.Is(err, managed) || !errors.Is(err, unmanaged) {
		t.Fatalf("limiter.run() error = %v, want both reap failures", err)
	}
	completeManagedReap()

	started := false
	_, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		started = true
		return Result{}, nil
	})
	if started || ErrorCode(err) != CodeCapacityExhausted {
		t.Fatalf("mixed reap failures released capacity: started=%v code=%q error=%v", started, ErrorCode(err), err)
	}
}

func TestHelperLimiterCyclicErrorReturnsAndKeepsSlot(t *testing.T) {
	limiter := newHelperLimiter(1)
	cyclic := &delayedCyclicError{delay: time.Millisecond}
	result := make(chan error, 1)
	safego.Go(context.Background(), nil, "toolbridge.schema-cyclic-error-test", func(context.Context) {
		_, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
			return Result{}, cyclic
		})
		result <- err
	})

	select {
	case err := <-result:
		if err != cyclic {
			t.Fatalf("limiter.run() error = %v, want original cyclic error", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("limiter.run() hung while traversing a cyclic error")
	}
	assertHelperLimiterFailClosed(t, limiter, "cyclic error")
}

func TestHelperLimiterNonComparableErrorKeepsSlotWithoutPanic(t *testing.T) {
	limiter := newHelperLimiter(1)
	fixture := nonComparableUnwrapError{children: []error{
		newDiagnostic(CodeReapFailed, "unmanaged fixture did not reap", nil),
	}}
	if _, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		return Result{}, fixture
	}); err == nil || err.Error() != fixture.Error() {
		t.Fatalf("limiter.run() error = %v, want original non-comparable error", err)
	}
	assertHelperLimiterFailClosed(t, limiter, "non-comparable error")
}

func TestHelperLimiterDeepErrorTreeReturnsAndKeepsSlot(t *testing.T) {
	limiter := newHelperLimiter(1)
	var fixture error = errors.New("deep fixture")
	for range 128 {
		fixture = fmt.Errorf("wrapped: %w", fixture)
	}
	if _, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		return Result{}, fixture
	}); err != fixture {
		t.Fatalf("limiter.run() error = %v, want original deep error", err)
	}
	assertHelperLimiterFailClosed(t, limiter, "deep error tree")
}

func assertHelperLimiterFailClosed(t *testing.T, limiter *helperLimiter, stage string) {
	t.Helper()
	started := false
	_, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		started = true
		return Result{}, nil
	})
	if started || ErrorCode(err) != CodeCapacityExhausted {
		t.Fatalf("%s released capacity: started=%v code=%q error=%v", stage, started, ErrorCode(err), err)
	}
}

func TestSchemaRecoveryFailureMappingIsStableAndSafe(t *testing.T) {
	secret := "stderr=postgres://admin:password@localhost/private key=-----BEGIN PRIVATE KEY----- token=sk-live-secret /Users/alice/private.db"
	tests := []struct {
		name          string
		code          Code
		wantRetryable bool
		wantAction    contract.RecoveryAction
	}{
		{name: "capacity exhausted", code: CodeCapacityExhausted, wantRetryable: true, wantAction: contract.RecoveryActionWaitThenRetry},
		{name: "reap failed", code: CodeReapFailed, wantAction: contract.RecoveryActionRestartApplication},
		{name: "digest mismatch", code: CodeDigestMismatch, wantAction: contract.RecoveryActionPreserveStateExportDiagnostics},
		{name: "protocol violation", code: CodeProtocolViolation, wantAction: contract.RecoveryActionPreserveStateExportDiagnostics},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertSchemaRecoveryFailureCase(t, test.code, test.wantRetryable, test.wantAction, secret)
		})
	}
}

func assertSchemaRecoveryFailureCase(
	t *testing.T, code Code, wantRetryable bool, wantAction contract.RecoveryAction, secret string,
) {
	t.Helper()
	unsafeErr := newDiagnostic(code, secret, errors.New(secret))
	failure, ok := RecoveryFailure(unsafeErr)
	if !ok {
		t.Fatalf("RecoveryFailure() ok = false for %q", code)
	}
	want := contract.RecoveryFailure{Code: string(code), Retryable: wantRetryable, Action: wantAction}
	if failure != want {
		t.Fatalf("RecoveryFailure() = %#v, want %#v", failure, want)
	}
	assertRecoveryFailureWireFields(t, failure)
	safeErr := SafeRecoveryError(unsafeErr)
	assertNoSchemaRecoverySecret(t, safeErr)
	mapped, ok := RecoveryFailure(safeErr)
	if !ok || mapped != failure {
		t.Fatalf("wrapped RecoveryFailure() = %#v, %v; want %#v, true", mapped, ok, failure)
	}
}

func assertRecoveryFailureWireFields(t *testing.T, failure contract.RecoveryFailure) {
	t.Helper()
	raw, err := json.Marshal(failure)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 {
		t.Fatalf("recovery metadata fields = %v, want exactly four", fields)
	}
}

func assertNoSchemaRecoverySecret(t *testing.T, err error) {
	t.Helper()
	for _, leaked := range []string{"postgres://", "PRIVATE KEY", "sk-live-secret", "/Users/alice", "stderr="} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("SafeRecoveryError() leaked %q in %q", leaked, err)
		}
	}
}

func TestSafeRecoveryErrorRebuildsOuterJoinWithoutSiblingLeak(t *testing.T) {
	secret := errors.New("sibling token=sk-outer-secret /Users/alice/private.db")
	safe := SafeRecoveryError(newDiagnostic(CodeCapacityExhausted, "capacity secret", nil))
	outer := errors.Join(secret, fmt.Errorf("nested: %w", safe))

	got := SafeRecoveryError(outer)
	if got == outer {
		t.Fatal("SafeRecoveryError() returned the outer join unchanged")
	}
	if got.Error() != string(CodeCapacityExhausted) {
		t.Fatalf("SafeRecoveryError() = %q, want stable code", got)
	}
	assertNoSchemaRecoverySecret(t, got)
}

func TestRecoveryFailureTraversalIsBounded(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "cycle", err: &delayedCyclicError{}},
		{name: "non-comparable", err: nonComparableUnwrapError{children: []error{newDiagnostic(CodeReapFailed, "secret", nil)}}},
		{name: "deep", err: deeplyWrappedSchemaError(128, newDiagnostic(CodeDigestMismatch, "secret", nil))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			done := make(chan struct{})
			safego.Go(t.Context(), nil, "schema-recovery-bounded-traversal-test", func(context.Context) {
				defer close(done)
				_, _ = RecoveryFailure(test.err)
				_ = ErrorCode(test.err)
			})
			select {
			case <-done:
			case <-time.After(300 * time.Millisecond):
				t.Fatal("recovery classification exceeded bounded traversal deadline")
			}
		})
	}
}

func deeplyWrappedSchemaError(depth int, cause error) error {
	for range depth {
		cause = fmt.Errorf("wrapped: %w", cause)
	}
	return cause
}
