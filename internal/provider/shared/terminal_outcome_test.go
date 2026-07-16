package shared

import "testing"

func TestResolveRawTerminalOutcomeStrictMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		data       any
		want       RawTerminalOutcome
		wantReject bool
	}{
		{name: "success", data: map[string]any{"success": true, "status": "completed"}, want: RawTerminalOutcome{Success: true, Status: "completed"}},
		{name: "provider cancelled default", data: map[string]any{"success": false, "status": "cancelled"}, want: RawTerminalOutcome{Status: "cancelled", Cause: "provider"}},
		{name: "user interrupted", data: map[string]any{"success": false, "status": "interrupted", "terminationCause": "user_request", "terminationRequestId": "stop-1"}, want: RawTerminalOutcome{Status: "interrupted", Cause: "user_request", RequestID: "stop-1"}},
		{name: "payload not object", data: "failed", wantReject: true},
		{name: "missing success", data: map[string]any{"status": "failed"}, wantReject: true},
		{name: "missing status", data: map[string]any{"success": false}, wantReject: true},
		{name: "unknown status", data: map[string]any{"success": false, "status": "done"}, wantReject: true},
		{name: "success status conflict", data: map[string]any{"success": true, "status": "failed"}, wantReject: true},
		{name: "non cancel termination", data: map[string]any{"success": false, "status": "failed", "termination_cause": "provider"}, wantReject: true},
		{name: "user request missing id", data: map[string]any{"success": false, "status": "cancelled", "termination_cause": "user_request"}, wantReject: true},
		{name: "provider carries request id", data: map[string]any{"success": false, "status": "cancelled", "termination_cause": "provider", "termination_request_id": "stop-1"}, wantReject: true},
		{name: "request id without cause", data: map[string]any{"success": false, "status": "cancelled", "termination_request_id": "stop-1"}, wantReject: true},
		{name: "conflicting aliases", data: map[string]any{"success": false, "status": "cancelled", "termination_cause": "provider", "terminationCause": "system"}, wantReject: true},
		{name: "non string cause", data: map[string]any{"success": false, "status": "cancelled", "termination_cause": 7}, wantReject: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveRawTerminalOutcome(test.data)
			if (got.ContractError != "") != test.wantReject {
				t.Fatalf("ResolveRawTerminalOutcome() = %#v, wantReject=%v", got, test.wantReject)
			}
			if !test.wantReject && got != test.want {
				t.Fatalf("ResolveRawTerminalOutcome() = %#v, want %#v", got, test.want)
			}
		})
	}
}
