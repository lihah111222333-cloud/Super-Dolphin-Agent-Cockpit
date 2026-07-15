package cron

import (
	"encoding/json"
	"testing"
	"time"
)

func TestJobRunStateChangedScheduledAtIsRequired(t *testing.T) {
	assertScheduledAtWireValue(t, time.Time{}, "0001-01-01T00:00:00Z")
	assertScheduledAtWireValue(t, time.Date(2026, 7, 15, 4, 5, 6, 0, time.UTC), "2026-07-15T04:05:06Z")
}

func assertScheduledAtWireValue(t *testing.T, scheduledAt time.Time, want string) {
	t.Helper()
	raw, err := json.Marshal(JobRunStateChanged{ScheduledAt: scheduledAt})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	var got string
	if err := json.Unmarshal(fields["scheduled_at"], &got); err != nil {
		t.Fatalf("scheduled_at is not a required timestamp: %v; JSON = %s", err, raw)
	}
	if got != want {
		t.Fatalf("scheduled_at = %q, want %q", got, want)
	}
}
