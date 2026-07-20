package contract

import "testing"

func TestRecoveryFailureRegistryCoversStableCodeActionMatrix(t *testing.T) {
	tests := []struct {
		code      string
		retryable bool
		action    RecoveryAction
	}{
		{code: "UPDATE_TRANSACTION_AMBIGUOUS", action: RecoveryActionPreserveStateExportDiagnostics},
		{code: "UPDATE_SIGNATURE_INVALID", action: RecoveryActionPreserveStateExportDiagnostics},
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
