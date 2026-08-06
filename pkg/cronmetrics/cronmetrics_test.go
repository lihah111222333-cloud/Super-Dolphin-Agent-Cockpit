package cronmetrics

import "testing"

func TestSnapshotAndResetForTesting(t *testing.T) {
	metrics := New()
	metrics.ResetForTesting()
	t.Cleanup(metrics.ResetForTesting)

	metrics.IncRecoveryFinalizeConflict()
	metrics.IncRecoveryFinalizeError()
	metrics.IncRecoveryFinalizeError()

	if got, want := metrics.Read(), (Snapshot{RecoveryFinalizeConflictTotal: 1, RecoveryFinalizeErrorTotal: 2}); got != want {
		t.Fatalf("metrics.Read() = %+v, want %+v", got, want)
	}

	metrics.ResetForTesting()
	if got := metrics.Read(); got != (Snapshot{}) {
		t.Fatalf("metrics.Read() after reset = %+v, want zero snapshot", got)
	}
}

func TestMetricsInstancesAreIndependent(t *testing.T) {
	first := New()
	second := New()

	first.IncRecoveryFinalizeConflict()
	first.IncRecoveryFinalizeError()

	if got, want := first.Read(), (Snapshot{RecoveryFinalizeConflictTotal: 1, RecoveryFinalizeErrorTotal: 1}); got != want {
		t.Fatalf("first.Read() = %+v, want %+v", got, want)
	}
	if got := second.Read(); got != (Snapshot{}) {
		t.Fatalf("second.Read() = %+v, want zero snapshot", got)
	}
}
