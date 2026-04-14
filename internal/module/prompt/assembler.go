package prompt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type computedSectionValue struct {
	Value *string
}

func (s *service) AssembleStart(ctx context.Context, in StartInput) (StartAssembly, error) {
	if err := ctx.Err(); err != nil {
		return StartAssembly{}, err
	}

	resolved, err := s.resolveSections(ctx, s.startSections(), buildStartSectionContext(in))
	if err != nil {
		s.logBuildFallback("start", err)
		return s.fallbackStartAssembly(in), nil
	}
	base := joinBlocks(renderResolvedSections(resolved), strings.TrimSpace(in.BaseInstructions))
	dev := strings.TrimSpace(in.DeveloperInstructions)
	displayName := shared.FirstNonEmpty(strings.TrimSpace(in.Name), strings.TrimSpace(in.Prompt))
	return StartAssembly{
		DisplayName:           displayName,
		BaseInstructions:      base,
		DeveloperInstructions: dev,
		ResolvedSections:      resolved,
		Snapshot:              s.newSnapshot(displayName, base, dev, in.Provider),
	}, nil
}

func (s *service) AssembleTurn(ctx context.Context, in TurnInput) (TurnAssembly, error) {
	if err := ctx.Err(); err != nil {
		return TurnAssembly{}, err
	}

	resolved, err := s.resolveSections(ctx, s.dynamicSections(), buildTurnSectionContext(in))
	if err != nil {
		s.logBuildFallback("turn", err)
		return TurnAssembly{}, nil
	}
	return TurnAssembly{
		UserContextText:  renderResolvedSections(resolved),
		ResolvedSections: resolved,
	}, nil
}

func (s *service) Invalidate(ctx context.Context, reason InvalidateReason) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	generation := s.cache.InvalidateAll(reason)
	if s.logger != nil {
		s.logger.Debug("prompt cache invalidated", "reason", reason, "generation", generation)
	}
	return nil
}

func (s *service) resolveSections(ctx context.Context, sections []PromptSection, input SectionContext) ([]ResolvedPromptSection, error) {
	resolved := make([]ResolvedPromptSection, 0, len(sections))
	for _, section := range sections {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item, err := s.resolveSection(ctx, section, input)
		if err != nil {
			return nil, err
		}
		if item != nil {
			resolved = append(resolved, *item)
		}
	}
	return resolved, nil
}

func (s *service) resolveSection(ctx context.Context, section PromptSection, input SectionContext) (*ResolvedPromptSection, error) {
	if section.StartOnly && input.Start == nil {
		return nil, nil
	}
	cacheKey := sectionInputCacheKey(section, input)
	generation := s.cache.Generation()
	if !section.Volatile {
		if cached, ok := s.cache.Lookup(cacheKey, generation); ok {
			return resolvedSection(section, cached), nil
		}
	}
	value, err := s.computeSection(ctx, generation, section, cacheKey, input)
	if err != nil {
		return nil, err
	}
	return resolvedSection(section, value), nil
}

