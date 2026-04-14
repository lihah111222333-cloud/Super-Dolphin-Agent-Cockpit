package contract

import "context"

type PromptRegion int

const (
	PromptRegionStatic PromptRegion = iota
	PromptRegionDynamic
)

type MCPSnapshot struct {
	Servers []string
	Tools   []string
}

type BuildCtx struct {
	CWD                          string
	GitRoot                      string
	Language                     string
	Provider                     string
	Model                        string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	MCPSnapshot                  MCPSnapshot
	SessionFlags                 map[string]bool
}

type ResolvedPromptSection struct {
	Name     string
	Region   PromptRegion
	Volatile bool
	Content  string
}

type InvalidateReason string

const (
	InvalidateClear          InvalidateReason = "clear"
	InvalidateCompact        InvalidateReason = "compact"
	InvalidateWorktree       InvalidateReason = "worktree"
	InvalidateResumeRestore  InvalidateReason = "resume_restore"
	InvalidateProviderSwitch InvalidateReason = "provider_switch"
)

const PromptAssemblySnapshotVersion = 1

type StartInput struct {
	ThreadID                     string
	Name                         string
	Prompt                       string
	BaseInstructions             string
	DeveloperInstructions        string
	Provider                     string
	CWD                          string
	GitRoot                      string
	Language                     string
	Model                        string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	MCPSnapshot                  MCPSnapshot
	SessionFlags                 map[string]bool
}

type TurnInput struct {
	ThreadID                     string
	Provider                     string
	UserText                     string
	SkillPrompt                  string
	Attachments                  []string
	CurrentDate                  string
	CWD                          string
	GitRoot                      string
	Language                     string
	Model                        string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	MCPSnapshot                  MCPSnapshot
	SessionFlags                 map[string]bool
}

type StartAssembly struct {
	DisplayName           string
	BaseInstructions      string
	DeveloperInstructions string
	ResolvedSections      []ResolvedPromptSection
	Snapshot              PromptAssemblySnapshot
}

type TurnAssembly struct {
	UserContextText  string
	ResolvedSections []ResolvedPromptSection
}

type PromptAssemblySnapshot struct {
	DisplayName           string
	BaseInstructions      string
	DeveloperInstructions string
	Provider              string
	Version               int
	Hash                  string
	Generation            uint64
}

// PromptAssemblyService 组装系统提示词。
type PromptAssemblyService interface {
	AssembleStart(ctx context.Context, in StartInput) (StartAssembly, error)
	AssembleTurn(ctx context.Context, in TurnInput) (TurnAssembly, error)
	Invalidate(ctx context.Context, reason InvalidateReason) error
}
