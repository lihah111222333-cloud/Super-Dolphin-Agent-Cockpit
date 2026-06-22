package prompt

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	DynamicSectionSessionGuidance        = contract.DynamicSectionSessionGuidance
	DynamicSectionProjectDefaultRules    = contract.DynamicSectionProjectDefaultRules
	DynamicSectionAvailableExperts       = contract.DynamicSectionAvailableExperts
	DynamicSectionRecallCatalog          = contract.DynamicSectionRecallCatalog
	DynamicSectionPersonalizationProfile = contract.DynamicSectionPersonalizationProfile
	DynamicSectionMemory                 = contract.DynamicSectionMemory
	DynamicSectionMemoryContext          = contract.DynamicSectionMemoryContext
	DynamicSectionMemoryEntrypoint       = contract.DynamicSectionMemoryEntrypoint
	DynamicSectionEnvInfoSimple          = contract.DynamicSectionEnvInfoSimple
	DynamicSectionDatasource             = contract.DynamicSectionDatasource
	DynamicSectionLanguage               = contract.DynamicSectionLanguage
	DynamicSectionMCPInstructions        = contract.DynamicSectionMCPInstructions
	DynamicSectionOutputStyle            = contract.DynamicSectionOutputStyle
	DynamicSectionScratchpad             = contract.DynamicSectionScratchpad
	DynamicSectionFRC                    = contract.DynamicSectionFRC
	DynamicSectionSummarizeToolResults   = contract.DynamicSectionSummarizeToolResults
	DynamicSectionNumericLengthAnchors   = contract.DynamicSectionNumericLengthAnchors
	DynamicSectionTokenBudget            = contract.DynamicSectionTokenBudget
	DynamicSectionBrief                  = contract.DynamicSectionBrief
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

// dynamicSectionSpecs 只排好各个 prompt slot 的顺序和缓存方式。
// 真正内容由 memory、thread 等模块注册进来，prompt 不直接读它们的内部实现。
var dynamicSectionSpecs = []dynamicSectionSpec{
	{name: DynamicSectionSessionGuidance, order: 110, cachePolicy: InputScoped},
	{name: DynamicSectionProjectDefaultRules, order: 112, cachePolicy: InputScoped},
	{name: DynamicSectionAvailableExperts, order: 115, cachePolicy: InputScoped},
	{name: DynamicSectionRecallCatalog, order: 118, cachePolicy: InputScoped},
	{name: DynamicSectionPersonalizationProfile, order: 119, cachePolicy: InputScoped},
	{name: DynamicSectionMemory, order: 120, cachePolicy: InputScoped, startOnly: true},
	{name: DynamicSectionMemoryEntrypoint, order: 122, cachePolicy: InputScoped, startOnly: true},
	{name: DynamicSectionMemoryContext, order: 125, cachePolicy: InputScoped},
	{name: DynamicSectionEnvInfoSimple, order: 130, cachePolicy: InputScoped},
	{name: DynamicSectionDatasource, order: 135, cachePolicy: Uncached},
	{name: DynamicSectionLanguage, order: 140, cachePolicy: InputScoped},
	{name: DynamicSectionMCPInstructions, order: 150, cachePolicy: Uncached},
	{name: DynamicSectionOutputStyle, order: 200, cachePolicy: CacheByName},
	{name: DynamicSectionScratchpad, order: 210, cachePolicy: CacheByName},
	{name: DynamicSectionFRC, order: 220, cachePolicy: CacheByName},
	{name: DynamicSectionSummarizeToolResults, order: 230, cachePolicy: CacheByName},
	{name: DynamicSectionNumericLengthAnchors, order: 240, cachePolicy: CacheByName},
	{name: DynamicSectionTokenBudget, order: 250, cachePolicy: CacheByName},
	{name: DynamicSectionBrief, order: 260, cachePolicy: CacheByName},
}

// SectionName 返回该上下文提供器写入的 prompt section 名称。
func (p DynamicTextProvider) SectionName() string {
	return p.Name
}

// Resolve 解析当前请求需要注入的 prompt 内容。
func (p DynamicTextProvider) Resolve(ctx context.Context, input SectionContext) (*string, error) {
	if p.ResolveFunc == nil {
		return nil, nil
	}
	return p.ResolveFunc(ctx, input)
}

// DynamicSlotNames 返回动态 prompt 支持的 slot 名称。
func DynamicSlotNames() []string {
	names := make([]string, 0, len(dynamicSectionSpecs))
	for _, spec := range dynamicSectionSpecs {
		names = append(names, spec.name)
	}
	return names
}

var _ DynamicSectionProvider = SessionGuidanceProvider{}

type SessionGuidanceProvider struct{}

// SectionName 返回该上下文提供器写入的 prompt section 名称。
func (SessionGuidanceProvider) SectionName() string {
	return DynamicSectionSessionGuidance
}

// Resolve 解析当前请求需要注入的 prompt 内容。
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

