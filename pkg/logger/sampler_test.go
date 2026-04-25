package logger

import "testing"

func TestNewEverySamplerLogsOnlyEveryNthHit(t *testing.T) {
	sampler := NewEverySampler(1000)
	for i := 1; i < 1000; i++ {
		if sampler.ShouldLog("delta") {
			t.Fatalf("hit %d logged before everyM boundary", i)
		}
	}
	if !sampler.ShouldLog("delta") {
		t.Fatal("hit 1000 did not log")
	}
	if sampler.ShouldLog("delta") {
		t.Fatal("hit 1001 logged unexpectedly")
	}
}
