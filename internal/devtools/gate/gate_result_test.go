package gate

import (
	"strings"
	"testing"
	"time"
)

func TestGateResultCancelledRequiresExitMinusOne(t *testing.T) {
	now := time.Now().UTC()
	result := GateResult{
		GateID: "frontend:test", Status: GateStatusCancelled, ExitCode: -1,
		StartedAt: now, CompletedAt: now.Add(time.Second),
		ArgvDigest: testDigest, LogDigest: testDigest,
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("Validate(cancelled) error = %v", err)
	}
	result.ExitCode = 1
	if err := result.Validate(); err == nil || !strings.Contains(err.Error(), "exit_code -1") {
		t.Fatalf("Validate(cancelled exit 1) error = %v", err)
	}
}

func TestGateResultTimeoutRequiresExitMinusOne(t *testing.T) {
	now := time.Now().UTC()
	result := GateResult{
		GateID: "backend:test_with_guard", Status: GateStatusTimeout, ExitCode: -1,
		StartedAt: now, CompletedAt: now.Add(time.Second),
		ArgvDigest: testDigest, LogDigest: testDigest,
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("Validate(timeout) error = %v", err)
	}
	result.ExitCode = 137
	if err := result.Validate(); err == nil || !strings.Contains(err.Error(), "exit_code -1") {
		t.Fatalf("Validate(timeout exit 137) error = %v", err)
	}
}
