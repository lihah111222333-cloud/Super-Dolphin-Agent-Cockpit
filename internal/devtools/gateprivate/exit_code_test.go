package gateprivate

import (
	"errors"
	"strings"
	"testing"
)

func TestExitCodeProtocol(t *testing.T) {
	for _, code := range []ExitCode{ExitOK, ExitProtocol, ExitGateViolation, ExitEvidenceIncomplete, ExitSourceMismatch, ExitInfrastructure, ExitRegistryInvariant, ExitCancelled, ExitTimeout} {
		if err := code.Validate(); err != nil {
			t.Fatalf("ExitCode(%d).Validate() = %v", code, err)
		}
	}
	if err := ExitCode(99).Validate(); err == nil {
		t.Fatal("unsupported exit code unexpectedly validated")
	}
}

func TestWithExitCodePreservesCauseAndClassifiesFailures(t *testing.T) {
	cause := errors.New("root cause")
	wrapped := WithExitCode(ExitGateViolation, cause)
	if !errors.Is(wrapped, cause) || ExitCodeOf(wrapped) != ExitGateViolation {
		t.Fatalf("wrapped error = %v, code = %d", wrapped, ExitCodeOf(wrapped))
	}
	if ExitCodeOf(nil) != ExitOK || ExitCodeOf(errors.New("unknown")) != ExitInfrastructure {
		t.Fatal("unclassified exit code mapping is not fail-closed")
	}
	if invalid := WithExitCode(ExitOK, cause); ExitCodeOf(invalid) != ExitRegistryInvariant || !strings.Contains(invalid.Error(), "invalid failure exit code") {
		t.Fatalf("invalid code error = %v, code = %d", invalid, ExitCodeOf(invalid))
	}
}
