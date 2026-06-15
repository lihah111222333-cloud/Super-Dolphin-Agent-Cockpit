package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	ServerConfigs            map[string]MCPServerConfig
	InstructionsDeltaEnabled bool
	InstructionAttachments   []MCPAttachmentRef
}

type MCPServerConfig struct {
	Transport string            `json:"transport,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

type MCPServerConfigProvider interface {
	ListMCPServerConfigs(ctx context.Context, cwd string) (map[string]MCPServerConfig, error)
}

// MCPServerAddRequest 是跨模块写入 MCP server 配置的输入，避免业务模块互相依赖具体实现。
type MCPServerAddRequest struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// MCPServerAddResult 返回 MCP server 配置写入位置和本次写入的服务名。
type MCPServerAddResult struct {
	ConfigPath  string   `json:"configPath"`
	ServerNames []string `json:"serverNames"`
}

// MCPServerListResult 返回当前工作区解析到的 MCP server 配置集合。
type MCPServerListResult struct {
	ConfigPath string                     `json:"configPath"`
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// MCPServerConfigWriter 暴露默认 MCP server 启动入口需要的最小配置读写能力。
type MCPServerConfigWriter interface {
	AddServers(context.Context, MCPServerAddRequest) (MCPServerAddResult, error)
	ListServers(context.Context) (MCPServerListResult, error)
}

// StoreMCPServerConfigParams 是写入 MCP server 配置表的最小输入。
type StoreMCPServerConfigParams struct {
	WorkspaceRoot string
	Name          string
	Config        MCPServerConfig
}

// MCPServerConfigStore 只暴露 MCP server 服务需要的配置持久化能力。
type MCPServerConfigStore interface {
	InsertServer(context.Context, StoreMCPServerConfigParams) (bool, error)
	ListServers(context.Context, string) (map[string]MCPServerConfig, error)
	DeleteServer(context.Context, string, string) (bool, error)
}

// DatasourceDocument 是数据源文件落库后的可检索内容。
type DatasourceDocument struct {
	WorkspaceRoot string `json:"workspaceRoot"`
	Name          string `json:"name"`
	Extension     string `json:"extension"`
	Size          int64  `json:"size"`
	StoredPath    string `json:"storedPath"`
	Content       string `json:"content"`
}

// UpsertDatasourceDocumentParams 是新增或覆盖数据源文档时需要的字段。
type UpsertDatasourceDocumentParams struct {
	WorkspaceRoot string
	Name          string
	Extension     string
	Size          int64
	StoredPath    string
	Content       string
}

// DatasourceDocumentStore 只暴露数据源服务需要的文档持久化能力。
type DatasourceDocumentStore interface {
	UpsertDocument(context.Context, UpsertDatasourceDocumentParams) error
	ListDocuments(context.Context, string) ([]DatasourceDocument, error)
	DeleteDocument(context.Context, string, string) error
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
	// Legacy launch-time skill selection carrier. V1 production skill
	// discovery is provider-native mirror based, so these fields are not a
	// prompt-catalog injection path; they are kept as additive compatibility
	// data for older callers and diagnostics.
	LaunchSkillNames  []string
	LaunchSkillRefs   []dto.SkillRef
	ForceLaunchSkills bool
	// SuppressedTools 是被 SkillMeta.ReplacesNative 声明替代的原生工具名列表。
	// prompt assembler 在 tool_preferences section 中渲染为 "Do NOT use..." 指令，
	// 引导所有模型优先使用项目 MCP 等价工具。
	SuppressedTools []string
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
	DynamicSectionProjectDefaultRules  = "project_default_rules"
	DynamicSectionAvailableExperts     = "available_experts"
	DynamicSectionRecallCatalog        = "recall_catalog"
	DynamicSectionMemory               = "memory"
	DynamicSectionMemoryContext        = "memory_context"
	DynamicSectionMemoryEntrypoint     = "memory_entrypoint"
	DynamicSectionEnvInfoSimple        = "env_info_simple"
	DynamicSectionDatasource           = "datasource"
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
	PromptKey        string
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
	// Legacy launch skill selection copied from StartRequest. The current
	// runtime does not consume it to inject skill bodies into prompt assembly;
	// provider-native mirrors are reconciled by provider drivers instead.
	LaunchSkillNames  []string
	LaunchSkillRefs   []dto.SkillRef
	ForceLaunchSkills bool
}

type TurnInput struct {
	ThreadID                     string
	Provider                     string
	UserText                     string
	PromptKey                    string
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

// SectionContextCWD 处理section上下文工作目录。
func SectionContextCWD(input SectionContext) string {
	if cwd := strings.TrimSpace(input.BuildCtx.CWD); cwd != "" {
		return cwd
	}
	if input.Start != nil {
		if cwd := strings.TrimSpace(input.Start.CWD); cwd != "" {
			return cwd
		}
	}
	if input.Turn != nil {
		return strings.TrimSpace(input.Turn.CWD)
	}
	return ""
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

type BuiltinPromptTemplate struct {
	ID          int64
	PromptKey   string
	Kind        string
	Title       string
	AgentKey    string
	ToolName    string
	PromptText  string
	WhenToUse   string
	Description string
	Tags        []string
	Enabled     bool
	Scope       string
	MatchWhen   json.RawMessage
	Priority    int
}

type BuiltinPromptSection struct {
	ID          int64
	TemplateID  int64
	SectionKey  string
	Region      string
	Ordinal     int
	Body        string
	EnableWhen  json.RawMessage
	Enabled     bool
	TriggerType string
	RecallTopic string
}

type BuiltinPromptRegistry interface {
	ListTemplates() []BuiltinPromptTemplate
	GetTemplate(promptKey string) (BuiltinPromptTemplate, bool)
	SectionsByTemplateID(templateID int64) []BuiltinPromptSection
}

type CriticalPromptSectionError struct {
	Section string
	Err     error
}

// NewCriticalPromptSectionError 创建criticalpromptsection错误。
func NewCriticalPromptSectionError(section string, err error) error {
	if err == nil {
		return nil
	}
	return CriticalPromptSectionError{Section: strings.TrimSpace(section), Err: err}
}

// Error 返回错误文本。
func (e CriticalPromptSectionError) Error() string {
	if e.Section == "" {
		return fmt.Sprintf("critical prompt section failed: %v", e.Err)
	}
	return fmt.Sprintf("critical prompt section %q failed: %v", e.Section, e.Err)
}

// Unwrap 返回底层错误。
func (e CriticalPromptSectionError) Unwrap() error {
	return e.Err
}

// IsCriticalPromptSectionError 判断criticalpromptsection错误是否可用。
func IsCriticalPromptSectionError(err error) bool {
	var target CriticalPromptSectionError
	return errors.As(err, &target)
}

// PrepareBaseInstructionBlocks applies the assembler's ordering, trimming, and gates.
// PrepareBaseInstructionBlocks 准备baseinstructionblocks。
func PrepareBaseInstructionBlocks(blocks []BaseInstructionBlock, buildCtx BuildCtx, userPrompt string, enableWhenEval EnableWhenEvaluator) []BaseInstructionBlock {
	sorted := make([]BaseInstructionBlock, len(blocks))
	copy(sorted, blocks)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Region != sorted[j].Region {
			return sorted[i].Region < sorted[j].Region
		}
		return sorted[i].Ordinal < sorted[j].Ordinal
	})
	out := make([]BaseInstructionBlock, 0, len(sorted))
	for _, block := range sorted {
		block.Body = strings.TrimSpace(block.Body)
		if enableWhenEval != nil && !enableWhenEval(block.EnableWhen, buildCtx, userPrompt) {
			continue
		}
		if block.Body != "" {
			out = append(out, block)
		}
	}
	return out
}

// TextFromBaseInstructionBlocks renders the section-only text snapshot.
// TextFromBaseInstructionBlocks 从baseinstructionblocks处理文本。
func TextFromBaseInstructionBlocks(blocks []BaseInstructionBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if body := strings.TrimSpace(block.Body); body != "" {
			parts = append(parts, body)
		}
	}
	return strings.Join(parts, "\n\n")
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
	ResolveClaudeMdSources(ctx context.Context, buildCtx BuildCtx) ([]ClaudeMdSource, error)
}

type TurnAttachmentProvider interface {
	ResolveTurnAttachments(ctx context.Context, buildCtx BuildCtx, turn TurnInput, baseSources []ClaudeMdSource) ([]dto.AttachmentEnvelope, error)
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

// FormatUserContextText 格式化user上下文文本。
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

// RenderUserContextMessage 渲染user上下文消息。
func RenderUserContextMessage(assembly TurnAssembly) string {
	if text := FormatUserContextText(assembly.UserContext); text != "" {
		return WrapSystemReminder(text)
	}
	return WrapSystemReminder(assembly.UserContextText)
}

// RenderStartRuntimeContext 渲染起点运行时上下文。
func RenderStartRuntimeContext(assembly StartAssembly) string {
	payload := assembly.UserContext
	userContextText := assembly.UserContextText
	if startRuntimeExtrasAlreadyRendered(assembly) {
		payload = cloneUserContextWithout(payload, "runtimeExtras")
		userContextText = FormatUserContextText(payload)
	}
	userContext := RenderUserContextMessage(TurnAssembly{
		UserContext:     payload,
		UserContextText: userContextText,
	})
	return appendPromptBlock(userContext, FormatSystemContextBlock(assembly.SystemContext))
}

func startRuntimeExtrasAlreadyRendered(assembly StartAssembly) bool {
	if strings.TrimSpace(assembly.UserContext["runtimeExtras"]) == "" {
		return false
	}
	if assembly.Boundary != nil && strings.TrimSpace(assembly.Boundary.UncachedTail) != "" {
		return true
	}
	return assembly.Snapshot.Boundary != nil && strings.TrimSpace(assembly.Snapshot.Boundary.UncachedTail) != ""
}

func cloneUserContextWithout(in map[string]string, dropKey string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		if key == dropKey {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AppendStartRuntimeContext 追加起点运行时上下文。
func AppendStartRuntimeContext(base string, assembly StartAssembly) string {
	return appendPromptBlock(base, RenderStartRuntimeContext(assembly))
}

func appendPromptBlock(base, block string) string {
	base = strings.TrimSpace(base)
	block = strings.TrimSpace(block)
	if block == "" {
		return base
	}
	if base == "" {
		return block
	}
	if strings.Contains(base, block) {
		return base
	}
	return base + "\n\n" + block
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

// normalizeUserContext 规范化user上下文。
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
// WrapSystemReminder 包装systemreminder。
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

// AppendSystemContextTail 追加system上下文tail。
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

// FormatSystemContextBlock 格式化system上下文block。
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

// orderedSystemContextKeys 处理orderedsystem上下文键。
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

// MatchWhenEvaluator decides whether a prompt_template's match_when JSONB
// expression is satisfied by the given BuildCtx and user prompt. The thread
// router calls this to implement the "match_when auto-route" tier without
// importing the prompt module. The concrete implementation lives in
// internal/module/prompt (EvaluateMatchWhen).
type MatchWhenEvaluator func(raw []byte, buildCtx BuildCtx, userPrompt string) bool

// EnableWhenEvaluator decides whether a prompt_template_section's enable_when
// JSONB expression is satisfied by the given BuildCtx and user prompt. The
// thread router uses this to materialize prompt_versions snapshots with the
// same section gates the prompt assembler applies at injection time.
type EnableWhenEvaluator func(raw []byte, buildCtx BuildCtx, userPrompt string) bool
