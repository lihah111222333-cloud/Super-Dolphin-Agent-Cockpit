package skill

import (
	"context"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

const (
	defaultSkillProgressiveDisclosure = false
	defaultSkillTokenBudget           = 3000
)

const (
	SkillMetricL1Tokens            = "skill_l1_tokens"
	SkillMetricExpandTotal         = "skill_expand_total"
	SkillMetricExpandErrorTotal    = "skill_expand_error_total"
	SkillMetricCacheHitTotal       = "skill_cache_hit_total"
	SkillMetricCacheMissTotal      = "skill_cache_miss_total"
	SkillMetricInjectionDecision   = "skill_injection_decision_total"
	SkillMetricContextTokensSaved  = "skill_context_tokens_saved_total"
)

type SkillPolicyInput struct {
	RequestedMode dto.SkillMode
}

type SkillPolicy interface {
	ProgressiveDisclosure() bool
	TokenBudget() int
	Mode(ctx context.Context, input SkillPolicyInput) dto.SkillMode
}

type SkillMetrics interface {
	Add(name string, delta int64)
}

type configBackedSkillPolicy struct {
	cfg *platformconfig.Config
}

type noopSkillMetrics struct{}

func NewDefaultSkillPolicy(cfg *platformconfig.Config) SkillPolicy {
	return &configBackedSkillPolicy{cfg: cfg}
}

func NewNoopSkillMetrics() SkillMetrics {
	return noopSkillMetrics{}
}

func (p *configBackedSkillPolicy) ProgressiveDisclosure() bool {
	if p == nil || p.cfg == nil {
		return defaultSkillProgressiveDisclosure
	}
	return p.cfg.Skill.ProgressiveDisclosure
}

func (p *configBackedSkillPolicy) TokenBudget() int {
	if p == nil || p.cfg == nil {
		return defaultSkillTokenBudget
	}
	return normalizeSkillTokenBudget(p.cfg.Skill.TokenBudget)
}

func (p *configBackedSkillPolicy) Mode(_ context.Context, input SkillPolicyInput) dto.SkillMode {
	return input.RequestedMode.Effective()
}

func (noopSkillMetrics) Add(string, int64) {}

func normalizeSkillTokenBudget(value int) int {
	if value <= 0 {
		return defaultSkillTokenBudget
	}
	return value
}
