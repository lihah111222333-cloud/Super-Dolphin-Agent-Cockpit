package cron

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func TestParseScheduleRejectsEmpty(t *testing.T) {
	t.Parallel()
	_, err := ParseSchedule("", "UTC")
	if !errors.Is(err, ErrInvalidScheduleExpr) {
		t.Fatalf("want ErrInvalidScheduleExpr, got %v", err)
	}
}

func TestParseScheduleRejectsInvalid(t *testing.T) {
	t.Parallel()
	_, err := ParseSchedule("not a cron", "UTC")
	if !errors.Is(err, ErrInvalidScheduleExpr) {
		t.Fatalf("want ErrInvalidScheduleExpr, got %v", err)
	}
}

func TestParseScheduleRejectsInvalidTimezone(t *testing.T) {
	t.Parallel()
	_, err := ParseSchedule("* * * * *", "Mars/Olympus")
	if err == nil || !strings.Contains(err.Error(), "timezone") {
		t.Fatalf("want timezone validation error, got %v", err)
	}
}

func TestComputeNextRunAtDailyAtNine(t *testing.T) {
	t.Parallel()
	// 2026-04-22 08:00:00 UTC — next 9am is the same day.
	after := time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC)
	next, err := ComputeNextRunAt("0 9 * * *", "UTC", after)
	if err != nil {
		t.Fatalf("ComputeNextRunAt error = %v", err)
	}
	want := time.Date(2026, 4, 22, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestComputeNextRunAtRespectsTimezone(t *testing.T) {
	t.Parallel()
	// 9am Asia/Seoul = 00:00 UTC. Starting from 2026-04-22 01:00 UTC,
	// next is next day's 00:00 UTC (Seoul midnight -> Seoul 9am).
	after := time.Date(2026, 4, 22, 1, 0, 0, 0, time.UTC)
	next, err := ComputeNextRunAt("0 9 * * *", "Asia/Seoul", after)
	if err != nil {
		t.Fatalf("ComputeNextRunAt error = %v", err)
	}
	// Expected: next day at 9am Seoul time = 00:00 UTC the day after.
	want := time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next (UTC) = %v, want %v", next.UTC(), want)
	}
}

func TestNextRetryAtReturnsZeroWhenPastNextRunAt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC)
	// Next scheduled fire is 30s from now; jitter will push retry past
	// that and the plan says we must drop the retry.
	nextRunAt := now.Add(5 * time.Second)
	cfg := BackoffConfig{Base: 30 * time.Second, Cap: 15 * time.Minute, Jitter: rand.New(rand.NewSource(1))}
	got := NextRetryAt(cfg, now, nextRunAt, 1)
	if !got.IsZero() {
		t.Fatalf("NextRetryAt should return zero when retry crosses next schedule, got %v", got)
	}
}

func TestNextRetryAtHonoursExponentialBackoff(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC)
	nextRunAt := now.Add(24 * time.Hour)
	cfg := BackoffConfig{Base: 30 * time.Second, Cap: 15 * time.Minute, Jitter: rand.New(rand.NewSource(42))}

	// With full jitter the retry falls in [now, now+delay]; check bounds
	// for the second failure (delay = 60s).
	got := NextRetryAt(cfg, now, nextRunAt, 2)
	if got.Before(now) || got.After(now.Add(60*time.Second)) {
		t.Fatalf("NextRetryAt failure=2 out of [now, now+60s]: %v", got)
	}
}

func TestDefaultBackoffReturnsIndependentConfig(t *testing.T) {
	t.Parallel()

	first := DefaultBackoff()
	first.Base = time.Nanosecond
	first.Cap = time.Nanosecond
	second := DefaultBackoff()

	if second.Base != 30*time.Second {
		t.Fatalf("default base = %s, want 30s", second.Base)
	}
	if second.Cap != 15*time.Minute {
		t.Fatalf("default cap = %s, want 15m", second.Cap)
	}
}

func TestDedupeKeyIsDeterministic(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC)
	a := DedupeKey("job-1", at, "idem-1")
	b := DedupeKey("job-1", at, "idem-1")
	if a != b {
		t.Fatalf("DedupeKey not deterministic: %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("DedupeKey len = %d, want 64 (sha256 hex)", len(a))
	}
	if c := DedupeKey("job-1", at, "idem-2"); c == a {
		t.Fatal("DedupeKey should differ when idempotency key differs")
	}
}
