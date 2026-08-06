//go:build windows

package gate

import (
	"errors"
	"testing"
)

func TestDefaultDurationLedgerObservationFilesystemProviderWindowsFailsClosed(t *testing.T) {
	facts, err := defaultDurationLedgerObservationFilesystemProvider("ignored")
	if !errors.Is(err, errDurationLedgerObservationUnavailable) {
		t.Fatalf("provider error = %v, want unavailable error", err)
	}
	if facts.PhysicalBytes != nil || facts.AvailableBytes != nil {
		t.Fatalf("provider facts = %#v, want empty facts", facts)
	}
}
