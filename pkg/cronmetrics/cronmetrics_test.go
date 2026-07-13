package cronmetrics

import "testing"

func TestSnapshotAndResetForTesting(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	IncRecoveryFinalizeConflict()
	IncRecoveryFinalizeError()
	IncRecoveryFinalizeError()

	if got, want := Read(), (Snapshot{RecoveryFinalizeConflictTotal: 1, RecoveryFinalizeErrorTotal: 2}); got != want {
		t.Fatalf("Read() = %+v, want %+v", got, want)
	}

	ResetForTesting()
	if got := Read(); got != (Snapshot{}) {
		t.Fatalf("Read() after reset = %+v, want zero snapshot", got)
	}
}
