//go:build !darwin && !linux && !windows

package multilsp

import (
	"testing"
	"time"
)

func assertTransportContextTerminationPlatform(t *testing.T, tr *transport) {
	t.Helper()
	select {
	case <-tr.done:
	case <-time.After(5 * time.Second):
		t.Fatal("context-cancelled transport did not finish after test cleanup")
	}
}
