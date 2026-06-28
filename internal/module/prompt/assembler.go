package prompt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"golang.org/x/sync/errgroup"
)

// computedSectionValue 包装 section 计算结果，用于 singleflight 的泛型返回值。
type computedSectionValue struct {
	Value *string
}

const (
	simpleStartIdentityLine   = "You are Claude Code, Anthropic's official CLI for Claude."
	envPromptStartCurrentDate = "PROMPT_START_CURRENT_DATE"
)

// BuildPrefixShape 从现有 start assembly 事实生成可观测形状。
// 返回值只包含 section 名称、字节数和 hash，不能把 prompt 正文写进日志或 wire 诊断。
func BuildPrefixShape(
	base string,
	developer string,
	boundary *contract.PromptAssemblyBoundary,
	sections []ResolvedPromptSection,
	suppressedTools []string,
	reason string,
) contract.PrefixShape {
	staticNames := make([]string, 0, len(sections))
	dynamicNames := make([]string, 0, len(sections))
	h := sha256.New()
	writeShapePart(h, "base", base)
	writeShapePart(h, "developer", developer)
	if boundary != nil {
		writeShapePart(h, "cached", boundary.CachedPrefix)
		writeShapePart(h, "uncached", boundary.UncachedTail)
	}
	for _, section := range sections {
		name := strings.TrimSpace(section.Name)
		if name == "" {
			continue
		}
		writeShapePart(h, name, section.Content)
		if section.Region == PromptRegionStatic && !section.Volatile {
			staticNames = append(staticNames, name)
		} else {
			dynamicNames = append(dynamicNames, name)
		}
	}
	sort.Strings(staticNames)
	sort.Strings(dynamicNames)
	tools := append([]string(nil), suppressedTools...)
	sort.Strings(tools)
	for _, tool := range tools {
		writeShapePart(h, "suppressed_tool", tool)
	}
	return contract.PrefixShape{
		Hash:                hex.EncodeToString(h.Sum(nil)),
		StaticSectionNames:  staticNames,
		DynamicSectionNames: dynamicNames,
		SuppressedToolNames: tools,
		CachedPrefixBytes:   len(promptBoundaryCachedPrefix(boundary)),
		UncachedTailBytes:   len(promptBoundaryUncachedTail(boundary)),
		DeveloperBytes:      len(developer),
		ChurnReason:         strings.TrimSpace(reason),
	}
}

func writeShapePart(h hash.Hash, name, content string) {
	h.Write([]byte(strings.TrimSpace(name)))
	h.Write([]byte{0})
	h.Write([]byte(content))
	h.Write([]byte{0})
}

func promptBoundaryCachedPrefix(boundary *contract.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return boundary.CachedPrefix
}

func promptBoundaryUncachedTail(boundary *contract.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return boundary.UncachedTail
}

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
	// DB 中的 prompt_template 会按 region 进入 cached prefix 或 uncached tail；
	// EnableWhen 不匹配当前 BuildCtx 的块在这里过滤，避免 provider 侧再判断一次。
	resolved = mergeTemplateSections(resolved, in.BaseInstructionBlocks, buildCtx, in.Prompt)
	boundary := startAssemblyBoundary(resolved, strings.TrimSpace(in.BaseInstructions))
	base := joinBlocks(boundaryCachedPrefix(boundary), boundaryUncachedTail(boundary))
	userMeta := s.buildStartUserMeta(buildCtx, resolved)
	systemCtx := s.buildSystemContext(ctx, buildCtx)
	// BaseInstructions 只追加用户可配置的 prompt hint，保持同一 cwd + BuildCtx 下字节稳定。
	// 当前日期、runtime extras 和 gitStatus 走结构化字段，避免逐 turn 变化污染可缓存前缀。
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
		PrefixShape:           BuildPrefixShape(base, dev, boundary, resolved, suppressedTools, ""),
		UserContext:           map[string]string(cloneUserContextPayload(userMeta)),
		UserContextText:       contract.FormatUserContextText(userMeta),
		SystemContext:         systemCtx,
	}, nil
}

// simpleStartEnabled 判断是否启用简化 start 路径（三行提示，不含 section 计算）。
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

