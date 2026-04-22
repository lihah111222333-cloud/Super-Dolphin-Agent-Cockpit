package prompt

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	DynamicSectionSessionGuidance      = contract.DynamicSectionSessionGuidance
	DynamicSectionMemory               = contract.DynamicSectionMemory
	DynamicSectionAgentMemory          = contract.DynamicSectionAgentMemory
	DynamicSectionMemoryContext        = contract.DynamicSectionMemoryContext
	DynamicSectionEnvInfoSimple        = contract.DynamicSectionEnvInfoSimple
	DynamicSectionLanguage             = contract.DynamicSectionLanguage
	DynamicSectionMCPInstructions      = contract.DynamicSectionMCPInstructions
	DynamicSectionOutputStyle          = contract.DynamicSectionOutputStyle
	DynamicSectionScratchpad           = contract.DynamicSectionScratchpad
	DynamicSectionFRC                  = contract.DynamicSectionFRC
	DynamicSectionSummarizeToolResults = contract.DynamicSectionSummarizeToolResults
	DynamicSectionNumericLengthAnchors = contract.DynamicSectionNumericLengthAnchors
	DynamicSectionTokenBudget          = contract.DynamicSectionTokenBudget
	DynamicSectionBrief                = contract.DynamicSectionBrief
	DynamicSectionSkillCatalog         = contract.DynamicSectionSkillCatalog
)

type DynamicSectionProvider = contract.DynamicSectionProvider

type InvalidationAwareProvider = contract.InvalidationAwareProvider

type DynamicTextProvider struct {
	Name        string
	ResolveFunc func(context.Context, SectionContext) (*string, error)
}

type CachePolicy = contract.CachePolicy

const (
	CacheByName = contract.CacheByName
	Uncached    = contract.Uncached
	InputScoped = contract.InputScoped
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
	// P20.1 Phase 10：skill_catalog L1 manifest slot。policy=Uncached 因为
	// provider 每 Resolve 都会调 skill.Service.ListSkills 扫盘（内部已有去抖/
	// revision 缓存），上层不需要 prompt cache 再二次缓存。
	//
	// 灰度：即使 skill_catalog 进入 spec 列表，若 Phase 10 SkillCatalogProvider
	// 未按 cfg.EnableSkillProgressiveDisclosure 注册，resolveDynamicSection()
	// 在 provider==nil 时返回 (nil, nil)，section 渲染为空 —— 等同关闭。
	{name: DynamicSectionSkillCatalog, order: 280, cachePolicy: Uncached},
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
	items := buildSessionGuidanceItems(
		sessionGuidanceToolSet(input.BuildCtx.EnabledTools),
		input.BuildCtx.SessionFlags,
	)
	if len(items) == 0 {
		return nil, nil
	}
	text := renderSessionGuidance(items)
	return &text, nil
}

func buildSessionGuidanceItems(enabled map[string]struct{}, flags map[string]bool) []string {
	items := make([]string, 0, 12)
	if item, ok := sessionGuidanceAskUserItem(enabled); ok {
		items = append(items, item)
	}
	if item, ok := sessionGuidanceInteractiveCommandItem(flags); ok {
		items = append(items, item)
	}
	items = append(items, sessionGuidanceAgentItems(enabled, flags)...)
	items = append(items, sessionGuidanceSkillItems(enabled, flags)...)
	return items
}

func renderSessionGuidance(items []string) string {
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, "# Session-specific guidance")
	for _, item := range items {
		lines = append(lines, "- "+item)
	}
	return strings.Join(lines, "\n")
}

func sessionGuidanceAskUserItem(enabled map[string]struct{}) (string, bool) {
	if !sessionGuidanceToolEnabled(enabled, "request_user_input") {
		return "", false
	}
	return "If a tool call is denied and the reason is unclear, use `request_user_input` to ask the user a focused follow-up.", true
}

func sessionGuidanceInteractiveCommandItem(flags map[string]bool) (string, bool) {
	if sessionGuidanceFlagEnabled(flags, "non_interactive", "nonInteractive", "headless", "headless_mode") {
		return "", false
	}
	return "If you need the user to run an interactive shell command themselves (for example, `gcloud auth login`), ask them to type `! <command>` so it runs in the current session and the output lands in the conversation.", true
}

