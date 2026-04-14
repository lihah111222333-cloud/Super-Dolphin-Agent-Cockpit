package prompt

import (
	"context"
	"fmt"
	"strings"

	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	DynamicSectionSessionGuidance      = "session_guidance"
	DynamicSectionMemory               = "memory"
	DynamicSectionAgentMemory          = "agent_memory"
	DynamicSectionMemoryContext        = "memory_context"
	DynamicSectionEnvInfoSimple        = "env_info_simple"
	DynamicSectionLanguage             = "language"
	DynamicSectionMCPInstructions      = "mcp_instructions"
	DynamicSectionOutputStyle          = "output_style"
	DynamicSectionScratchpad           = "scratchpad"
	DynamicSectionFRC                  = "frc"
	DynamicSectionSummarizeToolResults = "summarize_tool_results"
	DynamicSectionNumericLengthAnchors = "numeric_length_anchors"
	DynamicSectionTokenBudget          = "token_budget"
	DynamicSectionBrief                = "brief"
	DynamicSectionAntModelOverride     = "ant_model_override"
)

type DynamicSectionProvider interface {
	SectionName() string
	Resolve(ctx context.Context, input SectionContext) (*string, error)
}

type InvalidationAwareProvider interface {
	OnPromptInvalidate(reason InvalidateReason)
}

type DynamicTextProvider struct {
	Name        string
	ResolveFunc func(context.Context, SectionContext) (*string, error)
}

type CachePolicy int

const (
	CacheByName CachePolicy = iota
	Uncached
	InputScoped
)

type dynamicSectionSpec struct {
	name        string
	order       int
	cachePolicy CachePolicy
	startOnly   bool
}

var dynamicSectionSpecs = []dynamicSectionSpec{
	{name: DynamicSectionSessionGuidance, order: 110, cachePolicy: InputScoped},
	{name: DynamicSectionMemory, order: 120, cachePolicy: InputScoped, startOnly: true},
	{name: DynamicSectionAgentMemory, order: 123, cachePolicy: InputScoped, startOnly: true},
	{name: DynamicSectionMemoryContext, order: 125, cachePolicy: InputScoped},
	{name: DynamicSectionEnvInfoSimple, order: 130, cachePolicy: InputScoped},
	{name: DynamicSectionLanguage, order: 140, cachePolicy: InputScoped},
	{name: DynamicSectionMCPInstructions, order: 150, cachePolicy: Uncached},
	{name: DynamicSectionOutputStyle, order: 200, cachePolicy: CacheByName},
	{name: DynamicSectionScratchpad, order: 210, cachePolicy: CacheByName},
	{name: DynamicSectionFRC, order: 220, cachePolicy: CacheByName},
	{name: DynamicSectionSummarizeToolResults, order: 230, cachePolicy: CacheByName},
	{name: DynamicSectionNumericLengthAnchors, order: 240, cachePolicy: CacheByName},
	{name: DynamicSectionTokenBudget, order: 250, cachePolicy: CacheByName},
	{name: DynamicSectionBrief, order: 260, cachePolicy: CacheByName},
	{name: DynamicSectionAntModelOverride, order: 270, cachePolicy: CacheByName},
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

var _ DynamicSectionProvider = SessionGuidanceProvider{}

type SessionGuidanceProvider struct{}

func (SessionGuidanceProvider) SectionName() string {
	return DynamicSectionSessionGuidance
}

func (SessionGuidanceProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	enabled := sessionGuidanceToolSet(input.BuildCtx.EnabledTools)
	items := make([]string, 0, 3)
	if _, ok := enabled["request_user_input"]; ok {
		items = append(items, "If a tool call is denied and the reason is unclear, use `request_user_input` to ask the user a focused follow-up.")
	}
	if _, ok := enabled["spawn_agent"]; ok {
		items = append(items, "Use `spawn_agent` only for well-scoped parallel subtasks. Keep urgent blocking work local, give subagents clear ownership, and integrate their results before reporting completion.")
		if sessionGuidanceFlagEnabled(input.BuildCtx.SessionFlags, "verification_required", "require_verification", "verification_agent") {
			items = append(items, "When non-trivial implementation happens, schedule an independent verification pass before you report completion.")
		}
	}
	if len(items) == 0 {
		return nil, nil
	}
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, "# Session-specific guidance")
	for _, item := range items {
		lines = append(lines, "- "+item)
	}
	text := strings.Join(lines, "\n")
	return &text, nil
}

func sessionGuidanceToolSet(tools []string) map[string]struct{} {
	set := make(map[string]struct{}, len(tools))
	for _, tool := range sortedPromptValues(tools) {
		set[tool] = struct{}{}
	}
	return set
}

