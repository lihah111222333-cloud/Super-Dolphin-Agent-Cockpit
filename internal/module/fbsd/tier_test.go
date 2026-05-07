package fbsd

import (
	"testing"
	"time"

	dtoskill "github.com/anthropic-ai/super-agent-v3/internal/dto/skill"
)

func mkEntry(name string, mutate func(m *dtoskill.SkillMeta)) dtoskill.SkillEntry {
	m := &dtoskill.SkillMeta{Name: name}
	if mutate != nil {
		mutate(m)
	}
	return dtoskill.SkillEntry{Meta: m}
}

func mkCfg() TierConfig {
	cfg := DefaultTierConfig()
	// 缩小 budget 让 tier 边界更易构造测试
	cfg.Budget = 1000
	cfg.HotChars = 600
	cfg.WarmChars = 200
	cfg.ColdChars = 80
	return cfg
}

func TestAssignTiers_PinnedAlwaysHot(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	entries := []dtoskill.SkillEntry{
		mkEntry("pinned", func(m *dtoskill.SkillMeta) { m.Pinned = true }),
		mkEntry("normal", nil), // score=0
	}
	got := AssignTiers(entries, nil, nil, mkCfg(), now)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	// pinned 排第一且必为 Hot
	if got[0].Skill.Meta.Name != "pinned" || got[0].Tier != TierHot {
		t.Errorf("pinned should be first + Hot, got %+v", got[0])
	}
	// normal 是 score=0 + 非 pinned/grace → Frozen
	if got[1].Skill.Meta.Name != "normal" || got[1].Tier != TierFrozen {
		t.Errorf("normal score=0 should be Frozen, got %+v", got[1])
	}
}

func TestAssignTiers_GraceIsHot(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	graceTime := now.Add(-2 * 24 * time.Hour).Format(time.RFC3339) // 2 days ago, within 7-day grace
	entries := []dtoskill.SkillEntry{
		mkEntry("fresh", func(m *dtoskill.SkillMeta) { m.InstalledAt = graceTime }),
	}
	got := AssignTiers(entries, nil, nil, mkCfg(), now)
	if len(got) != 1 || got[0].Tier != TierHot {
		t.Errorf("grace skill should be Hot, got %+v", got)
	}
}

func TestAssignTiers_PinnedBeatsGrace(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	graceTime := now.Add(-2 * 24 * time.Hour).Format(time.RFC3339)
	cfg := mkCfg()
	// 让 budget 只够 1 个 Hot：HotChars=600, Budget=600
	cfg.Budget = 600
	entries := []dtoskill.SkillEntry{
		mkEntry("grace", func(m *dtoskill.SkillMeta) { m.InstalledAt = graceTime }),
		mkEntry("pinned", func(m *dtoskill.SkillMeta) { m.Pinned = true }),
	}
	got := AssignTiers(entries, nil, nil, cfg, now)
	// pinned 应排第一并占用 Hot；grace 退回更低 tier
	if got[0].Skill.Meta.Name != "pinned" || got[0].Tier != TierHot {
		t.Errorf("pinned should be first Hot, got %+v", got[0])
	}
	// budget 已用完；grace 退到 Warm 或 Cold（取决于剩余 0）
	// 实际 Budget=600 - 600 = 0；grace 进 Frozen 或更低
	// 注：grace 强制 Hot 本意是优先级而非 tier 强制，实际仍受 budget 约束
	if got[1].Tier == TierHot {
		t.Errorf("grace shouldn't get Hot when budget exhausted, got %+v", got[1])
	}
}

func TestAssignTiers_ScoreOrdersDescending(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	// 三个 normal skill，给 ws 不同 calls 数（≥ minCalls 阈值才用 ws-only）
	wsStats := Stats{
		"high": &SkillStats{Calls: repeatTime(now, 20)},
		"mid":  &SkillStats{Calls: repeatTime(now, 15)},
		"low":  &SkillStats{Calls: repeatTime(now, 10)},
	}
	entries := []dtoskill.SkillEntry{
		mkEntry("low", nil),
		mkEntry("high", nil),
		mkEntry("mid", nil),
	}
	got := AssignTiers(entries, wsStats, nil, mkCfg(), now)
	// 期望顺序：high → mid → low
	if got[0].Skill.Meta.Name != "high" || got[1].Skill.Meta.Name != "mid" || got[2].Skill.Meta.Name != "low" {
		t.Errorf("score order wrong: %+v", []string{got[0].Skill.Meta.Name, got[1].Skill.Meta.Name, got[2].Skill.Meta.Name})
	}
}

func TestAssignTiers_BudgetExhaustedDegrades(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	// Budget=1000, Hot=600, Warm=200, Cold=80
	// 1 个 Hot (600) → 余 400 → 1 个 Hot 又来不及（400 < 600）→ Warm (200) 余 200 → 又一个 Warm 余 0 → Cold(80) 也不够 → Frozen
	wsStats := Stats{
		"a": &SkillStats{Calls: repeatTime(now, 20)},
		"b": &SkillStats{Calls: repeatTime(now, 19)},
		"c": &SkillStats{Calls: repeatTime(now, 18)},
		"d": &SkillStats{Calls: repeatTime(now, 17)},
	}
	entries := []dtoskill.SkillEntry{
		mkEntry("a", nil), mkEntry("b", nil), mkEntry("c", nil), mkEntry("d", nil),
	}
	got := AssignTiers(entries, wsStats, nil, mkCfg(), now)
	if got[0].Tier != TierHot {
		t.Errorf("a should be Hot, got %v", got[0].Tier)
	}
	if got[1].Tier != TierWarm {
		t.Errorf("b should be Warm (budget left 400 after Hot=600), got %v", got[1].Tier)
	}
	if got[2].Tier != TierWarm {
		t.Errorf("c should also be Warm (200 left after b's 200), got %v", got[2].Tier)
	}
	if got[3].Tier != TierFrozen {
		t.Errorf("d should be Frozen (budget exhausted), got %v", got[3].Tier)
	}
}

func TestAssignTiers_DisabledSkipped(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	entries := []dtoskill.SkillEntry{
		mkEntry("dead", func(m *dtoskill.SkillMeta) { m.Disabled = true; m.Pinned = true }),
		mkEntry("alive", nil),
	}
	got := AssignTiers(entries, nil, nil, mkCfg(), now)
	if len(got) != 1 || got[0].Skill.Meta.Name != "alive" {
		t.Errorf("disabled should be skipped: %+v", got)
	}
}

func TestAssignTiers_ZeroScoreNonPinnedFrozen(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	entries := []dtoskill.SkillEntry{mkEntry("idle", nil)}
	got := AssignTiers(entries, nil, nil, mkCfg(), now)
	if got[0].Tier != TierFrozen {
		t.Errorf("score=0 + non-pinned + non-grace → Frozen, got %v", got[0].Tier)
	}
}

func TestWithinGrace_InvalidTimestampReturnsFalse(t *testing.T) {
	if withinGrace("not-a-time", 7*24*time.Hour, time.Now()) {
		t.Error("invalid timestamp should be conservative false")
	}
	if withinGrace("", 7*24*time.Hour, time.Now()) {
		t.Error("empty installed_at → false")
	}
	if withinGrace(time.Now().Format(time.RFC3339), 0, time.Now()) {
		t.Error("zero grace duration → false")
	}
}

func repeatTime(t time.Time, n int) []time.Time {
	out := make([]time.Time, n)
	for i := range out {
		out[i] = t
	}
	return out
}