func sessionGuidanceAgentItems(enabled map[string]struct{}, flags map[string]bool) []string {
	hasSpawn := sessionGuidanceToolEnabled(enabled, "spawn_agent")
	hasManaged := sessionGuidanceToolEnabled(enabled, "orchestration_launch_agent")
	if hasManaged && sessionGuidancePersistentSubagentDefault(flags) {
		hasSpawn = false
	}
	if !hasSpawn && !hasManaged {
		return nil
	}
	items := []string{sessionGuidanceAgentDelegationItem(enabled, flags)}
	if hasSpawn && sessionGuidanceExploreEnabled(flags) && !sessionGuidanceForkMode(flags) {
		items = append(items, sessionGuidanceExploreItem(enabled))
	}
	if hasSpawn && sessionGuidanceVerificationEnabled(flags) {
		items = append(items, sessionGuidanceVerificationItems()...)
	}
	return items
}

func sessionGuidanceAgentDelegationItem(enabled map[string]struct{}, flags map[string]bool) string {
	hasSpawn := sessionGuidanceToolEnabled(enabled, "spawn_agent")
	hasManaged := sessionGuidanceToolEnabled(enabled, "orchestration_launch_agent")
	if hasManaged && sessionGuidancePersistentSubagentDefault(flags) {
		hasSpawn = false
	}
	if hasManaged && sessionGuidancePersistentSubagentDefault(flags) {
		return "When creating a child agent for the user, use `orchestration_launch_agent` so it appears as a persistent UI-visible agent. Give it a short, user-friendly task name rather than an internal slug or generic role label."
	}
	if hasSpawn && sessionGuidanceForkMode(flags) {
		return "This session is using fork-style delegation: use `spawn_agent` for longer background research or implementation that would otherwise flood the main context. If you are already the delegated worker, execute directly and do not bounce the same task into another fork."
	}
	if hasSpawn {
		return "Use `spawn_agent` only for well-scoped parallel subtasks. Keep urgent blocking work local, avoid duplicating delegated work, give each subagent clear ownership, and integrate its results before reporting completion."
	}
	return "Use `orchestration_launch_agent` for child agents that should remain available in the UI as persistent conversations, and give them short, user-friendly task names."
}

func sessionGuidanceExploreItem(enabled map[string]struct{}) string {
	searchTools := sessionGuidanceDirectedSearchTools(enabled)
	return "For simple, directed codebase searches, use " + searchTools + " directly. Use an explore-oriented `spawn_agent` subtask only when targeted searches are insufficient or the task clearly needs broad, multi-query exploration."
}

func sessionGuidanceDirectedSearchTools(enabled map[string]struct{}) string {
	tools := make([]string, 0, 3)
	for _, name := range []string{"lsp_grep", "lsp_file", "lsp_inspect"} {
		if sessionGuidanceToolEnabled(enabled, name) {
			tools = append(tools, "`"+name+"`")
		}
	}
	switch len(tools) {
	case 0:
		return "the repository-aware search tools"
	case 1:
		return tools[0]
	case 2:
		return tools[0] + " and " + tools[1]
	default:
		return strings.Join(tools[:len(tools)-1], ", ") + " and " + tools[len(tools)-1]
	}
}

func sessionGuidanceSkillItems(enabled map[string]struct{}, flags map[string]bool) []string {
	if !sessionGuidanceSkillsAvailable(enabled, flags) {
		return nil
	}
	items := []string{
		"`/<skill-name>` refers only to surfaced user-invocable skills. Use only the surfaced skill names and prompts for this session; do not guess skill names or treat built-in slash commands as skills.",
	}
	if sessionGuidanceDiscoverEnabled(enabled, flags) {
		items = append(items, "If skill discovery is enabled and the currently surfaced skills do not cover the next action, use the discovery flow to surface more skills before improvising a new skill name. Discovery availability alone is not a reason to call it.")
	}
	return items
}

