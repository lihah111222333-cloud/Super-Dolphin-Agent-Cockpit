package turn

import "testing"

func TestValidateTurnRefV1RejectsMissingAndUnknownFields(t *testing.T) {
	t.Parallel()
	if err := ValidateTurnRefV1(map[string]any{"threadId": "thread-1", "turnId": "turn-1"}); err != nil {
		t.Fatalf("valid TurnRefV1: %v", err)
	}
	for _, value := range []map[string]any{
		{"threadId": "thread-1"},
		{"threadId": "thread-1", "turnId": "turn-1", "legacy": true},
	} {
		if err := ValidateTurnRefV1(value); err == nil {
			t.Fatalf("invalid TurnRefV1 accepted: %#v", value)
		}
	}
}

func TestValidatePublicErrorV1RejectsUnsafeAndUnimplementedRecoveryFields(t *testing.T) {
	t.Parallel()
	valid := publicErrorFixture()
	if err := ValidatePublicErrorV1(valid); err != nil {
		t.Fatalf("valid PublicErrorV1: %v", err)
	}
	valid["rawCause"] = "provider stack"
	if err := ValidatePublicErrorV1(valid); err == nil {
		t.Fatal("PublicErrorV1 accepted rawCause")
	}
	for _, action := range []string{"retry", "reconnect", "restart_provider", "reopen_thread", "invented"} {
		t.Run(action, func(t *testing.T) {
			value := publicErrorFixture()
			value["recoveryActions"] = []string{action}
			if err := ValidatePublicErrorV1(value); err == nil {
				t.Fatalf("PublicErrorV1 accepted unsupported recovery action %q", action)
			}
		})
	}
}

func TestValidateTurnTerminalV2OutcomeRules(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		value   map[string]any
		wantErr bool
	}{
		{name: "success", value: terminalFixture("success"), wantErr: false},
		{name: "failed requires public error", value: terminalFixture("failed"), wantErr: true},
		{name: "failed with public error", value: withPublicError(terminalFixture("failed")), wantErr: false},
		{name: "user interrupt requires accepted request", value: withCause(terminalFixture("interrupted"), "user_request"), wantErr: true},
		{name: "user interrupt", value: withRequestID(withCause(terminalFixture("interrupted"), "user_request")), wantErr: false},
		{name: "provider cancel requires public error", value: withCause(terminalFixture("cancelled"), "provider"), wantErr: true},
		{name: "provider cancel rejects request id", value: withRequestID(withPublicError(withCause(terminalFixture("cancelled"), "provider"))), wantErr: true},
		{name: "unknown outcome", value: terminalFixture("unknown"), wantErr: true},
		{name: "unknown terminal field", value: withUnknown(terminalFixture("success")), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateTurnTerminalV2(test.value)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateTurnTerminalV2() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func publicErrorFixture() map[string]any {
	return map[string]any{
		"code": "PROVIDER_FAILED", "title": "Provider failed", "message": "Try again.",
		"diagnosticId": "diag-1", "retryable": false, "recoveryActions": []string{"copy_diagnostics"},
	}
}

func terminalFixture(outcome string) map[string]any {
	return map[string]any{
		"schemaVersion": 2, "eventId": "event-1", "threadId": "thread-1", "turnId": "turn-1",
		"outcome": outcome, "occurredAt": "2026-07-16T00:00:00Z",
	}
}

func withPublicError(value map[string]any) map[string]any {
	value["publicError"] = publicErrorFixture()
	return value
}

func withCause(value map[string]any, cause string) map[string]any {
	value["terminationCause"] = cause
	return value
}

func withRequestID(value map[string]any) map[string]any {
	value["terminationRequestId"] = "request-1"
	return value
}

func withUnknown(value map[string]any) map[string]any {
	value["status"] = "success"
	return value
}
