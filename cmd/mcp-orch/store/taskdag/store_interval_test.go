package taskdag

import (
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

func TestIntervalValueAcceptsClockStyleIntervals(t *testing.T) {
	t.Parallel()

	tests := map[string]time.Duration{
		"00:00:30": 30 * time.Second,
		"00:02:00": 2 * time.Minute,
	}
	for raw, want := range tests {
		got, err := intervalValue(raw)
		if err != nil {
			t.Fatalf("intervalValue(%q) error = %v", raw, err)
		}
		if got != sqlc.IntervalMillis(want) {
			t.Fatalf("intervalValue(%q) = %d, want %d", raw, got, sqlc.IntervalMillis(want))
		}
	}
}

func TestIntervalValueRejectsInvalidClockStyleIntervals(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"00:60:00", "00:00:60", "-1:00:00"} {
		if _, err := intervalValue(raw); err == nil {
			t.Fatalf("intervalValue(%q) error = nil, want invalid interval", raw)
		}
	}
}