// simpleStartAssembly 生成简化 start prompt。
// 该路径只保留身份、CWD 和日期三行，不计算动态 section、gitStatus 或 runtime extras。
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
		PrefixShape:           BuildPrefixShape(base, dev, nil, nil, suppressedTools, ""),
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

// startSections 返回会话启动时参与组装的静态和动态 section，保持静态前缀先于动态上下文。
func (s *service) startSections() []PromptSection {
	staticSections := s.staticSections()
	dynamicSections := s.dynamicSections()
	sections := make([]PromptSection, 0, len(staticSections)+len(dynamicSections))
	sections = append(sections, staticSections...)
	sections = append(sections, dynamicSections...)
	return sections
}

// staticSections 返回静态 region 的 section，用于 cached prefix 和基础指令拼接。
func (s *service) staticSections() []PromptSection {
	return s.regionSections(PromptRegionStatic)
}

// dynamicSections 返回动态 region 的 section，用于 turn/start 阶段按输入实时计算。
func (s *service) dynamicSections() []PromptSection {
	return s.regionSections(PromptRegionDynamic)
}

// regionSections 从注册表筛选指定 region，保留原始排序交由 Sections 统一维护。
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

// fallbackStartAssembly 在 section 解析失败时用原始 BaseInstructions 构建降级 assembly。
// fallbackStartAssembly 在组装失败后保留原始 BaseInstructions 并补齐结构化上下文。
// 该路径仍写 snapshot，保证后续 resume/fork/recover 能拿到一致的降级结果。
func (s *service) fallbackStartAssembly(ctx context.Context, in StartInput) StartAssembly {
	displayName := strings.TrimSpace(in.Name)
	base := strings.TrimSpace(in.BaseInstructions)
	buildCtx := buildStartCtx(in)
	suppressedTools := s.aggregateSuppressedTools(ctx, strings.TrimSpace(in.CWD), strings.TrimSpace(in.Provider))
	buildCtx.SuppressedTools = suppressedTools
	userMeta := s.buildStartUserMeta(buildCtx, nil)
	systemCtx := s.buildSystemContext(ctx, buildCtx)
	if hint := s.resolvePromptHint(ctx, buildCtx.CWD); hint != "" {
		base = joinBlocks(base, hint)
	}
	dev := strings.TrimSpace(in.DeveloperInstructions)
	return StartAssembly{
		DisplayName:           displayName,
		BaseInstructions:      base,
		DeveloperInstructions: dev,
		Snapshot:              s.newSnapshot(displayName, base, dev, in.Provider, nil, nil),
		SuppressedTools:       append([]string(nil), suppressedTools...),
		UserContext:           map[string]string(cloneUserContextPayload(userMeta)),
		UserContextText:       contract.FormatUserContextText(userMeta),
		SystemContext:         systemCtx,
	}
}

// buildStartUserMeta 构建每次 start 都可能变化的结构化 user meta。
// currentDate 与 runtimeExtras 不进入 BaseInstructions，避免日期等动态内容破坏 provider 缓存前缀。
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

// startPromptCurrentDate 返回 start 提示使用的当前日期，优先读环境变量覆盖。
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

// logBuildFallback 在 prompt 组装降级时记录 warn 日志。
func (s *service) logBuildFallback(stage string, err error) {
	if s.logger != nil && err != nil {
		s.logger.Warn("prompt assembly fallback", "stage", stage, "error", err)
	}
}

// notifyInvalidationProviders 通知所有动态提供器以及 claudeMd 提供器缓存已失效。
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

// buildStartSectionContext 从 StartInput 构建 section 上下文。
func buildStartSectionContext(in StartInput) SectionContext {
	return SectionContext{BuildCtx: buildStartCtx(in), Start: &in}
}

// buildTurnSectionContext 从 TurnInput 构建 section 上下文。
func buildTurnSectionContext(in TurnInput) SectionContext {
	return SectionContext{BuildCtx: buildTurnCtx(in), Turn: &in}
}

// buildStartCtx 从 StartInput 构建 BuildCtx，所有字符串字段做 TrimSpace，切片做防御性拷贝。
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

// buildTurnCtx 从 TurnInput 构建 BuildCtx，所有字符串字段做 TrimSpace，切片做防御性拷贝。
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