func (s *service) computeSection(ctx context.Context, generation uint64, section PromptSection, cacheKey string, input SectionContext) (*string, error) {
	if section.Compute == nil {
		return nil, nil
	}
	if section.Volatile {
		value, err := section.Compute(ctx, input)
		if err != nil {
			return nil, err
		}
		s.cache.Store(cacheKey, generation, value)
		return cloneStringPtr(value), nil
	}

	key := fmt.Sprintf("%d:%s", generation, cacheKey)
	result, err, _ := s.flight.Do(key, func() (any, error) {
		if cached, ok := s.cache.Lookup(cacheKey, generation); ok {
			return computedSectionValue{Value: cloneStringPtr(cached)}, nil
		}
		value, err := section.Compute(ctx, input)
		if err != nil {
			return nil, err
		}
		s.cache.Store(cacheKey, generation, value)
		return computedSectionValue{Value: cloneStringPtr(value)}, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(computedSectionValue).Value, nil
}

func sectionInputCacheKey(section PromptSection, input SectionContext) string {
	payload := struct {
		Start *StartInput `json:"start,omitempty"`
		Turn  *TurnInput  `json:"turn,omitempty"`
	}{
		Start: input.Start,
		Turn:  input.Turn,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return section.Name
	}
	digest := sha256.Sum256(encoded)
	return section.Name + ":" + hex.EncodeToString(digest[:])
}

func (s *service) startSections() []PromptSection {
	staticSections := s.staticSections()
	dynamicSections := s.dynamicSections()
	sections := make([]PromptSection, 0, len(staticSections)+len(dynamicSections))
	sections = append(sections, staticSections...)
	sections = append(sections, dynamicSections...)
	return sections
}

func (s *service) staticSections() []PromptSection {
	return s.regionSections(PromptRegionStatic)
}

func (s *service) dynamicSections() []PromptSection {
	return s.regionSections(PromptRegionDynamic)
}

func (s *service) regionSections(region PromptRegion) []PromptSection {
	all := s.Sections()
	sections := make([]PromptSection, 0, len(all))
	for _, section := range all {
		if section.Region == region {
			sections = append(sections, section)
		}
	}
	return sections
}

func (s *service) fallbackStartAssembly(in StartInput) StartAssembly {
	displayName := shared.FirstNonEmpty(strings.TrimSpace(in.Name), strings.TrimSpace(in.Prompt))
	base := strings.TrimSpace(in.BaseInstructions)
	dev := strings.TrimSpace(in.DeveloperInstructions)
	return StartAssembly{
		DisplayName:           displayName,
		BaseInstructions:      base,
		DeveloperInstructions: dev,
		Snapshot:              s.newSnapshot(displayName, base, dev, in.Provider),
	}
}

func (s *service) newSnapshot(displayName, base, dev, provider string) PromptAssemblySnapshot {
	provider = strings.TrimSpace(provider)
	return PromptAssemblySnapshot{
		DisplayName:           displayName,
		BaseInstructions:      base,
		DeveloperInstructions: dev,
		Provider:              provider,
		Version:               SnapshotVersion,
		Hash:                  snapshotHash(displayName, base, dev, provider),
		Generation:            s.cache.Generation(),
	}
}

func (s *service) logBuildFallback(stage string, err error) {
	if s.logger != nil && err != nil {
		s.logger.Warn("prompt assembly fallback", "stage", stage, "error", err)
	}
}

func buildStartSectionContext(in StartInput) SectionContext {
	return SectionContext{BuildCtx: buildStartCtx(in), Start: &in}
}

func buildTurnSectionContext(in TurnInput) SectionContext {
	return SectionContext{BuildCtx: buildTurnCtx(in), Turn: &in}
}

func buildStartCtx(in StartInput) BuildCtx {
	return BuildCtx{
		CWD:                          strings.TrimSpace(in.CWD),
		GitRoot:                      strings.TrimSpace(in.GitRoot),
		IsWorktree:                   in.IsWorktree,
		Language:                     strings.TrimSpace(in.Language),
		Provider:                     strings.TrimSpace(in.Provider),
		Model:                        strings.TrimSpace(in.Model),
		EnabledTools:                 append([]string(nil), in.EnabledTools...),
		AdditionalWorkingDirectories: append([]string(nil), in.AdditionalWorkingDirectories...),
		MCPSnapshot:                  copyMCPSnapshot(in.MCPSnapshot),
		SessionFlags:                 copyFlags(in.SessionFlags),
	}
}

func buildTurnCtx(in TurnInput) BuildCtx {
	return BuildCtx{
		CWD:                          strings.TrimSpace(in.CWD),
		GitRoot:                      strings.TrimSpace(in.GitRoot),
		IsWorktree:                   in.IsWorktree,
		Language:                     strings.TrimSpace(in.Language),
		Provider:                     strings.TrimSpace(in.Provider),
		Model:                        strings.TrimSpace(in.Model),
		EnabledTools:                 append([]string(nil), in.EnabledTools...),
		AdditionalWorkingDirectories: append([]string(nil), in.AdditionalWorkingDirectories...),
		MCPSnapshot:                  copyMCPSnapshot(in.MCPSnapshot),
		SessionFlags:                 copyFlags(in.SessionFlags),
	}
}

func copyMCPSnapshot(snapshot MCPSnapshot) MCPSnapshot {
	cloned := MCPSnapshot{
		Servers: append([]string(nil), snapshot.Servers...),
		Tools:   append([]string(nil), snapshot.Tools...),
	}
	if len(snapshot.Instructions) > 0 {
		cloned.Instructions = make(map[string]string, len(snapshot.Instructions))
		maps.Copy(cloned.Instructions, snapshot.Instructions)
	}
	return cloned
}

func copyFlags(flags map[string]bool) map[string]bool {
	if len(flags) == 0 {
		return nil
	}
	cloned := make(map[string]bool, len(flags))
	maps.Copy(cloned, flags)
	return cloned
}

func resolvedSection(section PromptSection, value *string) *ResolvedPromptSection {
	if value == nil {
		return nil
	}
	content := strings.TrimSpace(*value)
	if content == "" {
		return nil
	}
	return &ResolvedPromptSection{
		Name:     section.Name,
		Region:   section.Region,
		Volatile: section.Volatile,
		Content:  content,
	}
}

func renderResolvedSections(sections []ResolvedPromptSection) string {
	blocks := make([]string, 0, len(sections))
	for _, section := range sections {
		if content := strings.TrimSpace(section.Content); content != "" {
			blocks = append(blocks, content)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func joinBlocks(parts ...string) string {
	blocks := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			blocks = append(blocks, trimmed)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func snapshotHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