func sessionGuidanceFlagEnabled(flags map[string]bool, keys ...string) bool {
	for _, key := range keys {
		if flags[key] {
			return true
		}
	}
	return false
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
		Name:        spec.name,
		Order:       spec.order,
		Region:      PromptRegionDynamic,
		Volatile:    spec.cachePolicy == Uncached,
		CachePolicy: spec.cachePolicy,
		StartOnly:   spec.startOnly,
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

func inputScopedSectionDependency(section PromptSection, input SectionContext) any {
	switch section.Name {
	case DynamicSectionSessionGuidance:
		return struct {
			Section      string   `json:"section"`
			EnabledTools []string `json:"enabledTools,omitempty"`
			SessionFlags []string `json:"sessionFlags,omitempty"`
		}{
			Section:      section.Name,
			EnabledTools: sortedPromptValues(input.BuildCtx.EnabledTools),
			SessionFlags: trueFlagKeys(input.BuildCtx.SessionFlags),
		}
	case DynamicSectionMemory, DynamicSectionAgentMemory:
		isChild, agentType := childAgentCacheDependency(input)
		return struct {
			Section   string `json:"section"`
			IsChild   bool   `json:"isChild,omitempty"`
			AgentType string `json:"agentType,omitempty"`
		}{Section: section.Name, IsChild: isChild, AgentType: agentType}
	case DynamicSectionMemoryContext:
		threadID := ""
		userText := ""
		if input.Turn != nil {
			threadID = strings.TrimSpace(input.Turn.ThreadID)
			userText = strings.TrimSpace(input.Turn.UserText)
		}
		return struct {
			Section  string `json:"section"`
			ThreadID string `json:"threadId,omitempty"`
			UserText string `json:"userText,omitempty"`
		}{Section: section.Name, ThreadID: threadID, UserText: userText}
	case DynamicSectionEnvInfoSimple:
		return struct {
			Section                      string   `json:"section"`
			CWD                          string   `json:"cwd,omitempty"`
			GitRoot                      string   `json:"gitRoot,omitempty"`
			IsWorktree                   bool     `json:"isWorktree,omitempty"`
			Platform                     string   `json:"platform,omitempty"`
			Shell                        string   `json:"shell,omitempty"`
			OSVersion                    string   `json:"osVersion,omitempty"`
			LanguageServerTools          []string `json:"languageServerTools,omitempty"`
			AdditionalWorkingDirectories []string `json:"additionalWorkingDirectories,omitempty"`
			Provider                     string   `json:"provider,omitempty"`
			ModelMetadata                string   `json:"modelMetadata,omitempty"`
			KnowledgeCutoff              string   `json:"knowledgeCutoff,omitempty"`
			FrontierGuidance             string   `json:"frontierGuidance,omitempty"`
		}{
			Section:                      section.Name,
			CWD:                          currentPromptCWD(input.BuildCtx),
			GitRoot:                      strings.TrimSpace(input.BuildCtx.GitRoot),
			IsWorktree:                   input.BuildCtx.IsWorktree,
			Platform:                     promptPlatform(),
			Shell:                        promptShellName(),
			OSVersion:                    promptUnameSR(),
			LanguageServerTools:          sectionLanguageServerTools(input.BuildCtx),
			AdditionalWorkingDirectories: sortedPromptValues(input.BuildCtx.AdditionalWorkingDirectories),
			Provider:                     strings.TrimSpace(input.BuildCtx.Provider),
			ModelMetadata:                promptModelMetadata(input.BuildCtx),
			KnowledgeCutoff:              promptKnowledgeCutoff(input.BuildCtx),
			FrontierGuidance:             promptFrontierGuidance(input.BuildCtx),
		}
	case DynamicSectionLanguage:
		return struct {
			Section  string `json:"section"`
			Language string `json:"language,omitempty"`
		}{Section: section.Name, Language: strings.TrimSpace(input.BuildCtx.Language)}
	default:
		return struct {
			Section string `json:"section"`
		}{Section: section.Name}
	}
}

func childAgentCacheDependency(input SectionContext) (bool, string) {
	if input.Start == nil || input.Turn != nil || strings.TrimSpace(input.Start.ParentAgentID) == "" {
		return false, ""
	}
	agentType := strings.TrimSpace(shared.FirstNonEmpty(input.Start.AgentType, input.Start.Name))
	if agentType == "" {
		return false, ""
	}
	return true, agentType
}

func trueFlagKeys(flags map[string]bool) []string {
	keys := make([]string, 0, len(flags))
	for key, enabled := range flags {
		if enabled {
			keys = append(keys, key)
		}
	}
	return sortedPromptValues(keys)
}

func sectionLanguageServerTools(build BuildCtx) []string {
	tools := make([]string, 0, len(build.EnabledTools))
	for _, tool := range sortedPromptValues(build.EnabledTools) {
		if strings.HasPrefix(tool, "lsp_") {
			tools = append(tools, tool)
		}
	}
	return tools
}
