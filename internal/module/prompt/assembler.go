package prompt

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"golang.org/x/sync/errgroup"
)

type computedSectionValue struct {
	Value *string
}

const (
	simpleStartIdentityLine   = "You are Claude Code, Anthropic's official CLI for Claude."
	envPromptStartCurrentDate = "PROMPT_START_CURRENT_DATE"
)

// AssembleStart 组出 thread/start 要交给 provider 的初始提示。
// 这里会带上 memory 规则、系统上下文和 snapshot，provider 侧不要再重拼一份。
func (s *service) AssembleStart(ctx context.Context, in StartInput) (StartAssembly, error) {
	if err := ctx.Err(); err != nil {
		return StartAssembly{}, err
	}
	buildCtx := buildStartCtx(in)
	suppressedTools := s.aggregateSuppressedTools(ctx, strings.TrimSpace(in.CWD), strings.TrimSpace(in.Provider))
	buildCtx.SuppressedTools = suppressedTools
	if _, err := s.resolveClaudeMdSources(ctx, buildCtx); err != nil {
		return StartAssembly{}, err
	}
	if simpleStartEnabled(in) {
		return s.simpleStartAssembly(ctx, in), nil
	}

	resolved, err := s.resolveSections(ctx, s.startSections(), SectionContext{BuildCtx: buildCtx, Start: &in})
	if err != nil {
		if contract.IsCriticalPromptSectionError(err) {
			return StartAssembly{}, err
		}
		s.logBuildFallback("start", err)
		return StartAssembly{}, err
	}
	// Merge DB-sourced prompt_template sections (if any). Static blocks flow
	// into CachedPrefix, dynamic into UncachedTail. Blocks whose EnableWhen
	// rejects the current BuildCtx are filtered out here (Step 3b gate).
	resolved = mergeTemplateSections(resolved, in.BaseInstructionBlocks, buildCtx, in.Prompt)
	boundary := startAssemblyBoundary(resolved, strings.TrimSpace(in.BaseInstructions))
	base := joinBlocks(boundaryCachedPrefix(boundary), boundaryUncachedTail(boundary))
	userMeta := s.buildStartUserMeta(buildCtx, resolved)
	systemCtx := s.buildSystemContext(ctx, buildCtx)
	// Phase 3: keep only the user-configurable prompt hint in BaseInstructions
	// so it stays bytewise stable in the same cwd + BuildCtx. Per-start user
	// meta (currentDate, runtimeExtras) and system context (gitStatus) are
	// intentionally NOT embedded here — they flow through the structured
	// UserContext / UserContextText / SystemContext fields so provider
	// bridges can route them into the synthetic user meta message, leaving
	// the cacheable prefix unaffected by per-turn variance.
	if hint := s.resolvePromptHint(ctx, buildCtx.CWD); hint != "" {
		base = joinBlocks(base, hint)
	}
	dev := strings.TrimSpace(in.DeveloperInstructions)
	displayName := strings.TrimSpace(in.Name)
	return StartAssembly{
		DisplayName:           displayName,
		BaseInstructions:      base,
		Boundary:              clonePromptBoundary(boundary),
		DeveloperInstructions: dev,
		ResolvedSections:      resolved,
		Snapshot:              s.newSnapshot(displayName, base, dev, in.Provider, boundary, resolved),
		SuppressedTools:       append([]string(nil), suppressedTools...),
		UserContext:           map[string]string(cloneUserContextPayload(userMeta)),
		UserContextText:       contract.FormatUserContextText(userMeta),
		SystemContext:         systemCtx,
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

// simpleStartAssembly implements Claude Code's CLAUDE_CODE_SIMPLE fast path
// (prompts.ts L444-454): the system prompt degrades to a strict three-line
// form with no <system-reminder>, no gitStatus, and no runtimeExtras. Phase 1
// tightened this path to match Claude parity; the full start path remains the
// one that emits the layered prompt when CLAUDE_CODE_SIMPLE is unset.
func (s *service) simpleStartAssembly(ctx context.Context, in StartInput) StartAssembly {
	displayName := strings.TrimSpace(in.Name)
	buildCtx := buildStartCtx(in)
	suppressedTools := s.aggregateSuppressedTools(ctx, strings.TrimSpace(in.CWD), strings.TrimSpace(in.Provider))
	buildCtx.SuppressedTools = suppressedTools
	base := strings.Join([]string{
		simpleStartIdentityLine,
		"CWD: " + currentPromptCWD(buildCtx),
		"Date: " + startPromptCurrentDate(),
	}, "\n")
	dev := strings.TrimSpace(in.DeveloperInstructions)
	return StartAssembly{
		DisplayName:           displayName,
		BaseInstructions:      base,
		DeveloperInstructions: dev,
		Snapshot:              s.newSnapshot(displayName, base, dev, in.Provider, nil, nil),
		SuppressedTools:       append([]string(nil), suppressedTools...),
	}
}

// AssembleTurn 只准备这一轮需要的新上下文和附件。
// start 时的系统提示不会在这里重复；要改 start-only 内容，需要重建 start snapshot。
func (s *service) AssembleTurn(ctx context.Context, in TurnInput) (TurnAssembly, error) {
	if err := ctx.Err(); err != nil {
		return TurnAssembly{}, err
	}

	sectionCtx := buildTurnSectionContext(in)
	resolved, err := s.resolveSections(ctx, s.dynamicSections(), sectionCtx)
	if err != nil {
		if contract.IsCriticalPromptSectionError(err) {
			return TurnAssembly{}, err
		}
		s.logBuildFallback("turn", err)
		return TurnAssembly{}, err
	}
	buildCtx := sectionCtx.BuildCtx
	sources, err := s.resolveClaudeMdSources(ctx, buildCtx)
	if err != nil {
		return TurnAssembly{}, err
	}
	base := s.buildBaseUserContext(ctx, sources)
	extras := CollectRuntimeUserContext(in, resolved)
	merged := MergeRuntimeUserContext(base, extras)
	attachments := s.resolveDynamicTurnAttachments(ctx, sectionCtx)
	if provider, ok := s.claudeMdProvider.(contract.TurnAttachmentProvider); ok {
		extraAttachments, err := provider.ResolveTurnAttachments(ctx, buildCtx, in, sources)
		if err != nil {
			return TurnAssembly{}, err
		}
		attachments = append(attachments, extraAttachments...)
	}
	return TurnAssembly{
		UserContext:      map[string]string(cloneUserContextPayload(merged)),
		UserContextText:  contract.FormatUserContextText(merged),
		SystemContext:    s.buildSystemContext(ctx, buildCtx),
		Attachments:      attachments,
		ResolvedSections: resolved,
	}, nil
}

// Invalidate 使已缓存的 prompt 组装结果失效。
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

// resolveSections 解析一组 prompt section。
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

// resolveSection 解析单个 prompt section。
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

// computeSection 计算动态或静态 prompt section 内容。
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

// buildStartUserMeta returns the structured per-start user meta payload
// (currentDate + runtimeExtras). It is the Go equivalent of Claude Code's
// getUserContext() entries for currentDate and runtimeExtras. The caller
// routes this into the synthetic user meta message (see
// contract.RenderUserContextMessage); it is intentionally kept out of the
// cacheable BaseInstructions prefix so daily-varying content does not
// invalidate the Anthropic org ephemeral cache.
func (s *service) buildStartUserMeta(_ BuildCtx, resolved []ResolvedPromptSection) userContextPayload {
	date := fmt.Sprintf("Today's date is %s.", startPromptCurrentDate())
	extraContents := runtimeExtraContents(resolved)
	runtimeExtras := strings.TrimSpace(joinBlocks(
		runtimeExtrasRelevanceDisclaimer,
		joinBlocks(extraContents...),
	))
	meta := userContextPayload{"currentDate": date}
	if runtimeExtras != "" {
		meta["runtimeExtras"] = runtimeExtras
	}
	return meta
}

func startPromptCurrentDate() string {
	if value := strings.TrimSpace(os.Getenv(envPromptStartCurrentDate)); value != "" {
		return value
	}
	return time.Now().Format("2006-01-02")
}

// newSnapshot 保存 start 提示的可恢复版本。
// resume/fork/recover 会复用它，所以不要把 provider 私有状态混进来。
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
		LaunchSkillNames:             append([]string(nil), in.LaunchSkillNames...),
		LaunchSkillRefs:              append([]dto.SkillRef(nil), in.LaunchSkillRefs...),
		ForceLaunchSkills:            in.ForceLaunchSkills,
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