// sessionGuidanceAgentItems 生成会话引导里的代理说明项。
func sessionGuidanceAgentItems(enabled map[string]struct{}, flags map[string]bool) []string {
	hasSpawn := sessionGuidanceToolEnabled(enabled, "spawn_agent")
	hasManaged := sessionGuidanceToolEnabled(enabled, "launch_agent", "orchestration_launch_agent")
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

// sessionGuidanceAgentDelegationItem 生成委派子代理的引导说明。
func sessionGuidanceAgentDelegationItem(enabled map[string]struct{}, flags map[string]bool) string {
	hasSpawn := sessionGuidanceToolEnabled(enabled, "spawn_agent")
	hasManaged := sessionGuidanceToolEnabled(enabled, "launch_agent", "orchestration_launch_agent")
	if hasManaged && sessionGuidancePersistentSubagentDefault(flags) {
		hasSpawn = false
	}
	if hasManaged && sessionGuidancePersistentSubagentDefault(flags) {
		return sessionGuidanceManagedAgentDelegationItem(enabled)
	}
	if hasSpawn && sessionGuidanceForkMode(flags) {
		return "This session is using fork-style delegation: use `spawn_agent` for longer background research or implementation that would otherwise flood the main context. If you are already the delegated worker, execute directly and do not bounce the same task into another fork."
	}
	if hasSpawn {
		return "Use `spawn_agent` only for well-scoped parallel subtasks. Keep urgent blocking work local, avoid duplicating delegated work, give each subagent clear ownership, and integrate its results before reporting completion."
	}
	return "Use `launch_agent` for child agents that should remain available in the UI as persistent conversations, and give them short, user-friendly task names."
}

// sessionGuidanceManagedAgentDelegationItem 生成父 agent 使用持久子 agent 的精简指引。
// 这里仅描述单个 context 字段的写法和等待 report 的流程，不扩展工具 schema。
func sessionGuidanceManagedAgentDelegationItem(enabled map[string]struct{}) string {
	parts := []string{
		"When creating a child agent for the user, use `launch_agent` with provider `codex` and a short, user-friendly task name.",
		"Choose `context_mode=\"minimal\"` for prompt-only work, or `context_mode=\"focused\"` when the child needs caller-selected task details.",
		"In focused mode, keep one concise `context` field with background, confirmed decisions, relevant file paths, forbidden actions, return format, and known risks.",
		"Prefer file paths, function names, line numbers, and constraints. Do not paste large code blocks unless the child cannot read them directly, and do not copy the parent conversation history.",
		"The child agent is a leaf worker and must not delegate again.",
		"Claude child-agent orchestration is not supported in this version; do not request it.",
	}
	hasSingleReport := sessionGuidanceToolEnabled(enabled, "get_agent_report", "orchestration_get_agent_report")
	hasBatchReport := sessionGuidanceToolEnabled(enabled, "get_agent_reports", "orchestration_get_agent_reports")
	if hasSingleReport || hasBatchReport {
		waitGuidance := "After launch, use `get_agent_report(wait=true)` with the returned agent_id before reporting that the child is finished."
		if hasSingleReport && hasBatchReport {
			waitGuidance = "After one launch, use `get_agent_report(wait=true)` with the returned agent_id. After multiple launches, use `get_agent_reports(wait=true)` with the returned agent_ids before reporting that the children are finished."
		} else if hasBatchReport {
			waitGuidance = "After launch, use `get_agent_reports(wait=true)` with the returned agent_ids before reporting that the children are finished."
		}
		parts = append(parts,
			waitGuidance,
			"Require the child report Markdown template: first line `状态: success | blocked | failed`, then `结论`, `证据`, `验证`, and `风险/待定`.",
			"Verify key claims and integrate the report instead of copying it verbatim to the user.",
		)
	}
	if sessionGuidanceToolEnabled(enabled, "send_message") {
		parts = append(parts, "Use `send_message(wait_report=true)` only for targeted follow-up to an idle child when you need a new report.")
	}
	if sessionGuidanceToolEnabled(enabled, "stop_agent") {
		parts = append(parts, "Use `stop_agent` only when cancellation is needed.")
	}
	return strings.Join(parts, " ")
}

func sessionGuidanceExploreItem(enabled map[string]struct{}) string {
	searchTools := sessionGuidanceDirectedSearchTools(enabled)
	return "For simple, directed codebase searches, use " + searchTools + " directly. Use an explore-oriented `spawn_agent` subtask only when targeted searches are insufficient or the task clearly needs broad, multi-query exploration."
}

// sessionGuidanceDirectedSearchTools 生成定向搜索工具的引导说明。
func sessionGuidanceDirectedSearchTools(enabled map[string]struct{}) string {
	tools := make([]string, 0, 3)
	for _, entry := range []struct {
		display string
		aliases []string
	}{
		{display: "grep", aliases: []string{"grep", "lsp_grep"}},
		{display: "file", aliases: []string{"file", "lsp_file"}},
		{display: "inspect", aliases: []string{"inspect", "lsp_inspect"}},
	} {
		for _, name := range entry.aliases {
			if sessionGuidanceToolEnabled(enabled, name) {
				tools = append(tools, "`"+entry.display+"`")
				break
			}
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

// RegisterDynamicProvider 把某个模块的内容接到已声明的 prompt slot。
// slot 名不存在就报错，避免 AI 或维护者拼出一个没人消费的新段落。
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

// UnregisterDynamicProvider 注销动态 prompt 提供器。
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
