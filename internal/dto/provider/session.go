package provider

import "encoding/json"

// PromptRegion 标识 prompt section 在缓存边界中的位置。
type PromptRegion int

// PromptRegion* 枚举值。
const (
	PromptRegionStatic  PromptRegion = iota // 静态前缀，可被缓存。
	PromptRegionDynamic                     // 动态尾部，每次 turn 重建。
)

// ResolvedPromptSection 是已解析的 prompt section，包含名称、区域和内容。
type ResolvedPromptSection struct {
	Name     string       `json:"name,omitempty"`
	Region   PromptRegion `json:"region,omitempty"`
	Volatile bool         `json:"volatile,omitempty"` // 易变内容不参与缓存。
	Content  string       `json:"content,omitempty"`
}

// SystemContext 是每次 start/turn 携带的系统级键值上下文（如 git status、cache breaker）。
type SystemContext map[string]string

// PromptAssemblyBoundary 描述 prompt 的缓存分界点。
type PromptAssemblyBoundary struct {
	CachedPrefix string `json:"cachedPrefix,omitempty"` // 可复用的缓存前缀内容。
	UncachedTail string `json:"uncachedTail,omitempty"` // 每次重建的动态尾部内容。
}

// PromptAssemblySnapshot 是 prompt 组装的不可变快照，用于 resume 时的对比与调试。
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

// StartAssembly 是 StartSessionRequest 中的 prompt 组装载荷。
// UserContext 和 SystemContext 在 Phase 3 迁移完成前仍同时嵌入 BaseInstructions，
// 消费方不能依赖 UserContext 为唯一载体。
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

// TurnAssembly 是每次 turn 请求携带的 prompt 组装增量，包含用户上下文和附件。
type TurnAssembly struct {
	UserContext      map[string]string       `json:"userContext,omitempty"`
	UserContextText  string                  `json:"userContextText,omitempty"`
	SystemContext    SystemContext           `json:"systemContext,omitempty"`
	Attachments      []AttachmentEnvelope    `json:"attachments,omitempty"`
	ResolvedSections []ResolvedPromptSection `json:"resolvedSections,omitempty"`
}

// StartSessionRequest 是新建 provider session 的请求 DTO。
// LaunchSkillNames/LaunchSkillRefs 仅保留线上兼容性，V1 provider-native 链路不再注入正文。
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

// ResumeSessionRequest 是恢复已有 provider session 的请求 DTO。
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
