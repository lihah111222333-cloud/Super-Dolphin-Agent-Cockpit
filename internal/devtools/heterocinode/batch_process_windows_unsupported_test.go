//go:build windows

package heterocinode

import (
	"errors"
	"testing"
)

func TestWindowsBatchProcessBoundaryFailsFastWithoutStartingWorkload(t *testing.T) {
	guard, err := startBatchProcess(nil)
	if !errors.Is(err, errUnsupportedPhysicalWindows) {
		t.Fatalf("startBatchProcess(nil) error = %v, want unsupported physical Windows error", err)
	}
	if err := guard.stop(nil); !errors.Is(err, errUnsupportedPhysicalWindows) {
		t.Fatalf("batchProcessGuard.stop(nil) error = %v, want unsupported physical Windows error", err)
	}
	if err := guard.close(); !errors.Is(err, errUnsupportedPhysicalWindows) {
		t.Fatalf("batchProcessGuard.close() error = %v, want unsupported physical Windows error", err)
	}
	if isBatchProcessGone(errUnsupportedPhysicalWindows) {
		t.Fatal("unsupported physical Windows error was classified as a gone process")
	}
}
