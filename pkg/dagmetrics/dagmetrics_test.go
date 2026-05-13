package dagmetrics

import (
	"strconv"
	"testing"
)

func TestRecordRetryCapsTrackedNodeSeries(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	for i := 0; i < maxTrackedRetryNodes+2; i++ {
		RecordRetry("dag-"+strconv.Itoa(i), "node", 1)
	}
	snap := Read()
	if got := len(snap.RetryCountPerNode); got != maxTrackedRetryNodes {
		t.Fatalf("tracked retry nodes = %d, want cap %d", got, maxTrackedRetryNodes)
	}
	if got := snap.RetryCountPerNodeOverflow; got != 2 {
		t.Fatalf("retry overflow = %d, want 2", got)
	}
}

func TestRecordRetryAlertIsNotSuppressedAcrossWakeups(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	first := RecordRetry("dag", "node", 3)
	second := RecordRetry("dag", "node", 3)
	if !first.ShouldAlert || !second.ShouldAlert {
		t.Fatalf("threshold alerts = first:%v second:%v, want both true", first.ShouldAlert, second.ShouldAlert)
	}
	if got := Read().RetryAlertTotal; got != 2 {
		t.Fatalf("retry alert total = %d, want 2", got)
	}
}
