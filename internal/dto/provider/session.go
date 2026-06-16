package provider

import "encoding/json"

// PromptRegion identifies whether a prompt section belongs to the static
// cacheable prefix or dynamic tail.
type PromptRegion int

const (
	PromptRegionStatic PromptRegion = iota
	PromptRegionDynamic
)

// ResolvedPromptSection records a rendered prompt section and its cache
// volatility metadata.
type ResolvedPromptSection struct {
	Name     string       `json:"name,omitempty"`
	Region   PromptRegion `json:"region,omitempty"`
	Volatile bool         `json:"volatile,omitempty"`
	Content  string       `json:"content,omitempty"`
}

// SystemContext carries provider-visible system metadata such as git status or
// cache breakers.
type SystemContext map[string]string

// PromptAssemblyBoundary splits assembled instructions into cached and
// uncached regions for providers that support prompt caching.
type PromptAssemblyBoundary struct {
	CachedPrefix string `json:"cachedPrefix,omitempty"`
	UncachedTail string `json:"uncachedTail,omitempty"`
}

// PromptAssemblySnapshot is the durable prompt assembly metadata stored for a
// thread and reused during resume.
type PromptAssemblySnapshot struct {
	DisplayName           string                  `json:"displayName,omitempty"`
	BaseInstructions      string                  `json:"baseInstructions,omitempty"`
	Boundary              *PromptAssemblyBoundary `json:"boundary,omitempty"`
	DeveloperInstructions string                  `json:"developerInstructions,omitempty"`
	Provider              string                  `json:"provider,omitempty"`
	Version               int                     `json:"version,omitempty"`
	Hash                  string                  `json:"hash,omitempty"`
	SectionSnapshot       map[string]string       `json:"sectionSnapshot,omitempty"`
	Generation            uint64                  `json:"generation,omitempty"`
}

// StartAssembly is the provider-ready prompt assembly for a new session.
type StartAssembly struct {
	DisplayName           string                  `json:"displayName,omitempty"`
	BaseInstructions      string                  `json:"baseInstructions,omitempty"`
	Boundary              *PromptAssemblyBoundary `json:"boundary,omitempty"`
	DeveloperInstructions string                  `json:"developerInstructions,omitempty"`
	ResolvedSections      []ResolvedPromptSection `json:"resolvedSections,omitempty"`
	Snapshot              PromptAssemblySnapshot  `json:"snapshot"`
	SuppressedTools       []string                `json:"suppressedTools,omitempty"`

	// UserContext mirrors TurnAssembly.UserContext: a structured map of
	// per-start user meta entries (currentDate, runtimeExtras, gitStatus,
	// claudeMd, ...). Introduced so provider bridges can route these to the
	// synthetic user meta message (Claude prependUserContext equivalent)
	// instead of the cacheable system prompt prefix. Until the migration in
	// Phase 3 completes, consumers must not rely on UserContext being the
	// sole carrier; the same data is still embedded into BaseInstructions for
	// backward compatibility.
	UserContext map[string]string `json:"userContext,omitempty"`
	// UserContextText is the rendered UserContext string (same rendering used
	// by TurnAssembly.UserContextText).
	UserContextText string `json:"userContextText,omitempty"`
	// SystemContext carries the per-start system context dict (git status,
	// cache breaker, ...). Populated in parallel with BaseInstructions during
	// the transition; Phase 3 will stop embedding it into BaseInstructions.
	SystemContext SystemContext `json:"systemContext,omitempty"`
}

// TurnAssembly is the provider-ready prompt and attachment assembly for one
// turn or steer request.
type TurnAssembly struct {
	UserContext      map[string]string       `json:"userContext,omitempty"`
	UserContextText  string                  `json:"userContextText,omitempty"`
	SystemContext    SystemContext           `json:"systemContext,omitempty"`
	Attachments      []AttachmentEnvelope    `json:"attachments,omitempty"`
	ResolvedSections []ResolvedPromptSection `json:"resolvedSections,omitempty"`
}

// StartSessionRequest carries all provider-neutral inputs needed to create a
// new provider session.
type StartSessionRequest struct {
	Provider        string         `json:"provider"`
	AgentID         string         `json:"agentId"`
	CWD             string         `json:"cwd"`
	Model           string         `json:"model,omitempty"`
	Instructions    string         `json:"instructions,omitempty"`
	StartAssembly   StartAssembly  `json:"startAssembly"`
	Config          map[string]any `json:"config,omitempty"`
	ToolSurfaceMode string         `json:"toolSurfaceMode,omitempty"`

	// LaunchSkillNames is the legacy additive launch-time skill selection
	// carrier. The current production path does not turn this into
	// baseInstructions, manifests, or dynamic skill tools; provider drivers
	// reconcile provider-native mirrors and let Claude/Codex discover skills.
	LaunchSkillNames []string `json:"launchSkillNames,omitempty"`
	// LaunchSkillRefs carries the precise UI launch selection for diagnostics
	// and same-name preservation. It is not a prompt injection path.
	LaunchSkillRefs []SkillRef `json:"launchSkillRefs,omitempty"`
	// ForceLaunchSkills mirrors the legacy UI manualSkillSelection flag.
	// It is retained for wire compatibility with existing clients.
	ForceLaunchSkills bool `json:"forceLaunchSkills,omitempty"`
}

// ResumeSessionRequest carries provider-neutral inputs needed to reconnect a
// stored thread to a provider runtime.
type ResumeSessionRequest struct {
	Provider                 string                 `json:"provider"`
	AgentID                  string                 `json:"agentId"`
	ThreadID                 string                 `json:"threadId"`
	ProviderThreadID         string                 `json:"providerThreadId,omitempty"`
	Path                     string                 `json:"path,omitempty"`
	CWD                      string                 `json:"cwd,omitempty"`
	Model                    string                 `json:"model,omitempty"`
	Effort                   string                 `json:"effort,omitempty"`
	Config                   map[string]any         `json:"config,omitempty"`
	PromptSnapshot           PromptAssemblySnapshot `json:"promptSnapshot"`
	ConfigOverride           ThreadConfigPatch      `json:"configOverride"`
	ClaudeHome               string                 `json:"claudeHome,omitempty"`
	CodexHome                string                 `json:"codexHome,omitempty"`
	CodexInstanceKey         string                 `json:"codexInstanceKey,omitempty"`
	CodexModelProvider       string                 `json:"codexModelProvider,omitempty"`
	CodexDisabledNativeTools []string               `json:"codexDisabledNativeTools,omitempty"`
}

var _ json.Marshaler = json.RawMessage(nil)
