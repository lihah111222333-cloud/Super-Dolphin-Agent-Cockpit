package provider

import "encoding/json"

type PromptRegion int

const (
	PromptRegionStatic PromptRegion = iota
	PromptRegionDynamic
)

type ResolvedPromptSection struct {
	Name     string       `json:"name,omitempty"`
	Region   PromptRegion `json:"region,omitempty"`
	Volatile bool         `json:"volatile,omitempty"`
	Content  string       `json:"content,omitempty"`
}

type SystemContext map[string]string

type PromptAssemblyBoundary struct {
	CachedPrefix string `json:"cachedPrefix,omitempty"`
	UncachedTail string `json:"uncachedTail,omitempty"`
}

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

type StartAssembly struct {
	DisplayName           string                  `json:"displayName,omitempty"`
	BaseInstructions      string                  `json:"baseInstructions,omitempty"`
	Boundary              *PromptAssemblyBoundary `json:"boundary,omitempty"`
	DeveloperInstructions string                  `json:"developerInstructions,omitempty"`
	ResolvedSections      []ResolvedPromptSection `json:"resolvedSections,omitempty"`
	Snapshot              PromptAssemblySnapshot  `json:"snapshot"`

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

type TurnAssembly struct {
	UserContext      map[string]string       `json:"userContext,omitempty"`
	UserContextText  string                  `json:"userContextText,omitempty"`
	SystemContext    SystemContext           `json:"systemContext,omitempty"`
	Attachments      []AttachmentEnvelope    `json:"attachments,omitempty"`
	ResolvedSections []ResolvedPromptSection `json:"resolvedSections,omitempty"`
}

type StartSessionRequest struct {
	Provider      string         `json:"provider"`
	AgentID       string         `json:"agentId"`
	CWD           string         `json:"cwd"`
	Model         string         `json:"model,omitempty"`
	Instructions  string         `json:"instructions,omitempty"`
	StartAssembly StartAssembly  `json:"startAssembly"`
	Config        map[string]any `json:"config,omitempty"`

	// LaunchSkillNames p20.3 §4.3：UI launch 时已知的 skill 名称列表。
	// additive optional carrier：旧 caller 不写时行为完全不变。p20.3
	// 只打通，不消费；p20.4/p20.7 将把它们并入 baseInstructions / manifest。
	LaunchSkillNames []string `json:"launchSkillNames,omitempty"`
	// ForceLaunchSkills 对应 UI `manualSkillSelection`：true 时 launch 不再做
	// auto-match / derivation，所选即所用。同样是 additive optional。
	ForceLaunchSkills bool `json:"forceLaunchSkills,omitempty"`
}

type ResumeSessionRequest struct {
	Provider         string                 `json:"provider"`
	AgentID          string                 `json:"agentId"`
	ThreadID         string                 `json:"threadId"`
	ProviderThreadID string                 `json:"providerThreadId,omitempty"`
	Path             string                 `json:"path,omitempty"`
	CWD              string                 `json:"cwd,omitempty"`
	Model            string                 `json:"model,omitempty"`
	Effort           string                 `json:"effort,omitempty"`
	PromptSnapshot   PromptAssemblySnapshot `json:"promptSnapshot"`
	ConfigOverride   ThreadConfigPatch      `json:"configOverride"`
}

var _ json.Marshaler = json.RawMessage(nil)
