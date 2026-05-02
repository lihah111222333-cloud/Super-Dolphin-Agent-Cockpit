package fbsd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Module 是 fbsd 的 fx wire-up 入口：Provide *Tracker + 注册 OnStop flush 钩子。
var Module = fx.Module("fbsd",
	fx.Provide(NewTrackerFromEnv, ProvideSkillDisclosureTierSource),
	fx.Invoke(registerFlush),
)

// NewTrackerFromEnv 构造 Tracker。FBSD 默认始终启用——历史灰度开关
// SUPER_DOLPHIN_SKILL_FBSD 已在用户决策（C 档清理）下删除。
//
// stats 文件路径：
//   - 全局：~/.multi-agent/skills-stats.json
//   - workspace：~/.multi-agent/workspaces/<host>/skills-stats.json
//
// 当前 workspace ID 用 hostname 简化（spec §9.6 严格定义留 P6.x）；
// multi-user / multi-project 同主机会混淆，建议未来改为 cwd hash。
func NewTrackerFromEnv() (*Tracker, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("fbsd: user home: %w", err)
	}
	globPath := filepath.Join(home, ".multi-agent", "skills-stats.json")
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	wsPath := filepath.Join(home, ".multi-agent", "workspaces", host, "skills-stats.json")
	return NewTracker(wsPath, globPath, true)
}

// registerFlush 注册 fx OnStop 钩子，确保 harness 退出前 flush stats。
// nil tracker 跳过；disabled 时 Flush 自身 no-op。
func registerFlush(lc fx.Lifecycle, t *Tracker) {
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return t.Flush(ctx)
		},
	})
}

// EnvTierConfig 从环境变量加载 spec §9.7 默认参数；缺失字段走默认。
// 主要给 buildSkillManifest 调用方用，统一参数来源。
func EnvTierConfig() TierConfig {
	cfg := DefaultTierConfig()
	applyEnvIntOverrides(&cfg)
	applyEnvWeightOverride(&cfg)
	return cfg
}

// applyEnvIntOverrides 把 8 个 int 型 env 调参项集中用 table-driven 应用到 cfg，
// 避免 EnvTierConfig 主体准则隶样 if-链 超过守卫 CC 上限。
func applyEnvIntOverrides(cfg *TierConfig) {
	type intSetter struct {
		key string
		set func(int)
	}
	dayDur := func(set func(time.Duration)) func(int) {
		return func(v int) { set(time.Duration(v) * 24 * time.Hour) }
	}
	items := []intSetter{
		{"SKILL_FBSD_BUDGET", func(v int) { cfg.Budget = v }},
		{"SKILL_FBSD_HALF_LIFE_DAYS", dayDur(func(d time.Duration) { cfg.HalfLife = d })},
		{"SKILL_FBSD_FROZEN_DAYS", dayDur(func(d time.Duration) { cfg.FrozenDuration = d })},
		{"SKILL_FBSD_GRACE_DAYS", dayDur(func(d time.Duration) { cfg.GraceDuration = d })},
		{"SKILL_FBSD_HOT_CHARS", func(v int) { cfg.HotChars = v }},
		{"SKILL_FBSD_WARM_CHARS", func(v int) { cfg.WarmChars = v }},
		{"SKILL_FBSD_COLD_CHARS", func(v int) { cfg.ColdChars = v }},
		{"SKILL_FBSD_WS_MIN_CALLS", func(v int) { cfg.WSMinCalls = v }},
	}
	for _, it := range items {
		if v := envInt(it.key, 0); v > 0 {
			it.set(v)
		}
	}
}

func applyEnvWeightOverride(cfg *TierConfig) {
	s := os.Getenv("SKILL_FBSD_WS_WEIGHT")
	if s == "" {
		return
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || v > 1 {
		return
	}
	cfg.WSWeight = v
}

func envInt(key string, def int) int {
	if s := os.Getenv(key); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return def
}

func ProvideSkillDisclosureTierSource(t *Tracker) contract.SkillDisclosureTierSource { return t }