func sessionGuidanceVerificationItems() []string {
	return []string{
		"Verification protocol: when non-trivial implementation happens, independent verification must happen before you report completion. Treat non-trivial as 3+ file edits, backend or API changes, or infrastructure changes. Your own checks, the implementer's self-checks, and fork self-checks do not count.",
		"Launch the verifier with `spawn_agent`. Pass the original user request, every changed file, the implementation approach, and the plan path if one exists. You may include concerns, but do not preload the verifier with your own test verdicts or claim the change already works.",
		"The verifier owns the verdict: `PASS`, `FAIL`, or `PARTIAL`. You cannot self-assign `PARTIAL`. On `FAIL`, fix the issues and rerun the verifier. On `PARTIAL`, report only the verified subset plus the remaining gap.",
		"On `PASS`, spot-check the verifier before reporting completion: rerun 2-3 commands from its report and confirm each `PASS` includes a command and output consistent with your spot check.",
	}
}

func sessionGuidanceToolSet(tools []string) map[string]struct{} {
	set := make(map[string]struct{}, len(tools))
	for _, tool := range sortedPromptValues(tools) {
		set[tool] = struct{}{}
	}
	return set
}

func sessionGuidanceToolEnabled(enabled map[string]struct{}, names ...string) bool {
	for _, name := range names {
		if _, ok := enabled[strings.TrimSpace(name)]; ok {
			return true
		}
	}
	return false
}

func sessionGuidanceFlagEnabled(flags map[string]bool, keys ...string) bool {
	for _, key := range keys {
		if flags[key] {
			return true
		}
	}
	return false
}

func sessionGuidanceForkMode(flags map[string]bool) bool {
	return sessionGuidanceFlagEnabled(flags,
		"fork_subagent",
		"forkSubagent",
		"fork_mode",
		"forkMode",
		"fork_subagent_enabled",
	)
}

func sessionGuidancePersistentSubagentDefault(flags map[string]bool) bool {
	return sessionGuidanceFlagEnabled(flags,
		"persistent_subagent_default",
		"persistentSubagentDefault",
		"managed_subagent_default",
		"managedSubagentDefault",
		"ui_persistent_subagent_default",
		"uiPersistentSubagentDefault",
	)
}

func sessionGuidanceExploreEnabled(flags map[string]bool) bool {
	return sessionGuidanceFlagEnabled(flags,
		"explore_agent",
		"exploreAgent",
		"explore_agent_enabled",
		"exploreAgentEnabled",
		"explore_plan_agents_enabled",
	)
}

func sessionGuidanceSkillsAvailable(enabled map[string]struct{}, flags map[string]bool) bool {
	if sessionGuidanceFlagEnabled(flags,
		"user_invocable_skills",
		"userInvocableSkills",
		"skills_enabled",
		"skillsEnabled",
		"skill_tool_enabled",
	) {
		return true
	}
	return sessionGuidanceToolEnabled(enabled, "skill", "skills", "skills/list", "thread/skills/list")
}

func sessionGuidanceDiscoverEnabled(enabled map[string]struct{}, flags map[string]bool) bool {
	if !sessionGuidanceSkillsAvailable(enabled, flags) {
		return false
	}
	if sessionGuidanceFlagEnabled(flags,
		"discover_skills",
		"discoverSkills",
		"discover_skills_enabled",
		"discoverSkillsEnabled",
		"experimental_skill_search",
	) {
		return true
	}
	return sessionGuidanceToolEnabled(enabled, "discover_skills")
}

