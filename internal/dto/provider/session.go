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
	// LaunchSkillNames p20.4 §4.4：launch 时 UI 选中的 skill 名称列表；纳入
	// promptSnapshotHash，保证 resume/fork 时 skill 选择变化会失效旧 snapshot。
	LaunchSkillNames []string `json:"launchSkillNames,omitempty"`
	// ForceLaunchSkills p20.4 §4.4：对应 UI manualSkillSelection，true 时
	// launch skill catalog provider 仅渲染命中条目，其余隐藏。
	ForceLaunchSkills bool `json:"forceLaunchSkills,omitempty"`
}

type StartAssembly struct {
	DisplayName           string                  `json:"displayName,omitempty"`
	BaseInstructions      string                  `json:"baseInstructions,omitempty"`
	Boundary              *PromptAssemblyBoundary `json:"boundary,omitempty"`
	DeveloperInstructions string                  `json:"developerInstructions,omitempty"`
	ResolvedSections      []ResolvedPromptSection `json:"resolvedSections,omitempty"`
	Snapshot              PromptAssemblySnapshot  `json:"snapshot"`
	// LaunchSkillNames p20.4 §4.4：runtime assembly 上的 launch skill 镜像，
	// 与 Snapshot 同源，方便 provider-neutral 消费者不必深挖 Snapshot。
	LaunchSkillNames []string `json:"launchSkillNames,omitempty"`
	// ForceLaunchSkills p20.4 §4.4：runtime assembly 上的 manualSkillSelection 镜像。
	ForceLaunchSkills bool `json:"forceLaunchSkills,omitempty"`
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
