package schema

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestHelperLimiterRetainsSlotForNestedReapFailure(t *testing.T) {
	limiter := newHelperLimiter(1)
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

	if _, err := limiter.run(context.Background(), func() (Result, error) {
		return Result{}, operationErr
	}); !errors.Is(err, operationErr) {
		t.Fatalf("limiter.run() error = %v, want joined operation error", err)
	}

	started := false
	_, err := limiter.run(context.Background(), func() (Result, error) {
		started = true
		return Result{}, nil
	})
	if started {
		t.Fatal("operation started after a nested reap failure consumed capacity")
	}
	if ErrorCode(err) != CodeCapacityExhausted {
		t.Fatalf("capacity code = %q, want %q; error=%v", ErrorCode(err), CodeCapacityExhausted, err)
	}
}
