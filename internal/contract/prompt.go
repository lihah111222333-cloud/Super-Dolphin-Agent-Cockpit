package contract

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

const PromptAssemblySnapshotVersion = 1

type StartInput struct {
	ThreadID                     string
	ParentAgentID                string
	AgentType                    string
	AgentMemoryScope             string
	Name                         string
	Prompt                       string
	BaseInstructions             string
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
		return wrapSystemReminder(text)
	}
	return wrapSystemReminder(assembly.UserContextText)
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

func wrapSystemReminder(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "<system-reminder>") {
		return text
	}
	return strings.Join([]string{"<system-reminder>", text, "</system-reminder>"}, "\n\n")
}

func NewRelevantMemoryAttachment(
	path, header, content string,
	updatedAt time.Time,
	limit int,
	truncated bool,
) dto.AttachmentEnvelope {
	envelope := dto.AttachmentEnvelope{
		Kind:      dto.AttachmentKindRelevantMemory,
		Path:      normalizeAttachmentPath(path),
		Header:    strings.TrimSpace(header),
		Content:   strings.TrimSpace(content),
		Limit:     limit,
		Truncated: truncated,
	}
	if envelope.Limit < 0 {
		envelope.Limit = 0
	}
	if !updatedAt.IsZero() {
		envelope.MtimeMs = updatedAt.UnixMilli()
		envelope.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	}
	return NormalizeAttachmentEnvelope(envelope)
}

func NormalizeAttachmentEnvelope(attachment dto.AttachmentEnvelope) dto.AttachmentEnvelope {
	attachment.Kind = strings.TrimSpace(attachment.Kind)
	attachment.Path = normalizeAttachmentPath(attachment.Path)
	attachment.Header = strings.TrimSpace(attachment.Header)
	attachment.Content = strings.TrimSpace(attachment.Content)
	attachment.UpdatedAt = strings.TrimSpace(attachment.UpdatedAt)
	if attachment.Limit < 0 {
		attachment.Limit = 0
	}
	if attachment.MtimeMs < 0 {
		attachment.MtimeMs = 0
	}
	return attachment
}

func IsValidAttachmentEnvelope(attachment dto.AttachmentEnvelope) bool {
	attachment = NormalizeAttachmentEnvelope(attachment)
	if attachment.Path == "" || attachment.Header == "" || attachment.Content == "" {
		return false
	}
	return attachment.MtimeMs > 0 || attachment.UpdatedAt != ""
}

func AttachmentDisplayName(attachment dto.AttachmentEnvelope) string {
	attachment = NormalizeAttachmentEnvelope(attachment)
	if attachment.Path == "" {
		return "attachment"
	}
	if base := strings.TrimSpace(filepath.Base(attachment.Path)); base != "" && base != "." {
		return base
	}
	return attachment.Path
}

func RenderAttachmentText(attachment dto.AttachmentEnvelope) string {
	attachment = NormalizeAttachmentEnvelope(attachment)
	if !IsValidAttachmentEnvelope(attachment) {
		return ""
	}
	lines := []string{"<attachment>"}
	if attachment.Kind != "" {
		lines = append(lines, "kind: "+attachment.Kind)
	}
	lines = append(lines, "path: "+attachment.Path)
	if attachment.MtimeMs > 0 {
		lines = append(lines, fmt.Sprintf("mtimeMs: %d", attachment.MtimeMs))
	}
	if attachment.UpdatedAt != "" {
		lines = append(lines, "updatedAt: "+attachment.UpdatedAt)
	}
	if attachment.Limit > 0 {
		lines = append(lines, fmt.Sprintf("limit: %d", attachment.Limit))
	}
	if attachment.Truncated {
		lines = append(lines, "truncated: true")
	}
	lines = append(lines, "", attachment.Header, attachment.Content, "</attachment>")
	return strings.Join(lines, "\n")
}

func normalizeAttachmentPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(path)
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

// PromptAssemblyService 组装系统提示词。
type PromptAssemblyService interface {
	AssembleStart(ctx context.Context, in StartInput) (StartAssembly, error)
	AssembleTurn(ctx context.Context, in TurnInput) (TurnAssembly, error)
	Invalidate(ctx context.Context, reason InvalidateReason) error
}
