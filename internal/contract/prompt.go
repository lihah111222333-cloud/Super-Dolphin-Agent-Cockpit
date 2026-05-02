package contract

import (
	"context"
	"sort"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

type PromptRegion = dto.PromptRegion

const (
	PromptRegionStatic  PromptRegion = dto.PromptRegionStatic
	PromptRegionDynamic PromptRegion = dto.PromptRegionDynamic
)

type MCPSnapshot struct {
	Servers                  []string
	Tools                    []string
	Instructions             map[string]string
	InstructionsDeltaEnabled bool
	InstructionAttachments   []MCPAttachmentRef
}

type MCPAttachmentRef struct {
	Name string
	URI  string
}

type OutputStyleConfig struct {
	Name                   string
	Description            string
	Prompt                 string
	Source                 string
	KeepCodingInstructions *bool
}

type BuildCtx struct {
	CWD                          string
	GitRoot                      string
	IsWorktree                   bool
	Language                     string
	Provider                     string
	Model                        string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	ClaudeMdExcludes             []string
	MCPSnapshot                  MCPSnapshot
	SessionFlags                 map[string]bool
	Summary                      string
	OutputStyleConfig            *OutputStyleConfig
	ScratchpadDir                string
	FRCConfig                    *FRCConfig
	KeepCodingInstructions       *bool
	// P20.4 launch skill wire：从 StartRequest 透传的创线期选中 skill。
	// 上游仅在 AssembleStart 时填充；turn 路径将保持为空。
	// SkillCatalogProvider 读到后可选：
	//   - 空列表 + Force=false → 按原来的全量扫盘渲染
	//   - 非空列表 + Force=false → 把命中的 skill 置顶 + 非命中的继续保留
	//   - 非空列表 + Force=true  → 只渲染命中的 skill，其余隰藏
	LaunchSkillNames  []string
	ForceLaunchSkills bool
}

type ClaudeMdSource struct {
	Path        string
	Content     string
	Type        string
	Description string
	Origin      string
	Conditional bool
	Globs       []string
	BaseDir     string
	RuleScope   string
	Digest      string
}

type ResolvedPromptSection = dto.ResolvedPromptSection

type SystemContext = dto.SystemContext

type PromptAssemblyBoundary = dto.PromptAssemblyBoundary

type InvalidateReason string

const (
	InvalidateClear          InvalidateReason = "clear"
	InvalidateCompact        InvalidateReason = "compact"
	InvalidateWorktree       InvalidateReason = "worktree"
	InvalidateResumeRestore  InvalidateReason = "resume_restore"
	InvalidateProviderSwitch InvalidateReason = "provider_switch"
	InvalidateMemoryWrite    InvalidateReason = "memory_write"
)

const (
	DynamicSectionSessionGuidance      = "session_guidance"
	DynamicSectionMemory               = "memory"
	DynamicSectionMemoryContext        = "memory_context"
	DynamicSectionMemoryEntrypoint     = "memory_entrypoint"
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
)

// PromptAssemblySnapshotVersion bumps on cache-layout-breaking changes.
// Version 2 (Phase 3 parity): BaseInstructions no longer embeds the per-start
// user meta block (currentDate, runtimeExtras) nor the System Context block
// (gitStatus). Consumers reading v1 snapshots must re-compute the assembly
// instead of trusting the embedded hash; see prompt_snapshot resume logic.
const PromptAssemblySnapshotVersion = 2

type StartInput struct {
	ThreadID         string
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
	Name             string
	Prompt           string
	BaseInstructions string
	// BaseInstructionBlocks carries ordered, region-tagged fragments sourced
	// from prompt_template_sections. When non-empty, the assembler merges
	// them into the resolved section list (static → CachedPrefix, dynamic →
	// UncachedTail) instead of treating BaseInstructions as a single opaque
	// tail block. Empty slice keeps legacy behavior (BaseInstructions only).
	BaseInstructionBlocks        []BaseInstructionBlock
	DeveloperInstructions        string
	Summary                      string
	Provider                     string
	CWD                          string
	GitRoot                      string
	IsWorktree                   bool
	Language                     string
	Model                        string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	ClaudeMdExcludes             []string
	MCPSnapshot                  MCPSnapshot
	SessionFlags                 map[string]bool
	OutputStyleConfig            *OutputStyleConfig
	ScratchpadDir                string
	FRCConfig                    *FRCConfig
	KeepCodingInstructions       *bool
	// P20.4：契约从 dto.StartSessionRequest 透传的 launch skill 选择。
	// thread.startSession 在构造 StartInput 时从 req.LaunchSkillNames / req.ForceLaunchSkills 映射。
	LaunchSkillNames  []string
	ForceLaunchSkills bool
}

type TurnInput struct {
	ThreadID                     string
	Provider                     string
	UserText                     string
	SkillPrompt                  string
	Attachments                  []string
	CurrentDate                  string
	RuntimeUserContext           map[string]string
	Summary                      string
	CWD                          string
	GitRoot                      string
	IsWorktree                   bool
	Language                     string
	Model                        string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	ClaudeMdExcludes             []string
	MCPSnapshot                  MCPSnapshot
	SessionFlags                 map[string]bool
	OutputStyleConfig            *OutputStyleConfig
	ScratchpadDir                string
	FRCConfig                    *FRCConfig
	KeepCodingInstructions       *bool
}

type SectionContext struct {
	BuildCtx BuildCtx
	Start    *StartInput
	Turn     *TurnInput
}

type SectionComputeFunc func(context.Context, SectionContext) (*string, error)

type CachePolicy int

const (
	CacheByName CachePolicy = iota
	Uncached
	InputScoped
)

type PromptSection struct {
	Name        string
	Order       int
	Region      PromptRegion
	Volatile    bool
	CachePolicy CachePolicy
	StartOnly   bool
	Compute     SectionComputeFunc
}

// BaseInstructionBlock is an ordered, region-tagged fragment coming from a
// prompt_templates row that has been migrated to the sectioned layout. The
// assembler converts it into a ResolvedPromptSection and appends it to the
// resolved list; region decides whether it flows into the cached prefix or
// the uncached tail via renderResolvedSectionsByRegion.
//
// EnableWhen carries the raw JSONB feature-gate expression (shape documented
// by prompt.EvaluateEnableWhen). nil / empty-object means "always inject";
// any mismatched key drops the block at merge time. Evaluation happens in
// the assembler (not the router) because BuildCtx is only finalized there.
type BaseInstructionBlock struct {
	Key        string
	Region     PromptRegion
	Ordinal    int
	Body       string
	EnableWhen []byte
}

type StartAssembly = dto.StartAssembly

type TurnAssembly = dto.TurnAssembly

type PromptAssemblySnapshot = dto.PromptAssemblySnapshot

type DynamicSectionProvider interface {
	SectionName() string
	Resolve(ctx context.Context, input SectionContext) (*string, error)
}

type InvalidationAwareProvider interface {
	OnPromptInvalidate(reason InvalidateReason)
}

// SectionInvalidator drops cached entries for the named sections, returning
// the new generation number. Implementations MUST be safe for concurrent
// use: callers fan out from background goroutines (auto-dream, extractor,
// turn-tracking) without external synchronization. The shipped
// prompt.Service implementation guards its cache with a mutex; downstream
// implementations that wrap or replace it must preserve that guarantee.
type SectionInvalidator interface {
	InvalidateSections(reason InvalidateReason, names ...string) uint64
}

type DynamicSectionRegistrar interface {
	RegisterDynamicProvider(provider DynamicSectionProvider) error
}

type ClaudeMdSourceProviderRegistrar interface {
	RegisterClaudeMdSourceProvider(provider ClaudeMdSourceProvider) error
}

type ClaudeMdSourceProvider interface {
	ResolveClaudeMdSources(ctx context.Context, buildCtx BuildCtx) []ClaudeMdSource
}

type TurnAttachmentProvider interface {
	ResolveTurnAttachments(ctx context.Context, buildCtx BuildCtx, turn TurnInput, baseSources []ClaudeMdSource) []dto.AttachmentEnvelope
}

type TurnContextPayload struct {
	Inputs      []shareddto.InputItem
	Attachments []dto.AttachmentEnvelope
}

type TurnContextProvider interface {
	PrepareTurnContext(ctx context.Context, session Session, buildCtx BuildCtx, threadID, query string) TurnContextPayload
}

var preferredUserContextKeys = []string{
	"claudeMd",
	"currentDate",
	"workerToolsContext",
	"terminalFocus",
	"runtimeExtras",
}

func FormatUserContextText(payload map[string]string) string {
	normalized := normalizeUserContext(payload)
	if len(normalized) == 0 {
		return ""
	}
	blocks := make([]string, 0, len(normalized))
	for _, key := range orderedUserContextKeys(normalized) {
		if block := renderUserContextSection(key, normalized[key]); block != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func RenderUserContextMessage(assembly TurnAssembly) string {
	if text := FormatUserContextText(assembly.UserContext); text != "" {
		return WrapSystemReminder(text)
	}
	return WrapSystemReminder(assembly.UserContextText)
}

func orderedUserContextKeys(payload map[string]string) []string {
	seen := make(map[string]struct{}, len(payload))
	ordered := make([]string, 0, len(payload))
	for _, key := range preferredUserContextKeys {
		if _, ok := payload[key]; ok {
			ordered = append(ordered, key)
			seen[key] = struct{}{}
		}
	}
	extra := make([]string, 0, len(payload))
	for key := range payload {
		if _, ok := seen[key]; ok {
			continue
		}
		extra = append(extra, key)
	}
	sort.Strings(extra)
	return append(ordered, extra...)
}

func normalizeUserContext(payload map[string]string) map[string]string {
	if len(payload) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(payload))
	for key, value := range payload {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		normalized[key] = value
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func renderUserContextSection(key, body string) string {
	key = strings.TrimSpace(key)
	body = strings.TrimSpace(body)
	if key == "" || body == "" {
		return ""
	}
	return "# " + key + "\n" + body
}

// WrapSystemReminder wraps the given text in <system-reminder> tags.
// Exported so the prompt assembler can embed system context into baseInstructions.
func WrapSystemReminder(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "<system-reminder>") {
		return text
	}
	return strings.Join([]string{"<system-reminder>", text, "</system-reminder>"}, "\n\n")
}

func AppendSystemContextTail(base string, ctx SystemContext) string {
	block := FormatSystemContextBlock(ctx)
	base = strings.TrimSpace(base)
	if base == "" {
		return block
	}
	if block == "" {
		return base
	}
	return base + "\n\n" + block
}

func FormatSystemContextBlock(ctx SystemContext) string {
	if len(ctx) == 0 {
		return ""
	}
	lines := []string{"# System Context"}
	for _, key := range orderedSystemContextKeys(ctx) {
		value := strings.TrimSpace(ctx[key])
		if value == "" {
			continue
		}
		switch key {
		case "gitStatus":
			lines = append(lines, "Git status:", value)
		case "cacheBreaker":
			lines = append(lines, "Cache breaker: "+value)
		default:
			lines = append(lines, key+":", value)
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func orderedSystemContextKeys(ctx SystemContext) []string {
	keys := make([]string, 0, len(ctx))
	for _, key := range []string{"gitStatus", "cacheBreaker"} {
		if value := strings.TrimSpace(ctx[key]); value != "" {
			keys = append(keys, key)
		}
	}
	extra := make([]string, 0, len(ctx))
	for key, value := range ctx {
		if key == "gitStatus" || key == "cacheBreaker" || strings.TrimSpace(value) == "" {
			continue
		}
		extra = append(extra, key)
	}
	sort.Strings(extra)
	return append(keys, extra...)
}

// AgentType identifies a subagent invocation class. It mirrors Claude Code's
// `agentDefinition.agentType` taxonomy (claude_system_prompts_mapping §7).
// Unknown agent types (user-defined values such as "Writer" flowing through
// the orchestration_launch_agent tool) do not trigger subagent post-processing
// and fall back to the main-thread AssembleStart path.
type AgentType string

const (
	AgentTypeDefault AgentType = ""
	AgentTypeExplore AgentType = "Explore"
	AgentTypePlan    AgentType = "Plan"
)

// AgentInput bundles subagent-specific knobs for AssembleAgent. When
// OverrideSystemPrompt is truthy it wins outright (Claude Code's
// override.systemPrompt direct pass-through). Otherwise the assembler runs
// AssembleStart then applies Explore/Plan claudeMd/gitStatus redaction +
// env-details appending based on AgentType.
type AgentInput struct {
	StartInput           StartInput
	AgentType            AgentType
	OverrideSystemPrompt string
}

// PromptAssemblyService 组装系统提示词。
type PromptAssemblyService interface {
	AssembleStart(ctx context.Context, in StartInput) (StartAssembly, error)
	AssembleTurn(ctx context.Context, in TurnInput) (TurnAssembly, error)
	AssembleAgent(ctx context.Context, in AgentInput) (StartAssembly, error)
	Invalidate(ctx context.Context, reason InvalidateReason) error
}
