package main

import "testing"

// TestRecoveryCustodyLifecycleContract runs the targeted local regression suite
// for this batch's shutdown, run, worker, error, and fail-closed startup
// lifecycle behavior. These eight tests intentionally avoid repeating the
// package-wide dependency guards for every small case; run this suite with a
// total timeout of 120 seconds. It is not a replacement for the full guard.
func TestRecoveryCustodyLifecycleContract(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "shutdown waits for active handlers",
			run:  TestServiceShutdownWaitsForActiveHandlers,
		},
		{
			name: "close waits for active handlers",
			run:  TestServiceCloseWaitsForActiveHandlers,
		},
		{
			name: "supervisor rejects concurrent run",
			run:  TestSupervisorRejectsConcurrentRun,
		},
		{
			name: "supervisor worker delivers one terminal result",
			run:  TestRunSupervisorWorkerDeliversOneTerminalResult,
		},
		{
			name: "cancellation classification preserves joined failure",
			run:  TestCancellationClassificationPreservesJoinedFailure,
		},
		{
			name: "supervisor construction runs startup scan before run",
			run:  TestSupervisorConstructionRunsStartupScanBeforeRun,
		},
		{
			name: "service lifecycle and health",
			run:  TestServiceLifecycleAndHealth,
		},
		{
			name: "missing external dependencies have no startup side effects",
			run:  TestRunMissingExternalDependenciesHasNoStartupSideEffects,
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}