func sessionGuidanceVerificationEnabled(flags map[string]bool) bool {
	return sessionGuidanceFlagEnabled(flags, "verification_required", "require_verification", "verification_agent")
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
			RenderMode                   string   `json:"renderMode,omitempty"`
			CWD                          string   `json:"cwd,omitempty"`
			GitRoot                      string   `json:"gitRoot,omitempty"`
			IsWorktree                   bool     `json:"isWorktree,omitempty"`
			Platform                     string   `json:"platform,omitempty"`
			Shell                        string   `json:"shell,omitempty"`
			ShellNote                    string   `json:"shellNote,omitempty"`
			OSVersion                    string   `json:"osVersion,omitempty"`
			LanguageServerTools          []string `json:"languageServerTools,omitempty"`
			AdditionalWorkingDirectories []string `json:"additionalWorkingDirectories,omitempty"`
			Provider                     string   `json:"provider,omitempty"`
			ModelMetadata                string   `json:"modelMetadata,omitempty"`
			KnowledgeCutoff              string   `json:"knowledgeCutoff,omitempty"`
			LatestModelFamily            string   `json:"latestModelFamily,omitempty"`
			FrontierGuidance             string   `json:"frontierGuidance,omitempty"`
		}{
			Section:                      section.Name,
			RenderMode:                   promptEnvRenderModeForInput(input).String(),
			CWD:                          currentPromptCWD(input.BuildCtx),
			GitRoot:                      strings.TrimSpace(input.BuildCtx.GitRoot),
			IsWorktree:                   input.BuildCtx.IsWorktree,
			Platform:                     promptPlatform(),
			Shell:                        promptShellName(),
			ShellNote:                    promptShellNote(),
			OSVersion:                    promptUnameSR(),
			LanguageServerTools:          sectionLanguageServerTools(input.BuildCtx),
			AdditionalWorkingDirectories: sortedPromptValues(input.BuildCtx.AdditionalWorkingDirectories),
			Provider:                     strings.TrimSpace(input.BuildCtx.Provider),
			ModelMetadata:                promptModelMetadata(input.BuildCtx),
			KnowledgeCutoff:              promptKnowledgeCutoff(input.BuildCtx),
			LatestModelFamily:            promptLatestModelFamily(input.BuildCtx),
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

func cacheByNameSectionDependency(section PromptSection, input SectionContext) any {
	switch section.Name {
	case DynamicSectionOutputStyle:
		style := input.BuildCtx.OutputStyleConfig
		if style == nil {
			return nil
		}
		return struct {
			Section     string `json:"section"`
			Name        string `json:"name,omitempty"`
			Description string `json:"description,omitempty"`
			Prompt      string `json:"prompt,omitempty"`
			Source      string `json:"source,omitempty"`
		}{Section: section.Name, Name: strings.TrimSpace(style.Name), Description: strings.TrimSpace(style.Description), Prompt: strings.TrimSpace(style.Prompt), Source: strings.TrimSpace(style.Source)}
	case DynamicSectionScratchpad:
		dir := strings.TrimSpace(input.BuildCtx.ScratchpadDir)
		if dir == "" {
			return nil
		}
		return struct {
			Section       string `json:"section"`
			ScratchpadDir string `json:"scratchpadDir,omitempty"`
		}{Section: section.Name, ScratchpadDir: dir}
	case DynamicSectionFRC:
		cfg := input.BuildCtx.FRCConfig.Normalize()
		if cfg == nil {
			return nil
		}
		return struct {
			Section                      string   `json:"section"`
			Model                        string   `json:"model,omitempty"`
			Enabled                      bool     `json:"enabled,omitempty"`
			SystemPromptSuggestSummaries bool     `json:"systemPromptSuggestSummaries,omitempty"`
			KeepRecent                   int      `json:"keepRecent,omitempty"`
			SupportedModels              []string `json:"supportedModels,omitempty"`
		}{
			Section:                      section.Name,
			Model:                        strings.TrimSpace(input.BuildCtx.Model),
			Enabled:                      cfg.Enabled,
			SystemPromptSuggestSummaries: cfg.SystemPromptSuggestSummaries,
			KeepRecent:                   cfg.KeepRecentCount(),
			SupportedModels:              append([]string(nil), cfg.SupportedModels...),
		}
	case DynamicSectionNumericLengthAnchors:
		return struct {
			Section  string `json:"section"`
			UserType string `json:"userType,omitempty"`
		}{Section: section.Name, UserType: strings.TrimSpace(promptUserType())}
	case DynamicSectionTokenBudget:
		return struct {
			Section string `json:"section"`
			Enabled bool   `json:"enabled,omitempty"`
		}{Section: section.Name, Enabled: tokenBudgetEnabled(input.BuildCtx)}
	case DynamicSectionBrief:
		return struct {
			Section string `json:"section"`
			Enabled bool   `json:"enabled,omitempty"`
			Summary string `json:"summary,omitempty"`
		}{Section: section.Name, Enabled: briefEnabled(input.BuildCtx), Summary: strings.TrimSpace(input.BuildCtx.Summary)}
	default:
		return nil
	}
}

func childAgentCacheDependency(input SectionContext) (bool, string) {
	if input.Start == nil || input.Turn != nil || strings.TrimSpace(input.Start.ParentAgentID) == "" {
		return false, ""
	}
	agentType := strings.TrimSpace(input.Start.AgentType)
	if agentType == "" {
		agentType = strings.TrimSpace(input.Start.Name)
	}
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
