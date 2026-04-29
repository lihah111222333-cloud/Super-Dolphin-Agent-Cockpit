package fbsd

import (
	"math"
	"testing"
	"time"
)

func mergeApprox(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func mkStats(calls ...time.Time) *SkillStats {
	return &SkillStats{Calls: append([]time.Time(nil), calls...)}
}

func TestEffectiveScore_BothNilReturnsZero(t *testing.T) {
	if got := EffectiveScore(nil, nil, time.Now(), 7*24*time.Hour, 90*24*time.Hour, 10, 0.3); got != 0 {
		t.Errorf("nil/nil should yield 0, got %v", got)
	}
}

func TestEffectiveScore_WSAboveMinUsesWSOnly(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	hl := 7 * 24 * time.Hour
	// 11 calls all at now → score = 11.0 from ws alone
	wsCalls := make([]time.Time, 11)
	for i := range wsCalls {
		wsCalls[i] = now
	}
	ws := &SkillStats{Calls: wsCalls}
	// global 故意给一个会显著改变结果的值；不应被使用
	glob := &SkillStats{Calls: []time.Time{now, now, now, now, now}}
	got := EffectiveScore(ws, glob, now, hl, 90*24*time.Hour, 10, 0.3)
	if !mergeApprox(got, 11.0) {
		t.Errorf("ws ≥ minCalls should use ws-only score=11, got %v", got)
	}
}

func TestEffectiveScore_WSBelowMinMixes(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	hl := 7 * 24 * time.Hour
	ws := mkStats(now, now)             // score = 2.0
	glob := mkStats(now, now, now, now) // score = 4.0
	got := EffectiveScore(ws, glob, now, hl, 90*24*time.Hour, 10, 0.3)
	want := 0.3*2.0 + 0.7*4.0 // 0.6 + 2.8 = 3.4
	if !mergeApprox(got, want) {
		t.Errorf("mix: got %v, want %v", got, want)
	}
}

func TestEffectiveScore_NilWSUsesGlobalDirectly(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	glob := mkStats(now, now, now)
	got := EffectiveScore(nil, glob, now, 7*24*time.Hour, 90*24*time.Hour, 10, 0.3)
	if !mergeApprox(got, 3.0) {
		t.Errorf("nil ws should use global directly, got %v", got)
	}
}

func TestEffectiveScore_NilGlobalSomeWSWeighted(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	ws := mkStats(now, now) // score = 2.0; below min=10 → spec §9.3 corner
	got := EffectiveScore(ws, nil, now, 7*24*time.Hour, 90*24*time.Hour, 10, 0.3)
	// 我们的策略：weight*ws = 0.6
	if !mergeApprox(got, 0.6) {
		t.Errorf("ws-only below min: got %v, want 0.6", got)
	}
}

func TestEffectiveScore_BothBelowMinMixesProperly(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	ws := mkStats(now)        // 1
	glob := mkStats(now, now) // 2
	got := EffectiveScore(ws, glob, now, 7*24*time.Hour, 90*24*time.Hour, 10, 0.3)
	want := 0.3*1.0 + 0.7*2.0 // 1.7
	if !mergeApprox(got, want) {
		t.Errorf("both below: got %v, want %v", got, want)
	}
}

func TestEffectiveScore_WeightBoundaries(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	ws := mkStats(now)
	glob := mkStats(now)
	if got := EffectiveScore(ws, glob, now, 7*24*time.Hour, 90*24*time.Hour, 10, 0.0); !mergeApprox(got, 1.0) {
		t.Errorf("weight=0 should equal global only, got %v", got)
	}
	if got := EffectiveScore(ws, glob, now, 7*24*time.Hour, 90*24*time.Hour, 10, 1.0); !mergeApprox(got, 1.0) {
		t.Errorf("weight=1 should equal ws only, got %v", got)
	}
}
