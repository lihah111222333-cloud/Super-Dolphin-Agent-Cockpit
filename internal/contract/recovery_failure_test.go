package contract

import (
	"fmt"
	"testing"
)

func TestRecoveryFailureMatrixCoversStableCodeActionMatrix(t *testing.T) {
	tests := []struct {
		code      string
		retryable bool
		action    RecoveryAction
	}{
		{code: "UPDATE_TRANSACTION_AMBIGUOUS", action: RecoveryActionPreserveStateExportDiagnostics},
		{code: "UPDATE_SIGNATURE_INVALID", action: RecoveryActionPreserveStateExportDiagnostics},
		{code: "UPDATE_INTEGRITY_INVALID", action: RecoveryActionPreserveStateExportDiagnostics},
		{code: "MCP_SCHEMA_CAPACITY_EXHAUSTED", retryable: true, action: RecoveryActionWaitThenRetry},
		{code: "MCP_SCHEMA_REAP_FAILED", action: RecoveryActionRestartApplication},
		{code: "MCP_SCHEMA_DIGEST_MISMATCH", action: RecoveryActionPreserveStateExportDiagnostics},
		{code: "MCP_SCHEMA_PROTOCOL_VIOLATION", action: RecoveryActionPreserveStateExportDiagnostics},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			failure, ok := RecoveryFailureForCode(test.code, "transaction-1")
			if !ok {
				t.Fatal("stable recovery code is missing from registry")
			}
			want := RecoveryFailure{Code: test.code, Retryable: test.retryable, Action: test.action, TransactionID: "transaction-1"}
			if failure != want {
				t.Fatalf("failure = %#v, want %#v", failure, want)
			}
			if err := ValidateRecoveryFailure(failure); err != nil {
				t.Fatalf("ValidateRecoveryFailure() error = %v", err)
			}
		})
	}
}

type testRecoveryFailureCarrier struct{ failure RecoveryFailure }

func (carrier testRecoveryFailureCarrier) Error() string                    { return "test recovery failure" }
func (carrier testRecoveryFailureCarrier) RecoveryFailure() RecoveryFailure { return carrier.failure }

type cyclicRecoveryError struct{}

func (err *cyclicRecoveryError) Error() string { return "cycle" }
func (err *cyclicRecoveryError) Unwrap() error { return err }

type nonComparableRecoveryTree struct{ children []error }

func (tree nonComparableRecoveryTree) Error() string   { return "tree" }
func (tree nonComparableRecoveryTree) Unwrap() []error { return tree.children }

func TestRecoveryFailureFromErrorFailsClosedWhenTraversalBudgetIsExhausted(t *testing.T) {
	failure, _ := RecoveryFailureForCode("MCP_SCHEMA_REAP_FAILED", "")
	var err error = testRecoveryFailureCarrier{failure: failure}
	for range 80 {
		err = fmt.Errorf("wrapped: %w", err)
	}
	if got, ok := RecoveryFailureFromError(err); ok || got != (RecoveryFailure{}) {
		t.Fatalf("RecoveryFailureFromError(deep) = %#v, %v, want fail-closed", got, ok)
	}
}

func TestRecoveryFailureFromErrorHandlesJoinCycleAndNonComparableTrees(t *testing.T) {
	failure, _ := RecoveryFailureForCode("MCP_SCHEMA_CAPACITY_EXHAUSTED", "transaction-1")
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "direct", err: testRecoveryFailureCarrier{failure: failure}, want: true},
		{name: "join", err: fmt.Errorf("outer: %w", nonComparableRecoveryTree{children: []error{fmt.Errorf("noise"), testRecoveryFailureCarrier{failure: failure}}}), want: true},
		{name: "cycle", err: &cyclicRecoveryError{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := RecoveryFailureFromError(test.err)
			if ok != test.want {
				t.Fatalf("RecoveryFailureFromError() = %#v, %v, want ok=%v", got, ok, test.want)
			}
			if test.want && got != failure {
				t.Fatalf("RecoveryFailureFromError() = %#v, want %#v", got, failure)
			}
		})
	}
}

func TestValidateRecoveryFailureRejectsConflictingSemantics(t *testing.T) {
	tests := []RecoveryFailure{
		{Code: "MCP_SCHEMA_REAP_FAILED", Retryable: true, Action: RecoveryActionRestartApplication},
		{Code: "MCP_SCHEMA_CAPACITY_EXHAUSTED", Action: RecoveryActionWaitThenRetry},
		{Code: "UPDATE_SIGNATURE_INVALID", Action: RecoveryActionWaitThenRetry},
		{Code: "UNKNOWN", Action: RecoveryActionPreserveStateExportDiagnostics},
	}
	for _, failure := range tests {
		if err := ValidateRecoveryFailure(failure); err == nil {
			t.Fatalf("ValidateRecoveryFailure(%#v) succeeded", failure)
		}
	}
}
