package memory

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"go.uber.org/fx"
)

var _ prompt.DynamicSectionProvider = (*MemoryRulesProvider)(nil)

type MemoryRulesProvider struct {
	engine          *MemoryRuleEngine
	autoEnabled     bool
	skipIndex       bool
	extraGuidelines []string
}

type promptProviderParams struct {
	fx.In

	Registry prompt.PromptRegistry `optional:"true"`
	Provider *MemoryRulesProvider  `optional:"true"`
}

func NewRulesProvider(cfg *Config, engine *MemoryRuleEngine) *MemoryRulesProvider {
	autoEnabled := cfg != nil && cfg.Enabled
	skipIndex := cfg != nil && cfg.SkipIndex
	var extraGuidelines []string
	if cfg != nil {
		extraGuidelines = cloneStrings(cfg.ExtraGuidelines)
	}
	if engine == nil {
		engine = NewMemoryRuleEngine()
	}
	return &MemoryRulesProvider{
		engine:          engine,
		autoEnabled:     autoEnabled,
		skipIndex:       skipIndex,
		extraGuidelines: extraGuidelines,
	}
}

func (p *MemoryRulesProvider) SectionName() string {
	return prompt.DynamicSectionMemory
}

func (p *MemoryRulesProvider) Resolve(_ context.Context, input prompt.SectionContext) (*string, error) {
	if p == nil || input.Start == nil || input.Turn != nil {
		return nil, nil
	}
	text := p.engine.LoadMemoryPrompt(MemoryModeStandard, p.autoEnabled, MemoryRuleOptions{
		SkipIndex:       p.skipIndex,
		ExtraGuidelines: p.extraGuidelines,
	})
	if text == nil || strings.TrimSpace(*text) == "" {
		return nil, nil
	}
	wrapped := "## " + prompt.DynamicSectionMemory + "\n\n" + strings.TrimSpace(*text)
	return &wrapped, nil
}

func registerPromptProvider(p promptProviderParams) error {
	if p.Registry == nil || p.Provider == nil {
		return nil
	}
	return p.Registry.RegisterDynamicProvider(p.Provider)
}
