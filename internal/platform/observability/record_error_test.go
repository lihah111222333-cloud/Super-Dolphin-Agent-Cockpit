package observability

import (
	"testing"
	"time"
)

func TestRecordErrorWarningLimiterIsOwnedByService(t *testing.T) {
	first := NewService(Config{})
	second := NewService(Config{})
	if first.recordErrorWarnings == nil || second.recordErrorWarnings == nil {
		t.Fatal("record error warning limiter is required")
	}
	if first.recordErrorWarnings == second.recordErrorWarnings {
		t.Fatal("services must not share a record error warning limiter")
	}

	now := time.Unix(100, 0)
	if !first.recordErrorWarnings.allow("toolbridge", now) {
		t.Fatal("first service initial warning = false, want true")
	}
	if first.recordErrorWarnings.allow("toolbridge", now.Add(time.Second)) {
		t.Fatal("first service repeated warning = true, want false")
	}
	if !second.recordErrorWarnings.allow("toolbridge", now.Add(time.Second)) {
		t.Fatal("second service warning inherited first service limit")
	}
}
