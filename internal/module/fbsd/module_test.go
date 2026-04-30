package fbsd

import (
	"context"
	"testing"
	"time"
)

func TestNewTrackerFromEnv_RespectsFlag(t *testing.T) {
	t.Setenv(envFlag, "on")
	tr, err := NewTrackerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Enabled() {
		t.Error("env=on should enable tracker")
	}
	// 立即 Flush 让 worker 退出（避免后台 goroutine 跑到 30s）
	_ = tr.Flush(context.Background())

	t.Setenv(envFlag, "")
	tr2, err := NewTrackerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if tr2.Enabled() {
		t.Error("env=\"\" should disable tracker")
	}
}

func TestEnvTierConfig_OverridesDefault(t *testing.T) {
	t.Setenv("SKILL_FBSD_BUDGET", "4096")
	t.Setenv("SKILL_FBSD_HALF_LIFE_DAYS", "14")
	t.Setenv("SKILL_FBSD_HOT_CHARS", "500")
	t.Setenv("SKILL_FBSD_WS_WEIGHT", "0.5")
	cfg := EnvTierConfig()
	if cfg.Budget != 4096 {
		t.Errorf("Budget = %d, want 4096", cfg.Budget)
	}
	if cfg.HalfLife != 14*24*time.Hour {
		t.Errorf("HalfLife = %v, want 14d", cfg.HalfLife)
	}
	if cfg.HotChars != 500 {
		t.Errorf("HotChars = %d, want 500", cfg.HotChars)
	}
	if cfg.WSWeight != 0.5 {
		t.Errorf("WSWeight = %v, want 0.5", cfg.WSWeight)
	}
}

func TestEnvTierConfig_InvalidValuesFallback(t *testing.T) {
	t.Setenv("SKILL_FBSD_BUDGET", "not-a-number")
	t.Setenv("SKILL_FBSD_WS_WEIGHT", "1.5") // out of [0,1]
	cfg := EnvTierConfig()
	def := DefaultTierConfig()
	if cfg.Budget != def.Budget {
		t.Errorf("invalid Budget should fallback to default %d, got %d", def.Budget, cfg.Budget)
	}
	if cfg.WSWeight != def.WSWeight {
		t.Errorf("out-of-range WSWeight should fallback to default %v, got %v", def.WSWeight, cfg.WSWeight)
	}
}
