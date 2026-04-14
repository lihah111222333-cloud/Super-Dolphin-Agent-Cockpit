package prompt

import (
	"context"
	"fmt"
	"strings"
)

const (
	DynamicSectionSessionGuidance = "session_guidance"
	DynamicSectionMemory          = "memory"
	DynamicSectionEnvInfoSimple   = "env_info_simple"
	DynamicSectionLanguage        = "language"
	DynamicSectionMCPInstructions = "mcp_instructions"
)

type DynamicSectionProvider interface {
	SectionName() string
	Resolve(ctx context.Context, input SectionContext) (*string, error)
}

type DynamicTextProvider struct {
	Name        string
	ResolveFunc func(context.Context, SectionContext) (*string, error)
}

type dynamicSectionSpec struct {
	name      string
	order     int
	volatile  bool
	startOnly bool
}

var dynamicSectionSpecs = []dynamicSectionSpec{
	{name: DynamicSectionSessionGuidance, order: 110},
	{name: DynamicSectionMemory, order: 120, startOnly: true},
	{name: DynamicSectionEnvInfoSimple, order: 130},
	{name: DynamicSectionLanguage, order: 140},
	{name: DynamicSectionMCPInstructions, order: 150, volatile: true},
}

func (p DynamicTextProvider) SectionName() string {
	return p.Name
}

func (p DynamicTextProvider) Resolve(ctx context.Context, input SectionContext) (*string, error) {
	if p.ResolveFunc == nil {
		return nil, nil
	}
	return p.ResolveFunc(ctx, input)
}

func DynamicSlotNames() []string {
	names := make([]string, 0, len(dynamicSectionSpecs))
	for _, spec := range dynamicSectionSpecs {
		names = append(names, spec.name)
	}
	return names
}

func (s *service) RegisterDynamicProvider(provider DynamicSectionProvider) error {
	if provider == nil {
		return fmt.Errorf("dynamic section provider is nil")
	}
	name := strings.TrimSpace(provider.SectionName())
	if _, ok := dynamicSectionSpecForName(name); !ok {
		return fmt.Errorf("unknown dynamic section %q", name)
	}

	s.dynamicMu.Lock()
	s.dynamic[name] = provider
	s.dynamicMu.Unlock()
	s.cache.InvalidateSections(name)
	return nil
}

func (s *service) UnregisterDynamicProvider(name string) bool {
	key := strings.TrimSpace(name)
	if key == "" {
		return false
	}

	s.dynamicMu.Lock()
	_, ok := s.dynamic[key]
	delete(s.dynamic, key)
	s.dynamicMu.Unlock()
	if ok {
		s.cache.InvalidateSections(key)
	}
	return ok
}

func (s *service) dynamicSlotSections() []PromptSection {
	sections := make([]PromptSection, 0, len(dynamicSectionSpecs))
	for _, spec := range dynamicSectionSpecs {
		sections = append(sections, s.dynamicSlotSection(spec))
	}
	return sections
}

func (s *service) dynamicSlotSection(spec dynamicSectionSpec) PromptSection {
	return PromptSection{
		Name:      spec.name,
		Order:     spec.order,
		Region:    PromptRegionDynamic,
		Volatile:  spec.volatile,
		StartOnly: spec.startOnly,
		Compute: func(ctx context.Context, input SectionContext) (*string, error) {
			return s.resolveDynamicSection(ctx, spec.name, input)
		},
	}
}

func (s *service) resolveDynamicSection(ctx context.Context, name string, input SectionContext) (*string, error) {
	s.dynamicMu.RLock()
	provider := s.dynamic[name]
	s.dynamicMu.RUnlock()
	if provider == nil {
		return nil, nil
	}
	return provider.Resolve(ctx, input)
}

func dynamicSectionSpecForName(name string) (dynamicSectionSpec, bool) {
	for _, spec := range dynamicSectionSpecs {
		if spec.name == name {
			return spec, true
		}
	}
	return dynamicSectionSpec{}, false
}
