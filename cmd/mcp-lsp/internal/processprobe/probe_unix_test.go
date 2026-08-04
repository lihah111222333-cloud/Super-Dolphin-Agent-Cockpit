//go:build darwin || linux

package processprobe

import (
	"context"
	"errors"
	"syscall"
	"testing"
)

func TestClassifySignalZeroPermissionDeniedFixture(t *testing.T) {
	alive, permissionDenied, err := classifySignalZeroError(syscall.EPERM)
	if !alive || !permissionDenied {
		t.Fatalf("classifySignalZeroError(EPERM) = alive=%t permissionDenied=%t", alive, permissionDenied)
	}
	if !errors.Is(err, errProcessPermissionDenied) {
		t.Fatalf("classifySignalZeroError(EPERM) error = %v, want %v", err, errProcessPermissionDenied)
	}
	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("classifySignalZeroError(EPERM) error = %v, want wrapped EPERM", err)
	}

	snapshot, probeErr := probeWithLiveness(context.Background(), 123, func(int) (bool, bool, error) {
		return alive, permissionDenied, err
	})
	if probeErr == nil {
		t.Fatal("Probe(EPERM fixture) error = nil")
	}
	if snapshot.Reason() != ReasonPermissionDenied {
		t.Fatalf("Probe(EPERM fixture) reason = %q, want %q", snapshot.Reason(), ReasonPermissionDenied)
	}
	if snapshot.SignalSent() || snapshot.AuthorityDecision() != AuthorityNoSignal {
		t.Fatal("permission-denied fixture crossed the no-signal boundary")
	}
}
