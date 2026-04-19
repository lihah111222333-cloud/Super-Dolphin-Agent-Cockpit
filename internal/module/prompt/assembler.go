package prompt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"golang.org/x/sync/errgroup"
)

type computedSectionValue struct {
	Value *string
}

const simpleStartIdentityLine = "You are Claude Code, Anthropic's official CLI for Claude."

func (s *service) AssembleStart(ctx context.Context, in StartInput) (StartAssembly, error) {
	if err := ctx.Err(); err != nil {
		return StartAssembly{}, err
	}
	if simpleStartEnabled(in) {
		return s.simpleStartAssembly(ctx, in), nil
	}

	resolved, err := s.resolveSections(ctx, s.startSections(), buildStartSectionContext(in))
	if err != nil {
		s.logBuildFallback("start", err)
		return s.fallbackStartAssembly(ctx, in), nil
	}
	boundary := startAssemblyBoundary(resolved, strings.TrimSpace(in.BaseInstructions))
	base := joinBlocks(boundaryCachedPrefix(boundary), boundaryUncachedTail(boundary))
	// Append system prompt content (currentDate, runtimeExtras, git status)
	// to baseInstructions so it is sent once in thread/start, not per-turn.
	if systemPrompt := s.buildStartSystemPrompt(ctx, buildStartCtx(in), resolved); systemPrompt != "" {
		base = joinBlocks(base, systemPrompt)
	}
	dev := strings.TrimSpace(in.DeveloperInstructions)
	displayName := shared.FirstNonEmpty(strings.TrimSpace(in.Name), strings.TrimSpace(in.Prompt))
	return StartAssembly{
		DisplayName:           displayName,
		BaseInstructions:      base,
		Boundary:              clonePromptBoundary(boundary),
		DeveloperInstructions: dev,
		ResolvedSections:      resolved,
		Snapshot:              s.newSnapshot(displayName, base, dev, in.Provider, boundary, resolved),
	}, nil
}

func simpleStartEnabled(in StartInput) bool {
	if parseBoolEnv(envClaudeSimple, false) {
		return true
	}
	for _, key := range []string{"simple_mode", "simpleMode", "simple"} {
		if in.SessionFlags[key] {
			return true
		}
	}
	return false
}

func (s *service) simpleStartAssembly(ctx context.Context, in StartInput) StartAssembly {
	displayName := shared.FirstNonEmpty(strings.TrimSpace(in.Name), strings.TrimSpace(in.Prompt))
	buildCtx := buildStartCtx(in)
	base := strings.Join([]string{
		simpleStartIdentityLine,
		"CWD: " + currentPromptCWD(buildCtx),
	}, "\n")
	if systemPrompt := s.buildStartSystemPrompt(ctx, buildCtx, nil); systemPrompt != "" {
		base = joinBlocks(base, systemPrompt)
	}
	dev := strings.TrimSpace(in.DeveloperInstructions)
	return StartAssembly{
		DisplayName:           displayName,
		BaseInstructions:      base,
		DeveloperInstructions: dev,
		Snapshot:              s.newSnapshot(displayName, base, dev, in.Provider, nil, nil),
	}
}

func (s *service) AssembleTurn(ctx context.Context, in TurnInput) (TurnAssembly, error) {
	if err := ctx.Err(); err != nil {
		return TurnAssembly{}, err
	}

	sectionCtx := buildTurnSectionContext(in)
	resolved, err := s.resolveSections(ctx, s.dynamicSections(), sectionCtx)
	if err != nil {
		s.logBuildFallback("turn", err)
		return TurnAssembly{}, nil
	}
	buildCtx := sectionCtx.BuildCtx
	sources := s.resolveClaudeMdSources(ctx, buildCtx)
	base := s.buildBaseUserContext(ctx, sources)
	extras := CollectRuntimeUserContext(in, resolved)
	merged := MergeRuntimeUserContext(base, extras)
	attachments := s.resolveDynamicTurnAttachments(ctx, sectionCtx)
	if provider, ok := s.claudeMdProvider.(contract.TurnAttachmentProvider); ok {
		attachments = append(attachments, provider.ResolveTurnAttachments(ctx, buildCtx, in, sources)...)
	}
	return TurnAssembly{
		UserContext:      map[string]string(cloneUserContextPayload(merged)),
		UserContextText:  FormatUserContextText(merged),
		SystemContext:    s.buildSystemContext(ctx, buildCtx),
		Attachments:      attachments,
		ResolvedSections: resolved,
	}, nil
}

