package contract

import (
	"context"
	"sort"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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

type StartAssembly = dto.StartAssembly

type TurnAssembly = dto.TurnAssembly

type PromptAssemblySnapshot = dto.PromptAssemblySnapshot

type ClaudeMdSourceProvider interface {
	ResolveClaudeMdSources(ctx context.Context, buildCtx BuildCtx) []ClaudeMdSource
}

type TurnAttachmentProvider interface {
	ResolveTurnAttachments(ctx context.Context, buildCtx BuildCtx, turn TurnInput, baseSources []ClaudeMdSource) []dto.AttachmentEnvelope
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
