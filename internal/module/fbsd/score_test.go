package fbsd

import (
	"math"
	"testing"
	"time"
)

func approx(a, b, eps float64) bool { return math.Abs(a-b) < eps }

func TestScore_NilStatsReturnsZero(t *testing.T) {
	if got := Score(nil, time.Now(), 7*24*time.Hour, 90*24*time.Hour); got != 0 {
		t.Errorf("nil stats should yield 0, got %v", got)
	}
}

func TestScore_EmptyCallsReturnsZero(t *testing.T) {
	s := &SkillStats{}
	if got := Score(s, time.Now(), 7*24*time.Hour, 90*24*time.Hour); got != 0 {
		t.Errorf("empty calls should yield 0, got %v", got)
	}
}

func TestScore_SingleCallNowApproachesOne(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	s := &SkillStats{Calls: []time.Time{now}}
	got := Score(s, now, 7*24*time.Hour, 90*24*time.Hour)
	if !approx(got, 1.0, 1e-9) {
		t.Errorf("call at now: got %v, want ~1.0", got)
	}
}

func TestScore_SingleCallHalfLifeAgoYieldsHalf(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	hl := 7 * 24 * time.Hour
	s := &SkillStats{Calls: []time.Time{now.Add(-hl)}}
	got := Score(s, now, hl, 90*24*time.Hour)
	if !approx(got, 0.5, 1e-9) {
		t.Errorf("call at now-half_life: got %v, want ~0.5", got)
	}
}

func TestScore_CallOutsideFrozenWindowExcluded(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	hl := 7 * 24 * time.Hour
	frozen := 30 * 24 * time.Hour
	old := now.Add(-31 * 24 * time.Hour) // 31 days ago, beyond frozen=30
	s := &SkillStats{Calls: []time.Time{old}}
	if got := Score(s, now, hl, frozen); got != 0 {
		t.Errorf("call outside frozen window should be excluded, got %v", got)
	}
}

func TestScore_MultipleCallsAccumulate(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	hl := 7 * 24 * time.Hour
	s := &SkillStats{Calls: []time.Time{
		now,              // ~1.0
		now.Add(-hl),     // ~0.5
		now.Add(-2 * hl), // ~0.25
	}}
	got := Score(s, now, hl, 90*24*time.Hour)
	want := 1.0 + 0.5 + 0.25
	if !approx(got, want, 1e-9) {
		t.Errorf("accumulate: got %v, want %v", got, want)
	}
}

func TestScore_NonPositiveHalfLifeReturnsZero(t *testing.T) {
	s := &SkillStats{Calls: []time.Time{time.Now()}}
	if got := Score(s, time.Now(), 0, 90*24*time.Hour); got != 0 {
		t.Errorf("halfLife=0 should yield 0, got %v", got)
	}
	if got := Score(s, time.Now(), -time.Hour, 90*24*time.Hour); got != 0 {
		t.Errorf("halfLife<0 should yield 0, got %v", got)
	}
}