func (s *service) Invalidate(ctx context.Context, reason InvalidateReason) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	generation := s.cache.InvalidateAll(reason)
	if s.userContextCache != nil {
		s.userContextCache.InvalidateAll()
	}
	s.notifyInvalidationProviders(reason)
	if s.logger != nil {
		s.logger.Debug("prompt cache invalidated", "reason", reason, "generation", generation)
	}
	return nil
}

func (s *service) resolveSections(ctx context.Context, sections []PromptSection, input SectionContext) ([]ResolvedPromptSection, error) {
	if len(sections) == 0 {
		return nil, nil
	}
	group, groupCtx := errgroup.WithContext(ctx)
	items := make([]*ResolvedPromptSection, len(sections))
	for idx, section := range sections {
		group.Go(func() error {
			if err := groupCtx.Err(); err != nil {
				return err
			}
			item, err := s.resolveSection(groupCtx, section, input)
			if err != nil {
				return err
			}
			items[idx] = item
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	resolved := make([]ResolvedPromptSection, 0, len(items))
	for _, item := range items {
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
	cacheKey, cacheable := sectionInputCacheKey(section, input)
	generation := s.cache.Generation()
	if cacheable && !section.Volatile {
		if cached, ok := s.cache.Lookup(cacheKey, generation); ok {
			return resolvedSection(section, cached), nil
		}
	}
	value, err := s.computeSection(ctx, generation, section, cacheKey, cacheable, input)
	if err != nil {
		return nil, err
	}
	return resolvedSection(section, value), nil
}

func (s *service) computeSection(ctx context.Context, generation uint64, section PromptSection, cacheKey string, cacheable bool, input SectionContext) (*string, error) {
	if section.Compute == nil {
		return nil, nil
	}
	if section.Volatile {
		value, err := section.Compute(ctx, input)
		if err != nil {
			return nil, err
		}
		if !cacheable {
			return cloneStringPtr(value), nil
		}
		stable, _ := s.cache.ObserveVolatile(cacheKey, generation, value)
		return stable, nil
	}
	if !cacheable {
		value, err := section.Compute(ctx, input)
		if err != nil {
			return nil, err
		}
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

func (s *service) fallbackStartAssembly(ctx context.Context, in StartInput) StartAssembly {
	displayName := shared.FirstNonEmpty(strings.TrimSpace(in.Name), strings.TrimSpace(in.Prompt))
	base := strings.TrimSpace(in.BaseInstructions)
	if systemPrompt := s.buildStartSystemPrompt(ctx, buildStartCtx(in), nil); systemPrompt != "" {
		base = joinBlocks(base, systemPrompt)
	}
	dev := strings.TrimSpace(in.DeveloperInstructions)
	return StartAssembly{
		DisplayName:           displayName,
		BaseInstructions:      base,
		DeveloperInstructions: dev,
		Snapshot:              s.newSnapshot(displayName, base, dev, in.Provider, nil, nil),
	}
}

// buildStartSystemPrompt constructs the one-time system prompt block that is
// injected into baseInstructions at session start. It includes currentDate,
// runtimeExtras, and system context (git status). This avoids the previous
// per-turn injection which wasted tokens by repeating on every message.
func (s *service) buildStartSystemPrompt(ctx context.Context, buildCtx BuildCtx, resolved []ResolvedPromptSection) string {
	date := fmt.Sprintf("Today's date is %s.", time.Now().Format("2006-01-02"))
	extraContents := runtimeExtraContents(resolved)
	runtimeExtras := strings.TrimSpace(joinBlocks(
		runtimeExtrasRelevanceDisclaimer,
		joinBlocks(extraContents...),
	))
	userCtxText := contract.FormatUserContextText(map[string]string{
		"currentDate":   date,
		"runtimeExtras": runtimeExtras,
	})
	reminder := contract.WrapSystemReminder(userCtxText)
	systemCtx := contract.FormatSystemContextBlock(s.buildSystemContext(ctx, buildCtx))
	return joinBlocks(reminder, systemCtx)
}

func (s *service) newSnapshot(
	displayName, base, dev, provider string,
	boundary *dto.PromptAssemblyBoundary,
	sections []ResolvedPromptSection,
) PromptAssemblySnapshot {
	provider = strings.TrimSpace(provider)
	return PromptAssemblySnapshot{
		DisplayName:           displayName,
		BaseInstructions:      base,
		Boundary:              clonePromptBoundary(boundary),
		DeveloperInstructions: dev,
		Provider:              provider,
		Version:               SnapshotVersion,
		Hash:                  snapshotHash(snapshotHashParts(displayName, base, dev, provider, boundary)...),
		SectionSnapshot:       resolvedSectionSnapshot(sections),
		Generation:            s.cache.Generation(),
	}
}

func (s *service) logBuildFallback(stage string, err error) {
	if s.logger != nil && err != nil {
		s.logger.Warn("prompt assembly fallback", "stage", stage, "error", err)
	}
}

func (s *service) notifyInvalidationProviders(reason InvalidateReason) {
	if s == nil {
		return
	}
	s.dynamicMu.RLock()
	providers := make([]DynamicSectionProvider, 0, len(s.dynamic))
	for _, provider := range s.dynamic {
		providers = append(providers, provider)
	}
	s.dynamicMu.RUnlock()
	for _, provider := range providers {
		aware, ok := provider.(InvalidationAwareProvider)
		if ok {
			aware.OnPromptInvalidate(reason)
		}
	}
	if aware, ok := s.claudeMdProvider.(InvalidationAwareProvider); ok {
		aware.OnPromptInvalidate(reason)
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
		ClaudeMdExcludes:             append([]string(nil), in.ClaudeMdExcludes...),
		MCPSnapshot:                  copyMCPSnapshot(in.MCPSnapshot),
		SessionFlags:                 copyFlags(in.SessionFlags),
		Summary:                      strings.TrimSpace(in.Summary),
		OutputStyleConfig:            copyOutputStyleConfig(in.OutputStyleConfig),
		ScratchpadDir:                strings.TrimSpace(in.ScratchpadDir),
		FRCConfig:                    copyFRCConfig(in.FRCConfig),
		KeepCodingInstructions:       copyOptionalBool(in.KeepCodingInstructions),
		// P20.4：透传创线期选中 skill 到 BuildCtx，供 SkillCatalogProvider 按 pin/force 策略渲染。
		LaunchSkillNames:  append([]string(nil), in.LaunchSkillNames...),
		ForceLaunchSkills: in.ForceLaunchSkills,
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
		ClaudeMdExcludes:             append([]string(nil), in.ClaudeMdExcludes...),
		MCPSnapshot:                  copyMCPSnapshot(in.MCPSnapshot),
		SessionFlags:                 copyFlags(in.SessionFlags),
		Summary:                      strings.TrimSpace(in.Summary),
		OutputStyleConfig:            copyOutputStyleConfig(in.OutputStyleConfig),
		ScratchpadDir:                strings.TrimSpace(in.ScratchpadDir),
		FRCConfig:                    copyFRCConfig(in.FRCConfig),
		KeepCodingInstructions:       copyOptionalBool(in.KeepCodingInstructions),
	}
}

func copyMCPSnapshot(snapshot MCPSnapshot) MCPSnapshot {
	cloned := MCPSnapshot{
		Servers:                  append([]string(nil), snapshot.Servers...),
		Tools:                    append([]string(nil), snapshot.Tools...),
		InstructionsDeltaEnabled: snapshot.InstructionsDeltaEnabled,
		InstructionAttachments:   append([]MCPAttachmentRef(nil), snapshot.InstructionAttachments...),
	}
	if len(snapshot.Instructions) > 0 {
		cloned.Instructions = make(map[string]string, len(snapshot.Instructions))
		maps.Copy(cloned.Instructions, snapshot.Instructions)
	}
	return cloned
}

type dynamicTurnAttachmentProvider interface {
	ResolveTurnAttachments(context.Context, SectionContext) []dto.AttachmentEnvelope
}

func (s *service) resolveDynamicTurnAttachments(ctx context.Context, sectionCtx SectionContext) []dto.AttachmentEnvelope {
	if s == nil {
		return nil
	}
	sections := s.dynamicSections()
	attachments := make([]dto.AttachmentEnvelope, 0, len(sections))
	s.dynamicMu.RLock()
	defer s.dynamicMu.RUnlock()
	for _, section := range sections {
		provider, ok := s.dynamic[section.Name]
		if !ok || section.StartOnly {
			continue
		}
		attachmentProvider, ok := provider.(dynamicTurnAttachmentProvider)
		if !ok {
			continue
		}
		attachments = append(attachments, attachmentProvider.ResolveTurnAttachments(ctx, sectionCtx)...)
	}
	return attachments
}

func copyOutputStyleConfig(cfg *OutputStyleConfig) *OutputStyleConfig {
	if cfg == nil {
		return nil
	}
	cloned := *cfg
	cloned.KeepCodingInstructions = copyOptionalBool(cfg.KeepCodingInstructions)
	return &cloned
}

func copyFRCConfig(cfg *contract.FRCConfig) *contract.FRCConfig {
	if cfg == nil {
		return nil
	}
	return cfg.Normalize()
}

func copyOptionalBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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

func renderResolvedSectionsByRegion(sections []ResolvedPromptSection, region PromptRegion) string {
	blocks := make([]string, 0, len(sections))
	for _, section := range sections {
		if section.Region != region {
			continue
		}
		if content := strings.TrimSpace(section.Content); content != "" {
			blocks = append(blocks, content)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func startAssemblyBoundary(resolved []ResolvedPromptSection, baseTail string) *dto.PromptAssemblyBoundary {
	prefix := renderResolvedSectionsByRegion(resolved, PromptRegionStatic)
	tail := joinBlocks(renderResolvedSectionsByRegion(resolved, PromptRegionDynamic), baseTail)
	if prefix == "" && tail == "" {
		return nil
	}
	return &dto.PromptAssemblyBoundary{
		CachedPrefix: prefix,
		UncachedTail: tail,
	}
}

func resolvedSectionSnapshot(sections []ResolvedPromptSection) map[string]string {
	if len(sections) == 0 {
		return nil
	}
	snapshot := make(map[string]string, len(sections))
	for _, section := range sections {
		name := strings.TrimSpace(section.Name)
		content := strings.TrimSpace(section.Content)
		if name != "" && content != "" {
			snapshot[name] = content
		}
	}
	if len(snapshot) == 0 {
		return nil
	}
	return snapshot
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

func clonePromptBoundary(boundary *dto.PromptAssemblyBoundary) *dto.PromptAssemblyBoundary {
	if boundary == nil {
		return nil
	}
	cloned := dto.PromptAssemblyBoundary{
		CachedPrefix: strings.TrimSpace(boundary.CachedPrefix),
		UncachedTail: strings.TrimSpace(boundary.UncachedTail),
	}
	if cloned.CachedPrefix == "" && cloned.UncachedTail == "" {
		return nil
	}
	return &cloned
}

func boundaryCachedPrefix(boundary *dto.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return strings.TrimSpace(boundary.CachedPrefix)
}

func boundaryUncachedTail(boundary *dto.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return strings.TrimSpace(boundary.UncachedTail)
}

func snapshotHashParts(displayName, base, dev, provider string, boundary *dto.PromptAssemblyBoundary) []string {
	parts := []string{displayName, base, dev, provider}
	return append(parts, boundaryCachedPrefix(boundary), boundaryUncachedTail(boundary))
}

func snapshotHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
