package dagmetrics

import (
	"strconv"
	"testing"
)

func TestRecordRetryCapsTrackedNodeSeries(t *testing.T) {
	registry := NewRegistry()

	for i := range maxTrackedRetryNodes + 2 {
		registry.RecordRetry("dag-"+strconv.Itoa(i), "node", 1)
	}
	snap := registry.Read()
	if got := len(snap.RetryCountPerNode); got != maxTrackedRetryNodes {
		t.Fatalf("tracked retry nodes = %d, want cap %d", got, maxTrackedRetryNodes)
	}
	if got := snap.RetryCountPerNodeOverflow; got != 2 {
		t.Fatalf("retry overflow = %d, want 2", got)
	}
}

func TestRecordRetryAlertIsNotSuppressedAcrossWakeups(t *testing.T) {
	registry := NewRegistry()

	first := registry.RecordRetry("dag", "node", 3)
	second := registry.RecordRetry("dag", "node", 3)
	if !first.ShouldAlert || !second.ShouldAlert {
		t.Fatalf("threshold alerts = first:%v second:%v, want both true", first.ShouldAlert, second.ShouldAlert)
	}
	if got := registry.Read().RetryAlertTotal; got != 2 {
		t.Fatalf("retry alert total = %d, want 2", got)
	}
}
